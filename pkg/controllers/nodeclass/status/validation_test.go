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
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

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

	BeforeEach(func() {
		ctx = context.Background()
		fakeDesAPI = &fake.DiskEncryptionSetsAPI{}
		opts := &options.Options{}

		// Note: client is nil since these basic tests don't need to interact with k8s objects
		reconciler = status.NewValidationReconciler(nil, fakeDesAPI, opts)
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
		It("should always set ValidationSucceeded condition to true", func() {
			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())

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

			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())

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
			opts := &options.Options{
				DiskEncryptionSetID: testDESID,
			}
			desReconciler = status.NewValidationReconciler(nil, fakeDesClient, opts)
		})

		It("should set ValidationSucceeded to true when DES RBAC check passes", func() {
			// Configure fake client to return success
			fakeDesClient.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				return armcompute.DiskEncryptionSetsClientGetResponse{
					DiskEncryptionSet: armcompute.DiskEncryptionSet{
						Name:     lo.ToPtr("test-des"),
						Location: lo.ToPtr("eastus"),
					},
				}, nil
			}

			_, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())
		})

		It("should set ValidationSucceeded to false when DES RBAC check fails with 403", func() {
			// Configure fake client to return 403 Forbidden
			fakeDesClient.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				return armcompute.DiskEncryptionSetsClientGetResponse{}, &azcore.ResponseError{
					StatusCode: http.StatusForbidden,
					RawResponse: &http.Response{
						StatusCode: http.StatusForbidden,
					},
				}
			}

			_, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred()) // Should not return error, but set condition

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Message).To(ContainSubstring("does not have Reader role on Disk Encryption Set"))
		})

		It("should cache successful validations and avoid redundant API calls", func() {
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

			// First reconcile - should call API
			_, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(1))

			// Second reconcile immediately after - should use cache
			_, err = desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(1)) // No additional call

			// Third reconcile - should still use cache
			_, err = desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(1)) // Still no additional call
		})

		It("should retry on every reconcile when validation fails", func() {
			callCount := 0
			fakeDesClient.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				callCount++
				return armcompute.DiskEncryptionSetsClientGetResponse{}, &azcore.ResponseError{
					StatusCode: http.StatusForbidden,
					RawResponse: &http.Response{
						StatusCode: http.StatusForbidden,
					},
				}
			}

			// First reconcile - should call API and fail
			_, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(1))
			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())

			// Second reconcile - should retry (not use cache for failures)
			_, err = desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(2)) // Called again

			// Third reconcile - should retry again
			_, err = desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(3)) // Called again
		})

		It("should invalidate cache and revalidate after ClearValidationCache is called", func() {
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

			// First reconcile - should call API
			_, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(1))

			// Clear cache
			desReconciler.ClearValidationCache()

			// Second reconcile after cache clear - should call API again
			_, err = desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(2)) // Called again after cache clear
		})

		It("should handle non-authorization errors without caching", func() {
			callCount := 0
			fakeDesClient.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				callCount++
				return armcompute.DiskEncryptionSetsClientGetResponse{}, errors.New("network error")
			}

			// First reconcile - should fail with network error
			_, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(1))
			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Message).To(ContainSubstring("failed to validate DiskEncryptionSet"))

			// Second reconcile - should retry (not cached)
			_, err = desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(2)) // Called again
		})

		It("should handle transition from failure to success", func() {
			callCount := 0
			shouldFail := true
			fakeDesClient.GetFunc = func(ctx context.Context, resourceGroupName string, diskEncryptionSetName string, options *armcompute.DiskEncryptionSetsClientGetOptions) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
				callCount++
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
			_, err := desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(1))
			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsFalse()).To(BeTrue())

			// Simulate RBAC being granted
			shouldFail = false

			// Second reconcile - should succeed now
			_, err = desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(2))
			condition = nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())

			// Third reconcile - should use cache (no additional call)
			_, err = desReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(2)) // Still 2, using cache
		})
	})
})
