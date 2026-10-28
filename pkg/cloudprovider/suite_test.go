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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/Azure/skewer"
	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	autoscalingv1beta1 "sigs.k8s.io/karpenter/pkg/apis/autoscaling/v1beta1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	karpv1alpha1 "sigs.k8s.io/karpenter/pkg/apis/v1alpha1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/capacitybuffer"
	"sigs.k8s.io/karpenter/pkg/controllers/dynamicresources/deviceallocation"
	"sigs.k8s.io/karpenter/pkg/controllers/node/health"
	"sigs.k8s.io/karpenter/pkg/controllers/nodeoverlay"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	"sigs.k8s.io/karpenter/pkg/state/virtualpods"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
	corestatus "github.com/awslabs/operatorpkg/status"
)

var ctx context.Context
var testOptions *options.Options
var stop context.CancelFunc
var env *coretest.Environment
var azureEnv *test.Environment
var azureEnvNonZonal *test.Environment
var coreProvisioner *provisioning.Provisioner
var coreProvisionerNonZonal *provisioning.Provisioner
var cloudProvider *CloudProvider
var cloudProviderNonZonal *CloudProvider
var cluster *state.Cluster
var clusterNonZonal *state.Cluster
var virtualPodCache *virtualpods.Cache
var fakeClock *clock.FakeClock
var recorder events.Recorder
var statusController *status.Controller

var nodePool *karpv1.NodePool
var nodeClass *v1beta1.AKSNodeClass
var nodeClaim *karpv1.NodeClaim

var fakeZone1 = zones.MakeAKSLabelZoneFromARMZone(fake.Region, "1")
var defaultTestSKU = fake.MakeSKU("Standard_D2_v3")

func TestCloudProvider(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "cloudProvider/Azure")
}

var _ = BeforeSuite(func() {
	env = coretest.NewEnvironment(
		coretest.WithCRDs(apis.CRDs...),
		coretest.WithCRDs(v1alpha1.CRDs...),
		coretest.WithFieldIndexers(coretest.NodeProviderIDFieldIndexer(ctx)))

	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx, stop = context.WithCancel(ctx)
	fakeClock = clock.NewFakeClock(time.Now())
	recorder = events.NewRecorder(&record.FakeRecorder{})
})

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = BeforeEach(func() {
	nodeClass = test.AKSNodeClass(test.WithVersionBasedDefaults(env.Version))
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
})

var _ = AfterEach(func() {
	ExpectCleanedUp(ctx, env.Client)
})

// Helper functions for NodeClaim validation
func validateNodeClaimCommon(nodeClaim *karpv1.NodeClaim, nodePool *karpv1.NodePool) {
	// Basic validation
	Expect(nodeClaim).ToNot(BeNil())
	Expect(nodeClaim.Status.Capacity).ToNot(BeEmpty())

	// Status fields
	Expect(nodeClaim.Status.ProviderID).ToNot(BeEmpty())
	Expect(nodeClaim.Status.ImageID).ToNot(BeEmpty())

	// Common labels validation
	Expect(nodeClaim.Labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
	Expect(nodeClaim.Labels).To(HaveKey(karpv1.NodePoolLabelKey))
	if nodePool != nil {
		Expect(nodeClaim.Labels[karpv1.NodePoolLabelKey]).To(Equal(nodePool.Name))
	}
	Expect(nodeClaim.Labels).To(HaveKey(v1.LabelInstanceTypeStable))
	Expect(nodeClaim.Labels).To(HaveKey(v1.LabelArchStable))
	Expect(nodeClaim.Labels).To(HaveKey(v1beta1.LabelSKUName))
	Expect(nodeClaim.Labels).To(HaveKey(v1beta1.LabelSKUFamily))
	Expect(nodeClaim.Labels).To(HaveKey(v1beta1.LabelSKUCPU))
	Expect(nodeClaim.Labels).To(HaveKey(v1beta1.LabelSKUMemory))
	Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationCapacityReservationGroupID))

	// Zone validation (conditional)
	zone := nodeClaim.Labels[v1.LabelTopologyZone]
	if zone != "" && zone != zones.Regional {
		Expect(zone).To(MatchRegexp(`^[a-z0-9-]+-[0-9]+$`))
	}

	// Capacity and Allocatable resources
	Expect(nodeClaim.Status.Capacity).To(HaveKey(v1.ResourceCPU))
	Expect(nodeClaim.Status.Capacity).To(HaveKey(v1.ResourceMemory))
	Expect(nodeClaim.Status.Allocatable).To(HaveKey(v1.ResourceCPU))
	Expect(nodeClaim.Status.Allocatable).To(HaveKey(v1.ResourceMemory))

	// Lifecycle validation
	Expect(nodeClaim.DeletionTimestamp).To(BeNil())
}

