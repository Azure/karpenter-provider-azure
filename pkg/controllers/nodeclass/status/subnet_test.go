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
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
)

var _ = Describe("SubnetStatus", func() {
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
	})

	It("should mark nodeclass as ready when subnet exists and has capacity", func() {
		azureEnv.SubnetsAPI.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
			return armnetwork.SubnetsClientGetResponse{
				Subnet: armnetwork.Subnet{
					Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: lo.ToPtr("10.0.0.0/16"),
						IPConfigurations: []*armnetwork.IPConfiguration{
							{}, {}, {}, {}, {}, // 5 used IPs
						},
					},
				},
			}, nil
		}

		ExpectApplied(ctx, env.Client, nodeClass)
		ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetReady)
		Expect(cond.IsTrue()).To(BeTrue())
	})

	// Note: This test uses direct reconciler because ExpectObjectReconciled doesn't handle errors
	It("should mark nodeclass as not ready when subnet doesn't exist", func() {
		reconciler := status.NewSubnetReconciler(azureEnv.SubnetsAPI)
		
		azureEnv.SubnetsAPI.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
			return armnetwork.SubnetsClientGetResponse{}, &azcore.ResponseError{
				ErrorCode:  "ResourceNotFound",
				StatusCode: http.StatusNotFound,
				RawResponse: &http.Response{
					StatusCode: http.StatusNotFound,
				},
			}
		}

		result, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).To(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{RequeueAfter: time.Minute}))

		cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetReady)
		Expect(cond.IsFalse()).To(BeTrue())
		Expect(cond.Reason).To(Equal("SubnetNotFound"))
	})

	It("should use nodeclass subnet ID when specified", func() {
		nodeClass.Spec.VNETSubnetID = lo.ToPtr("/subscriptions/87654321-4321-4321-4321-210987654321/resourceGroups/nodeclass-rg/providers/Microsoft.Network/virtualNetworks/nodeclass-vnet/subnets/nodeclass-subnet")

		azureEnv.SubnetsAPI.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
			Expect(resourceGroupName).To(Equal("nodeclass-rg"))
			Expect(virtualNetworkName).To(Equal("nodeclass-vnet"))
			Expect(subnetName).To(Equal("nodeclass-subnet"))

			return armnetwork.SubnetsClientGetResponse{
				Subnet: armnetwork.Subnet{
					Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: lo.ToPtr("10.0.0.0/16"),
					},
				},
			}, nil
		}

		ExpectApplied(ctx, env.Client, nodeClass)
		ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetReady)
		Expect(cond.IsTrue()).To(BeTrue())
	})

	Context("SubnetReconciler direct tests", func() {
		var reconciler *status.SubnetReconciler

		BeforeEach(func() {
			reconciler = status.NewSubnetReconciler(azureEnv.SubnetsAPI)
			nodeClass = test.AKSNodeClass()
		})

		It("should mark nodeclass as ready when subnet exists with sufficient capacity", func() {
			azureEnv.SubnetsAPI.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
				return armnetwork.SubnetsClientGetResponse{
					Subnet: armnetwork.Subnet{
						Properties: &armnetwork.SubnetPropertiesFormat{
							AddressPrefix: lo.ToPtr("10.0.0.0/16"),
							IPConfigurations: []*armnetwork.IPConfiguration{
								{}, {}, {}, {}, {}, // 5 used IPs
							},
						},
					},
				}, nil
			}

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{RequeueAfter: time.Minute * 3}))

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetReady)
			Expect(cond.IsTrue()).To(BeTrue())
		})

		It("should mark nodeclass as not ready when subnet doesn't exist", func() {
			azureEnv.SubnetsAPI.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
				return armnetwork.SubnetsClientGetResponse{}, &azcore.ResponseError{
					ErrorCode:  "ResourceNotFound",
					StatusCode: http.StatusNotFound,
					RawResponse: &http.Response{
						StatusCode: http.StatusNotFound,
					},
				}
			}

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).To(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{RequeueAfter: time.Minute}))

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetReady)
			Expect(cond.IsFalse()).To(BeTrue())
			Expect(cond.Reason).To(Equal("SubnetNotFound"))
		})

		It("should use nodeclass subnet ID when specified", func() {
			nodeClass.Spec.VNETSubnetID = lo.ToPtr("/subscriptions/87654321-4321-4321-4321-210987654321/resourceGroups/nodeclass-rg/providers/Microsoft.Network/virtualNetworks/nodeclass-vnet/subnets/nodeclass-subnet")

			azureEnv.SubnetsAPI.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
				Expect(resourceGroupName).To(Equal("nodeclass-rg"))
				Expect(virtualNetworkName).To(Equal("nodeclass-vnet"))
				Expect(subnetName).To(Equal("nodeclass-subnet"))

				return armnetwork.SubnetsClientGetResponse{
					Subnet: armnetwork.Subnet{
						Properties: &armnetwork.SubnetPropertiesFormat{
							AddressPrefix: lo.ToPtr("10.0.0.0/16"),
						},
					},
				}, nil
			}

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{RequeueAfter: time.Minute * 3}))

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetReady)
			Expect(cond.IsTrue()).To(BeTrue())
		})
	})
})