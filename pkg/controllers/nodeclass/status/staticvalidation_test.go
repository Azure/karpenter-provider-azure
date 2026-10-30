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

package status_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/samber/lo"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
)

// createZoneOverride creates a LocalDNSZoneOverride with all required fields
func createZoneOverride(zone string, forwardToVnetDNS bool) v1beta1.LocalDNSZoneOverride {
	forwardDest := v1beta1.LocalDNSForwardDestinationClusterCoreDNS
	if forwardToVnetDNS {
		forwardDest = v1beta1.LocalDNSForwardDestinationVnetDNS
	}
	return v1beta1.LocalDNSZoneOverride{
		Zone:               zone,
		QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
		Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
		ForwardDestination: forwardDest,
		ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
		MaxConcurrent:      lo.ToPtr(int32(100)),
		CacheDuration:      karpv1.MustParseNillableDuration("1h"),
		ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
		ServeStale:         v1beta1.LocalDNSServeStaleVerify,
	}
}

var _ = Describe("NodeClass Status Controller image family compatibility", func() {
	DescribeTable("derives readiness from Ubuntu compatibility with the selected Kubernetes version",
		func(controlPlaneVersion, requestedVersion string, compatible bool) {
			provider := &testKubernetesVersionProvider{
				controlPlaneVersion: controlPlaneVersion,
				supportedVersions:   map[string]bool{requestedVersion: true},
			}
			statusController := status.NewController(
				env.Client, azureEnv.SubscriptionID, fake.Region, provider, azureEnv.ImageProvider,
				env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface,
				azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID,
				options.FromContext(ctx).NetworkPolicy, options.FromContext(ctx).NetworkPlugin,
				azureEnv.CapacityReservationGroupsAPI, azureEnv.CapacityReservationsAPI,
				azureEnv.InstanceTypesProvider, azureEnv.UnavailableOfferingsCache, testOptions.SystemNamespace,
			)
			pinnedNodeClass := test.AKSNodeClass(test.WithVersionBasedDefaults(env.Version))
			pinnedNodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
			if requestedVersion != "" {
				pinnedNodeClass.Spec.Versions = &v1beta1.Versions{KubernetesVersion: lo.ToPtr(requestedVersion)}
			}
			ExpectApplied(ctx, env.Client, pinnedNodeClass)
			ExpectObjectReconciled(ctx, env.Client, statusController, pinnedNodeClass)
			pinnedNodeClass = ExpectExists(ctx, env.Client, pinnedNodeClass)

			Expect(pinnedNodeClass.StatusConditions().Get(v1beta1.ConditionTypeKubernetesVersionReady).IsTrue()).To(BeTrue())
			Expect(pinnedNodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady).IsTrue()).To(BeTrue())
			validation := pinnedNodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(validation.IsTrue()).To(Equal(compatible))
			Expect(pinnedNodeClass.StatusConditions().Root().IsTrue()).To(Equal(compatible))
			if !compatible {
				Expect(validation.Reason).To(Equal(status.ImageFamilyKubernetesVersionIncompatible))
				Expect(pinnedNodeClass.StatusConditions().Root().IsFalse()).To(BeTrue())
			}
		},
		Entry("incompatible discovered version", "1.31.0", "", false),
		Entry("compatible discovered version", "1.32.0", "", true),
		Entry("incompatible pinned version despite compatible control plane", "1.32.0", "1.31.0", false),
	)
})

