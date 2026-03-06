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

package instancetype_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"

	"github.com/Azure/skewer"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/labels"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	nodeclaimutils "github.com/Azure/karpenter-provider-azure/pkg/utils/nodeclaim"
)

var ctx context.Context
var testOptions *options.Options
var stop context.CancelFunc
var env *coretest.Environment
var azureEnv, azureEnvNonZonal, azureEnvBootstrap *test.Environment
var fakeClock *clock.FakeClock
var coreProvisioner, coreProvisionerNonZonal, coreProvisionerBootstrap *provisioning.Provisioner
var cluster, clusterNonZonal, clusterBootstrap *state.Cluster
var cloudProvider, cloudProviderNonZonal, cloudProviderBootstrap *cloudprovider.CloudProvider

var fakeZone1 = utils.MakeAKSLabelZoneFromARMZone(fake.Region, "1")

func TestAzure(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)

	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx, stop = context.WithCancel(ctx)
	testOptions = test.Options()
	ctx = options.ToContext(ctx, testOptions)
	ctxBootstrap := options.ToContext(ctx, test.Options(test.OptionsFields{
		ProvisionMode: lo.ToPtr(consts.ProvisionModeBootstrappingClient),
	}))

	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...))

	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
	azureEnvBootstrap = test.NewEnvironment(ctxBootstrap, env)

	fakeClock = &clock.FakeClock{}
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
	cloudProviderNonZonal = cloudprovider.New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, azureEnvNonZonal.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider, azureEnv.InstanceTypeStore)
	cloudProviderBootstrap = cloudprovider.New(azureEnvBootstrap.InstanceTypesProvider, azureEnvBootstrap.VMInstanceProvider, azureEnvBootstrap.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvBootstrap.ImageProvider, azureEnv.InstanceTypeStore)

	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	clusterNonZonal = state.NewCluster(fakeClock, env.Client, cloudProviderNonZonal)
	clusterBootstrap = state.NewCluster(fakeClock, env.Client, cloudProviderBootstrap)
	coreProvisioner = provisioning.NewProvisioner(env.Client, events.NewRecorder(&record.FakeRecorder{}), cloudProvider, cluster, fakeClock)
	coreProvisionerNonZonal = provisioning.NewProvisioner(env.Client, events.NewRecorder(&record.FakeRecorder{}), cloudProviderNonZonal, clusterNonZonal, fakeClock)
	coreProvisionerBootstrap = provisioning.NewProvisioner(env.Client, events.NewRecorder(&record.FakeRecorder{}), cloudProviderBootstrap, clusterBootstrap, fakeClock)

	RunSpecs(t, "Provider/Azure")
}