func validateVMNodeClaim(nodeClaim *karpv1.NodeClaim, nodePool *karpv1.NodePool) {
	// Common validations
	validateNodeClaimCommon(nodeClaim, nodePool)

	// VM-specific validation (should NOT have AKS machine annotation)
	Expect(nodeClaim.Annotations).ToNot(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
}

func reconcileCapacityOverlay(customResource v1.ResourceName, overlayCapacity resource.Quantity) {
	GinkgoHelper()
	nodeOverlay := coretest.NodeOverlay(karpv1alpha1.NodeOverlay{
		Spec: karpv1alpha1.NodeOverlaySpec{
			Requirements: []karpv1alpha1.NodeSelectorRequirement{{
				Key:      karpv1.NodePoolLabelKey,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{nodePool.Name},
			}},
			Capacity: v1.ResourceList{customResource: overlayCapacity},
		},
	})
	ExpectApplied(ctx, env.Client, nodeOverlay)
	nodeOverlayController := nodeoverlay.NewController(env.Clock, env.Client, cloudProvider, azureEnv.InstanceTypeStore, cluster)
	ExpectReconcileSucceeded(ctx, nodeOverlayController, client.ObjectKeyFromObject(nodeOverlay))
}

type nodeOverlayCapacityTestOptions struct {
	validateNodeClaim func(*karpv1.NodeClaim)
	resetCreateCalls  func()
	expectCreateCalls func()
}

func runNodeOverlayCapacityTests(testOptions nodeOverlayCapacityTestOptions) {
	Context("NodeOverlay", func() {
		It("should launch a NodeClaim that requests capacity added by a NodeOverlay", func() {
			ctx = coreoptions.ToContext(ctx, coretest.Options(coretest.OptionsFields{
				FeatureGates: coretest.FeatureGates{NodeOverlay: lo.ToPtr(true)},
			}))
			customResource := v1.ResourceName("example.com/dongle")
			overlayCapacity := resource.MustParse("100")
			nodeClaim.Spec.Resources.Requests = v1.ResourceList{customResource: resource.MustParse("1")}

			ExpectApplied(ctx, env.Client, nodeClass, nodePool, nodeClaim)
			reconcileCapacityOverlay(customResource, overlayCapacity)

			if testOptions.resetCreateCalls != nil {
				testOptions.resetCreateCalls()
			}
			cloudProviderMachine, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, nodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(cloudProviderMachine).ToNot(BeNil())
			if testOptions.validateNodeClaim != nil {
				testOptions.validateNodeClaim(cloudProviderMachine)
			}
			if testOptions.expectCreateCalls != nil {
				testOptions.expectCreateCalls()
			}
			capacity, ok := cloudProviderMachine.Status.Capacity[customResource]
			Expect(ok).To(BeTrue())
			Expect(capacity.Cmp(overlayCapacity)).To(Equal(0))
			allocatable, ok := cloudProviderMachine.Status.Allocatable[customResource]
			Expect(ok).To(BeTrue())
			Expect(allocatable.Cmp(overlayCapacity)).To(Equal(0))
		})

		It("should not use overlaid capacity when NodeOverlay is disabled", func() {
			// Explicitly disable the NodeOverlay feature gate so this test does not
			// depend on ordering with the previous It block that enables it.
			ctx = coreoptions.ToContext(ctx, coretest.Options(coretest.OptionsFields{
				FeatureGates: coretest.FeatureGates{NodeOverlay: lo.ToPtr(false)},
			}))
			customResource := v1.ResourceName("example.com/dongle")
			overlayCapacity := resource.MustParse("100")
			nodeClaim.Spec.Resources.Requests = v1.ResourceList{customResource: resource.MustParse("1")}

			ExpectApplied(ctx, env.Client, nodeClass, nodePool, nodeClaim)
			reconcileCapacityOverlay(customResource, overlayCapacity)

			if testOptions.resetCreateCalls != nil {
				testOptions.resetCreateCalls()
			}
			cloudProviderMachine, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, nodeClaim)
			Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
			Expect(cloudProviderMachine).To(BeNil())
		})
	})
}

