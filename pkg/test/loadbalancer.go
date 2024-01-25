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

package test

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
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
