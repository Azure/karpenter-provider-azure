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
	"time"

	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"

	"github.com/awslabs/operatorpkg/object"
	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"

	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "knative.dev/pkg/logging/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
)

var ctx context.Context
var stop context.CancelFunc
var env *coretest.Environment
var azureEnv *test.Environment
var prov *provisioning.Provisioner
var cloudProvider *CloudProvider
var cluster *state.Cluster
var fakeClock *clock.FakeClock
var recorder events.Recorder

var nodePool *karpv1.NodePool
var nodeClass *v1alpha2.AKSNodeClass
var nodeClaim *karpv1.NodeClaim

func TestCloudProvider(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "cloudProvider/Azure")
}

var _ = BeforeSuite(func() {
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...))
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, test.Options())
	ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	fakeClock = clock.NewFakeClock(time.Now())
	recorder = events.NewRecorder(&record.FakeRecorder{})
	cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.InstanceProvider, recorder, env.Client, azureEnv.ImageProvider)
	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	prov = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
})

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = BeforeEach(func() {
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, test.Options())

	nodeClass = test.AKSNodeClass()
	test.ApplyDefaultStatus(nodeClass, env)

	nodePool = coretest.NodePool(karpv1.NodePool{
		Spec: karpv1.NodePoolSpec{
			Template: karpv1.NodeClaimTemplate{
				Spec: karpv1.NodeClaimTemplateSpec{
					NodeClassRef: &karpv1.NodeClassReference{
						Group: object.GVK(nodeClass).Group,
						Kind:  object.GVK(nodeClass).Kind,
						Name:  nodeClass.Name,
					},
				},
			},
		},
	})

	nodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
		},
		Spec: karpv1.NodeClaimSpec{
			NodeClassRef: &karpv1.NodeClassReference{
				Group: object.GVK(nodeClass).Group,
				Kind:  object.GVK(nodeClass).Kind,
				Name:  nodeClass.Name,
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
		pod := coretest.UnschedulablePod()
		ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, prov, pod)
		ExpectScheduled(ctx, env.Client, pod)
		Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

		nodeClaims, _ := cloudProvider.List(ctx)
		Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))
		queryRequest := azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Pop().Query
		Expect(*queryRequest.Query).To(Equal(instance.GetVMListQueryBuilder(azureEnv.AzureResourceGraphAPI.ResourceGroup).String()))
		Expect(nodeClaims).To(HaveLen(1))
		Expect(nodeClaims[0]).ToNot(BeNil())
		resp, _ := azureEnv.VirtualMachinesAPI.Get(ctx, azureEnv.AzureResourceGraphAPI.ResourceGroup, nodeClaims[0].Name, nil)
		Expect(resp.VirtualMachine).ToNot(BeNil())
	})
	It("should return an ICE error when there are no instance types to launch", func() {
		// Specify no instance types and expect to receive a capacity error
		nodeClaim.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
			{
				NodeSelectorRequirement: v1.NodeSelectorRequirement{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"doesnotexist"}, // will not match any instance types
				},
			},
		}

		ExpectApplied(ctx, env.Client, nodePool, nodeClass, nodeClaim)
		cloudProviderMachine, err := cloudProvider.Create(ctx, nodeClaim)
		Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
		Expect(cloudProviderMachine).To(BeNil())
	})
	Context("Drift", func() {
		var nodeClaim *karpv1.NodeClaim
		var pod *v1.Pod
		BeforeEach(func() {
			instanceType := "Standard_D2_v2"
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod = coretest.UnschedulablePod(coretest.PodOptions{
				NodeSelector: map[string]string{v1.LabelInstanceTypeStable: instanceType},
			})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, prov, pod)
			node := ExpectScheduled(ctx, env.Client, pod)
			// KubeletVersion must be applied to the node to satisfy k8s drift
			node.Status.NodeInfo.KubeletVersion = "v" + nodeClass.Status.KubernetesVersion
			ExpectApplied(ctx, env.Client, node)
			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			input := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			rg := input.ResourceGroupName
			vmName := input.VMName
			// Corresponding NodeClaim
			nodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
				Status: karpv1.NodeClaimStatus{
					NodeName:   node.Name,
					ProviderID: utils.ResourceIDToProviderID(ctx, utils.MkVMID(rg, vmName)),
				},
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						karpv1.NodePoolLabelKey:    nodePool.Name,
						v1.LabelInstanceTypeStable: instanceType,
					},
				},
				Spec: karpv1.NodeClaimSpec{
					NodeClassRef: &karpv1.NodeClassReference{
						Group: object.GVK(nodeClass).Group,
						Kind:  object.GVK(nodeClass).Kind,
						Name:  nodeClass.Name,
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
			nodeClaim.Status = karpv1.NodeClaimStatus{}
			drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
			Expect(err).To(HaveOccurred())
			Expect(drifted).To(BeEmpty())
		})
		It("should trigger drift when the image gallery changes to SIG", func() {
			options := test.Options(test.OptionsFields{
				UseSIG: lo.ToPtr(true),
			})
			ctx = options.ToContext(ctx)
			drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(drifted)).To(Equal("ImageVersionDrift"))
		})

		Context("Kubernetes Version", func() {
			It("should succeed with no drift when nothing changes", func() {
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))
			})

			It("should succeed with no drift when KubernetesVersionReady is not true", func() {
				nodeClass = ExpectExists(ctx, env.Client, nodeClass)
				nodeClass.StatusConditions().SetFalse(v1alpha2.ConditionTypeKubernetesVersionReady, "K8sVersionNoLongerReady", "test when k8s isn't ready")
				ExpectApplied(ctx, env.Client, nodeClass)
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))
			})

			// TODO (charliedmcb): I'm wondering if we actually want to have these soft-error cases switch to return an error if no-drift condition was found.
			It("shouldn't error or be drifted when KubernetesVersion is empty", func() {
				nodeClass = ExpectExists(ctx, env.Client, nodeClass)
				nodeClass.Status.KubernetesVersion = ""
				ExpectApplied(ctx, env.Client, nodeClass)
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))
			})

			// TODO (charliedmcb): I'm wondering if we actually want to have these soft-error cases switch to return an error if no-drift condition was found.
			It("shouldn't error or be drifted when NodeName is missing", func() {
				nodeClaim.Status.NodeName = ""
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))
			})

			It("should error when node is not found", func() {
				nodeClaim.Status.NodeName = "NodeWhoDoesNotExist"
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).To(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))
			})

			It("should succeed with drift true when KubernetesVersion is new", func() {
				nodeClass = ExpectExists(ctx, env.Client, nodeClass)

				semverCurrentK8sVersion := lo.Must(semver.ParseTolerant(nodeClass.Status.KubernetesVersion))
				semverCurrentK8sVersion.Minor = semverCurrentK8sVersion.Minor + 1
				nodeClass.Status.KubernetesVersion = semverCurrentK8sVersion.String()

				ExpectApplied(ctx, env.Client, nodeClass)

				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(K8sVersionDrift))
			})
		})
	})
})
