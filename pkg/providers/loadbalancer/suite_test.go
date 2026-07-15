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

package loadbalancer_test

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
)

const resourceGroup = "test-rg"

type testFixture struct {
	ctx      context.Context
	api      *fake.LoadBalancersAPI
	cache    *cache.Cache
	provider *loadbalancer.Provider
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()
	ctx := context.Background()
	api := &fake.LoadBalancersAPI{}
	c := cache.New(time.Second, time.Second)
	provider := loadbalancer.NewProvider(api, c, resourceGroup)
	return &testFixture{ctx: ctx, api: api, cache: c, provider: provider}
}

func TestLoadBalancerBackendPools_ReturnsOnlyWellKnownPools(t *testing.T) {
	g := NewWithT(t)
	f := newTestFixture(t)

	standardLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBName, true)
	internalLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.InternalSLBName, false)
	otherLB := test.MakeStandardLoadBalancer(resourceGroup, "some-lb", true)

	f.api.LoadBalancers.Store(lo.FromPtr(standardLB.ID), standardLB)
	f.api.LoadBalancers.Store(lo.FromPtr(internalLB.ID), internalLB)
	f.api.LoadBalancers.Store(lo.FromPtr(otherLB.ID), otherLB)

	pools, err := f.provider.LoadBalancerBackendPools(f.ctx)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(pools.IPv4PoolIDs).To(HaveLen(3))
	g.Expect(pools.IPv6PoolIDs).To(HaveLen(0))
	g.Expect(pools.IPv4PoolIDs[0]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/kubernetes"))
	g.Expect(pools.IPv4PoolIDs[1]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/aksOutboundBackendPool"))
	g.Expect(pools.IPv4PoolIDs[2]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes-internal/backendAddressPools/kubernetes"))
}

func TestLoadBalancerBackendPools_DoesNotReturnIPv6Pools(t *testing.T) {
	g := NewWithT(t)
	f := newTestFixture(t)

	standardLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBName, true)
	internalLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.InternalSLBName, false)
	otherLB := test.MakeStandardLoadBalancer(resourceGroup, "some-lb", true)
	ipv6LB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBNameIPv6, true)

	f.api.LoadBalancers.Store(lo.FromPtr(standardLB.ID), standardLB)
	f.api.LoadBalancers.Store(lo.FromPtr(internalLB.ID), internalLB)
	f.api.LoadBalancers.Store(lo.FromPtr(otherLB.ID), otherLB)
	f.api.LoadBalancers.Store(lo.FromPtr(ipv6LB.ID), ipv6LB)

	pools, err := f.provider.LoadBalancerBackendPools(f.ctx)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(pools.IPv4PoolIDs).To(HaveLen(3))
	g.Expect(pools.IPv6PoolIDs).To(HaveLen(0))
	g.Expect(pools.IPv4PoolIDs[0]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/kubernetes"))
	g.Expect(pools.IPv4PoolIDs[1]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/aksOutboundBackendPool"))
	g.Expect(pools.IPv4PoolIDs[2]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes-internal/backendAddressPools/kubernetes"))
}

func TestLoadBalancerBackendPools_DoesNotReturnIPBasedPools(t *testing.T) {
	g := NewWithT(t)
	f := newTestFixture(t)

	standardLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBName, true)
	standardLB.Properties.BackendAddressPools[1].Properties.LoadBalancerBackendAddresses = []*armnetwork.LoadBalancerBackendAddress{
		{
			Properties: &armnetwork.LoadBalancerBackendAddressPropertiesFormat{
				IPAddress: lo.ToPtr("1.2.3.4"),
			},
		},
	}
	internalLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.InternalSLBName, false)

	f.api.LoadBalancers.Store(lo.FromPtr(standardLB.ID), standardLB)
	f.api.LoadBalancers.Store(lo.FromPtr(internalLB.ID), internalLB)

	pools, err := f.provider.LoadBalancerBackendPools(f.ctx)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(pools.IPv4PoolIDs).To(HaveLen(2))
	g.Expect(pools.IPv6PoolIDs).To(HaveLen(0))
	g.Expect(pools.IPv4PoolIDs[0]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/kubernetes"))
	g.Expect(pools.IPv4PoolIDs[1]).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers/kubernetes-internal/backendAddressPools/kubernetes"))
}

func TestRefreshBackendPools_RefreshesWhenGenerationMatchesCurrentCache(t *testing.T) {
	g := NewWithT(t)
	f := newTestFixture(t)

	standardLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBName, true)
	f.api.LoadBalancers.Store(lo.FromPtr(standardLB.ID), standardLB)

	pools, err := f.provider.LoadBalancerBackendPools(f.ctx)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(pools.IPv4PoolIDs).To(HaveLen(2))
	g.Expect(f.api.NewListPagerBehavior.Calls()).To(Equal(1))

	// Simulate LB deletion
	f.api.LoadBalancers.Clear()

	refreshed, err := f.provider.RefreshBackendPools(f.ctx, pools)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(refreshed.IPv4PoolIDs).To(HaveLen(0))
	g.Expect(f.api.NewListPagerBehavior.Calls()).To(Equal(2))

	// Subsequent LoadBalancerBackendPools should serve from the refreshed cache
	pools2, err := f.provider.LoadBalancerBackendPools(f.ctx)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(pools2.IPv4PoolIDs).To(HaveLen(0))
	g.Expect(f.api.NewListPagerBehavior.Calls()).To(Equal(2)) // no additional call
}

func TestRefreshBackendPools_ReusesNewerGenerationWithoutCallingAzure(t *testing.T) {
	g := NewWithT(t)
	f := newTestFixture(t)

	standardLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBName, true)
	f.api.LoadBalancers.Store(lo.FromPtr(standardLB.ID), standardLB)

	// Get gen 1
	pools1, err := f.provider.LoadBalancerBackendPools(f.ctx)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(f.api.NewListPagerBehavior.Calls()).To(Equal(1))

	// Force a refresh to gen 2
	f.cache.Flush()
	pools2, err := f.provider.LoadBalancerBackendPools(f.ctx)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(f.api.NewListPagerBehavior.Calls()).To(Equal(2))

	// Now try to refresh with stale gen-1 pools — should NOT call Azure again
	refreshed, err := f.provider.RefreshBackendPools(f.ctx, pools1)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(f.api.NewListPagerBehavior.Calls()).To(Equal(2)) // no new call
	g.Expect(refreshed.IPv4PoolIDs).To(HaveLen(len(pools2.IPv4PoolIDs)))
}
