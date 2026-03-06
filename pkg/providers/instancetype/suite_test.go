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
	"strings"
	"testing"

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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
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

	BeforeEach(func() {
		// Reset testOptions and ctx in case a test edited them
		// TODO: It would be nice to find a cleaner way to edit ctx/options in these tests...
		testOptions = test.Options()
		ctx = options.ToContext(ctx, testOptions)

		nodeClass = test.AKSNodeClass()
		test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)

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

	// E2E provisioning tests (ProvisionMode = BootstrappingClient, ProvisionMode = AKSScriptless,
	// Basic provisioning label tests, etc.) have been moved to
	// pkg/cloudprovider/suite_instancetype_e2e_test.go

	Context("ProvisionMode = AKSScriptless", func() {
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

			// FindMaxEphemeralSizeGBAndPlacement tests have been moved to
			// ephemeral_disk_test.go (table-driven)
			// Ephemeral Disk Placement E2E tests have been moved to
			// pkg/cloudprovider/suite_instancetype_e2e_test.go
		})

		// Zone-aware provisioning, CloudProvider Create Error Cases, and Unavailable Offerings
		// tests are now shared in pkg/cloudprovider/suite_offerings_test.go via runShared*Tests
	})

	Context("Provider List", func() {
		Context("Filtering in InstanceType", func() {
			var instanceTypes corecloudprovider.InstanceTypes
			var err error
			getName := func(instanceType *corecloudprovider.InstanceType) string { return instanceType.Name }

			BeforeEach(func() {
				instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
			})

			It("should not include SKUs marked as restricted", func() {
				isRestricted := func(instanceType *corecloudprovider.InstanceType) bool {
					return instancetype.AKSRestrictedVMSizes.Has(instanceType.Name)
				}
				Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(isRestricted, Equal(true))))
				Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(isRestricted, Equal(true))))
			})
			It("should not include SKUs with constrained CPUs, but include unconstrained ones", func() {
				Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_M8-2ms"))))
				Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D2_v2"))))
			})
			It("should not include confidential SKUs", func() {
				Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_DC8s_v3"))))
			})
			It("should not include SKUs without compatible image", func() {
				Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_D2as_v6"))))
			})
		})
		Context("Filtering GPU SKUs AzureLinux", func() {
			var instanceTypes corecloudprovider.InstanceTypes
			var err error
			getName := func(instanceType *corecloudprovider.InstanceType) string { return instanceType.Name }

			BeforeEach(func() {
				nodeClassAZLinux := test.AKSNodeClass()
				nodeClassAZLinux.Spec.ImageFamily = lo.ToPtr("AzureLinux")
				ExpectApplied(ctx, env.Client, nodeClassAZLinux)
				instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClassAZLinux)
				Expect(err).ToNot(HaveOccurred())
			})

			It("should not include AKSUbuntu GPU SKUs in list results", func() {
				Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_NC24ads_A100_v4"))))
			})
			It("should include AKSUbuntu GPU SKUs in list results", func() {
				Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_NC16as_T4_v3"))))
			})
		})

		Context("Filtering by Encryption at Host", func() {
			var instanceTypes corecloudprovider.InstanceTypes
			var err error
			getName := func(instanceType *corecloudprovider.InstanceType) string { return instanceType.Name }

			Context("when encryption at host is enabled", func() {
				BeforeEach(func() {
					nodeClassWithEncryption := test.AKSNodeClass()
					if nodeClassWithEncryption.Spec.Security == nil {
						nodeClassWithEncryption.Spec.Security = &v1beta1.Security{}
					}
					nodeClassWithEncryption.Spec.Security.EncryptionAtHost = lo.ToPtr(true)
					ExpectApplied(ctx, env.Client, nodeClassWithEncryption)
					instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClassWithEncryption)
					Expect(err).ToNot(HaveOccurred())
				})

				It("should only include SKUs that support encryption at host", func() {
					// Standard_D2_v2 does not support encryption at host, so it should be filtered out
					Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_D2_v2"))))
					// Standard_D2s_v3 supports encryption at host, so it should be included
					Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D2s_v3"))))
					// Standard_D2_v5 supports encryption at host, so it should be included
					Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D2_v5"))))
				})
			})

			Context("when encryption at host is disabled or not set", func() {
				It("should include SKUs regardless of encryption at host support", func() {
					nodeClassWithoutEncryption := test.AKSNodeClass()
					// default is disabled when Security is nil or EncryptionAtHost is nil
					ExpectApplied(ctx, env.Client, nodeClassWithoutEncryption)
					instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClassWithoutEncryption)
					Expect(err).ToNot(HaveOccurred())

					// Standard_D2_v2 does not support encryption at host, but should still be included when encryption is not required
					Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D2_v2"))))
					// Standard_D2s_v3 supports encryption at host and should be included
					Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D2s_v3"))))
					// Standard_D2_v5 supports encryption at host and should be included
					Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D2_v5"))))
				})
			})
		})

		Context("MaxPods", func() {
			BeforeEach(func() {
				ctx = options.ToContext(ctx, test.Options())
			})
			It("should set pods equal to MaxPods in the AKSNodeClass when specified", func() {
				maxPods := int32(150)
				nodeClass.Spec.MaxPods = lo.ToPtr(maxPods)

				instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
				Expect(err).NotTo(HaveOccurred())
				ExpectCapacityPodsToMatchMaxPods(instanceTypes, maxPods)

				nodeClass.Spec.MaxPods = lo.ToPtr(int32(100))
				// Expect that an updated nodeclass is reflected
				instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
				Expect(err).NotTo(HaveOccurred())
				ExpectCapacityPodsToMatchMaxPods(instanceTypes, int32(100))
			})
			It("should set pods equal to the expected default MaxPods for NodeSubnet", func() {
				ctx = options.ToContext(
					ctx,
					test.Options(test.OptionsFields{
						NetworkPlugin:     lo.ToPtr("azure"),
						NetworkPluginMode: lo.ToPtr(""),
					}),
				)
				Expect(options.FromContext(ctx).NetworkPlugin).To(Equal("azure"))
				Expect(options.FromContext(ctx).NetworkPluginMode).To(Equal(""))
				instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
				Expect(err).NotTo(HaveOccurred())
				ExpectCapacityPodsToMatchMaxPods(instanceTypes, int32(30))
			})
			It("should set pods equal to the expected default MaxPods for AzureCNI Overlay", func() {
				// The default options should be using azure cni + overlay networking
				Expect(options.FromContext(ctx).NetworkPlugin).To(Equal("azure"))
				Expect(options.FromContext(ctx).NetworkPluginMode).To(Equal("overlay"))
				instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
				Expect(err).NotTo(HaveOccurred())
				ExpectCapacityPodsToMatchMaxPods(instanceTypes, int32(250))
			})
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

			// Label provisioning tests (WellKnownLabelEntry, individual instance type labels,
			// restricted labels, non-schedulable labels, bootstrap API labels) have been moved to
			// pkg/cloudprovider/suite_instancetype_e2e_test.go

			It("entries should cover every WellKnownLabel", func() {
				type WellKnownLabelEntry struct {
					Label string
				}
				entries := []WellKnownLabelEntry{
					// Well known
					{Label: v1.LabelTopologyRegion},
					{Label: karpv1.NodePoolLabelKey},
					{Label: v1.LabelTopologyZone},
					{Label: v1.LabelInstanceTypeStable},
					{Label: v1.LabelOSStable},
					{Label: v1.LabelArchStable},
					{Label: karpv1.CapacityTypeLabelKey},
					// Well Known to AKS
					{Label: v1beta1.LabelSKUName},
					{Label: v1beta1.LabelSKUFamily},
					{Label: v1beta1.LabelSKUSeries},
					{Label: v1beta1.LabelSKUVersion},
					{Label: v1beta1.LabelSKUStorageEphemeralOSMaxSize},
					{Label: v1beta1.LabelSKUAcceleratedNetworking},
					{Label: v1beta1.LabelSKUStoragePremiumCapable},
					{Label: v1beta1.LabelSKUGPUName},
					{Label: v1beta1.LabelSKUGPUManufacturer},
					{Label: v1beta1.LabelSKUGPUCount},
					{Label: v1beta1.LabelSKUCPU},
					{Label: v1beta1.LabelSKUMemory},
					// AKS domain
					{Label: v1beta1.AKSLabelCPU},
					{Label: v1beta1.AKSLabelMemory},
					{Label: v1beta1.AKSLabelMode},
					{Label: v1beta1.AKSLabelMode},
					{Label: v1beta1.AKSLabelScaleSetPriority},
					{Label: v1beta1.AKSLabelScaleSetPriority},
					{Label: v1beta1.AKSLabelOSSKU},
					{Label: v1beta1.AKSLabelFIPSEnabled},
					// Deprecated Labels
					{Label: v1.LabelFailureDomainBetaRegion},
					{Label: v1.LabelFailureDomainBetaZone},
					{Label: "beta.kubernetes.io/arch"},
					{Label: "beta.kubernetes.io/os"},
					{Label: v1.LabelInstanceType},
					{Label: "topology.disk.csi.azure.com/zone"},
					// Unsupported labels
					{Label: v1.LabelWindowsBuild},
					// Cluster Label
					{Label: v1beta1.AKSLabelCluster},
				}
				expectedLabels := append(karpv1.WellKnownLabels.UnsortedList(), lo.Keys(karpv1.NormalizedLabels)...)
				Expect(lo.Map(entries, func(item WellKnownLabelEntry, _ int) string { return item.Label })).To(ContainElements(expectedLabels))
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
})

// KubeReservedResources tests have been moved to kube_reserved_test.go (table-driven)

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
