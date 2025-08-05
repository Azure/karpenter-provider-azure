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
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("SubnetStatus", func() {
	var subnetClient *fake.SubnetsAPI
	var reconciler *status.SubnetReconciler
	var nodeClass *v1beta1.AKSNodeClass
	var ctx context.Context

	BeforeEach(func() {
		ctx = options.ToContext(context.Background(), &options.Options{
			SubnetID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
		})
		subnetClient = &fake.SubnetsAPI{}
		reconciler = status.NewSubnetReconciler(subnetClient)
		nodeClass = &v1beta1.AKSNodeClass{
			Spec: v1beta1.AKSNodeClassSpec{},
		}
	})

	Context("Subnet validation", func() {
		It("should mark nodeclass as ready when subnet exists and has capacity", func() {
			subnetClient.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
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
			subnetClient.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
				return armnetwork.SubnetsClientGetResponse{}, fmt.Errorf("subnet not found")
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

			subnetClient.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
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
