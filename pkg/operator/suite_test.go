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

package operator

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	autoscalingv1beta1 "sigs.k8s.io/karpenter/pkg/apis/autoscaling/v1beta1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	karpv1alpha1 "sigs.k8s.io/karpenter/pkg/apis/v1alpha1"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

var (
	ctx context.Context
	env *coretest.Environment
)

func TestOperator(t *testing.T) {
	ctx = TestContextWithLogger(t)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Operator")
}

var _ = BeforeSuite(func() {
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...))
})

var _ = AfterSuite(func() {
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = Describe("getVnetGUID", func() {
	const subnetID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/cluster-subnet"
	var vnetClient *fake.VirtualNetworksAPI

	BeforeEach(func() {
		vnetClient = &fake.VirtualNetworksAPI{}
	})

	DescribeTable("should resolve the GUID from the subnet's parent VNet", func(subnetName string) {
		expectedGUID := "12345678-1234-1234-1234-123456789012"
		calls := 0
		vnetClient.GetFunc = func(callCtx context.Context, resourceGroupName, virtualNetworkName string, opts *armnetwork.VirtualNetworksClientGetOptions) (armnetwork.VirtualNetworksClientGetResponse, error) {
			calls++
			Expect(callCtx).To(Equal(ctx))
			Expect(resourceGroupName).To(Equal("test-rg"))
			Expect(virtualNetworkName).To(Equal("test-vnet"))
			Expect(opts).To(BeNil())
			return armnetwork.VirtualNetworksClientGetResponse{
				VirtualNetwork: armnetwork.VirtualNetwork{
					Properties: &armnetwork.VirtualNetworkPropertiesFormat{
						ResourceGUID: lo.ToPtr(expectedGUID),
					},
				},
			}, nil
		}

		id := utils.GetSubnetResourceID("12345678-1234-1234-1234-123456789012", "test-rg", "test-vnet", subnetName)
		guid, err := getVnetGUID(ctx, vnetClient, id)
		Expect(err).ToNot(HaveOccurred())
		Expect(guid).To(Equal(expectedGUID))
		Expect(calls).To(Equal(1))
	},
		Entry("cluster subnet", "cluster-subnet"),
		Entry("another subnet in the same VNet", "custom-subnet"),
	)

	It("should reject an invalid subnet ID without calling Azure", func() {
		vnetClient.GetFunc = func(context.Context, string, string, *armnetwork.VirtualNetworksClientGetOptions) (armnetwork.VirtualNetworksClientGetResponse, error) {
			Fail("unexpected VNet lookup for an invalid subnet ID")
			return armnetwork.VirtualNetworksClientGetResponse{}, nil
		}

		guid, err := getVnetGUID(ctx, vnetClient, "invalid-subnet-id")
		Expect(err).To(MatchError("invalid vnet subnet id: invalid-subnet-id"))
		Expect(guid).To(BeEmpty())
	})

	DescribeTable("should propagate VNet lookup errors", func(statusCode int, errorCode string) {
		expectedErr := &azcore.ResponseError{StatusCode: statusCode, ErrorCode: errorCode}
		vnetClient.GetFunc = func(context.Context, string, string, *armnetwork.VirtualNetworksClientGetOptions) (armnetwork.VirtualNetworksClientGetResponse, error) {
			return armnetwork.VirtualNetworksClientGetResponse{}, expectedErr
		}

		guid, err := getVnetGUID(ctx, vnetClient, subnetID)
		Expect(err).To(MatchError(expectedErr))
		Expect(guid).To(BeEmpty())
	},
		Entry("not found", http.StatusNotFound, "ResourceNotFound"),
		Entry("forbidden", http.StatusForbidden, "AuthorizationFailed"),
		Entry("throttled", http.StatusTooManyRequests, "TooManyRequests"),
	)

	DescribeTable("should reject a VNet without a resource GUID", func(properties *armnetwork.VirtualNetworkPropertiesFormat) {
		vnetClient.GetFunc = func(context.Context, string, string, *armnetwork.VirtualNetworksClientGetOptions) (armnetwork.VirtualNetworksClientGetResponse, error) {
			return armnetwork.VirtualNetworksClientGetResponse{
				VirtualNetwork: armnetwork.VirtualNetwork{Properties: properties},
			}, nil
		}

		guid, err := getVnetGUID(ctx, vnetClient, subnetID)
		Expect(err).To(MatchError("vnet test-vnet does not have a resource GUID"))
		Expect(guid).To(BeEmpty())
	},
		Entry("missing properties", nil),
		Entry("missing GUID", &armnetwork.VirtualNetworkPropertiesFormat{}),
	)

	It("should propagate cancellation to the VNet lookup", func() {
		canceledCtx, cancel := context.WithCancel(ctx)
		cancel()
		vnetClient.GetFunc = func(callCtx context.Context, _ string, _ string, _ *armnetwork.VirtualNetworksClientGetOptions) (armnetwork.VirtualNetworksClientGetResponse, error) {
			return armnetwork.VirtualNetworksClientGetResponse{}, callCtx.Err()
		}

		guid, err := getVnetGUID(canceledCtx, vnetClient, subnetID)
		Expect(err).To(MatchError(context.Canceled))
		Expect(guid).To(BeEmpty())
	})
})

var _ = Describe("getRequiredGVKs", func() {
	It("should not require CapacityBuffer when its feature gate is disabled", func() {
		disabledCtx := coreoptions.ToContext(ctx, coretest.Options())
		gvks := getRequiredGVKs(disabledCtx)
		Expect(gvks).To(HaveLen(4))
		Expect(gvks).To(ContainElement(object.GVK(&karpv1.NodePool{})))
		Expect(gvks).To(ContainElement(object.GVK(&karpv1.NodeClaim{})))
		Expect(gvks).To(ContainElement(object.GVK(&karpv1alpha1.NodeOverlay{})))
		Expect(gvks).To(ContainElement(object.GVK(&v1beta1.AKSNodeClass{})))
		Expect(gvks).ToNot(ContainElement(object.GVK(&autoscalingv1beta1.CapacityBuffer{})))
	})

	It("should require CapacityBuffer v1beta1 when its feature gate is enabled", func() {
		enabledCtx := coreoptions.ToContext(ctx, coretest.Options(coretest.OptionsFields{
			FeatureGates: coretest.FeatureGates{CapacityBuffer: lo.ToPtr(true)},
		}))
		gvks := getRequiredGVKs(enabledCtx)
		Expect(gvks).To(HaveLen(5))
		Expect(gvks).To(ContainElement(object.GVK(&autoscalingv1beta1.CapacityBuffer{})))
	})
})
