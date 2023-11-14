/*
Portions Copyright (c) Microsoft Corporation.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package link_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	. "knative.dev/pkg/logging/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"github.com/aws/karpenter-core/pkg/events"
	coreoptions "github.com/aws/karpenter-core/pkg/operator/options"
	"github.com/aws/karpenter-core/pkg/operator/scheme"
	coretest "github.com/aws/karpenter-core/pkg/test"
	. "github.com/aws/karpenter-core/pkg/test/expectations"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/karpenter/pkg/apis"
	"github.com/Azure/karpenter/pkg/apis/settings"
	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter/pkg/cloudprovider"
	"github.com/Azure/karpenter/pkg/controllers/nodeclaim/link"
	"github.com/Azure/karpenter/pkg/fake"
	"github.com/Azure/karpenter/pkg/providers/instance"
	"github.com/Azure/karpenter/pkg/test"
	"github.com/Azure/karpenter/pkg/utils"
)

var ctx context.Context
var stop context.CancelFunc
var env *coretest.Environment
var azureEnv *test.Environment
var cloudProvider *cloudprovider.CloudProvider
var linkController *link.Controller

func TestAPIs(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "NodeClaim")
}

var _ = BeforeSuite(func() {
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	// ctx = options.ToContext(ctx, test.Options())
	ctx = settings.ToContext(ctx, test.Settings())

	env = coretest.NewEnvironment(scheme.Scheme, coretest.WithCRDs(apis.CRDs...))

	ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider)

	linkController = link.NewController(env.Client, cloudProvider)
})

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = BeforeEach(func() {
	azureEnv.Reset()
})

var _ = AfterEach(func() {
	ExpectCleanedUp(ctx, env.Client)
	linkController.Cache.Flush()
})

var _ = Describe("NodeClaimLink", func() {
	var vm armcompute.VirtualMachine
	var providerID string

	var nodeClass *v1alpha2.AKSNodeClass
	var nodePool *corev1beta1.NodePool

	BeforeEach(func() {
		id := utils.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, "vm-a")
		providerID = utils.ResourceIDToProviderID(ctx, id)
		nodeClass = test.AKSNodeClass()
		nodePool = coretest.NodePool(corev1beta1.NodePool{
			Spec: corev1beta1.NodePoolSpec{
				Template: corev1beta1.NodeClaimTemplate{
					Spec: corev1beta1.NodeClaimSpec{
						NodeClassRef: &corev1beta1.NodeClassReference{
							Name: nodeClass.Name,
						},
					},
				},
			},
		})
		vm = armcompute.VirtualMachine{
			ID:       lo.ToPtr(id),
			Name:     lo.ToPtr("vm-a"),
			Tags:     map[string]*string{instance.NodePoolTagKey: lo.ToPtr(nodePool.Name)},
			Zones:    []*string{lo.ToPtr(fmt.Sprintf("%s-1a", fake.Region))},
			Location: lo.ToPtr(fake.Region),
			Properties: lo.ToPtr(armcompute.VirtualMachineProperties{
				TimeCreated: lo.ToPtr(time.Now()),
			}),
		}
		azureEnv.VirtualMachinesAPI.Instances.Store(*vm.ID, vm)
	})

	It("should link an instance with basic spec set", func() {
		nodePool.Spec.Template.Spec.Taints = []v1.Taint{
			{
				Key:    "testkey",
				Value:  "testvalue",
				Effect: v1.TaintEffectNoSchedule,
			},
		}
		nodePool.Spec.Template.Spec.StartupTaints = []v1.Taint{
			{
				Key:    "othertestkey",
				Value:  "othertestvalue",
				Effect: v1.TaintEffectNoExecute,
			},
		}
		ExpectApplied(ctx, env.Client, nodePool)
		ExpectVMExists(*vm.Name)
		ExpectReconcileSucceeded(ctx, linkController, client.ObjectKey{})

		nodeClaims := &corev1beta1.NodeClaimList{}
		Expect(env.Client.List(ctx, nodeClaims)).To(Succeed())
		Expect(nodeClaims.Items).To(HaveLen(1))
		nodeClaim := nodeClaims.Items[0]

		// Expect NodeClaim to have populated fields from the node
		Expect(nodeClaim.Spec.Taints).To(Equal(nodePool.Spec.Template.Spec.Taints))
		Expect(nodeClaim.Spec.StartupTaints).To(Equal(nodePool.Spec.Template.Spec.StartupTaints))

		// Expect NodeClaim has linking annotation to get NodeClaim details
		Expect(nodeClaim.Annotations).To(HaveKeyWithValue(v1alpha2.NodeClaimLinkedAnnotationKey, providerID))
		vm = ExpectVMExists(*vm.Name)
		ExpectProvisionerNameTagExists(vm)
	})
	It("should link an instance with expected requirements and labels", func() {
		nodePool.Spec.Template.Spec.Requirements = []v1.NodeSelectorRequirement{
			{
				Key:      v1.LabelTopologyZone,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"test-zone-1a", "test-zone-1b", "test-zone-1c"},
			},
			{
				Key:      v1.LabelOSStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{string(v1.Linux), string(v1.Windows)},
			},
			{
				Key:      v1.LabelArchStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{corev1beta1.ArchitectureAmd64},
			},
		}
		ExpectApplied(ctx, env.Client, nodePool)
		ExpectVMExists(*vm.Name)
		ExpectReconcileSucceeded(ctx, linkController, client.ObjectKey{})

		nodeClaims := &corev1beta1.NodeClaimList{}
		Expect(env.Client.List(ctx, nodeClaims)).To(Succeed())
		Expect(nodeClaims.Items).To(HaveLen(1))
		nodeClaim := nodeClaims.Items[0]

		Expect(nodeClaim.Spec.Requirements).To(HaveLen(3))
		Expect(nodeClaim.Spec.Requirements).To(ContainElements(
			v1.NodeSelectorRequirement{
				Key:      v1.LabelTopologyZone,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"test-zone-1a", "test-zone-1b", "test-zone-1c"},
			},
			v1.NodeSelectorRequirement{
				Key:      v1.LabelOSStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{string(v1.Linux), string(v1.Windows)},
			},
			v1.NodeSelectorRequirement{
				Key:      v1.LabelArchStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{corev1beta1.ArchitectureAmd64},
			},
		))

		// Expect NodeClaim has linking annotation to get NodeClaim details
		Expect(nodeClaim.Annotations).To(HaveKeyWithValue(v1alpha2.NodeClaimLinkedAnnotationKey, providerID))
		ExpectVMExists(*vm.Name)
	})
	It("should link an instance with expected kubelet from provisioner kubelet configuration", func() {
		nodePool.Spec.Template.Spec.Kubelet = &corev1beta1.KubeletConfiguration{
			ClusterDNS: []string{"10.0.0.1"},
			MaxPods:    lo.ToPtr[int32](10),
		}
		ExpectApplied(ctx, env.Client, nodePool)
		ExpectReconcileSucceeded(ctx, linkController, client.ObjectKey{})

		nodeClaims := &corev1beta1.NodeClaimList{}
		Expect(env.Client.List(ctx, nodeClaims)).To(Succeed())
		Expect(nodeClaims.Items).To(HaveLen(1))
		nodeClaim := nodeClaims.Items[0]

		Expect(nodeClaim.Spec.Kubelet).ToNot(BeNil())
		Expect(nodeClaim.Spec.Kubelet.ClusterDNS[0]).To(Equal("10.0.0.1"))
		Expect(lo.FromPtr(nodeClaim.Spec.Kubelet.MaxPods)).To(BeNumerically("==", 10))

		// Expect NodeClaim has linking annotation to get NodeClaim details
		Expect(nodeClaim.Annotations).To(HaveKeyWithValue(v1alpha2.NodeClaimLinkedAnnotationKey, providerID))
		vm = ExpectVMExists(*vm.Name)
		ExpectProvisionerNameTagExists(vm)
	})
	It("should link many instances to many machines", func() {
		azureEnv.VirtualMachinesAPI.Reset() // Reset so we don't store the extra VM from BeforeEach()
		ExpectApplied(ctx, env.Client, nodePool)
		// Generate 100 instances that have different vmIDs
		var vmNames []string
		var vmName string
		var vmID string
		for i := 0; i < 100; i++ {
			vmName = fmt.Sprintf("vm-%d", i)
			vmID = utils.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)
			azureEnv.VirtualMachinesAPI.Instances.Store(
				vmID,
				armcompute.VirtualMachine{
					ID:   lo.ToPtr(utils.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
					Name: lo.ToPtr(vmName),
					Properties: &armcompute.VirtualMachineProperties{
						TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute)),
					},
					Tags: map[string]*string{
						instance.NodePoolTagKey: lo.ToPtr(nodePool.Name),
					},
					Zones:    []*string{lo.ToPtr(fmt.Sprintf("%s-1a", fake.Region))},
					Location: lo.ToPtr(fake.Region),
				})
			vmNames = append(vmNames, vmName)
		}

		// Generate a reconcile loop to link the machines
		ExpectReconcileSucceeded(ctx, linkController, client.ObjectKey{})

		nodeClaims := &corev1beta1.NodeClaimList{}
		Expect(env.Client.List(ctx, nodeClaims)).To(Succeed())
		Expect(nodeClaims.Items).To(HaveLen(100))

		nodeClaimInstanceIDs := sets.New(lo.Map(nodeClaims.Items, func(m corev1beta1.NodeClaim, _ int) string {
			return lo.Must(utils.GetVMName(m.Annotations[v1alpha2.NodeClaimLinkedAnnotationKey]))
		})...)

		Expect(nodeClaimInstanceIDs).To(HaveLen(len(vmNames)))
		for _, name := range vmNames {
			Expect(nodeClaimInstanceIDs.Has(name)).To(BeTrue())
			vm = ExpectVMExists(name)
			ExpectProvisionerNameTagExists(vm)
		}
	})
	It("should link an instance without node template existence", func() {
		// No node template has been applied here
		ExpectApplied(ctx, env.Client, nodePool)
		ExpectReconcileSucceeded(ctx, linkController, client.ObjectKey{})

		nodeClaims := &corev1beta1.NodeClaimList{}
		Expect(env.Client.List(ctx, nodeClaims)).To(Succeed())
		Expect(nodeClaims.Items).To(HaveLen(1))
		nodeClaim := nodeClaims.Items[0]

		// Expect NodeClaim has linking annotation to get NodeClaim details
		Expect(nodeClaim.Annotations).To(HaveKeyWithValue(v1alpha2.NodeClaimLinkedAnnotationKey, providerID))
		vm = ExpectVMExists(*vm.Name)
		ExpectProvisionerNameTagExists(vm)
	})
	It("should link an instance that was re-owned with a provisioner-name label", func() {
		azureEnv.VirtualMachinesAPI.Reset() // Reset so we don't store the extra instance

		// Don't include the provisioner-name tag
		vm.Tags = lo.OmitBy(vm.Tags, func(key string, value *string) bool {
			return key == instance.NodePoolTagKey
		})
		azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), vm)
		node := coretest.Node(coretest.NodeOptions{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					corev1beta1.NodePoolLabelKey: nodePool.Name,
				},
			},
			ProviderID: providerID,
		})
		ExpectApplied(ctx, env.Client, node, nodePool)
		ExpectReconcileSucceeded(ctx, linkController, client.ObjectKey{})

		nodeClaims := &corev1beta1.NodeClaimList{}
		Expect(env.Client.List(ctx, nodeClaims)).To(Succeed())
		Expect(nodeClaims.Items).To(HaveLen(1))
		nodeClaim := nodeClaims.Items[0]
		Expect(nodeClaim.Annotations).To(HaveKeyWithValue(v1alpha2.NodeClaimLinkedAnnotationKey, providerID))
	})
	It("should not link an instance without a provisioner tag", func() {
		v := ExpectVMExists(*vm.Name)
		v.Tags = lo.OmitBy(v.Tags, func(key string, value *string) bool {
			return key == instance.NodePoolTagKey
		})
		azureEnv.VirtualMachinesAPI.Instances.Store(*v.ID, v)

		ExpectApplied(ctx, env.Client, nodePool)
		ExpectReconcileSucceeded(ctx, linkController, client.ObjectKey{})

		nodeClaims := &corev1beta1.NodeClaimList{}
		Expect(env.Client.List(ctx, nodeClaims)).To(Succeed())
		Expect(nodeClaims.Items).To(HaveLen(0))
	})
	It("should not link an instance without a provisioner that exists on the cluster", func() {
		// No provisioner has been applied here
		ExpectApplied(ctx, env.Client)
		ExpectReconcileSucceeded(ctx, linkController, client.ObjectKey{})

		nodeClaims := &corev1beta1.NodeClaimList{}
		Expect(env.Client.List(ctx, nodeClaims)).To(Succeed())
		Expect(nodeClaims.Items).To(HaveLen(0))

		// Expect that the instance was left alone if the provisioner wasn't found
		ExpectVMExists(*vm.Name)
	})
	It("should not link an instance for an instance that is already linked", func() {
		m := coretest.NodeClaim(corev1beta1.NodeClaim{
			Status: corev1beta1.NodeClaimStatus{
				ProviderID: providerID,
			},
		})
		ExpectApplied(ctx, env.Client, nodePool, m)

		nodeClaims := &corev1beta1.NodeClaimList{}
		Expect(env.Client.List(ctx, nodeClaims)).To(Succeed())
		Expect(nodeClaims.Items).To(HaveLen(1))

		// Expect that we go to link machines, and we don't add extra machines from the existing one
		ExpectReconcileSucceeded(ctx, linkController, client.ObjectKey{})
		Expect(env.Client.List(ctx, nodeClaims)).To(Succeed())
		Expect(nodeClaims.Items).To(HaveLen(1))
	})
	It("should not remove existing tags when linking", func() {
		v := ExpectVMExists(*vm.Name)
		v.Tags["testKey"] = lo.ToPtr("testVal")
		azureEnv.VirtualMachinesAPI.Instances.Store(*v.ID, v)

		ExpectApplied(ctx, env.Client, nodePool)
		ExpectReconcileSucceeded(ctx, linkController, client.ObjectKey{})

		nodeClaims := &corev1beta1.NodeClaimList{}
		Expect(env.Client.List(ctx, nodeClaims)).To(Succeed())
		Expect(nodeClaims.Items).To(HaveLen(1))
		nodeClaim := nodeClaims.Items[0]

		// Expect NodeClaim has linking annotation to get NodeClaim details
		Expect(nodeClaim.Annotations).To(HaveKeyWithValue(v1alpha2.NodeClaimLinkedAnnotationKey, providerID))
		vm = ExpectVMExists(*vm.Name)
		ExpectProvisionerNameTagExists(vm)
		Expect(*v.Tags["testKey"]).To(Equal("testVal"))
	})
})

func ExpectVMExists(vmName string) armcompute.VirtualMachine {
	resp, err := azureEnv.VirtualMachinesAPI.Get(ctx, azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName, nil)
	Expect(err).NotTo(HaveOccurred())
	return resp.VirtualMachine
}

func ExpectProvisionerNameTagExists(vm armcompute.VirtualMachine) {
	_, ok := lo.FindKeyBy(vm.Tags, func(key string, value *string) bool {
		return key == instance.NodePoolTagKey
	})
	Expect(ok).To(BeTrue())
}
