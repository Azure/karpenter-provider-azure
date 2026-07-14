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
	"strings"

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

	Context("provision mode field validation", func() {
		expectValidationFailed := func(expectedReason string, messageSubstring string) {
			GinkgoHelper()
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(expectedReason))
			Expect(condition.Message).To(ContainSubstring(messageSubstring))
		}
		expectValidationSucceeded := func() {
			GinkgoHelper()
			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded).IsTrue()).To(BeTrue())
		}

		Context("in AKS provision modes", func() {
			BeforeEach(func() {
				ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
					ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSScriptless),
				}))
			})

			It("should reject non-empty userData", func() {
				nodeClass.Spec.UserData = lo.ToPtr("#cloud-config\n")
				expectValidationFailed(status.UnsupportedFieldsForProvisionMode, "spec.userData")
			})

			It("should reject networkSecurityGroupID", func() {
				nodeClass.Spec.NetworkSecurityGroupID = lo.ToPtr("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg")
				expectValidationFailed(status.UnsupportedFieldsForProvisionMode, "spec.networkSecurityGroupID")
			})

			It("should reject userData in AKS Machine API mode", func() {
				ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
					ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
				}))
				nodeClass.Spec.UserData = lo.ToPtr("#cloud-config\n")
				expectValidationFailed(status.UnsupportedFieldsForProvisionMode, "spec.userData")
			})

			It("should accept a NodeClass without the userdata-only fields", func() {
				expectValidationSucceeded()
			})
		})

		Context("in userdata provision mode", func() {
			BeforeEach(func() {
				ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
					ProvisionMode: lo.ToPtr(consts.ProvisionModeUserdata),
				}))
				nodeClass.Spec.MaxPods = lo.ToPtr(int32(110))
				nodeClass.Spec.UserData = lo.ToPtr("#cloud-config\n")
			})

			It("should reject missing userData", func() {
				nodeClass.Spec.UserData = nil
				expectValidationFailed(status.MissingRequiredFields, "spec.userData")
			})

			It("should reject empty userData", func() {
				nodeClass.Spec.UserData = lo.ToPtr("")
				expectValidationFailed(status.MissingRequiredFields, "spec.userData")
			})

			It("should accept non-empty userData and networkSecurityGroupID", func() {
				nodeClass.Spec.UserData = lo.ToPtr("#cloud-config\nruncmd:\n  - echo hello\n")
				nodeClass.Spec.NetworkSecurityGroupID = lo.ToPtr("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg")
				expectValidationSucceeded()
			})

			It("should accept gpu configuration", func() {
				nodeClass.Spec.GPU = &v1beta1.GPU{Mode: lo.ToPtr(v1beta1.GPUModeNone)}
				expectValidationSucceeded()
			})

			It("should accept fipsMode Disabled", func() {
				nodeClass.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeDisabled)
				expectValidationSucceeded()
			})

			It("should reject kubelet configuration", func() {
				nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{}
				expectValidationFailed(status.UnsupportedFieldsForProvisionMode, "spec.kubelet")
			})

			It("should reject localDNS configuration", func() {
				nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeDisabled}
				expectValidationFailed(status.UnsupportedFieldsForProvisionMode, "spec.localDNS")
			})

			It("should reject linuxOSConfig configuration", func() {
				nodeClass.Spec.LinuxOSConfig = &v1beta1.LinuxOSConfiguration{}
				expectValidationFailed(status.UnsupportedFieldsForProvisionMode, "spec.linuxOSConfig")
			})

			It("should reject artifactStreaming configuration", func() {
				nodeClass.Spec.ArtifactStreaming = &v1beta1.ArtifactStreaming{Enabled: lo.ToPtr(true)}
				expectValidationFailed(status.UnsupportedFieldsForProvisionMode, "spec.artifactStreaming")
			})

			It("should reject fipsMode FIPS", func() {
				nodeClass.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeFIPS)
				expectValidationFailed(status.UnsupportedFieldsForProvisionMode, "spec.fipsMode")
			})

			It("should reject missing maxPods", func() {
				nodeClass.Spec.MaxPods = nil
				expectValidationFailed(status.MissingRequiredFields, "spec.maxPods")
			})

			It("should reject whitespace-only userData", func() {
				nodeClass.Spec.UserData = lo.ToPtr(" \n\t ")
				expectValidationFailed(status.InvalidUserData, "whitespace")
			})

			It("should reject userData larger than 65535 bytes", func() {
				nodeClass.Spec.UserData = lo.ToPtr(strings.Repeat("a", 65536))
				expectValidationFailed(status.InvalidUserData, "65535 bytes")
			})
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
