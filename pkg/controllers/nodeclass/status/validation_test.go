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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
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
	var fakeDiskEncryptionSetID string

	BeforeEach(func() {
		ctx = context.Background()
		fakeDesAPI = &fake.DiskEncryptionSetsAPI{}
		fakeDiskEncryptionSetID = ""

		reconciler = status.NewValidationReconciler(fakeDesAPI, fakeDiskEncryptionSetID)
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
			Expect(result.RequeueAfter).To(Equal(status.DESValidationSuccessRequeueInterval))

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
			Expect(result.RequeueAfter).To(Equal(status.DESValidationSuccessRequeueInterval))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())
		})
	})

	Context("DES RBAC validation", func() {
		var fakeDesClient *fake.DiskEncryptionSetsAPI
		var desReconciler *status.ValidationReconciler
		const testDESID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-des"

		BeforeEach(func() {
			fakeDesClient = &fake.DiskEncryptionSetsAPI{}
			desReconciler = status.NewValidationReconciler(fakeDesClient, testDESID)
		})

		It("should set ValidationSucceeded to true and requeue after success interval when DES RBAC check passes", func() {
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
			Expect(result.RequeueAfter).To(Equal(status.DESValidationSuccessRequeueInterval))

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
			Expect(result.RequeueAfter).To(Equal(status.DESValidationFailureRequeueInterval))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.ValidationFailedReasonDESRBACMissing))
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
			Expect(result.RequeueAfter).To(Equal(status.DESValidationFailureRequeueInterval))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.ValidationFailedReasonDESRBACMissing))
		})

		It("should return error and not change condition for non-authorization errors", func() {
			// Set initial condition to True
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeValidationSucceeded)

			// Configure fake client to return network error
			fakeDesClient.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				return armcompute.DiskEncryptionSetsClientGetResponse{}, errors.New("network error")
			}

			// First reconcile - should return error for controller-runtime retry
			result, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to validate DiskEncryptionSet"))
			Expect(result).To(Equal(reconcile.Result{})) // No RequeueAfter, error triggers retry

			// Condition should still be True (unchanged)
			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())
		})

		It("should call API on every reconcile (no caching)", func() {
			callCount := 0
			fakeDesClient.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				callCount++
				return armcompute.DiskEncryptionSetsClientGetResponse{
					DiskEncryptionSet: armcompute.DiskEncryptionSet{
						Name:     lo.ToPtr("test-des"),
						Location: lo.ToPtr("eastus"),
					},
				}, nil
			}

			// First reconcile
			_, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(1))

			// Second reconcile - should call API again (no caching)
			_, err = desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(2))

			// Third reconcile - should call API again
			_, err = desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(3))
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
			Expect(result.RequeueAfter).To(Equal(status.DESValidationFailureRequeueInterval))
			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())

			// Simulate RBAC being granted
			shouldFail = false

			// Second reconcile - should succeed now
			result, err = desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(status.DESValidationSuccessRequeueInterval))
			condition = nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())
		})

		It("should validate correct resource group and DES name are passed to API", func() {
			var capturedRG, capturedName string
			fakeDesClient.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				capturedRG = resourceGroupName
				capturedName = diskEncryptionSetName
				return armcompute.DiskEncryptionSetsClientGetResponse{
					DiskEncryptionSet: armcompute.DiskEncryptionSet{
						Name:     lo.ToPtr(diskEncryptionSetName),
						Location: lo.ToPtr("eastus"),
					},
				}, nil
			}

			_, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(capturedRG).To(Equal("test-rg"))
			Expect(capturedName).To(Equal("test-des"))
		})
	})
})
