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

package cloudprovider

// TODO v1beta1 extra refactor into suite_test.go / cloudprovider_test.go
import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	corecloudprovider "github.com/aws/karpenter-core/pkg/cloudprovider"

	"github.com/aws/karpenter-core/pkg/controllers/provisioning"
	"github.com/aws/karpenter-core/pkg/controllers/state"
	"github.com/aws/karpenter-core/pkg/events"
	coreoptions "github.com/aws/karpenter-core/pkg/operator/options"
	"github.com/aws/karpenter-core/pkg/operator/scheme"
	coretest "github.com/aws/karpenter-core/pkg/test"

	. "knative.dev/pkg/logging/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/aws/karpenter-core/pkg/test/expectations"
)

var ctx context.Context
var stop context.CancelFunc
var env *coretest.Environment
var azureEnv *test.Environment
var fakeClock *clock.FakeClock
var coreProvisioner *provisioning.Provisioner

var nodePool *corev1beta1.NodePool
var nodeClass *v1alpha2.AKSNodeClass
var nodeClaim *corev1beta1.NodeClaim
var cluster *state.Cluster
var cloudProvider *CloudProvider

func TestCloudProvider(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "CloudProvider")
}

func toBoolPtr(b bool) *bool {
	return &b
}

var _ = BeforeSuite(func() {
	env = coretest.NewEnvironment(scheme.Scheme, coretest.WithCRDs(apis.CRDs...))
	ctx = coreoptions.ToContext(ctx, coretest.Options(coretest.OptionsFields{FeatureGates: coretest.FeatureGates{Drift: toBoolPtr(true)}}))
	ctx = options.ToContext(ctx, test.Options())
	ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)

	fakeClock = &clock.FakeClock{}
	cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider)
	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	coreProvisioner = provisioning.NewProvisioner(env.Client, env.KubernetesInterface.CoreV1(), events.NewRecorder(&record.FakeRecorder{}), cloudProvider, cluster)
})

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = BeforeEach(func() {
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	// TODO v1beta1 options
	// ctx = options.ToContext(ctx, test.Options())
	ctx = options.ToContext(ctx, test.Options())
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
	nodeClaim = coretest.NodeClaim(corev1beta1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{corev1beta1.NodePoolLabelKey: nodePool.Name},
		},
		Spec: corev1beta1.NodeClaimSpec{
			NodeClassRef: &corev1beta1.NodeClassReference{
				Name: nodeClass.Name,
			},
		},
	})

	cluster.Reset()
	azureEnv.Reset()
})

var _ = AfterEach(func() {
	ExpectCleanedUp(ctx, env.Client)
})

var _ = Describe("CloudProvider", func() {
	It("should list nodeclaim created by the CloudProvider", func() {
		ExpectApplied(ctx, env.Client, nodeClass, nodePool)
		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
		ExpectScheduled(ctx, env.Client, pod)
		Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

		nodeClaims, _ := cloudProvider.List(ctx)
		Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))
		queryRequest := azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Pop().Query
		Expect(*queryRequest.Query).To(Equal(instance.GetListQueryBuilder(azureEnv.AzureResourceGraphAPI.ResourceGroup).String()))
		Expect(nodeClaims).To(HaveLen(1))
		Expect(nodeClaims[0]).ToNot(BeNil())
		resp, _ := azureEnv.VirtualMachinesAPI.Get(ctx, azureEnv.AzureResourceGraphAPI.ResourceGroup, nodeClaims[0].Name, nil)
		Expect(resp.VirtualMachine).ToNot(BeNil())
	})
	It("should return an ICE error when there are no instance types to launch", func() {
		// Specify no instance types and expect to receive a capacity error
		nodeClaim.Spec.Requirements = []v1.NodeSelectorRequirement{
			{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"doesnotexist"}, // will not match any instance types
			},
		}
		ExpectApplied(ctx, env.Client, nodePool, nodeClass, nodeClaim)
		cloudProviderMachine, err := cloudProvider.Create(ctx, nodeClaim)
		Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
		Expect(cloudProviderMachine).To(BeNil())
	})
	Context("Drift", func() {
		var nodeClaim *corev1beta1.NodeClaim
		var pod *v1.Pod
		BeforeEach(func() {
			instanceType := "Standard_D2_v2"
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod = coretest.UnschedulablePod(coretest.PodOptions{
				NodeSelector: map[string]string{v1.LabelInstanceTypeStable: instanceType},
			})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			input := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			rg := input.ResourceGroupName
			vmName := input.VMName

			// Corresponding NodeClaim
			nodeClaim = coretest.NodeClaim(corev1beta1.NodeClaim{
				Status: corev1beta1.NodeClaimStatus{
					ProviderID: utils.ResourceIDToProviderID(ctx, utils.MkVMID(rg, vmName)),
				},
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						corev1beta1.NodePoolLabelKey: nodePool.Name,
						v1.LabelInstanceTypeStable:   instanceType,
					},
				},
			})
		})
		It("should not fail if nodeClass does not exist", func() {
			ExpectDeleted(ctx, env.Client, nodeClass)
			drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(BeEmpty())
		})
		It("should not fail if nodePool does not exist", func() {
			ExpectDeleted(ctx, env.Client, nodePool)
			drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(BeEmpty())
		})
		It("should not return drifted if the NodeClaim is valid", func() {
			drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(BeEmpty())
		})
		It("should error drift if NodeClaim doesn't have provider id", func() {
			nodeClaim.Status = corev1beta1.NodeClaimStatus{}
			drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
			Expect(err).To(HaveOccurred())
			Expect(drifted).To(BeEmpty())
		})
	})
})
