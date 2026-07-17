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

package instance

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	. "github.com/onsi/gomega"
)

// makeInvalidResourceReferenceError constructs an error that looks like what Azure Network RP
// returns when a NIC references a deleted load balancer backend pool. The error wraps an
// *azcore.ResponseError (so sdkerrors.IsResponseError finds it) and includes the referenced
// resource ID in the message (so isMissingSubmittedBackendPool can match it).
func makeInvalidResourceReferenceError(referencedResourceID string) error {
	inner := &azcore.ResponseError{
		ErrorCode:  "InvalidResourceReference",
		StatusCode: 400,
	}
	return fmt.Errorf(
		"Resource %s referenced by resource /subscriptions/sub1/resourceGroups/MC_rg/providers/Microsoft.Network/networkInterfaces/aks-test-node-abc12 was not found. "+
			"Please make sure that the referenced resource exists, and that both resources are in the same region.: %w",
		referencedResourceID,
		inner,
	)
}

func TestIsMissingSubmittedBackendPool(t *testing.T) {
	pools := &loadbalancer.BackendAddressPools{
		IPv4PoolIDs: []string{
			"/subscriptions/sub1/resourceGroups/mc_rg/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/kubernetes",
			"/subscriptions/sub1/resourceGroups/mc_rg/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/aksOutboundBackendPool",
		},
	}
	tests := []struct {
		name   string
		err    error
		pools  *loadbalancer.BackendAddressPools
		expect bool
	}{
		{
			name:   "matching InvalidResourceReference with pool ID in message",
			err:    makeInvalidResourceReferenceError(pools.IPv4PoolIDs[0]),
			pools:  pools,
			expect: true,
		},
		{
			name:   "matching InvalidResourceReference with second pool ID",
			err:    makeInvalidResourceReferenceError(pools.IPv4PoolIDs[1]),
			pools:  pools,
			expect: true,
		},
		{
			name: "matching InvalidResourceReference with case-insensitive pool ID",
			err: makeInvalidResourceReferenceError(
				"/subscriptions/sub1/resourceGroups/MC_RG/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/KUBERNETES",
			),
			pools:  pools,
			expect: true,
		},
		{
			name: "InvalidResourceReference about a subnet does not match",
			err: makeInvalidResourceReferenceError(
				"/subscriptions/sub1/resourceGroups/mc_rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/default",
			),
			pools:  pools,
			expect: false,
		},
		{
			name:   "different error code does not match",
			err:    fmt.Errorf("subnet issue: %w", &azcore.ResponseError{ErrorCode: "SubnetNotFound", StatusCode: 400}),
			pools:  pools,
			expect: false,
		},
		{
			name:   "non-Azure error does not match",
			err:    errors.New("connection refused"),
			pools:  pools,
			expect: false,
		},
		{
			name:   "nil error does not match",
			err:    nil,
			pools:  pools,
			expect: false,
		},
		{
			name:   "empty pool list does not match",
			err:    makeInvalidResourceReferenceError(pools.IPv4PoolIDs[0]),
			pools:  &loadbalancer.BackendAddressPools{},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isMissingSubmittedBackendPool(tt.err, tt.pools)).To(Equal(tt.expect))
		})
	}
}
