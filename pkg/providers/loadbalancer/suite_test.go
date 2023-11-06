// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package loadbalancer_test

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	. "knative.dev/pkg/logging/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter/pkg/fake"
	"github.com/Azure/karpenter/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter/pkg/test"
)

var ctx context.Context
var stop context.CancelFunc

var resourceGroup string
var fakeLoadBalancersAPI *fake.LoadBalancersAPI
var loadBalancerProvider *loadbalancer.Provider
var loadBalancerCache *cache.Cache

func TestAKS(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Providers/LoadBalancer/AKS")
}

var _ = BeforeSuite(func() {
	ctx, stop = context.WithCancel(ctx)

	fakeLoadBalancersAPI = &fake.LoadBalancersAPI{}
	resourceGroup = "test-rg"
	loadBalancerCache = cache.New(time.Second, time.Second)
	loadBalancerProvider = loadbalancer.NewProvider(fakeLoadBalancersAPI, loadBalancerCache, resourceGroup)
})

var _ = AfterSuite(func() {
	stop()
})

var _ = BeforeEach(func() {
	fakeLoadBalancersAPI.Reset()
	loadBalancerCache.Flush()
})

var _ = Describe("LoadBalancer Provider", func() {
	Context("Backend pools", func() {
		It("should return only well-known loadbalancer pools", func() {
			standardLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBName, true)
			internalLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.InternalSLBName, false)
			otherLB := test.MakeStandardLoadBalancer(resourceGroup, "some-lb", true)

			fakeLoadBalancersAPI.LoadBalancers.Store(standardLB.ID, standardLB)
			fakeLoadBalancersAPI.LoadBalancers.Store(internalLB.ID, internalLB)
			fakeLoadBalancersAPI.LoadBalancers.Store(otherLB.ID, otherLB)

			pools, err := loadBalancerProvider.LoadBalancerBackendPools(ctx)
			Expect(err).ToNot(HaveOccurred())

			Expect(pools.IPv4PoolIDs).To(HaveLen(3))
			Expect(pools.IPv6PoolIDs).To(HaveLen(0))
			Expect(pools.IPv4PoolIDs[0]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/kubernetes"))
			Expect(pools.IPv4PoolIDs[1]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/aksOutboundBackendPool"))
			Expect(pools.IPv4PoolIDs[2]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes-internal/backendAddressPools/kubernetes"))
		})

		It("should not return IPV6 pools", func() {
			standardLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBName, true)
			internalLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.InternalSLBName, false)
			otherLB := test.MakeStandardLoadBalancer(resourceGroup, "some-lb", true)
			ipv6LB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBNameIPv6, true)

			fakeLoadBalancersAPI.LoadBalancers.Store(standardLB.ID, standardLB)
			fakeLoadBalancersAPI.LoadBalancers.Store(internalLB.ID, internalLB)
			fakeLoadBalancersAPI.LoadBalancers.Store(otherLB.ID, otherLB)
			fakeLoadBalancersAPI.LoadBalancers.Store(ipv6LB.ID, ipv6LB)

			pools, err := loadBalancerProvider.LoadBalancerBackendPools(ctx)
			Expect(err).ToNot(HaveOccurred())

			Expect(pools.IPv4PoolIDs).To(HaveLen(3))
			Expect(pools.IPv6PoolIDs).To(HaveLen(0))
			Expect(pools.IPv4PoolIDs[0]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/kubernetes"))
			Expect(pools.IPv4PoolIDs[1]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/aksOutboundBackendPool"))
			Expect(pools.IPv4PoolIDs[2]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes-internal/backendAddressPools/kubernetes"))
		})

		It("should not return IP-based pools", func() {
			standardLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBName, true)
			standardLB.Properties.BackendAddressPools[1].Properties.LoadBalancerBackendAddresses = []*armnetwork.LoadBalancerBackendAddress{
				{
					Properties: &armnetwork.LoadBalancerBackendAddressPropertiesFormat{
						IPAddress: lo.ToPtr("1.2.3.4"),
					},
				},
			}
			internalLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.InternalSLBName, false)

			fakeLoadBalancersAPI.LoadBalancers.Store(standardLB.ID, standardLB)
			fakeLoadBalancersAPI.LoadBalancers.Store(internalLB.ID, internalLB)

			pools, err := loadBalancerProvider.LoadBalancerBackendPools(ctx)
			Expect(err).ToNot(HaveOccurred())

			Expect(pools.IPv4PoolIDs).To(HaveLen(2))
			Expect(pools.IPv6PoolIDs).To(HaveLen(0))
			Expect(pools.IPv4PoolIDs[0]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/kubernetes"))
			Expect(pools.IPv4PoolIDs[1]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes-internal/backendAddressPools/kubernetes"))
		})
	})
})