var _ = BeforeSuite(func() {
})

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = Describe("InstanceType Provider", func() {
	var nodeClass *v1beta1.AKSNodeClass
	var nodePool *karpv1.NodePool

	BeforeEach(func() {
		// Reset testOptions and ctx in case a test edited them
		// TODO: It would be nice to find a cleaner way to edit ctx/options in these tests...
		testOptions = test.Options()
		ctx = options.ToContext(ctx, testOptions)

		nodeClass = test.AKSNodeClass()
		test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)

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

		cluster.Reset()
		clusterNonZonal.Reset()
		clusterBootstrap.Reset()
		azureEnv.Reset()
		azureEnvNonZonal.Reset()
		azureEnvBootstrap.Reset()

		// Populate the expected cluster NSG
		nsg := test.MakeNetworkSecurityGroup(options.FromContext(ctx).NodeResourceGroup, fmt.Sprintf("aks-agentpool-%s-nsg", options.FromContext(ctx).ClusterID))
		azureEnv.NetworkSecurityGroupAPI.NSGs.Store(nsg.ID, nsg)
	})

	AfterEach(func() {
		cloudProvider.WaitForInstancePromises()
		ExpectCleanedUp(ctx, env.Client)
	})

	// BootstrappingClient tests moved to pkg/cloudprovider/suite_vm_bootstrap_test.go

	// VM-specific E2E tests (Subnet/CNI, Custom DNS, CIG, VM Profile, Bootstrap, LoadBalancer)
	// moved to pkg/cloudprovider/suite_vm_bootstrap_test.go

	// "additional-tags" tests are now shared in pkg/cloudprovider/suite_features_test.go via runSharedAdditionalTagsTests

	DescribeTable("Filtering by LocalDNS",
		func(localDNSMode v1beta1.LocalDNSMode, k8sVersion string, shouldIncludeD2s, shouldIncludeD4s bool) {
			if localDNSMode != "" {
				// Create complete LocalDNS configuration with all required fields
				// Note: VnetDNS and KubeDNS overrides must contain both "." and "cluster.local" zones
				nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
					Mode: localDNSMode,
					VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{
						{
							Zone:               ".",
							QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
							Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
							ForwardDestination: v1beta1.LocalDNSForwardDestinationVnetDNS,
							ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
							MaxConcurrent:      lo.ToPtr(int32(100)),
							CacheDuration:      karpv1.MustParseNillableDuration("1h"),
							ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
							ServeStale:         v1beta1.LocalDNSServeStaleVerify,
						},
						{
							Zone:               "cluster.local",
							QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
							Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
							ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
							ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
							MaxConcurrent:      lo.ToPtr(int32(100)),
							CacheDuration:      karpv1.MustParseNillableDuration("1h"),
							ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
							ServeStale:         v1beta1.LocalDNSServeStaleVerify,
						},
					},
					KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
						{
							Zone:               ".",
							QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
							Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
							ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
							ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
							MaxConcurrent:      lo.ToPtr(int32(100)),
							CacheDuration:      karpv1.MustParseNillableDuration("1h"),
							ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
							ServeStale:         v1beta1.LocalDNSServeStaleVerify,
						},
						{
							Zone:               "cluster.local",
							QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
							Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
							ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
							ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
							MaxConcurrent:      lo.ToPtr(int32(100)),
							CacheDuration:      karpv1.MustParseNillableDuration("1h"),
							ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
							ServeStale:         v1beta1.LocalDNSServeStaleVerify,
						},
					},
				}
			}
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			if k8sVersion != "" {
				nodeClass.Status.KubernetesVersion = lo.ToPtr(k8sVersion)
			}
			ExpectApplied(ctx, env.Client, nodeClass)
			instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(instanceTypes).ShouldNot(BeEmpty())

			getName := func(instanceType *corecloudprovider.InstanceType) string { return instanceType.Name }

			if shouldIncludeD2s {
				Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D2s_v3"))),
					"Standard_D2s_v3 (2 vCPUs) should be included")
			} else {
				Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_D2s_v3"))),
					"Standard_D2s_v3 (2 vCPUs) should be excluded")
			}

			if shouldIncludeD4s {
				Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D4s_v3"))),
					"Standard_D4s_v3 (4 vCPUs) should be included")
			}
		},
		Entry("when LocalDNS is required - filters to 4+ vCPUs and 244+ MiB",
			v1beta1.LocalDNSModeRequired, "", false, true),
		Entry("when LocalDNS is preferred with k8s >= 1.35 - filters to 4+ vCPUs and 244+ MiB",
			v1beta1.LocalDNSModePreferred, "1.35.0", false, true),
		Entry("when LocalDNS is preferred with k8s < 1.35 - includes all SKUs",
			v1beta1.LocalDNSModePreferred, "1.34.0", true, true),
		Entry("when LocalDNS is disabled - includes all SKUs",
			v1beta1.LocalDNSModeDisabled, "", true, true),
		Entry("when LocalDNS is not set - includes all SKUs",
			v1beta1.LocalDNSMode(""), "", true, true),
	)

	Context("Cache invalidation with LocalDNS", func() {
		It("should return different instance type lists when LocalDNS mode changes", func() {
			// First, get instance types with LocalDNS disabled
			nodeClassDisabled := test.AKSNodeClass()
			nodeClassDisabled.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModeDisabled,
				VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{
					{
						Zone:               ".",
						QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
						Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
						ForwardDestination: v1beta1.LocalDNSForwardDestinationVnetDNS,
						ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
						MaxConcurrent:      lo.ToPtr(int32(100)),
						CacheDuration:      karpv1.MustParseNillableDuration("1h"),
						ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
						ServeStale:         v1beta1.LocalDNSServeStaleVerify,
					},
					{
						Zone:               "cluster.local",
						QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
						Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
						ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
						ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
						MaxConcurrent:      lo.ToPtr(int32(100)),
						CacheDuration:      karpv1.MustParseNillableDuration("1h"),
						ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
						ServeStale:         v1beta1.LocalDNSServeStaleVerify,
					},
				},
				KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
					{
						Zone:               ".",
						QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
						Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
						ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
						ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
						MaxConcurrent:      lo.ToPtr(int32(100)),
						CacheDuration:      karpv1.MustParseNillableDuration("1h"),
						ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
						ServeStale:         v1beta1.LocalDNSServeStaleVerify,
					},
					{
						Zone:               "cluster.local",
						QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
						Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
						ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
						ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
						MaxConcurrent:      lo.ToPtr(int32(100)),
						CacheDuration:      karpv1.MustParseNillableDuration("1h"),
						ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
						ServeStale:         v1beta1.LocalDNSServeStaleVerify,
					},
				},
			}
			ExpectApplied(ctx, env.Client, nodeClassDisabled)
			instanceTypesDisabled, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClassDisabled)
			Expect(err).ToNot(HaveOccurred())

			// Now get instance types with LocalDNS required
			nodeClassEnabled := test.AKSNodeClass()
			nodeClassEnabled.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModeRequired,
				VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{
					{
						Zone:               ".",
						QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
						Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
						ForwardDestination: v1beta1.LocalDNSForwardDestinationVnetDNS,
						ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
						MaxConcurrent:      lo.ToPtr(int32(100)),
						CacheDuration:      karpv1.MustParseNillableDuration("1h"),
						ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
						ServeStale:         v1beta1.LocalDNSServeStaleVerify,
					},
					{
						Zone:               "cluster.local",
						QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
						Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
						ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
						ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
						MaxConcurrent:      lo.ToPtr(int32(100)),
						CacheDuration:      karpv1.MustParseNillableDuration("1h"),
						ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
						ServeStale:         v1beta1.LocalDNSServeStaleVerify,
					},
				},
				KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
					{
						Zone:               ".",
						QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
						Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
						ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
						ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
						MaxConcurrent:      lo.ToPtr(int32(100)),
						CacheDuration:      karpv1.MustParseNillableDuration("1h"),
						ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
						ServeStale:         v1beta1.LocalDNSServeStaleVerify,
					},
					{
						Zone:               "cluster.local",
						QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
						Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
						ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
						ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
						MaxConcurrent:      lo.ToPtr(int32(100)),
						CacheDuration:      karpv1.MustParseNillableDuration("1h"),
						ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
						ServeStale:         v1beta1.LocalDNSServeStaleVerify,
					},
				},
			}
			ExpectApplied(ctx, env.Client, nodeClassEnabled)
			instanceTypesEnabled, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClassEnabled)
			Expect(err).ToNot(HaveOccurred())

			// The lists should be different sizes
			Expect(len(instanceTypesEnabled)).To(BeNumerically("<", len(instanceTypesDisabled)),
				"LocalDNS Required should filter out small SKUs")

			getName := func(instanceType *corecloudprovider.InstanceType) string { return instanceType.Name }

			// Verify that small SKUs (< 4 vCPUs) are present when disabled but absent when enabled
			Expect(instanceTypesDisabled).Should(ContainElement(WithTransform(getName, Equal("Standard_D2s_v3"))),
				"Standard_D2s_v3 (2 vCPUs) should be included when LocalDNS is disabled")
			Expect(instanceTypesEnabled).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_D2s_v3"))),
				"Standard_D2s_v3 (2 vCPUs) should be excluded when LocalDNS is required")

			// Verify that large SKUs (>= 4 vCPUs) are present in both
			Expect(instanceTypesDisabled).Should(ContainElement(WithTransform(getName, Equal("Standard_D4s_v3"))),
				"Standard_D4s_v3 (4 vCPUs) should be included when LocalDNS is disabled")
			Expect(instanceTypesEnabled).Should(ContainElement(WithTransform(getName, Equal("Standard_D4s_v3"))),
				"Standard_D4s_v3 (4 vCPUs) should be included when LocalDNS is required")
		})
	})

	Context("Ephemeral Disk", func() {
		var originalOptions *options.Options
		BeforeEach(func() {
			originalOptions = options.FromContext(ctx)
			ctx = options.ToContext(
				ctx,
				test.Options(test.OptionsFields{
					UseSIG: lo.ToPtr(true),
				}))
		})

		AfterEach(func() {
			ctx = options.ToContext(ctx, originalOptions)
		})

		// FindMaxEphemeralSizeGBAndPlacement unit tests migrated to ephemeral_disk_test.go
		// using standard Go table-driven testing for better developer experience
		Context("Placement", func() {
			It("should prefer NVMe disk if supported for ephemeral", func() {
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D128ds_v6"},
					},
				})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
				Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement)).To(Equal(armcompute.DiffDiskPlacementNvmeDisk))
			})
			It("should not select NVMe ephemeral disk placement if the sku has an nvme disk, supports ephemeral os disk, but doesnt support NVMe placement", func() {
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_NC24ads_A100_v4"},
					},
				})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
				Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement)).ToNot(Equal(armcompute.DiffDiskPlacementNvmeDisk))
			})
			It("should prefer cache disk placement when both cache and temp disk support ephemeral and fit the default 128GB threshold", func() {
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D64s_v3"},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
				Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement)).To(Equal(armcompute.DiffDiskPlacementCacheDisk))
			})
			It("should select managed disk if cache disk is too small but temp disk supports ephemeral and fits osDiskSizeGB to have parity with the AKS Nodepool API", func() {
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_B20ms"},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).To(BeNil())
			})
		})

		// 5 ephemeral disk shared tests are now in pkg/cloudprovider/suite_features_test.go via runSharedEphemeralDiskTests

		It("should select NvmeDisk for v6 skus with maxNvmeDiskSize > 0", func() {
			nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: v1.NodeSelectorRequirement{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"Standard_D128ds_v6"},
				}})
			nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](100)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm).NotTo(BeNil())

			Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
			Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement)).To(Equal(armcompute.DiffDiskPlacementNvmeDisk))
		})
	}) // End Ephemeral Disk

	// VM-specific E2E tests moved to pkg/cloudprovider/suite_vm_bootstrap_test.go
	// Including: MaxPods calculation tests for different network plugins

	Context("MaxPods", func() {
		It("should set pods equal to expected default MaxPods for network plugin none", func() {
			ctx = options.ToContext(
				ctx,
				test.Options(test.OptionsFields{
					NetworkPlugin: lo.ToPtr("none"),
				}),
			)
			Expect(options.FromContext(ctx).NetworkPlugin).To(Equal("none"))

			instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
			Expect(err).NotTo(HaveOccurred())
			ExpectCapacityPodsToMatchMaxPods(instanceTypes, int32(250))
		})
		It("should set pods equal to expected default MaxPods for unsupported cni", func() {
			ctx = options.ToContext(
				ctx,
				test.Options(test.OptionsFields{
					NetworkPlugin: lo.ToPtr("kubenet"),
				}),
			)
			Expect(options.FromContext(ctx).NetworkPlugin).To(Equal("kubenet"))

			instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
			Expect(err).NotTo(HaveOccurred())
			ExpectCapacityPodsToMatchMaxPods(instanceTypes, int32(110))
		})
	})

	Context("Basic", func() {
		var instanceTypes corecloudprovider.InstanceTypes
		var err error
		BeforeEach(func() {
			// disable VM memory overhead for simpler capacity testing
			ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
				VMMemoryOverheadPercent: lo.ToPtr[float64](0),
			}))
			instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should have all the requirements on every sku", func() {
			for _, instanceType := range instanceTypes {
				reqs := instanceType.Requirements

				Expect(reqs.Has(v1.LabelArchStable)).To(BeTrue())
				Expect(reqs.Has(v1.LabelOSStable)).To(BeTrue())
				Expect(reqs.Has(v1.LabelInstanceTypeStable)).To(BeTrue())

				Expect(reqs.Has(v1beta1.LabelSKUName)).To(BeTrue())

				Expect(reqs.Has(v1beta1.LabelSKUStoragePremiumCapable)).To(BeTrue())
				Expect(reqs.Has(v1beta1.LabelSKUAcceleratedNetworking)).To(BeTrue())
				Expect(reqs.Has(v1beta1.LabelSKUHyperVGeneration)).To(BeTrue())
				Expect(reqs.Has(v1beta1.LabelSKUStorageEphemeralOSMaxSize)).To(BeTrue())
			}
		})
		It("boolean requirements should have a value, either 'true' or 'false'", func() {
			for _, instanceType := range instanceTypes {
				reqs := instanceType.Requirements
				Expect(reqs.Get(v1beta1.LabelSKUStoragePremiumCapable).Values()).To(HaveLen(1))
				Expect(reqs.Get(v1beta1.LabelSKUStoragePremiumCapable).Values()[0]).To(SatisfyAny(Equal("true"), Equal("false")))
				Expect(reqs.Get(v1beta1.LabelSKUAcceleratedNetworking).Values()).To(HaveLen(1))
				Expect(reqs.Get(v1beta1.LabelSKUAcceleratedNetworking).Values()[0]).To(SatisfyAny(Equal("true"), Equal("false")))
			}
		})

		It("should have all compute capacity", func() {
			for _, instanceType := range instanceTypes {
				capList := instanceType.Capacity
				Expect(capList).To(HaveKey(v1.ResourceCPU))
				Expect(capList).To(HaveKey(v1.ResourceMemory))
				Expect(capList).To(HaveKey(v1.ResourcePods))
				Expect(capList).To(HaveKey(v1.ResourceEphemeralStorage))
			}
		})

		// TODO: Is this stuff really about Provider List? Feels like no, should we put it elsewhere?
		type WellKnownLabelEntry struct {
			Name      string
			Label     string
			ValueFunc func() string
			SetupFunc func()
			// ExpectedInKubeletLabels indicates if we expect to see this in the KUBELET_NODE_LABELS section of the custom script extension.
			// If this is false it means that Karpenter will not set it on the node via KUBELET_NODE_LABELS.
			// It does NOT mean that it will not be on the resulting Node object in a real cluster, as it may be written by another process.
			// We expect that if ExpectedOnNode is set, ExpectedInKubeletLabels is also set.
			ExpectedInKubeletLabels bool
			// ExpectedOnNode indicates if we expect to see this on the node.
			// If this is false it means is that Karpenter will not set it on the node directly via kube-apiserver.
			// It does NOT mean that it will not be on the resulting Node object in a real cluster, as it may be written as part of KUBELET_NODE_LABELS (see above)
			// or by another process. We're asserting on this distinction currently because it helps clarify who is doing what
			ExpectedOnNode bool
		}

		// TODO: Is this stuff really about Provider List? Feels like no, should we put it elsewhere?
		entries := []WellKnownLabelEntry{
			// Well known
			{Name: v1.LabelTopologyRegion, Label: v1.LabelTopologyRegion, ValueFunc: func() string { return fake.Region }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: karpv1.NodePoolLabelKey, Label: karpv1.NodePoolLabelKey, ValueFunc: func() string { return nodePool.Name }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1.LabelTopologyZone, Label: v1.LabelTopologyZone, ValueFunc: func() string { return fakeZone1 }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1.LabelInstanceTypeStable, Label: v1.LabelInstanceTypeStable, ValueFunc: func() string { return "Standard_NC24ads_A100_v4" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1.LabelOSStable, Label: v1.LabelOSStable, ValueFunc: func() string { return "linux" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1.LabelArchStable, Label: v1.LabelArchStable, ValueFunc: func() string { return "amd64" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: karpv1.CapacityTypeLabelKey, Label: karpv1.CapacityTypeLabelKey, ValueFunc: func() string { return "on-demand" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			// Well Known to AKS
			{Name: v1beta1.LabelSKUName, Label: v1beta1.LabelSKUName, ValueFunc: func() string { return "Standard_NC24ads_A100_v4" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.LabelSKUFamily, Label: v1beta1.LabelSKUFamily, ValueFunc: func() string { return "N" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.LabelSKUSeries, Label: v1beta1.LabelSKUSeries, ValueFunc: func() string { return "NCads_v4" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.LabelSKUVersion, Label: v1beta1.LabelSKUVersion, ValueFunc: func() string { return "4" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.LabelSKUStorageEphemeralOSMaxSize, Label: v1beta1.LabelSKUStorageEphemeralOSMaxSize, ValueFunc: func() string { return "429" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.LabelSKUAcceleratedNetworking, Label: v1beta1.LabelSKUAcceleratedNetworking, ValueFunc: func() string { return "true" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.LabelSKUStoragePremiumCapable, Label: v1beta1.LabelSKUStoragePremiumCapable, ValueFunc: func() string { return "true" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.LabelSKUGPUName, Label: v1beta1.LabelSKUGPUName, ValueFunc: func() string { return "A100" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.LabelSKUGPUManufacturer, Label: v1beta1.LabelSKUGPUManufacturer, ValueFunc: func() string { return "nvidia" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.LabelSKUGPUCount, Label: v1beta1.LabelSKUGPUCount, ValueFunc: func() string { return "1" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.LabelSKUCPU, Label: v1beta1.LabelSKUCPU, ValueFunc: func() string { return "24" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.LabelSKUMemory, Label: v1beta1.LabelSKUMemory, ValueFunc: func() string { return "8192" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			// AKS domain
			{Name: v1beta1.AKSLabelCPU, Label: v1beta1.AKSLabelCPU, ValueFunc: func() string { return "24" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.AKSLabelMemory, Label: v1beta1.AKSLabelMemory, ValueFunc: func() string { return "8192" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.AKSLabelMode + "=user", Label: v1beta1.AKSLabelMode, ValueFunc: func() string { return "user" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.AKSLabelMode + "=system", Label: v1beta1.AKSLabelMode, ValueFunc: func() string { return "system" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.AKSLabelScaleSetPriority + "=regular", Label: v1beta1.AKSLabelScaleSetPriority, ValueFunc: func() string { return "regular" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.AKSLabelScaleSetPriority + "=spot", Label: v1beta1.AKSLabelScaleSetPriority, ValueFunc: func() string { return "spot" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{Name: v1beta1.AKSLabelOSSKU, Label: v1beta1.AKSLabelOSSKU, ValueFunc: func() string { return "Ubuntu" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
			{
				Name:  v1beta1.AKSLabelFIPSEnabled,
				Label: v1beta1.AKSLabelFIPSEnabled,
				// Needs special setup because it only works on FIPS
				SetupFunc: func() {
					testOptions.UseSIG = true
					ctx = options.ToContext(ctx, testOptions)

					nodeClass.Spec.FIPSMode = &v1beta1.FIPSModeFIPS
					nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
					test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
				},
				ValueFunc:               func() string { return "true" },
				ExpectedInKubeletLabels: true,
				ExpectedOnNode:          true,
			},
			// Deprecated Labels -- note that these are not expected in kubelet labels or on the node.
			// They are written by CloudProvider so don't need to be sent to kubelet, and they aren't required on the node object because Karpenter does a mapping from
			// the new labels to the old labels for compatibility.
			{Name: v1.LabelFailureDomainBetaRegion, Label: v1.LabelFailureDomainBetaRegion, ValueFunc: func() string { return fake.Region }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
			{Name: v1.LabelFailureDomainBetaZone, Label: v1.LabelFailureDomainBetaZone, ValueFunc: func() string { return fakeZone1 }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
			{Name: "beta.kubernetes.io/arch", Label: "beta.kubernetes.io/arch", ValueFunc: func() string { return "amd64" }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
			{Name: "beta.kubernetes.io/os", Label: "beta.kubernetes.io/os", ValueFunc: func() string { return "linux" }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
			{Name: v1.LabelInstanceType, Label: v1.LabelInstanceType, ValueFunc: func() string { return "Standard_NC24ads_A100_v4" }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
			{Name: "topology.disk.csi.azure.com/zone", Label: "topology.disk.csi.azure.com/zone", ValueFunc: func() string { return fakeZone1 }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
			// Unsupported labels
			{Name: v1.LabelWindowsBuild, Label: v1.LabelWindowsBuild, ValueFunc: func() string { return "window" }, ExpectedInKubeletLabels: true, ExpectedOnNode: false},
			// Cluster Label
			{Name: v1beta1.AKSLabelCluster, Label: v1beta1.AKSLabelCluster, ValueFunc: func() string { return "test-resourceGroup" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		}

		It("should support individual instance type labels (when all pods scheduled at once)", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			var podDetails []struct {
				pod   *v1.Pod
				entry WellKnownLabelEntry
			}
			for _, item := range entries {
				if item.SetupFunc != nil {
					continue // can't support nonstandard setup here as we're putting all labels on one pod
				}
				podDetails = append(podDetails, struct {
					pod   *v1.Pod
					entry WellKnownLabelEntry
				}{
					pod:   coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{item.Label: item.ValueFunc()}}),
					entry: item,
				})
			}
			pods := lo.Map(
				podDetails,
				func(detail struct {
					pod   *v1.Pod
					entry WellKnownLabelEntry
				}, _ int) *v1.Pod {
					return detail.pod
				})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pods...)

			// Collect all the VMs we provisioned
			vmInputs := map[string]*fake.VirtualMachineCreateOrUpdateInput{}

			for vmInput := range azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.All() {
				vmInputs[*vmInput.VM.Name] = vmInput
			}

			for _, detail := range podDetails {
				key := lo.Keys(detail.pod.Spec.NodeSelector)[0]
				node := ExpectScheduled(ctx, env.Client, detail.pod)
				if detail.entry.ExpectedOnNode {
					Expect(node.Labels[key]).To(Equal(detail.pod.Spec.NodeSelector[key]))
				} else {
					Expect(node.Labels).ToNot(HaveKey(key))
				}

				// Get the VM creation input and decode custom data
				// Extract the vm name from the provider ID
				vmName, err := nodeclaimutils.GetVMName(node.Spec.ProviderID)
				Expect(err).ToNot(HaveOccurred())

				vm := vmInputs[vmName].VM
				if detail.entry.ExpectedInKubeletLabels {
					ExpectKubeletNodeLabelsInCustomData(&vm, detail.entry.Label, detail.entry.ValueFunc())
				} else {
					ExpectKubeletNodeLabelsNotInCustomData(&vm, detail.entry.Label, detail.entry.ValueFunc())
				}
			}
		})

		DescribeTable(
			"should support individual instance type labels (when all pods scheduled individually)",
			func(item WellKnownLabelEntry) {
				if item.SetupFunc != nil {
					item.SetupFunc()
				}

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				value := item.ValueFunc()

				pod := coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{item.Label: value}})
				// Simulate multiple scheduling passes before final binding, this ensures that when real scheduling happens we won't
				// end up with a new node for each scheduling attempt
				if item.Label != v1.LabelWindowsBuild { // TODO: special case right now as we don't support it
					bindings := []Bindings{}
					for range 3 {
						bindings = append(bindings, ExpectProvisionedNoBinding(ctx, env.Client, clusterBootstrap, cloudProviderBootstrap, coreProvisionerBootstrap, pod))
					}
					for i := range len(bindings) {
						Expect(lo.Values(bindings[i])).ToNot(BeEmpty())
						Expect(lo.Values(bindings[i])[0].Node.Name).To(Equal(lo.Values(bindings[0])[0].Node.Name), "expected all bindings to have the same node name")
					}
				}
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)

				if item.ExpectedOnNode {
					Expect(node.Labels[item.Label]).To(Equal(value))
				} else {
					Expect(node.Labels).ToNot(HaveKey(item.Label))
				}

				// Get the VM creation input and decode custom data
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vmInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				vm := vmInput.VM
				if item.ExpectedInKubeletLabels {
					ExpectKubeletNodeLabelsInCustomData(&vm, item.Label, value)
				} else {
					ExpectKubeletNodeLabelsNotInCustomData(&vm, item.Label, value)
				}
			},
			lo.Map(entries, func(item WellKnownLabelEntry, _ int) TableEntry {
				return Entry(item.Name, item)
			}),
		)

		DescribeTable(
			"should support individual instance type labels (when all pods scheduled individually) on bootstrap API",
			func(item WellKnownLabelEntry) {
				if item.SetupFunc != nil {
					item.SetupFunc()
				}

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				value := item.ValueFunc()

				pod := coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{item.Label: value}})
				// Simulate multiple scheduling passes before final binding, this ensures that when real scheduling happens we won't
				// end up with a new node for each scheduling attempt
				if item.Label != v1.LabelWindowsBuild { // TODO: special case right now as we don't support it
					bindings := []Bindings{}
					for range 3 {
						bindings = append(bindings, ExpectProvisionedNoBinding(ctx, env.Client, clusterBootstrap, cloudProviderBootstrap, coreProvisionerBootstrap, pod))
					}
					for i := range len(bindings) {
						Expect(lo.Values(bindings[i])).ToNot(BeEmpty())
						Expect(lo.Values(bindings[i])[0].Node.Name).To(Equal(lo.Values(bindings[0])[0].Node.Name), "expected all bindings to have the same node name")
					}
				}
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, clusterBootstrap, cloudProviderBootstrap, coreProvisionerBootstrap, azureEnvBootstrap, pod)

				node := ExpectScheduled(ctx, env.Client, pod)

				if item.ExpectedOnNode {
					Expect(node.Labels[item.Label]).To(Equal(value))
				} else {
					Expect(node.Labels).ToNot(HaveKey(item.Label))
				}

				// Get the bootstrap API input
				Expect(azureEnvBootstrap.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Len()).To(Equal(1))
				bootstrapInput := azureEnvBootstrap.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Pop()
				if item.ExpectedInKubeletLabels {
					Expect(bootstrapInput.Params.ProvisionProfile.CustomNodeLabels).To(HaveKeyWithValue(item.Label, value))
				} else {
					Expect(bootstrapInput.Params.ProvisionProfile.CustomNodeLabels).ToNot(HaveKeyWithValue(item.Label, value))
				}
			},
			lo.Map(entries, func(item WellKnownLabelEntry, _ int) TableEntry {
				return Entry(item.Name, item)
			}),
		)

		It("entries should cover every WellKnownLabel", func() {
			expectedLabels := append(karpv1.WellKnownLabels.UnsortedList(), lo.Keys(karpv1.NormalizedLabels)...)
			Expect(lo.Map(entries, func(item WellKnownLabelEntry, _ int) string { return item.Label })).To(ContainElements(expectedLabels))
		})

		nonSchedulableLabels := map[string]string{
			labels.AKSLabelRole:                     "agent",
			v1beta1.AKSLabelKubeletIdentityClientID: test.Options().KubeletIdentityClientID,
			"kubernetes.azure.com/mode":             "user", // TODO: Will become a WellKnownLabel soon
			//We expect the vnetInfoLabels because we're simulating network plugin Azure by default and they are included there
			labels.AKSLabelSubnetName:          "aks-subnet",
			labels.AKSLabelVNetGUID:            test.Options().VnetGUID,
			labels.AKSLabelAzureCNIOverlay:     strconv.FormatBool(true),
			labels.AKSLabelPodNetworkType:      consts.NetworkPluginModeOverlay,
			karpv1.NodeDoNotSyncTaintsLabelKey: "true",
		}

		It("should write other (non-schedulable) labels to kubelet", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Not checking on the node as not all these labels are expected there (via Karpenter setting them, they'll get there via kubelet)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vmInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			vm := vmInput.VM
			for key, value := range nonSchedulableLabels {
				ExpectKubeletNodeLabelsInCustomData(&vm, key, value)
			}
		})

		DescribeTable("should not write restricted labels to kubelet, but should write allowed labels", func(domain string, allowed bool) {
			nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
				{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: domain + "/team", Operator: v1.NodeSelectorOpExists}},
				{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: domain + "/custom-label", Operator: v1.NodeSelectorOpExists}},
				{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: "subdomain." + domain + "/custom-label", Operator: v1.NodeSelectorOpExists}},
			}

			nodeSelector := map[string]string{
				domain + "/team":                        "team-1",
				domain + "/custom-label":                "custom-value",
				"subdomain." + domain + "/custom-label": "custom-value",
			}

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: nodeSelector})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			node := ExpectScheduled(ctx, env.Client, pod)

			// Not checking on the node as not all these labels are expected there (via Karpenter setting them, they'll get there via kubelet)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vmInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			vm := vmInput.VM

			// Ensure that the requirements/labels specified above are propagated onto the node and that it didn't do so via kubelet labels
			for k, v := range nodeSelector {
				Expect(node.Labels).To(HaveKeyWithValue(k, v))
				if allowed {
					ExpectKubeletNodeLabelsInCustomData(&vm, k, v)
				} else {
					ExpectKubeletNodeLabelsNotInCustomData(&vm, k, v)
				}
			}
		},
			Entry("node-restriction.kubernetes.io", "node-restriction.kubernetes.io", false),
			Entry("node.kubernetes.io", "node.kubernetes.io", true),
		)

		It("should write other (non-schedulable) labels to kubelet on bootstrap API", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, clusterBootstrap, cloudProviderBootstrap, coreProvisionerBootstrap, azureEnvBootstrap, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Not checking on the node as not all these labels are expected there (via Karpenter setting them, they'll get there via kubelet)

			Expect(azureEnvBootstrap.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Len()).To(Equal(1))
			bootstrapInput := azureEnvBootstrap.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Pop()
			for key, value := range nonSchedulableLabels {
				Expect(bootstrapInput.Params.ProvisionProfile.CustomNodeLabels).To(HaveKeyWithValue(key, value))
			}
		})

		It("should propagate all values to requirements from skewer", func() {
			var gpuNode *corecloudprovider.InstanceType
			var normalNode *corecloudprovider.InstanceType
			for _, instanceType := range instanceTypes {
				if instanceType.Name == "Standard_D2_v2" {
					normalNode = instanceType
				}
				// #nosec G101
				if instanceType.Name == "Standard_NC24ads_A100_v4" {
					gpuNode = instanceType
				}
			}

			Expect(normalNode.Name).To(Equal("Standard_D2_v2"))
			Expect(gpuNode.Name).To(Equal("Standard_NC24ads_A100_v4"))

			Expect(normalNode.Requirements.Get(v1beta1.LabelSKUName).Values()).To(ConsistOf("Standard_D2_v2"))
			Expect(gpuNode.Requirements.Get(v1beta1.LabelSKUName).Values()).To(ConsistOf("Standard_NC24ads_A100_v4"))

			Expect(normalNode.Requirements.Get(v1beta1.LabelSKUHyperVGeneration).Values()).To(ConsistOf(v1beta1.HyperVGenerationV1))
			Expect(gpuNode.Requirements.Get(v1beta1.LabelSKUHyperVGeneration).Values()).To(ConsistOf(v1beta1.HyperVGenerationV2))

			Expect(normalNode.Requirements.Get(v1beta1.LabelSKUVersion).Values()).To(ConsistOf("2"))
			Expect(gpuNode.Requirements.Get(v1beta1.LabelSKUVersion).Values()).To(ConsistOf("4"))

			// CPU (requirements and capacity)
			Expect(normalNode.Requirements.Get(v1beta1.LabelSKUCPU).Values()).To(ConsistOf("2"))
			Expect(normalNode.Capacity.Cpu().Value()).To(Equal(int64(2)))
			Expect(gpuNode.Requirements.Get(v1beta1.LabelSKUCPU).Values()).To(ConsistOf("24"))
			Expect(gpuNode.Capacity.Cpu().Value()).To(Equal(int64(24)))

			// Memory (requirements and capacity)
			Expect(normalNode.Requirements.Get(v1beta1.LabelSKUMemory).Values()).To(ConsistOf(fmt.Sprint(7 * 1024))) // 7GiB in MiB
			Expect(normalNode.Capacity.Memory().Value()).To(Equal(int64(7 * 1024 * 1024 * 1024)))                    // 7GiB in bytes
			Expect(gpuNode.Requirements.Get(v1beta1.LabelSKUMemory).Values()).To(ConsistOf(fmt.Sprint(220 * 1024)))  // 220GiB in MiB
			Expect(gpuNode.Capacity.Memory().Value()).To(Equal(int64(220 * 1024 * 1024 * 1024)))                     // 220GiB in bytes

			// GPU -- Number of GPUs
			gpuQuantity, ok := gpuNode.Capacity["nvidia.com/gpu"]
			Expect(ok).To(BeTrue(), "Expected nvidia.com/gpu to be present in capacity")
			Expect(gpuQuantity.Value()).To(Equal(int64(1)))

			gpuQuantityNonGPU, ok := normalNode.Capacity["nvidia.com/gpu"]
			Expect(ok).To(BeTrue(), "Expected nvidia.com/gpu to be present in capacity, and be zero")
			Expect(gpuQuantityNonGPU.Value()).To(Equal(int64(0)))
		})
	})
})

// KubeReservedResources unit tests migrated to kube_reserved_test.go
// using standard Go table-driven testing for better developer experience


func ExpectKubeletFlagsPassed(customData string) string {
	GinkgoHelper()
	return customData[strings.Index(customData, "KUBELET_FLAGS=")+len("KUBELET_FLAGS=") : strings.Index(customData, "KUBELET_NODE_LABELS")]
}

func ExpectKubeletNodeLabelsPassed(customData string) string {
	GinkgoHelper()
	startIdx := strings.Index(customData, "KUBELET_NODE_LABELS=") + len("KUBELET_NODE_LABELS=")
	endIdx := strings.Index(customData[startIdx:], "\n")
	if endIdx == -1 {
		// If no newline found, take to the end
		return customData[startIdx:]
	}
	return customData[startIdx : startIdx+endIdx]
}

func ExpectCapacityPodsToMatchMaxPods(instanceTypes []*corecloudprovider.InstanceType, expectedMaxPods int32) {
	GinkgoHelper()
	expected := int64(expectedMaxPods)
	for _, inst := range instanceTypes {
		pods, found := inst.Capacity[v1.ResourcePods]
		Expect(found).To(BeTrue(), "resource pods not found for instance")
		podsCount, ok := pods.AsInt64()
		Expect(ok).To(BeTrue(), "failed to convert pods capacity to int64")
		Expect(podsCount).To(Equal(expected), "pods capacity does not match expected value")
	}
}

func SkewerSKU(skuName string) *skewer.SKU {
	data := fake.ResourceSkus["southcentralus"]
	// Note we could do a more efficient lookup if this data
	// was in a map by skuname, but with less than 20 skus linear search rather than O(1) is fine.
	for _, sku := range data {
		if lo.FromPtr(sku.Name) == skuName {
			return &skewer.SKU{
				Name:         sku.Name,
				Capabilities: sku.Capabilities,
				Locations:    sku.Locations,
				Family:       sku.Family,
				ResourceType: sku.ResourceType,
			}
		}
	}
	return nil
}

func ExpectKubeletNodeLabelsInCustomData(vm *armcompute.VirtualMachine, key string, value string) {
	GinkgoHelper()

	Expect(vm.Properties).ToNot(BeNil())
	Expect(vm.Properties.OSProfile).ToNot(BeNil())
	Expect(vm.Properties.OSProfile.CustomData).ToNot(BeNil())

	customData := *vm.Properties.OSProfile.CustomData
	Expect(customData).ToNot(BeNil())

	decodedBytes, err := base64.StdEncoding.DecodeString(customData)
	Expect(err).To(Succeed())
	decodedString := string(decodedBytes[:])

	// Extract and check KUBELET_NODE_LABELS contains the expected label
	kubeletNodeLabels := ExpectKubeletNodeLabelsPassed(decodedString)
	Expect(kubeletNodeLabels).To(ContainSubstring(fmt.Sprintf("%s=%s", key, value)))
}

func ExpectKubeletNodeLabelsNotInCustomData(vm *armcompute.VirtualMachine, key string, value string) {
	GinkgoHelper()

	Expect(vm.Properties).ToNot(BeNil())
	Expect(vm.Properties.OSProfile).ToNot(BeNil())
	Expect(vm.Properties.OSProfile.CustomData).ToNot(BeNil())

	customData := *vm.Properties.OSProfile.CustomData
	Expect(customData).ToNot(BeNil())

	decodedBytes, err := base64.StdEncoding.DecodeString(customData)
	Expect(err).To(Succeed())
	decodedString := string(decodedBytes[:])

	// Extract and check KUBELET_NODE_LABELS contains the expected label
	kubeletNodeLabels := ExpectKubeletNodeLabelsPassed(decodedString)
	Expect(kubeletNodeLabels).ToNot(ContainSubstring(fmt.Sprintf("%s=%s", key, value)))
}