func vmNodeOverlayCapacityTestOptions() nodeOverlayCapacityTestOptions {
	return nodeOverlayCapacityTestOptions{
		validateNodeClaim: func(nodeClaim *karpv1.NodeClaim) {
			validateVMNodeClaim(nodeClaim, nodePool)
		},
		resetCreateCalls: func() {
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
		},
		expectCreateCalls: func() {
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
		},
	}
}

func runCapacityBufferTests(expectCreateCalls func(expectedCalls int)) {
	Context("CapacityBuffer", func() {
		It("should launch capacity for a PodTemplate-backed buffer", func() {
			ctx = coreoptions.ToContext(ctx, coretest.Options(coretest.OptionsFields{
				FeatureGates: coretest.FeatureGates{CapacityBuffer: lo.ToPtr(true)},
			}))
			virtualPodCache = virtualpods.NewVirtualPodCache(env.Client)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock, deviceallocation.NewController(env.Client), virtualPodCache)
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)

			podTemplate := &v1.PodTemplate{
				ObjectMeta: metav1.ObjectMeta{Name: "capacity-buffer-template", Namespace: "default"},
				Template: v1.PodTemplateSpec{
					Spec: v1.PodSpec{
						Containers: []v1.Container{{
							Name:  "workload",
							Image: "pause:latest",
							Resources: v1.ResourceRequirements{Requests: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("1"),
								v1.ResourceMemory: resource.MustParse("1Gi"),
							}},
						}},
						NodeSelector: map[string]string{v1.LabelOSStable: string(v1.Linux)},
					},
				},
			}
			buffer := coretest.CapacityBuffer(autoscalingv1beta1.CapacityBuffer{
				ObjectMeta: metav1.ObjectMeta{Name: "capacity-buffer", Namespace: "default"},
				Spec: autoscalingv1beta1.CapacityBufferSpec{
					ProvisioningStrategy: lo.ToPtr(autoscalingv1beta1.ActiveProvisioningStrategy),
					PodTemplateRef:       &autoscalingv1beta1.LocalObjectRef{Name: podTemplate.Name},
					Replicas:             lo.ToPtr(int32(1)),
				},
			})
			DeferCleanup(func() {
				ExpectDeleted(ctx, env.Client, buffer, podTemplate)
			})

			ExpectApplied(ctx, env.Client, podTemplate, buffer)
			ExpectReconcileSucceeded(ctx, capacitybuffer.NewController(env.Client, coreProvisioner, virtualPodCache), client.ObjectKeyFromObject(buffer))
			EventuallyExpectCapacityBufferReplicas(ctx, env.Client, buffer, 1)

			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv)
			nodeClaims := ExpectNodeClaims(ctx, env.Client)
			Expect(nodeClaims).To(HaveLen(1))
			Expect(nodeClaims[0].Spec.Requirements).To(ContainElement(And(
				HaveField("Key", v1.LabelOSStable),
				HaveField("Values", ContainElement(string(v1.Linux))),
			)))
			expectCreateCalls(1)
		})
	})
}

