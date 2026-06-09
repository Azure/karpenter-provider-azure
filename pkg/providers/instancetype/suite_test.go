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
	"fmt"
	"strings"
	"testing"

	"github.com/awslabs/operatorpkg/object"
	"github.com/blang/semver/v4"
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
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
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
		azureEnv.Reset(ctx)
		azureEnvNonZonal.Reset(ctx)
		azureEnvBootstrap.Reset(ctx)
	})

	AfterEach(func() {
		cloudProvider.WaitForInstancePromises()
		ExpectCleanedUp(ctx, env.Client)
	})

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
			// Mirror what the resolver would do: set Status.LocalDNSState=Enabled
			// when the resolution would land on Enabled. The test instance-type
			// provider uses a nil resolver, so Status is the only path to
			// surface Enabled.
			setEnabledStatus := func() {
				nodeClass.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
			}
			switch localDNSMode {
			case v1beta1.LocalDNSModeRequired:
				setEnabledStatus()
			case v1beta1.LocalDNSModeDisabled:
				// no status needed; Disabled mode -> false
			case v1beta1.LocalDNSModePreferred:
				threshold := semver.MustParse("1.36.0")
				parsed, perr := semver.ParseTolerant(strings.TrimPrefix(k8sVersion, "v"))
				if perr == nil && parsed.GTE(threshold) {
					setEnabledStatus()
				}
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
		Entry("when LocalDNS is preferred with k8s >= 1.36 - filters to 4+ vCPUs and 244+ MiB",
			v1beta1.LocalDNSModePreferred, "1.36.0", false, true),
		Entry("when LocalDNS is preferred with k8s < 1.36 - includes all SKUs",
			v1beta1.LocalDNSModePreferred, "1.35.0", true, true),
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
			nodeClassDisabled.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
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
			nodeClassEnabled.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
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

	DescribeTable("Filtering by ArtifactStreaming",
		func(artifactStreaming *v1beta1.ArtifactStreaming, shouldIncludeArm64 bool) {
			nodeClass.Spec.ArtifactStreaming = artifactStreaming
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			ExpectApplied(ctx, env.Client, nodeClass)
			instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(instanceTypes).ShouldNot(BeEmpty())

			getName := func(instanceType *corecloudprovider.InstanceType) string { return instanceType.Name }

			if shouldIncludeArm64 {
				Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D16plds_v5"))),
					"ARM64 instance type Standard_D16plds_v5 should be included")
			} else {
				Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_D16plds_v5"))),
					"ARM64 instance type Standard_D16plds_v5 should be excluded")
			}

			// AMD64 instance types should always be included regardless of artifact streaming setting
			Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D2s_v3"))),
				"AMD64 instance type Standard_D2s_v3 should always be included")
		},
		Entry("when artifact streaming is not set (default) - includes ARM64",
			nil, true),
		Entry("when artifact streaming is explicitly enabled - excludes ARM64",
			&v1beta1.ArtifactStreaming{Enabled: lo.ToPtr(true)}, false),
		Entry("when artifact streaming is explicitly disabled - includes ARM64",
			&v1beta1.ArtifactStreaming{Enabled: lo.ToPtr(false)}, true),
	)

	Context("Ephemeral Disk", func() {
		var originalOptions *options.Options
		BeforeEach(func() {
			originalOptions = options.FromContext(ctx)
			ctx = options.ToContext(
				ctx,
				test.Options(test.OptionsFields{
					UseSIG: lo.ToPtr(true),
				}))

			// Repopilate instance types based on above ctx
			Expect(azureEnv.InstanceTypesProvider.UpdateInstanceTypes(ctx)).To(Succeed())
		})

		AfterEach(func() {
			ctx = options.ToContext(ctx, originalOptions)
			// Clean up instance types
			Expect(azureEnv.InstanceTypesProvider.UpdateInstanceTypes(ctx)).To(Succeed())
		})

		Context("FindMaxEphemeralSizeGBAndPlacement(sku *skewer.SKU) -> diskSizeGB, *placement", func() {
			// B20ms:
			// NvmeDiskSizeInMiB == 0
			// CacheDiskBytes == 32212254720 -> 32.21225472 GB .. we should select this as the ephemeral disk size
			// placement == CacheDisk
			// MaxResourceVolumeMB == 163840 MiB -> 171.80 GB,
			// Standard_D128ds_v6:
			// NvmeDiskSizeInMiB == 7208960 -> 7559.142441 GB // SupportedEphemeralOSDiskPlacements == NvmeDisk
			// and this is greater than 0, so we select 7559, placement == NvmeDisk
			// Standard_D16plds_v5:
			// NvmeDiskSizeInMiB == 0
			// CacheDiskBytes == 429496729600 -> 429.4967296, this is greater than zero, so we select this as the ephemeral disk size
			// placement == CacheDisk and size == 429.4967296 GB
			// MaxResourceVolumeMB == 614400 MiB
			// Standard_D2as_v6: -> EphemeralOSDiskSupported is false, it should return 0 and nil for placement
			// Standard_D128ds_v6:
			// NvmeDiskSizeInMiB == 7208960 -> 7559.142441 GB // SupportedEphemeralOSDiskPlacements == NvmeDisk
			// and this is greater than 0, so we select 7559, placement == NvmeDisk
			// Standard_NC24ads_A100_v4:
			// {Name: lo.ToPtr("SupportedEphemeralOSDiskPlacements"), Value: lo.ToPtr("ResourceDisk,CacheDisk")},
			// NvmeDiskSizeInMiB == 915527 -> 959.99964 GB  but no SupportedEphemeralOSDiskPlacements == NvmeDisk so we move to cache disk
			// CacheDiskBytes == 274877906944 -> 274.877906944 GB so we select cache disk + 274
			// MaxResourceVolumeMB == 65536 MiB
			// Standard_D64s_v3:
			// NvmeDiskSizeInMiB == 0
			// CacheDiskBytes == 1717986918400 -> 1717.9869184 GB, this is greater than zero, so we select this as the ephemeral disk size
			// placement == CacheDisk and size == 1717 GB
			// Standard_A0
			// NvmeDiskSizeInMiB == 0
			// CacheDiskBytes == 0, this is zero
			// MaxResourceVolumeMB == 20480 Mib -> 21.474836 GB. Note that this sku doesnt support ephemeral os disk
			DescribeTable("should return the max ephemeral disk size in GB for a given instance type",
				func(sku *skewer.SKU, expectedSize int64, expectedPlacement *armcompute.DiffDiskPlacement) {
					sizeGB, placement := instancetype.FindMaxEphemeralSizeGBAndPlacement(sku)
					Expect(sizeGB).To(Equal(expectedSize))
					Expect(placement).To(Equal(expectedPlacement))
				}, Entry("Standard_B20ms", fake.MakeSKU("Standard_B20ms"), int64(32), lo.ToPtr(armcompute.DiffDiskPlacementCacheDisk)),
				Entry("Standard_D128ds_v6", fake.MakeSKU("Standard_D128ds_v6"), int64(7559), lo.ToPtr(armcompute.DiffDiskPlacementNvmeDisk)),
				Entry("Standard_D16plds_v5", fake.MakeSKU("Standard_D16plds_v5"), int64(429), lo.ToPtr(armcompute.DiffDiskPlacementCacheDisk)),
				Entry("Standard_D2as_v6", fake.MakeSKU("Standard_D2as_v6"), int64(0), nil), // does not support ephemeral
				Entry("Standard_NC24ads_A100_v4", fake.MakeSKU("Standard_NC24ads_A100_v4"), int64(274), lo.ToPtr(armcompute.DiffDiskPlacementCacheDisk)),
				Entry("Standard_D64s_v3", fake.MakeSKU("Standard_D64s_v3"), int64(1717), lo.ToPtr(armcompute.DiffDiskPlacementCacheDisk)),
				Entry("Standard_A0", fake.MakeSKU("Standard_A0"), int64(0), nil),       // does not support ephemeral
				Entry("Standard_D2_v2", fake.MakeSKU("Standard_D2_v2"), int64(0), nil), // does not support ephemeral
				// TODO: codegen
				// Entry("Standard_D2pls_v5", fake.MakeSKU("Standard_D2pls_v5"), int64(0), nil), // does not support ephemeral
				// Entry("Standard_D2lds_v5", fake.MakeSKU("Standard_D2lds_v5"), int64(80), armcompute.DiffDiskPlacementResourceDisk),
				Entry("Nil SKU", nil, int64(0), nil),
			)
		})
		Context("Placement", func() {
		})

	})

	Context("Zone-aware provisioning", func() {
		It("should not include empty zone domain in instance type offerings", func() {
			// Verify that no instance type has an offering with zone=""
			// which would introduce a phantom domain in topology spread constraint calculations.
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			instanceTypes, err := cloudProvider.GetInstanceTypes(ctx, nodePool)
			Expect(err).ToNot(HaveOccurred())
			Expect(instanceTypes).ToNot(BeEmpty())

			for _, it := range instanceTypes {
				for _, offering := range it.Offerings {
					zone := offering.Requirements.Get(v1.LabelTopologyZone).Any()
					Expect(zone).ToNot(BeEmpty(),
						fmt.Sprintf("instance type %s has an offering with empty zone, which breaks topology spread constraints", it.Name))
				}
			}
		})
	})

	Context("Unavailable Offerings", func() {
		DescribeTable("Should not return unavailable offerings", func(azEnv *test.Environment) {
			for _, zone := range azEnv.Zones() {
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", fake.MakeSKU("Standard_D2_v2"), zone, karpv1.CapacityTypeSpot)
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", fake.MakeSKU("Standard_D2_v2"), zone, karpv1.CapacityTypeOnDemand)
			}
			instanceTypes, err := azEnv.InstanceTypesProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			seeUnavailable := false
			for _, instanceType := range instanceTypes {
				if instanceType.Name == "Standard_D2_v2" {
					seeUnavailable = true
					if azEnv == azureEnv {
						Expect(lo.Map(instanceType.Offerings.Available(), func(offering *corecloudprovider.Offering, _ int) string {
							return offering.Requirements.Get(v1.LabelTopologyZone).Any()
						})).To(ConsistOf(zones.Regional, zones.Regional))
					} else {
						Expect(len(instanceType.Offerings.Available())).To(Equal(0))
					}
				} else {
					Expect(len(instanceType.Offerings.Available())).To(Not(Equal(0)))
				}
			}
			// we should see the unavailable offering in the list
			Expect(seeUnavailable).To(BeTrue())
		},
			Entry("zonal", azureEnv),
			Entry("non-zonal", azureEnvNonZonal),
		)
	})

	Context("Provider List", func() {
		Context("Filtering in InstanceType", func() {
			var instanceTypes corecloudprovider.InstanceTypes
			var err error
			getName := func(instanceType *corecloudprovider.InstanceType) string { return instanceType.Name }

			BeforeEach(func() {
				Expect(azureEnv.InstanceTypesProvider.UpdateInstanceTypes(ctx)).To(Succeed())
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
				Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_NC16ads_A10_v4"))))
			})
			It("should include AzureLinux GPU SKUs in list results", func() {
				Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_NC16as_T4_v3"))))
				Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_NC24ads_A100_v4"))))
			})
		})

		Context("Filtering by GPU Driver Mode", func() {
			var instanceTypes corecloudprovider.InstanceTypes
			var err error
			getName := func(instanceType *corecloudprovider.InstanceType) string { return instanceType.Name }

			Context("when driverInstallation is Install (default)", func() {
				BeforeEach(func() {
					// Default nodeClass has no GPU config -> defaults to Install mode
					nodeClassDefault := test.AKSNodeClass()
					ExpectApplied(ctx, env.Client, nodeClassDefault)
					instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClassDefault)
					Expect(err).ToNot(HaveOccurred())
				})

				It("should include NVIDIA GPU SKUs", func() {
					Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_NC16as_T4_v3"))))
				})
				// Standard_NV4ads_V710_v5 is not in the fake SKU data for southcentralus
				PIt("should not include AMD GPU SKUs", func() {
					Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_NV4ads_V710_v5"))))
				})
				It("should include non-GPU SKUs", func() {
					Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D2s_v3"))))
				})
			})

			Context("when mode is explicitly set to Driver", func() {
				BeforeEach(func() {
					driverMode := v1beta1.GPUModeDriver
					nodeClassInstall := test.AKSNodeClass()
					nodeClassInstall.Spec.GPU = &v1beta1.GPU{Mode: &driverMode}
					ExpectApplied(ctx, env.Client, nodeClassInstall)
					instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClassInstall)
					Expect(err).ToNot(HaveOccurred())
				})

				It("should include NVIDIA GPU SKUs", func() {
					Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_NC16as_T4_v3"))))
				})
				It("should include non-GPU SKUs", func() {
					Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D2s_v3"))))
				})
			})

			Context("when mode is None", func() {
				BeforeEach(func() {
					noneMode := v1beta1.GPUModeNone
					nodeClassNone := test.AKSNodeClass()
					nodeClassNone.Spec.GPU = &v1beta1.GPU{Mode: &noneMode}
					ExpectApplied(ctx, env.Client, nodeClassNone)
					instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClassNone)
					Expect(err).ToNot(HaveOccurred())
				})

				It("should include NVIDIA GPU SKUs", func() {
					Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_NC16as_T4_v3"))))
				})
				It("should include non-GPU SKUs", func() {
					Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D2s_v3"))))
				})
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

var _ = Describe("Tax Calculator", func() {
	Context("KubeReservedResources", func() {
		It("should have 4 cores, 7GiB", func() {
			cpus := int64(4) // 4 cores
			memory := 7.0    // 7 GiB
			expectedCPU := "140m"
			expectedMemory := "1638Mi"

			resources := instancetype.KubeReservedResources(cpus, memory)
			gotCPU := resources[v1.ResourceCPU]
			gotMemory := resources[v1.ResourceMemory]

			Expect(gotCPU.String()).To(Equal(expectedCPU))
			Expect(gotMemory.String()).To(Equal(expectedMemory))
		})

		It("should have 2 cores, 8GiB", func() {
			cpus := int64(2) // 2 cores
			memory := 8.0    // 8 GiB
			expectedCPU := "100m"
			expectedMemory := "1843Mi"

			resources := instancetype.KubeReservedResources(cpus, memory)
			gotCPU := resources[v1.ResourceCPU]
			gotMemory := resources[v1.ResourceMemory]

			Expect(gotCPU.String()).To(Equal(expectedCPU))
			Expect(gotMemory.String()).To(Equal(expectedMemory))
		})

		It("should have 3 cores, 64GiB", func() {
			cpus := int64(3) // 3 cores
			memory := 64.0   // 64 GiB
			expectedCPU := "120m"
			expectedMemory := "5611Mi"

			resources := instancetype.KubeReservedResources(cpus, memory)
			gotCPU := resources[v1.ResourceCPU]
			gotMemory := resources[v1.ResourceMemory]

			Expect(gotCPU.String()).To(Equal(expectedCPU))
			Expect(gotMemory.String()).To(Equal(expectedMemory))
		})
	})
})

func ExpectKubeletFlagsPassed(customData string) string {
	GinkgoHelper()
	return customData[strings.Index(customData, "KUBELET_FLAGS=")+len("KUBELET_FLAGS=") : strings.Index(customData, "KUBELET_NODE_LABELS")]
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