var _ = Describe("Validation Reconciler", func() {
	var ctx context.Context
	var reconciler *status.StaticValidationReconciler
	var nodeClass *v1beta1.AKSNodeClass
	var fakeDesAPI *fake.DiskEncryptionSetsAPI
	var emptyDiskEncryptionSetID *arm.ResourceID

	BeforeEach(func() {
		ctx = options.ToContext(context.Background(), test.Options())
		fakeDesAPI = &fake.DiskEncryptionSetsAPI{}

		reconciler = status.NewStaticValidationReconciler(fakeDesAPI, emptyDiskEncryptionSetID)

		nodeClass = test.AKSNodeClass(
			test.WithDefaultStatus(env, false),
			test.WithVersionBasedDefaults(env.Version))
	})

	Context("provision mode compatibility", func() {
		DescribeTable("enforces both cluster FIPS and ACL requirements",
			func(fipsMode *v1beta1.FIPSMode, useSIG bool, expectedReason string) {
				ctx = options.ToContext(ctx, &options.Options{
					ProvisionMode: consts.ProvisionModeAKSMachineAPI, UseSIG: useSIG, EnableFIPS: true,
				})
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureContainerLinuxImageFamily)
				nodeClass.Spec.FIPSMode = fipsMode
				_, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				if expectedReason == "" {
					Expect(condition.IsTrue()).To(BeTrue())
				} else {
					Expect(condition.IsFalse()).To(BeTrue())
					Expect(condition.Reason).To(Equal(expectedReason))
				}
			},
			Entry("rejects missing FIPS even with SIG", nil, true, status.FIPSRequired),
			Entry("rejects missing SIG even with FIPS", &v1beta1.FIPSModeFIPS, false, status.SIGRequiredForAzureContainerLinux),
			Entry("accepts FIPS with SIG", &v1beta1.FIPSModeFIPS, true, ""),
		)

		DescribeTable("validates image family compatibility",
			func(imageFamily, provisionMode string, useSIG bool, expectedReason string) {
				ctx = options.ToContext(context.Background(), &options.Options{ProvisionMode: provisionMode, UseSIG: useSIG})
				nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				if expectedReason == "" {
					Expect(condition.IsTrue()).To(BeTrue())
					Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
				} else {
					Expect(condition.IsFalse()).To(BeTrue())
					Expect(condition.Reason).To(Equal(expectedReason))
					Expect(condition.Message).To(ContainSubstring("requires an AKS Machine API provision mode"))
					Expect(condition.Message).To(ContainSubstring("shared image gallery access (UseSIG=true)"))
					Expect(result).To(Equal(reconcile.Result{}))
				}
			},
			Entry("ACL with Machine API and SIG", v1beta1.AzureContainerLinuxImageFamily, consts.ProvisionModeAKSMachineAPI, true, ""),
			Entry("ACL with batched Machine API and SIG", v1beta1.AzureContainerLinuxImageFamily, consts.ProvisionModeAKSMachineAPIHeaderBatch, true, ""),
			Entry("ACL with Machine API without SIG", v1beta1.AzureContainerLinuxImageFamily, consts.ProvisionModeAKSMachineAPI, false, status.SIGRequiredForAzureContainerLinux),
			Entry("ACL with batched Machine API without SIG", v1beta1.AzureContainerLinuxImageFamily, consts.ProvisionModeAKSMachineAPIHeaderBatch, false, status.SIGRequiredForAzureContainerLinux),
			Entry("ACL with bootstrapping client and SIG", v1beta1.AzureContainerLinuxImageFamily, consts.ProvisionModeBootstrappingClient, true, status.IncompatibleProvisionMode),
			Entry("ACL with local scriptless and SIG", v1beta1.AzureContainerLinuxImageFamily, consts.ProvisionModeAKSScriptless, true, status.IncompatibleProvisionMode),
			Entry("ACL with bootstrapping client without SIG", v1beta1.AzureContainerLinuxImageFamily, consts.ProvisionModeBootstrappingClient, false, status.IncompatibleProvisionMode),
			Entry("ACL with local scriptless without SIG", v1beta1.AzureContainerLinuxImageFamily, consts.ProvisionModeAKSScriptless, false, status.IncompatibleProvisionMode),
			Entry("Ubuntu with bootstrapping client", v1beta1.UbuntuImageFamily, consts.ProvisionModeBootstrappingClient, false, ""),
			Entry("Azure Linux with local scriptless", v1beta1.AzureLinuxImageFamily, consts.ProvisionModeAKSScriptless, false, ""),
		)

		It("should recover after SIG access is enabled and fail again if disabled", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureContainerLinuxImageFamily)
			for _, useSIG := range []bool{false, true, false} {
				ctx = options.ToContext(context.Background(), &options.Options{ProvisionMode: consts.ProvisionModeAKSMachineAPI, UseSIG: useSIG})
				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				Expect(condition.IsTrue()).To(Equal(useSIG))
				if useSIG {
					Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
				} else {
					Expect(condition.Reason).To(Equal(status.SIGRequiredForAzureContainerLinux))
				}
			}
		})
	})

	// All LocalDNS validations are now handled declaratively by CEL and kubebuilder markers.
	// The StaticValidationReconciler is a skeleton for future runtime validations that cannot be
	// expressed in the CRD schema (e.g., external API calls, cross-resource checks, etc.).

	Context("basic validation reconciliation", func() {
		It("should always set ValidationSucceeded condition to true and requeue after success interval", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())
		})

		It("should set ValidationSucceeded to true even with LocalDNS configured", func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModeRequired,
				VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{
					createZoneOverride(".", true),
					createZoneOverride("cluster.local", false),
				},
				KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
					createZoneOverride(".", false),
					createZoneOverride("cluster.local", false),
				},
			}

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())
		})
	})

	Context("image family Kubernetes version compatibility", func() {
		setVersion := func(version string) {
			nodeClass.Status.KubernetesVersion = lo.ToPtr(version)
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
		}

		DescribeTable("validates pinned Ubuntu families",
			func(family, version string, fips bool, expectedMessage string) {
				nodeClass.Spec.ImageFamily = lo.ToPtr(family)
				if fips {
					nodeClass.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeFIPS)
				}
				setVersion(version)

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				if expectedMessage == "" {
					Expect(condition.IsTrue()).To(BeTrue())
					Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
				} else {
					Expect(condition.IsFalse()).To(BeTrue())
					Expect(condition.Reason).To(Equal("ImageFamilyKubernetesVersionIncompatible"))
					Expect(condition.Message).To(Equal(expectedMessage))
					Expect(nodeClass.StatusConditions().Root().IsFalse()).To(BeTrue())
					Expect(result).To(Equal(reconcile.Result{}))
				}
			},
			Entry("Ubuntu2404 below lower bound", v1beta1.Ubuntu2404ImageFamily, "1.31", false,
				`requested image family Ubuntu2404 is not supported with discovered Kubernetes version "1.31"; supported range is >= 1.32.0`),
			Entry("Ubuntu2404 lower bound", v1beta1.Ubuntu2404ImageFamily, "1.32.0", false, ""),
			Entry("Ubuntu2204 lower bound", v1beta1.Ubuntu2204ImageFamily, "1.25.2", false, ""),
			Entry("Ubuntu2204 upper bound", v1beta1.Ubuntu2204ImageFamily, "1.37.0", false,
				`requested image family Ubuntu2204 is not supported with discovered Kubernetes version "1.37.0"; supported range is >= 1.25.2 and < 1.37.0`),
			Entry("Ubuntu2204 FIPS extended range", v1beta1.Ubuntu2204ImageFamily, "1.38.9", true, ""),
			Entry("Ubuntu2204 FIPS upper bound", v1beta1.Ubuntu2204ImageFamily, "1.39.0", true,
				`requested image family Ubuntu2204 FIPS is not supported with discovered Kubernetes version "1.39.0"; supported range is >= 1.25.2 and < 1.39.0`),
		)

		DescribeTable("applies the same policy in every provisioning mode",
			func(mode string) {
				ctx = options.ToContext(ctx, &options.Options{ProvisionMode: mode})
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
				setVersion("1.31.0")
				_, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).Reason).To(Equal("ImageFamilyKubernetesVersionIncompatible"))
			},
			Entry("scriptless", consts.ProvisionModeAKSScriptless),
			Entry("bootstrapping client", consts.ProvisionModeBootstrappingClient),
			Entry("Machine API", consts.ProvisionModeAKSMachineAPI),
			Entry("batched Machine API", consts.ProvisionModeAKSMachineAPIHeaderBatch),
		)

		It("recovers after a spec change or Kubernetes upgrade", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
			setVersion("1.31.0")
			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsFalse()).To(BeTrue())

			nodeClass.Generation++
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)
			setVersion("1.31.0")
			_, err = reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())

			nodeClass.Generation++
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
			setVersion("1.31.0")
			_, err = reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsFalse()).To(BeTrue())

			setVersion("1.32.0")
			_, err = reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
		})

		DescribeTable("reports unavailable or malformed versions as validation failures and recovers",
			func(version *string, versionReady bool, expectedReason, expectedMessage string) {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
				nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeValidationSucceeded)
				nodeClass.Generation++
				nodeClass.Status.KubernetesVersion = version
				if versionReady {
					nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
				} else {
					nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionUnsupported", "requested version is not supported")
				}

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal(expectedReason))
				Expect(condition.Message).To(ContainSubstring(expectedMessage))
				Expect(condition.ObservedGeneration).To(Equal(nodeClass.Generation))
				Expect(nodeClass.StatusConditions().Root().IsFalse()).To(BeTrue())

				setVersion("1.32.0")
				result, err = reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
				condition = nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				Expect(condition.IsTrue()).To(BeTrue())
				Expect(condition.ObservedGeneration).To(Equal(nodeClass.Generation))
			},
			Entry("version not ready", nil, false, "KubernetesVersionNotReady", "requested version is not supported"),
			Entry("version not ready despite a populated status", lo.ToPtr("1.32.0"), false, "KubernetesVersionNotReady", "requested version is not supported"),
			Entry("uninitialized version", nil, true, "KubernetesVersionNotReady", "KubernetesVersion is uninitialized"),
			Entry("empty version", lo.ToPtr(""), true, "KubernetesVersionNotReady", "KubernetesVersion is uninitialized"),
			Entry("malformed version", lo.ToPtr("1.32.x"), true, "MalformedKubernetesVersion", `malformed Kubernetes version "1.32.x"`),
			Entry("bogus version", lo.ToPtr("Bogus"), true, "MalformedKubernetesVersion", `malformed Kubernetes version "Bogus"`),
		)

		It("waits for Kubernetes version readiness to catch up with the current generation", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeValidationSucceeded)
			nodeClass.Generation++

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal("KubernetesVersionNotReady"))
			Expect(condition.Message).To(ContainSubstring("ObservedGeneration"))
			Expect(condition.ObservedGeneration).To(Equal(nodeClass.Generation))

			setVersion("1.32.0")
			result, err = reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
		})

		DescribeTable("validates DES for unpinned families once the Kubernetes version is ready",
			func(family *string, desDenied bool) {
				nodeClass.Spec.ImageFamily = family
				parsedID := lo.Must(arm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-des"))
				reconciler = status.NewStaticValidationReconciler(fakeDesAPI, parsedID)
				calls := 0
				fakeDesAPI.GetFunc = func(context.Context, string, string, *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
					calls++
					if desDenied {
						return armcompute.DiskEncryptionSetsClientGetResponse{}, &azcore.ResponseError{StatusCode: http.StatusForbidden}
					}
					return armcompute.DiskEncryptionSetsClientGetResponse{}, nil
				}

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(calls).To(Equal(1))
				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				if desDenied {
					Expect(condition.IsFalse()).To(BeTrue())
					Expect(condition.Reason).To(Equal(status.DiskEncryptionSetRBACMissing))
					Expect(result.RequeueAfter).To(Equal(status.ValidationFailureRequeueInterval))
				} else {
					Expect(condition.IsTrue()).To(BeTrue())
					Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
				}
			},
			Entry("generic Ubuntu with DES access", lo.ToPtr(v1beta1.UbuntuImageFamily), false),
			Entry("generic Ubuntu without DES access", lo.ToPtr(v1beta1.UbuntuImageFamily), true),
			Entry("unset with DES access", nil, false),
			Entry("unset without DES access", nil, true),
			Entry("AzureLinux with DES access", lo.ToPtr(v1beta1.AzureLinuxImageFamily), false),
			Entry("AzureLinux without DES access", lo.ToPtr(v1beta1.AzureLinuxImageFamily), true),
		)

		DescribeTable("defers DES validation until the Kubernetes version is ready",
			func(family *string, kata bool) {
				nodeClass.Spec.ImageFamily = family
				if kata {
					nodeClass.Spec.WorkloadRuntime = lo.ToPtr(v1beta1.WorkloadRuntimeKataVMIsolation)
					ctx = options.ToContext(ctx, &options.Options{ProvisionMode: consts.ProvisionModeAKSMachineAPI})
				}
				nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionInvalidFormat", "invalid kubernetes version format")
				parsedID := lo.Must(arm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-des"))
				reconciler = status.NewStaticValidationReconciler(fakeDesAPI, parsedID)
				calls := 0
				fakeDesAPI.GetFunc = func(context.Context, string, string, *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
					calls++
					return armcompute.DiskEncryptionSetsClientGetResponse{}, nil
				}

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(calls).To(BeZero())
				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal("KubernetesVersionNotReady"))

				setVersion("1.32.0")
				result, err = reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
				Expect(calls).To(Equal(1))
				Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
			},
			Entry("Ubuntu2204", lo.ToPtr(v1beta1.Ubuntu2204ImageFamily), false),
			Entry("Ubuntu2404", lo.ToPtr(v1beta1.Ubuntu2404ImageFamily), false),
			Entry("generic Ubuntu", lo.ToPtr(v1beta1.UbuntuImageFamily), false),
			Entry("unset image family", nil, false),
			Entry("AzureLinux", lo.ToPtr(v1beta1.AzureLinuxImageFamily), false),
			Entry("AzureLinux with Kata", lo.ToPtr(v1beta1.AzureLinuxImageFamily), true),
		)

		It("skips DES validation until the pinned image family is compatible", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
			parsedID := lo.Must(arm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-des"))
			reconciler = status.NewStaticValidationReconciler(fakeDesAPI, parsedID)
			calls := 0
			fakeDesAPI.GetFunc = func(context.Context, string, string, *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				calls++
				return armcompute.DiskEncryptionSetsClientGetResponse{}, nil
			}
			setVersion("1.31.0")
			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(calls).To(BeZero())

			setVersion("1.32.0")
			_, err = reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(calls).To(Equal(1))
		})
	})

	Context("Windows network dataplane validation", func() {
		DescribeTable("should fail validation for Windows image families on Cilium",
			func(imageFamily string) {
				ctx = options.ToContext(ctx, &options.Options{NetworkDataplane: consts.NetworkDataplaneCilium})
				nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(BeZero())

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal(status.WindowsUnsupportedNetworkDataplane))
				Expect(condition.Message).To(Equal(fmt.Sprintf("imageFamily %q is not supported with network-dataplane %q", imageFamily, consts.NetworkDataplaneCilium)))
			},
			Entry("Windows2022", v1beta1.Windows2022ImageFamily),
			Entry("Windows2025", v1beta1.Windows2025ImageFamily),
		)

		It("should pass validation for Windows on the Azure dataplane", func() {
			ctx = options.ToContext(ctx, &options.Options{
				NetworkDataplane: consts.NetworkDataplaneAzure,
				ProvisionMode:    consts.ProvisionModeAKSMachineAPI,
			})
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Windows2022ImageFamily)

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
		})

		It("should pass validation for Linux on Cilium", func() {
			ctx = options.ToContext(ctx, &options.Options{NetworkDataplane: consts.NetworkDataplaneCilium})
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)
			nodeClass.Status.KubernetesVersion = lo.ToPtr("1.32.0")
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
		})
	})

	Context("Windows provision mode validation", func() {
		DescribeTable("should fail validation outside AKS Machine API provision modes",
			func(imageFamily, provisionMode string) {
				ctx = options.ToContext(ctx, &options.Options{
					NetworkDataplane: consts.NetworkDataplaneAzure,
					ProvisionMode:    provisionMode,
				})
				nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(BeZero())

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal(status.WindowsUnsupportedProvisionMode))
				Expect(condition.Message).To(Equal(fmt.Sprintf("imageFamily %q is not supported with provision-mode %q; Windows requires an AKS Machine API provision mode", imageFamily, provisionMode)))
			},
			Entry("Windows2022 on aksscriptless", v1beta1.Windows2022ImageFamily, consts.ProvisionModeAKSScriptless),
			Entry("Windows2022 on bootstrappingclient", v1beta1.Windows2022ImageFamily, consts.ProvisionModeBootstrappingClient),
			Entry("Windows2025 on aksscriptless", v1beta1.Windows2025ImageFamily, consts.ProvisionModeAKSScriptless),
			Entry("Windows2025 on bootstrappingclient", v1beta1.Windows2025ImageFamily, consts.ProvisionModeBootstrappingClient),
		)

		DescribeTable("should pass validation in AKS Machine API provision modes",
			func(imageFamily, provisionMode string) {
				ctx = options.ToContext(ctx, &options.Options{
					NetworkDataplane: consts.NetworkDataplaneAzure,
					ProvisionMode:    provisionMode,
				})
				nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
				Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
			},
			Entry("Windows2022 on aksmachineapi", v1beta1.Windows2022ImageFamily, consts.ProvisionModeAKSMachineAPI),
			Entry("Windows2022 on aksmachineapiheaderbatch", v1beta1.Windows2022ImageFamily, consts.ProvisionModeAKSMachineAPIHeaderBatch),
			Entry("Windows2025 on aksmachineapi", v1beta1.Windows2025ImageFamily, consts.ProvisionModeAKSMachineAPI),
			Entry("Windows2025 on aksmachineapiheaderbatch", v1beta1.Windows2025ImageFamily, consts.ProvisionModeAKSMachineAPIHeaderBatch),
		)

		DescribeTable("should not restrict Linux provision modes",
			func(provisionMode string) {
				ctx = options.ToContext(ctx, &options.Options{
					NetworkDataplane: consts.NetworkDataplaneAzure,
					ProvisionMode:    provisionMode,
				})
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)
				nodeClass.Status.KubernetesVersion = lo.ToPtr("1.32.0")
				nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
				Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
			},
			Entry("aksscriptless", consts.ProvisionModeAKSScriptless),
			Entry("bootstrappingclient", consts.ProvisionModeBootstrappingClient),
		)
	})

	Context("Kata Pod Sandboxing (workloadRuntime) validation", func() {
		BeforeEach(func() {
			nodeClass.Spec.WorkloadRuntime = lo.ToPtr(v1beta1.WorkloadRuntimeKataVMIsolation)
		})

		It("should fail validation on the aksscriptless provision mode, which cannot install the Kata host stack", func() {
			ctx = options.ToContext(ctx, &options.Options{
				ProvisionMode: consts.ProvisionModeAKSScriptless,
			})
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.KataPodSandboxingUnsupportedProvisionMode))
		})

		DescribeTable("should pass validation on provision modes that can express workloadRuntime",
			func(provisionMode string) {
				ctx = options.ToContext(ctx, &options.Options{ProvisionMode: provisionMode})
				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				Expect(condition.IsTrue()).To(BeTrue())
			},
			Entry("aksmachineapi", consts.ProvisionModeAKSMachineAPI),
			Entry("aksmachineapiheaderbatch", consts.ProvisionModeAKSMachineAPIHeaderBatch),
			Entry("bootstrappingclient", consts.ProvisionModeBootstrappingClient),
		)

		It("should fail validation when the Kubernetes version selects Azure Linux 2", func() {
			ctx = options.ToContext(ctx, &options.Options{ProvisionMode: consts.ProvisionModeAKSMachineAPI})
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
			nodeClass.Status.KubernetesVersion = lo.ToPtr("1.31.0")
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.KataRequiresAzureLinux3))
			Expect(condition.Message).To(Equal("workloadRuntime KataVmIsolation requires Azure Linux 3 and Kubernetes 1.32 or newer; Kubernetes version 1.31.0 resolves imageFamily AzureLinux to Azure Linux 2"))
		})

		It("should pass validation when the Kubernetes version selects Azure Linux 3", func() {
			ctx = options.ToContext(ctx, &options.Options{ProvisionMode: consts.ProvisionModeAKSMachineAPI})
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
			nodeClass.Status.KubernetesVersion = lo.ToPtr("1.32.0")
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
		})
	})

	Context("managed GPU provision mode validation", func() {
		BeforeEach(func() {
			nodeClass.Spec.GPU = &v1beta1.GPU{
				Mode: lo.ToPtr(v1beta1.GPUModeDriver),
				Nvidia: &v1beta1.NvidiaGPU{
					ManagementMode: lo.ToPtr(v1beta1.ManagementModeManaged),
				},
			}
		})

		DescribeTable("should reject provision modes that cannot express the managed NVIDIA GPU profile",
			func(provisionMode string) {
				ctx = options.ToContext(ctx, &options.Options{ProvisionMode: provisionMode})

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(BeZero())

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal(status.ManagedGPUUnsupportedProvisionMode))
				Expect(condition.Message).To(ContainSubstring(provisionMode))
			},
			Entry("aksscriptless", consts.ProvisionModeAKSScriptless),
			Entry("bootstrappingclient", consts.ProvisionModeBootstrappingClient),
		)

		DescribeTable("should accept AKS Machine API provision modes",
			func(provisionMode string) {
				ctx = options.ToContext(ctx, &options.Options{ProvisionMode: provisionMode})

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
				Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
			},
			Entry("aksmachineapi", consts.ProvisionModeAKSMachineAPI),
			Entry("aksmachineapiheaderbatch", consts.ProvisionModeAKSMachineAPIHeaderBatch),
		)

		It("should clear the failure after the configuration is corrected", func() {
			ctx = options.ToContext(ctx, &options.Options{ProvisionMode: consts.ProvisionModeAKSScriptless})
			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsFalse()).To(BeTrue())

			nodeClass.Spec.GPU.Nvidia.ManagementMode = lo.ToPtr(v1beta1.ManagementModeUnmanaged)
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
		})

		DescribeTable("should not restrict configurations that do not enable managed GPU",
			func(nvidia *v1beta1.NvidiaGPU) {
				ctx = options.ToContext(ctx, &options.Options{ProvisionMode: consts.ProvisionModeAKSScriptless})
				nodeClass.Spec.GPU.Nvidia = nvidia

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
				Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
			},
			Entry("omitted NVIDIA settings", nil),
			Entry("empty NVIDIA settings", &v1beta1.NvidiaGPU{}),
			Entry("explicit Unmanaged", &v1beta1.NvidiaGPU{ManagementMode: lo.ToPtr(v1beta1.ManagementModeUnmanaged)}),
		)
	})

	Context("node public IP provision mode validation", func() {
		BeforeEach(func() {
			nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true)}
		})

		DescribeTable("should reject provision modes that do not support node public IP",
			func(provisionMode string) {
				ctx = options.ToContext(ctx, &options.Options{ProvisionMode: provisionMode})

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(BeZero())

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal(status.NodePublicIPUnsupportedProvisionMode))
				Expect(condition.Message).To(ContainSubstring(provisionMode))
				Expect(condition.Message).To(ContainSubstring(consts.ProvisionModeAKSMachineAPI))
				Expect(condition.Message).To(ContainSubstring(consts.ProvisionModeAKSMachineAPIHeaderBatch))
			},
			Entry("aksscriptless", consts.ProvisionModeAKSScriptless),
			Entry("bootstrappingclient", consts.ProvisionModeBootstrappingClient),
		)

		DescribeTable("should accept node public IP in AKS Machine API provision modes",
			func(provisionMode string) {
				ctx = options.ToContext(ctx, &options.Options{ProvisionMode: provisionMode})

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
				Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
			},
			Entry("aksmachineapi", consts.ProvisionModeAKSMachineAPI),
			Entry("aksmachineapiheaderbatch", consts.ProvisionModeAKSMachineAPIHeaderBatch),
		)

		It("should clear the failure after node public IP is disabled", func() {
			ctx = options.ToContext(ctx, &options.Options{ProvisionMode: consts.ProvisionModeAKSScriptless})
			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsFalse()).To(BeTrue())

			nodeClass.Spec.NodePublicIP.Enabled = lo.ToPtr(false)
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
		})

		DescribeTable("should not restrict configurations that do not enable node public IP",
			func(provisionMode string) {
				for _, nodePublicIP := range []*v1beta1.NodePublicIP{nil, {}, {Enabled: lo.ToPtr(false)}} {
					ctx = options.ToContext(ctx, &options.Options{ProvisionMode: provisionMode})
					nodeClass.Spec.NodePublicIP = nodePublicIP

					result, err := reconciler.Reconcile(ctx, nodeClass)
					Expect(err).ToNot(HaveOccurred())
					Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
					Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
				}
			},
			Entry("aksscriptless", consts.ProvisionModeAKSScriptless),
			Entry("bootstrappingclient", consts.ProvisionModeBootstrappingClient),
			Entry("aksmachineapi", consts.ProvisionModeAKSMachineAPI),
			Entry("aksmachineapiheaderbatch", consts.ProvisionModeAKSMachineAPIHeaderBatch),
		)
	})

	Context("cluster-level FIPS validation", func() {
		BeforeEach(func() {
			ctx = options.ToContext(ctx, test.Options(test.OptionsFields{EnableFIPS: lo.ToPtr(true)}))
		})

		It("should reject a NodeClass with unset FIPS mode", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.FIPSRequired))
			Expect(condition.Message).To(Equal("AKSNodeClass spec.fipsMode must be set to FIPS because FIPS is enabled at the cluster level"))
		})

		It("should reject a NodeClass with FIPS explicitly disabled", func() {
			nodeClass.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeDisabled)

			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.FIPSRequired))
		})

		It("should accept a NodeClass with FIPS enabled", func() {
			nodeClass.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeFIPS)

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())
		})
	})

	Context("Disk Encryption Set RBAC validation", func() {
		var fakeDesClient *fake.DiskEncryptionSetsAPI
		var desReconciler *status.StaticValidationReconciler
		const testID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-des"

		BeforeEach(func() {
			fakeDesClient = &fake.DiskEncryptionSetsAPI{}
			parsedID, err := arm.ParseResourceID(testID)
			Expect(err).ToNot(HaveOccurred())
			desReconciler = status.NewStaticValidationReconciler(fakeDesClient, parsedID)
		})

		It("should set ValidationSucceeded to true and requeue after success interval when Disk Encryption Set RBAC check passes", func() {
			// Configure fake client to return success
			fakeDesClient.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				return armcompute.DiskEncryptionSetsClientGetResponse{
					DiskEncryptionSet: armcompute.DiskEncryptionSet{
						Name:     lo.ToPtr("test-des"),
						Location: lo.ToPtr("eastus"),
					},
				}, nil
			}

			result, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())
		})

		It("should set ValidationSucceeded to false and requeue soon when DES RBAC check fails with 403", func() {
			// Configure fake client to return 403 Forbidden
			fakeDesClient.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				return armcompute.DiskEncryptionSetsClientGetResponse{}, &azcore.ResponseError{
					StatusCode: http.StatusForbidden,
					RawResponse: &http.Response{
						StatusCode: http.StatusForbidden,
					},
				}
			}

			result, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred()) // Auth errors don't return error, just set condition
			Expect(result.RequeueAfter).To(Equal(status.ValidationFailureRequeueInterval))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.DiskEncryptionSetRBACMissing))
			Expect(condition.Message).To(ContainSubstring("does not have Reader role on Disk Encryption Set"))
		})

		It("should set ValidationSucceeded to false and requeue soon when DES RBAC check fails with 401", func() {
			// Configure fake client to return 401 Unauthorized
			fakeDesClient.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				return armcompute.DiskEncryptionSetsClientGetResponse{}, &azcore.ResponseError{
					StatusCode: http.StatusUnauthorized,
					RawResponse: &http.Response{
						StatusCode: http.StatusUnauthorized,
					},
				}
			}

			result, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationFailureRequeueInterval))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.DiskEncryptionSetRBACMissing))
		})

		It("should return error for non-authorization errors", func() {
			// Configure fake client to return network error
			fakeDesClient.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				return armcompute.DiskEncryptionSetsClientGetResponse{}, errors.New("network error")
			}

			// First reconcile - should return error for controller-runtime retry
			result, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to validate DiskEncryptionSet"))
			Expect(result).To(Equal(reconcile.Result{})) // No RequeueAfter, error triggers retry
		})

		It("should handle transition from failure to success", func() {
			shouldFail := true
			fakeDesClient.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				if shouldFail {
					return armcompute.DiskEncryptionSetsClientGetResponse{}, &azcore.ResponseError{
						StatusCode: http.StatusForbidden,
						RawResponse: &http.Response{
							StatusCode: http.StatusForbidden,
						},
					}
				}
				return armcompute.DiskEncryptionSetsClientGetResponse{
					DiskEncryptionSet: armcompute.DiskEncryptionSet{
						Name:     lo.ToPtr("test-des"),
						Location: lo.ToPtr("eastus"),
					},
				}, nil
			}

			// First reconcile - should fail
			result, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationFailureRequeueInterval))
			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())

			// Simulate RBAC being granted
			shouldFail = false

			// Second reconcile - should succeed now
			result, err = desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
			condition = nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())
		})
	})
})