var _ = Describe("CloudProvider", func() {
	Context("Repair", func() {
		const (
			legacyRepairMode      = true
			budgetAwareRepairMode = false
		)

		It("should explicitly reject in-place reboot", func() {
			err := (&CloudProvider{}).Reboot(ctx, nodeClaim, "operation-id")
			Expect(corecloudprovider.IsNodeRebootNotImplementedError(err)).To(BeTrue())
		})

		DescribeTable("should preserve repair tolerations and forceful drain bounds", func(conditionType v1.NodeConditionType, conditionStatus v1.ConditionStatus, toleration time.Duration) {
			repairCtx := coreoptions.ToContext(ctx, coretest.Options(coretest.OptionsFields{
				FeatureGates:     coretest.FeatureGates{NodeRepair: lo.ToPtr(true)},
				LegacyNodeRepair: lo.ToPtr(false),
			}))
			legacyProvider := New(nil, nil, nil, nil, nil, nil, nil, legacyRepairMode)
			Expect(legacyProvider.RepairPolicies()).To(HaveLen(4))
			Expect(legacyProvider.RepairPolicies()).To(ContainElement(corecloudprovider.RepairPolicy{
				ConditionType:      conditionType,
				ConditionStatus:    conditionStatus,
				TolerationDuration: toleration,
				Action:             corecloudprovider.ReplaceNode,
			}))

			provider := New(nil, nil, nil, nil, nil, nil, nil, budgetAwareRepairMode)
			matcher, err := health.NewRepairPolicyMatcher(repairCtx, provider)
			Expect(err).ToNot(HaveOccurred())
			Expect(matcher).ToNot(BeNil())
			now := fakeClock.Now()
			node := coretest.Node()
			node.CreationTimestamp = metav1.NewTime(now.Add(-time.Hour))
			node.Status.Conditions = []v1.NodeCondition{{
				Type:               conditionType,
				Status:             conditionStatus,
				Reason:             "UnrecognizedReason",
				LastTransitionTime: metav1.NewTime(now),
			}}
			matches := matcher.Match(node)
			Expect(matches).To(HaveLen(1))
			Expect(health.Resolve(matches, now.Add(toleration-time.Nanosecond), time.Time{}).Action).To(BeEmpty())
			result := health.Resolve(matches, now.Add(toleration), time.Time{})
			Expect(result.Action).To(Equal(corecloudprovider.ReplaceNode))
			Expect(result.Condition).To(Equal(conditionType))
			Expect(result.TerminationGracePeriod).To(Equal(lo.ToPtr(time.Duration(0))))

			node.Status.Conditions[0].Status = lo.Ternary(conditionStatus == v1.ConditionTrue, v1.ConditionFalse, v1.ConditionTrue)
			Expect(matcher.Match(node)).To(BeEmpty())
		},
			Entry("NodeReady=False", v1.NodeReady, v1.ConditionFalse, 10*time.Minute),
			Entry("NodeReady=Unknown", v1.NodeReady, v1.ConditionUnknown, 10*time.Minute),
			Entry("NodeHealthy=False", v1.NodeConditionType("kubernetes.azure.com/NodeHealthy"), v1.ConditionFalse, time.Duration(0)),
			Entry("PreemptionScheduled=True", v1.NodeConditionType(SpotConditionPreemptionScheduled), v1.ConditionTrue, time.Duration(0)),
		)

		It("should select the legacy policy set explicitly", func() {
			provider := New(nil, nil, nil, nil, nil, nil, nil, legacyRepairMode)
			policies := provider.RepairPolicies()
			Expect(policies).To(HaveLen(4))
			for _, policy := range policies {
				Expect(policy.Action).To(Equal(corecloudprovider.ReplaceNode))
				Expect(policy.ReasonRegex).To(BeEmpty())
				Expect(policy.TerminationGracePeriod).To(BeNil())
			}
		})

		It("should not construct the new matcher for legacy repair", func() {
			repairCtx := coreoptions.ToContext(ctx, coretest.Options(coretest.OptionsFields{
				FeatureGates:     coretest.FeatureGates{NodeRepair: lo.ToPtr(true)},
				LegacyNodeRepair: lo.ToPtr(true),
			}))
			provider := New(nil, nil, nil, nil, nil, nil, nil, legacyRepairMode)
			matcher, err := health.NewRepairPolicyMatcher(repairCtx, provider)
			Expect(err).ToNot(HaveOccurred())
			Expect(matcher).To(BeNil())
		})

		It("should keep returned repair policy profiles independent", func() {
			legacyProvider := New(nil, nil, nil, nil, nil, nil, nil, legacyRepairMode)
			provider := New(nil, nil, nil, nil, nil, nil, nil, budgetAwareRepairMode)
			legacyPolicies := legacyProvider.RepairPolicies()
			policies := provider.RepairPolicies()
			legacyPolicies[0].TolerationDuration = time.Hour
			*policies[0].TerminationGracePeriod = time.Minute
			Expect(legacyProvider.RepairPolicies()[0].TolerationDuration).To(Equal(10 * time.Minute))
			Expect(provider.RepairPolicies()[0].TerminationGracePeriod).To(Equal(lo.ToPtr(time.Duration(0))))
		})
	})

	// Attention: tests under "ProvisionMode = AKSScriptless" are not applicable to ProvisionMode = AKSMachineAPI option.
	// Due to different assumptions, not all tests can be shared. Add tests for AKS machine instances in a different Context/file.
	// If ProvisionMode = AKSScriptless is no longer supported, their code/tests will be replaced with ProvisionMode = AKSMachineAPI.
	Context("ProvisionMode = AKSScriptless, ManageExistingAKSMachines = false", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode:             lo.ToPtr(consts.ProvisionModeAKSScriptless),
				ManageExistingAKSMachines: lo.ToPtr(false),
			})
			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)

			azureEnv = test.NewEnvironment(ctx, env)
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore, coreoptions.FromContext(ctx).LegacyNodeRepair)

			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock, deviceallocation.NewController(env.Client), virtualpods.NewVirtualPodCache(env.Client))
		})

		AfterEach(func() {
			// Wait for any async polling goroutines to complete before resetting
			cloudProvider.WaitForInstancePromises()
			cluster.Reset()
			azureEnv.Reset(ctx)
		})

		runCapacityBufferTests(func(expectedCalls int) {
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(expectedCalls))
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
		})

		It("should reject managed GPU before creating a VM on an unsupported provision mode", func() {
			nodeClass.Spec.GPU = &v1beta1.GPU{
				Mode: lo.ToPtr(v1beta1.GPUModeDriver),
				Nvidia: &v1beta1.NvidiaGPU{
					ManagementMode: lo.ToPtr(v1beta1.ManagementModeManaged),
				},
			}
			statusController = status.NewController(env.Client, azureEnv.SubscriptionID, fake.Region, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID, options.FromContext(ctx).NetworkPolicy, options.FromContext(ctx).NetworkPlugin,
				azureEnv.CapacityReservationGroupsAPI, azureEnv.CapacityReservationsAPI, azureEnv.InstanceTypesProvider, azureEnv.UnavailableOfferingsCache, testOptions.SystemNamespace)

			ExpectApplied(ctx, env.Client, nodePool, nodeClass, nodeClaim)
			ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
			nodeClass = ExpectExists(ctx, env.Client, nodeClass)

			validationCondition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(validationCondition.IsFalse()).To(BeTrue())
			Expect(validationCondition.Reason).To(Equal(status.ManagedGPUUnsupportedProvisionMode))
			Expect(nodeClass.StatusConditions().Get(corestatus.ConditionReady).IsFalse()).To(BeTrue())

			created, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, nodeClaim)
			Expect(corecloudprovider.IsNodeClassNotReadyError(err)).To(BeTrue())
			Expect(created).To(BeNil())
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeZero())
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeZero())
		})

		It("should list nodeclaim created by the CloudProvider", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

			nodeClaims, _ := cloudProvider.List(ctx)
			Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(0))
			Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))
			queryRequest := azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Pop().Query
			Expect(*queryRequest.Query).To(Equal(instance.GetVMListQueryBuilder(azureEnv.AzureResourceGraphAPI.ResourceGroup).String()))
			Expect(nodeClaims).To(HaveLen(1))
			validateVMNodeClaim(nodeClaims[0], nodePool)
			resp, _ := azureEnv.VirtualMachinesAPI.Get(ctx, azureEnv.AzureResourceGraphAPI.ResourceGroup, nodeClaims[0].Name, nil)
			Expect(resp.VirtualMachine).ToNot(BeNil())
		})

		It("should track quota across accepted VM create and delete", func() {
			sku := fake.MakeSKU("Standard_D2_v3")
			vCPUs := lo.Must(sku.VCPU())
			azureEnv.UsageAPI.Usages.Append(&armcompute.Usage{
				Name:         &armcompute.UsageName{Value: lo.ToPtr(sku.GetFamilyName())},
				CurrentValue: lo.ToPtr(int32(100 - vCPUs)),
				Limit:        lo.ToPtr[int64](100),
			})
			Expect(azureEnv.QuotaProvider.Update(ctx)).To(Succeed())
			nodeClaim.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
				{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{sku.GetName()},
				},
				{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{karpv1.CapacityTypeOnDemand},
				},
			}
			ExpectApplied(ctx, env.Client, nodeClass, nodePool, nodeClaim)
			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)

			created, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, nodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(azureEnv.QuotaProvider.HasQuotaFor(ctx, sku)).To(BeFalse())

			retried, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, nodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(retried).ToNot(BeNil())
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

			nodeClaim.Status = created.Status
			nodeClaim.Labels = created.Labels
			nodeClaim.Annotations = created.Annotations
			// The completed delete releases quota immediately.
			Expect(cloudProvider.Delete(ctx, nodeClaim)).To(Succeed())
			Expect(azureEnv.QuotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue())

			// A subsequent NotFound remains idempotent.
			err = cloudProvider.Delete(ctx, nodeClaim)
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
			Expect(azureEnv.QuotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue())
		})

		It("should not adjust family quota for Spot VM create or delete", func() {
			spotSKU := fake.MakeSKU("Standard_D2s_v3")
			largerSKU := fake.MakeSKU("Standard_D4s_v3")
			Expect(largerSKU.GetFamilyName()).To(Equal(spotSKU.GetFamilyName()))
			azureEnv.UsageAPI.Usages.Append(&armcompute.Usage{
				Name:         &armcompute.UsageName{Value: lo.ToPtr(spotSKU.GetFamilyName())},
				CurrentValue: lo.ToPtr[int32](98),
				Limit:        lo.ToPtr[int64](100),
			})
			Expect(azureEnv.QuotaProvider.Update(ctx)).To(Succeed())
			nodeClaim.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
				{Key: v1.LabelInstanceTypeStable, Operator: v1.NodeSelectorOpIn, Values: []string{spotSKU.GetName()}},
				{Key: karpv1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{karpv1.CapacityTypeSpot}},
			}
			ExpectApplied(ctx, env.Client, nodeClass, nodePool, nodeClaim)
			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)

			created, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, nodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(created.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeSpot))
			Expect(azureEnv.QuotaProvider.HasQuotaFor(ctx, spotSKU)).To(BeTrue(), "Spot create must not consume family quota")
			Expect(azureEnv.QuotaProvider.HasQuotaFor(ctx, largerSKU)).To(BeFalse())

			// Clear any create adjustment before verifying delete independently.
			Expect(azureEnv.QuotaProvider.Update(ctx)).To(Succeed())
			nodeClaim.Status = created.Status
			nodeClaim.Labels = created.Labels
			nodeClaim.Annotations = created.Annotations
			Expect(cloudProvider.Delete(ctx, nodeClaim)).To(Succeed())
			Expect(azureEnv.QuotaProvider.HasQuotaFor(ctx, largerSKU)).To(BeFalse(), "Spot delete must not release family quota")
		})

		It("should release quota after failed VM creation cleanup", func() {
			sku := fake.MakeSKU("Standard_D2_v3")
			vCPUs := lo.Must(sku.VCPU())
			azureEnv.UsageAPI.Usages.Append(&armcompute.Usage{
				Name:         &armcompute.UsageName{Value: lo.ToPtr(sku.GetFamilyName())},
				CurrentValue: lo.ToPtr(int32(100 - vCPUs)),
				Limit:        lo.ToPtr[int64](100),
			})
			Expect(azureEnv.QuotaProvider.Update(ctx)).To(Succeed())
			delete(nodeClaim.Labels, karpv1.NodePoolLabelKey)
			nodeClaim.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
				{Key: v1.LabelInstanceTypeStable, Operator: v1.NodeSelectorOpIn, Values: []string{sku.GetName()}},
				{Key: karpv1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{karpv1.CapacityTypeOnDemand}},
			}
			ExpectApplied(ctx, env.Client, nodeClass, nodeClaim)
			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.Error.Set(&azcore.ResponseError{ErrorCode: "InternalOperationError"})

			created, err := cloudProvider.Create(ctx, nodeClaim)
			Expect(err).To(HaveOccurred())
			Expect(created).To(BeNil())
			Expect(azureEnv.QuotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue())

			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.Error.Set(nil)
			created, err = cloudProvider.Create(ctx, nodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(created).ToNot(BeNil())
			Expect(azureEnv.QuotaProvider.HasQuotaFor(ctx, sku)).To(BeFalse())
		})

		It("should retain quota when VM promise cleanup cannot delete the instance", func() {
			sku := fake.MakeSKU("Standard_D2_v3")
			vCPUs := lo.Must(sku.VCPU())
			azureEnv.UsageAPI.Usages.Append(&armcompute.Usage{
				Name:         &armcompute.UsageName{Value: lo.ToPtr(sku.GetFamilyName())},
				CurrentValue: lo.ToPtr(int32(100 - vCPUs)),
				Limit:        lo.ToPtr[int64](100),
			})
			Expect(azureEnv.QuotaProvider.Update(ctx)).To(Succeed())
			nodeClaim.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
				{Key: v1.LabelInstanceTypeStable, Operator: v1.NodeSelectorOpIn, Values: []string{sku.GetName()}},
				{Key: karpv1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{karpv1.CapacityTypeOnDemand}},
			}
			instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			instanceTypes = lo.Filter(instanceTypes, func(instanceType *corecloudprovider.InstanceType, _ int) bool {
				return instanceType.Name == sku.GetName()
			})
			Expect(instanceTypes).To(HaveLen(1))
			promise, err := azureEnv.VMInstanceProvider.BeginCreate(ctx, nodeClass, nodeClaim, instanceTypes)
			Expect(err).ToNot(HaveOccurred())
			Expect(azureEnv.QuotaProvider.HasQuotaFor(ctx, sku)).To(BeFalse())

			azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.BeginError.Set(&azcore.ResponseError{ErrorCode: "DeleteFailed"})

			err = promise.Cleanup(ctx)
			Expect(err).To(HaveOccurred())
			Expect(azureEnv.QuotaProvider.HasQuotaFor(ctx, sku)).To(BeFalse(), "failed cleanup must retain the accepted-create debit")
		})

		Context("Quota deletion metadata", func() {
			initializeQuota := func() *skewer.SKU {
				sku := fake.MakeSKU("Standard_D2_v3")
				azureEnv.UsageAPI.Usages.Append(&armcompute.Usage{
					Name:         &armcompute.UsageName{Value: lo.ToPtr(sku.GetFamilyName())},
					CurrentValue: lo.ToPtr[int32](100),
					Limit:        lo.ToPtr[int64](100),
				})
				Expect(azureEnv.QuotaProvider.Update(ctx)).To(Succeed())
				nodeClaim.Labels[v1.LabelInstanceTypeStable] = sku.GetName()
				nodeClaim.Labels[karpv1.CapacityTypeLabelKey] = karpv1.CapacityTypeOnDemand
				return sku
			}

			It("should release quota for an ordinary instance after the NodeClass adds a reservation", func() {
				sku := initializeQuota()
				nodeClaim.Annotations = map[string]string{v1beta1.AnnotationCapacityReservationGroupID: ""}
				nodeClass.Spec.CapacityReservation = &v1beta1.CapacityReservationConfiguration{GroupID: lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/capacityReservationGroups/crg")}

				err := azureEnv.VMInstanceProvider.Delete(ctx, nodeClaim, "missing")
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
				Expect(azureEnv.QuotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue())
			})

			It("should not release quota for a reserved instance after the NodeClass removes its reservation", func() {
				sku := initializeQuota()
				nodeClaim.Annotations = map[string]string{v1beta1.AnnotationCapacityReservationGroupID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/capacityReservationGroups/crg"}
				nodeClass.Spec.CapacityReservation = nil

				err := azureEnv.VMInstanceProvider.Delete(ctx, nodeClaim, "missing")
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
				Expect(azureEnv.QuotaProvider.HasQuotaFor(ctx, sku)).To(BeFalse())
			})

			It("should release quota while the NodeClass is terminating", func() {
				sku := initializeQuota()
				nodeClaim.Annotations = map[string]string{v1beta1.AnnotationCapacityReservationGroupID: ""}
				nodeClass.Finalizers = []string{test.TestingFinalizer}
				ExpectApplied(ctx, env.Client, nodeClass)
				Expect(env.Client.Delete(ctx, nodeClass)).To(Succeed())

				err := azureEnv.VMInstanceProvider.Delete(ctx, nodeClaim, "missing")
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
				Expect(azureEnv.QuotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue())
			})
		})

		It("should list nodeclaim with correct instance type even after capacity error marks offerings unavailable", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

			// Get the instance type from the created VM
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			vmSize := string(lo.FromPtr(vm.Properties.HardwareProfile.VMSize))
			Expect(vmSize).ToNot(BeEmpty())

			// Simulate a capacity error by marking all offerings for this instance type as unavailable
			for _, zone := range azureEnv.Zones() {
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", fake.MakeSKU(vmSize), zone, karpv1.CapacityTypeOnDemand)
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", fake.MakeSKU(vmSize), zone, karpv1.CapacityTypeSpot)
			}

			// List should still return the nodeclaim with the correct instance type
			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaims).To(HaveLen(1))
			validateVMNodeClaim(nodeClaims[0], nodePool)
			Expect(nodeClaims[0].Labels[v1.LabelInstanceTypeStable]).To(Equal(vmSize))
		})
		It("should return an ICE error when there are no instance types to launch", func() {
			// Specify no instance types and expect to receive a capacity error
			nodeClaim.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
				{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"doesnotexist"}, // will not match any instance types
				},
			}

			ExpectApplied(ctx, env.Client, nodePool, nodeClass, nodeClaim)
			cloudProviderMachine, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, nodeClaim)
			Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
			Expect(cloudProviderMachine).To(BeNil())
		})

		runNodeOverlayCapacityTests(vmNodeOverlayCapacityTestOptions())

		Context("AKS Machine API integration", func() {
			It("should not call writes to AKS Machine API", func() {
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
			})

			Context("AKS Machines Pool Management", func() {
				It("should handle AKS machines pool not found on each CloudProvider operation", func() {
					// First create a successful VM
					ExpectApplied(ctx, env.Client, nodeClass, nodePool)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					// cloudprovider.List should return vm nodeclaim
					nodeClaims, err := cloudProvider.List(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(nodeClaims).To(HaveLen(1))
					validateVMNodeClaim(nodeClaims[0], nodePool)

					// cloudprovider.Delete should be fine also
					err = cloudProvider.Delete(ctx, nodeClaims[0])
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})
	})

	Context("ProvisionMode = AKSScriptless, ManageExistingAKSMachines = true", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode:             lo.ToPtr(consts.ProvisionModeAKSScriptless),
				ManageExistingAKSMachines: lo.ToPtr(true),
			})
			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)

			azureEnv = test.NewEnvironment(ctx, env)
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore, coreoptions.FromContext(ctx).LegacyNodeRepair)

			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock, deviceallocation.NewController(env.Client), virtualpods.NewVirtualPodCache(env.Client))
		})

		AfterEach(func() {
			// Wait for any async polling goroutines to complete before resetting
			cloudProvider.WaitForInstancePromises()
			cluster.Reset()
			azureEnv.Reset(ctx)
		})

		It("should list nodeclaim created by the CloudProvider", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

			nodeClaims, _ := cloudProvider.List(ctx)
			Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(1)) // Expect to be called in case of existing AKS machines
			Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))
			queryRequest := azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Pop().Query
			Expect(*queryRequest.Query).To(Equal(instance.GetVMListQueryBuilder(azureEnv.AzureResourceGraphAPI.ResourceGroup).String()))
			Expect(nodeClaims).To(HaveLen(1))
			validateVMNodeClaim(nodeClaims[0], nodePool)
			resp, _ := azureEnv.VirtualMachinesAPI.Get(ctx, azureEnv.AzureResourceGraphAPI.ResourceGroup, nodeClaims[0].Name, nil)
			Expect(resp.VirtualMachine).ToNot(BeNil())
		})
		It("should return an ICE error when there are no instance types to launch", func() {
			// Specify no instance types and expect to receive a capacity error
			nodeClaim.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
				{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"doesnotexist"}, // will not match any instance types
				},
			}

			ExpectApplied(ctx, env.Client, nodePool, nodeClass, nodeClaim)
			cloudProviderMachine, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, nodeClaim)
			Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
			Expect(cloudProviderMachine).To(BeNil())
		})

		runNodeOverlayCapacityTests(vmNodeOverlayCapacityTestOptions())

		Context("AKS Machine API integration", func() {
			It("should not call writes to AKS Machine API", func() {
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
			})

			Context("AKS Machines Pool Management", func() {
				It("should handle AKS machines pool not found on each CloudProvider operation", func() {
					// First create a successful VM
					ExpectApplied(ctx, env.Client, nodeClass, nodePool)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					// cloudprovider.List should return vm nodeclaim
					nodeClaims, err := cloudProvider.List(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(nodeClaims).To(HaveLen(1))
					validateVMNodeClaim(nodeClaims[0], nodePool)

					// cloudprovider.Delete should be fine also
					err = cloudProvider.Delete(ctx, nodeClaims[0])
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})
	})
})
