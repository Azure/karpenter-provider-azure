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
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	opstatus "github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func setKubernetesVersionReady(nodeClass *v1beta1.AKSNodeClass, kubernetesVersion string) {
	nodeClass.Status.KubernetesVersion = lo.ToPtr(kubernetesVersion)
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
}

// newAuthorizationError returns the authorization failure the DES API surfaces when the
// controlling identity lacks the Reader role, for the DES RBAC cases below.
func newAuthorizationError(statusCode int) error {
	return &azcore.ResponseError{
		StatusCode: statusCode,
		RawResponse: &http.Response{
			StatusCode: statusCode,
		},
	}
}

var _ = Describe("Validation Reconciler", func() {
	var ctx context.Context
	var reconciler *status.ValidationReconciler
	var nodeClass *v1beta1.AKSNodeClass
	var fakeDesAPI *fake.DiskEncryptionSetsAPI
	var emptyDiskEncryptionSetID *arm.ResourceID

	BeforeEach(func() {
		ctx = context.Background()
		fakeDesAPI = &fake.DiskEncryptionSetsAPI{}

		reconciler = status.NewValidationReconciler(fakeDesAPI, emptyDiskEncryptionSetID)
		nodeClass = &v1beta1.AKSNodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-nodeclass",
				Generation: 1,
			},
			Spec: v1beta1.AKSNodeClassSpec{},
		}
		setKubernetesVersionReady(nodeClass, "1.32.0")
	})

	// All LocalDNS validations are now handled declaratively by CEL and kubebuilder markers.
	// The ValidationReconciler is a skeleton for future runtime validations that cannot be
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

	Context("image family compatibility validation", func() {
		It("should set ValidationSucceeded to false with the incompatibility reason and actionable message for Ubuntu2404 on Kubernetes 1.31", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
			setKubernetesVersionReady(nodeClass, "1.31")

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			// Static incompatibility: no polling, the spec watch and Kubernetes version
			// reconciliation are what can make this change.
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.ImageFamilyKubernetesVersionIncompatible))
			Expect(condition.Message).To(Equal(`requested image family "Ubuntu2404" is not supported with discovered Kubernetes version "1.31"; supported range is >= 1.32.0`))
		})

		It("should reject Ubuntu2204 on Kubernetes 1.37", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)
			setKubernetesVersionReady(nodeClass, "1.37")

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.ImageFamilyKubernetesVersionIncompatible))
			Expect(condition.Message).To(Equal(`requested image family "Ubuntu2204" is not supported with discovered Kubernetes version "1.37"; supported range is >= 1.25.2 and < 1.37.0`))
		})

		DescribeTable("should pass compatible boundary combinations",
			func(imageFamily string, kubernetesVersion string) {
				nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
				setKubernetesVersionReady(nodeClass, kubernetesVersion)

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				Expect(condition.IsTrue()).To(BeTrue())
			},
			Entry("Ubuntu2204 lower bound", v1beta1.Ubuntu2204ImageFamily, "1.25.2"),
			Entry("Ubuntu2404 lower bound", v1beta1.Ubuntu2404ImageFamily, "1.32.0"),
		)

		DescribeTable("should not validate image families that are not explicitly version pinned",
			func(imageFamily *string, kubernetesVersion string) {
				nodeClass.Spec.ImageFamily = imageFamily
				setKubernetesVersionReady(nodeClass, kubernetesVersion)

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				Expect(condition.IsTrue()).To(BeTrue())
			},
			Entry("generic Ubuntu past the Ubuntu2204 upper bound", lo.ToPtr(v1beta1.UbuntuImageFamily), "1.37.0"),
			Entry("generic Ubuntu below the Ubuntu2204 lower bound", lo.ToPtr(v1beta1.UbuntuImageFamily), "1.25.1"),
			Entry("unset image family past the Ubuntu2204 upper bound", nil, "1.37.0"),
			Entry("AzureLinux on an old cluster", lo.ToPtr(v1beta1.AzureLinuxImageFamily), "1.20.0"),
		)

		It("should recover ValidationSucceeded to true after changing an incompatible image family to a compatible one", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
			setKubernetesVersionReady(nodeClass, "1.31")

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.ImageFamilyKubernetesVersionIncompatible))

			nodeClass.Generation = 2
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)
			setKubernetesVersionReady(nodeClass, "1.31")

			result, err = reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
			condition = nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())
		})

		It("should recover ValidationSucceeded to true when the cluster is upgraded to a supported Kubernetes version", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
			setKubernetesVersionReady(nodeClass, "1.31")

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsFalse()).To(BeTrue())

			setKubernetesVersionReady(nodeClass, "1.32.0")

			result, err = reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
		})

		It("should not call the DES API for static image family incompatibility", func() {
			parsedID, err := arm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-des")
			Expect(err).ToNot(HaveOccurred())

			desCalls := 0
			fakeDesAPI.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				desCalls++
				return armcompute.DiskEncryptionSetsClientGetResponse{}, nil
			}
			desReconciler := status.NewValidationReconciler(fakeDesAPI, parsedID)
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
			setKubernetesVersionReady(nodeClass, "1.31")

			result, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			Expect(desCalls).To(Equal(0))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.ImageFamilyKubernetesVersionIncompatible))
		})

		It("should still execute DES validation for compatible image family and Kubernetes version", func() {
			parsedID, err := arm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-des")
			Expect(err).ToNot(HaveOccurred())

			desCalls := 0
			fakeDesAPI.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				desCalls++
				return armcompute.DiskEncryptionSetsClientGetResponse{
					DiskEncryptionSet: armcompute.DiskEncryptionSet{
						Name:     lo.ToPtr("test-des"),
						Location: lo.ToPtr("eastus"),
					},
				}, nil
			}
			desReconciler := status.NewValidationReconciler(fakeDesAPI, parsedID)
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
			setKubernetesVersionReady(nodeClass, "1.32.0")

			result, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
			Expect(desCalls).To(Equal(1))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())
		})

		It("should return an error for a pinned image family when the Kubernetes version is unavailable and should not use the incompatibility reason", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
			nodeClass.Status.KubernetesVersion = nil
			nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionUnavailable", "object is awaiting reconciliation")

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("getting kubernetes version: NodeClass condition KubernetesVersionReady"))
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.Reason).ToNot(Equal(status.ImageFamilyKubernetesVersionIncompatible))
		})

		DescribeTable("should not require the Kubernetes version for image families that are not explicitly version pinned",
			func(imageFamily *string) {
				nodeClass.Spec.ImageFamily = imageFamily
				nodeClass.Status.KubernetesVersion = nil
				nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionUnavailable", "object is awaiting reconciliation")

				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				Expect(condition.IsTrue()).To(BeTrue())
			},
			Entry("generic Ubuntu", lo.ToPtr(v1beta1.UbuntuImageFamily)),
			Entry("unset image family", nil),
			Entry("AzureLinux", lo.ToPtr(v1beta1.AzureLinuxImageFamily)),
		)

		DescribeTable("should still run DES validation for image families that are not explicitly version pinned when the Kubernetes version is unavailable",
			func(imageFamily *string, desErr error, expectSucceeded bool, expectedReason string) {
				parsedID, err := arm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-des")
				Expect(err).ToNot(HaveOccurred())

				desCalls := 0
				fakeDesAPI.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
					desCalls++
					if desErr != nil {
						return armcompute.DiskEncryptionSetsClientGetResponse{}, desErr
					}
					return armcompute.DiskEncryptionSetsClientGetResponse{
						DiskEncryptionSet: armcompute.DiskEncryptionSet{
							Name:     lo.ToPtr("test-des"),
							Location: lo.ToPtr("eastus"),
						},
					}, nil
				}
				desReconciler := status.NewValidationReconciler(fakeDesAPI, parsedID)
				nodeClass.Spec.ImageFamily = imageFamily
				nodeClass.Status.KubernetesVersion = nil
				nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionUnavailable", "object is awaiting reconciliation")

				result, err := desReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(desCalls).To(Equal(1))

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
				if expectSucceeded {
					Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
					Expect(condition.IsTrue()).To(BeTrue())
					return
				}
				Expect(result.RequeueAfter).To(Equal(status.ValidationFailureRequeueInterval))
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal(expectedReason))
			},
			Entry("generic Ubuntu with DES access", lo.ToPtr(v1beta1.UbuntuImageFamily), nil, true, ""),
			Entry("unset image family with DES access", nil, nil, true, ""),
			Entry("AzureLinux with DES access", lo.ToPtr(v1beta1.AzureLinuxImageFamily), nil, true, ""),
			Entry("generic Ubuntu without DES access", lo.ToPtr(v1beta1.UbuntuImageFamily), newAuthorizationError(http.StatusForbidden), false, status.DiskEncryptionSetRBACMissing),
			Entry("unset image family without DES access", nil, newAuthorizationError(http.StatusForbidden), false, status.DiskEncryptionSetRBACMissing),
			Entry("AzureLinux without DES access", lo.ToPtr(v1beta1.AzureLinuxImageFamily), newAuthorizationError(http.StatusForbidden), false, status.DiskEncryptionSetRBACMissing),
		)

		It("should return an error when the Kubernetes version is malformed and should not use the incompatibility reason", func() {
			parsedID, err := arm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-des")
			Expect(err).ToNot(HaveOccurred())

			desCalls := 0
			fakeDesAPI.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				desCalls++
				return armcompute.DiskEncryptionSetsClientGetResponse{}, nil
			}
			desReconciler := status.NewValidationReconciler(fakeDesAPI, parsedID)
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)
			setKubernetesVersionReady(nodeClass, "1.32.x")
			initialCondition := *nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)

			result, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).To(MatchError(ContainSubstring(`validating image family compatibility: malformed discovered Kubernetes version "1.32.x"`)))
			// An unexpected (non-incompatibility) error must never masquerade as a static
			// incompatibility: it is returned for retry rather than latched onto the condition.
			var incompatibleErr *imagefamily.ImageFamilyKubernetesVersionIncompatibleError
			Expect(errors.As(err, &incompatibleErr)).To(BeFalse())
			Expect(result).To(Equal(reconcile.Result{}))
			Expect(desCalls).To(Equal(0))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition).ToNot(BeNil())
			Expect(condition.IsUnknown()).To(BeTrue())
			Expect(condition.IsFalse()).To(BeFalse())
			Expect(*condition).To(Equal(initialCondition))
		})

		It("should not treat a malformed Kubernetes version as an error when the image family is not version pinned", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.UbuntuImageFamily)
			setKubernetesVersionReady(nodeClass, "1.32.x")

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.ValidationSuccessRequeueInterval))
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
		})
	})

	Context("Disk Encryption Set RBAC validation", func() {
		var fakeDesClient *fake.DiskEncryptionSetsAPI
		var desReconciler *status.ValidationReconciler
		const testID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-des"

		BeforeEach(func() {
			fakeDesClient = &fake.DiskEncryptionSetsAPI{}
			parsedID, err := arm.ParseResourceID(testID)
			Expect(err).ToNot(HaveOccurred())
			desReconciler = status.NewValidationReconciler(fakeDesClient, parsedID)
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
				return armcompute.DiskEncryptionSetsClientGetResponse{}, newAuthorizationError(http.StatusForbidden)
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
				return armcompute.DiskEncryptionSetsClientGetResponse{}, newAuthorizationError(http.StatusUnauthorized)
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
					return armcompute.DiskEncryptionSetsClientGetResponse{}, newAuthorizationError(http.StatusForbidden)
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

// stubKubernetesVersionProvider reports a fixed Kubernetes version, letting the
// status controller be exercised against versions the envtest API server does not run.
type stubKubernetesVersionProvider struct {
	version string
}

func (p *stubKubernetesVersionProvider) KubeServerVersion(_ context.Context) (string, error) {
	return p.version, nil
}

var _ = Describe("NodeClass Status Controller image family compatibility", func() {
	// Covers the full chain the ValidationReconciler participates in: an explicitly
	// pinned image family that the discovered Kubernetes version does not support must
	// leave the NodeClass not Ready, which is what blocks provisioning.
	newControllerForVersion := func(kubernetesVersion string) *status.Controller {
		return status.NewController(
			env.Client,
			&stubKubernetesVersionProvider{version: kubernetesVersion},
			azureEnv.ImageProvider,
			env.KubernetesInterface,
			env.KubernetesInterface,
			azureEnv.DynamicInterface,
			azureEnv.SubnetsAPI,
			azureEnv.DiskEncryptionSetsAPI,
			testOptions.ParsedDiskEncryptionSetID,
			options.FromContext(ctx).NetworkPolicy,
			options.FromContext(ctx).NetworkPlugin,
		)
	}

	It("should leave the NodeClass not Ready when Ubuntu2404 is pinned on Kubernetes 1.31", func() {
		statusController := newControllerForVersion("1.31.0")

		pinnedNodeClass := test.AKSNodeClass()
		pinnedNodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
		ExpectApplied(ctx, env.Client, pinnedNodeClass)
		ExpectObjectReconciled(ctx, env.Client, statusController, pinnedNodeClass)
		pinnedNodeClass = ExpectExists(ctx, env.Client, pinnedNodeClass)

		Expect(lo.FromPtr(pinnedNodeClass.Status.KubernetesVersion)).To(Equal("1.31.0"))

		validationCondition := pinnedNodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
		Expect(validationCondition.IsFalse()).To(BeTrue())
		Expect(validationCondition.Reason).To(Equal(status.ImageFamilyKubernetesVersionIncompatible))
		Expect(validationCondition.Message).To(ContainSubstring(`requested image family "Ubuntu2404"`))

		// The other status reconcilers succeed, so the NodeClass is not Ready specifically
		// because of the image family incompatibility.
		Expect(pinnedNodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady).IsTrue()).To(BeTrue())
		Expect(pinnedNodeClass.StatusConditions().Get(v1beta1.ConditionTypeKubernetesVersionReady).IsTrue()).To(BeTrue())

		readyCondition := pinnedNodeClass.StatusConditions().Get(opstatus.ConditionReady)
		Expect(readyCondition.IsFalse()).To(BeTrue())
		Expect(readyCondition.Message).To(ContainSubstring(v1beta1.ConditionTypeValidationSucceeded))
	})

	It("should become Ready when Ubuntu2404 is pinned on a supported Kubernetes version", func() {
		statusController := newControllerForVersion("1.32.0")

		pinnedNodeClass := test.AKSNodeClass()
		pinnedNodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
		ExpectApplied(ctx, env.Client, pinnedNodeClass)
		ExpectObjectReconciled(ctx, env.Client, statusController, pinnedNodeClass)
		pinnedNodeClass = ExpectExists(ctx, env.Client, pinnedNodeClass)

		Expect(pinnedNodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
		Expect(pinnedNodeClass.StatusConditions().Get(opstatus.ConditionReady).IsTrue()).To(BeTrue())
	})
})
