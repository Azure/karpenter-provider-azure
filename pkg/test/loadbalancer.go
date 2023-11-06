// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package test

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter/pkg/fake"
	"github.com/Azure/karpenter/pkg/providers/loadbalancer"
	"github.com/samber/lo"
)

func MakeStandardLoadBalancer(resourceGroup string, lbName string, includeOutbound bool) armnetwork.LoadBalancer {
	lbID := fake.MakeLoadBalancerID(resourceGroup, lbName)

	result := armnetwork.LoadBalancer{
		ID:   &lbID,
		Name: &lbName,
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			BackendAddressPools: []*armnetwork.BackendAddressPool{
				{
					ID:         lo.ToPtr(fake.MakeBackendAddressPoolID(resourceGroup, lbName, loadbalancer.SLBInboundBackendPoolName)),
					Name:       lo.ToPtr(loadbalancer.SLBInboundBackendPoolName),
					Properties: &armnetwork.BackendAddressPoolPropertiesFormat{},
				},
			},
		},
	}

	if includeOutbound {
		result.Properties.BackendAddressPools = append(result.Properties.BackendAddressPools, &armnetwork.BackendAddressPool{
			ID:         lo.ToPtr(fake.MakeBackendAddressPoolID(resourceGroup, lbName, loadbalancer.SLBOutboundBackendPoolName)),
			Name:       lo.ToPtr(loadbalancer.SLBOutboundBackendPoolName),
			Properties: &armnetwork.BackendAddressPoolPropertiesFormat{},
		})
	}

	return result
}
