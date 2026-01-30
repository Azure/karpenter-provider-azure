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

package cloudprovider

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenerateNodeClaimName(t *testing.T) {
	tests := []struct {
		name     string
		vmName   string
		expected string
	}{
		{
			name:     "basic",
			vmName:   "aks-default-a1b2c",
			expected: "default-a1b2c",
		},
		{
			name:     "dashes nodepool name",
			vmName:   "aks-node-pool-name-a1b2c",
			expected: "node-pool-name-a1b2c",
		},
		{
			name:     "aks",
			vmName:   "aks-aks-default-a1b2c",
			expected: "aks-default-a1b2c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result := GetNodeClaimNameFromVMName(tt.vmName)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestVmInstanceToNodeClaim_NilProperties(t *testing.T) {
	tests := []struct {
		name           string
		vm             *armcompute.VirtualMachine
		expectNoTime   bool
		expectTimeSet  bool
	}{
		{
			name: "nil Properties",
			vm: &armcompute.VirtualMachine{
				Name: to.Ptr("aks-test-vm"),
				ID:   to.Ptr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/aks-test-vm"),
			},
			expectNoTime: true,
		},
		{
			name: "nil TimeCreated",
			vm: &armcompute.VirtualMachine{
				Name: to.Ptr("aks-test-vm"),
				ID:   to.Ptr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/aks-test-vm"),
				Properties: &armcompute.VirtualMachineProperties{},
			},
			expectNoTime: true,
		},
		{
			name: "valid TimeCreated",
			vm: &armcompute.VirtualMachine{
				Name: to.Ptr("aks-test-vm"),
				ID:   to.Ptr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/aks-test-vm"),
				Properties: &armcompute.VirtualMachineProperties{
					TimeCreated: to.Ptr(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
				},
			},
			expectTimeSet: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()
			
			cp := &CloudProvider{}
			nodeClaim, err := cp.vmInstanceToNodeClaim(ctx, tt.vm, nil)
			
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(nodeClaim).ToNot(BeNil())
			
			if tt.expectNoTime {
				// CreationTimestamp should be zero value when Properties or TimeCreated is nil
				g.Expect(nodeClaim.CreationTimestamp).To(Equal(metav1.Time{}))
			}
			
			if tt.expectTimeSet {
				// CreationTimestamp should be set when TimeCreated is valid
				g.Expect(nodeClaim.CreationTimestamp).ToNot(Equal(metav1.Time{}))
				g.Expect(nodeClaim.CreationTimestamp.Time).To(Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)))
			}
		})
	}
}
