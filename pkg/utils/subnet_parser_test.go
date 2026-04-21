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

package utils

import (
	"testing"
)

func Benchmark(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := GetVnetSubnetIDComponents("/subscriptions/00000000-0000-0000-0000-0000000000/resourceGroups/myrg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/default1")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestGetVnetSubnetIDComponents(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		wantErr           bool
		wantSubscription  string
		wantResourceGroup string
		wantVNetName      string
		wantSubnetName    string
	}{
		{
			name:              "should return correct subnet id components",
			input:             "/subscriptions/00000000-0000-0000-0000-0000000000/resourceGroups/myrg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/default1",
			wantErr:           false,
			wantSubscription:  "00000000-0000-0000-0000-0000000000",
			wantResourceGroup: "myrg",
			wantVNetName:      "my-vnet",
			wantSubnetName:    "default1",
		},
		{
			name:    "should return error for invalid format (short string)",
			input:   "someSubnetID",
			wantErr: true,
		},
		{
			name:    "should return error for incorrect resourceGroups keyword",
			input:   "/subscriptions/00000000-0000-0000-0000-0000000000/resourceGr/myrg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/default1",
			wantErr: true,
		},
		{
			name:    "should return error for repeated subnets in path",
			input:   "/subscriptions/00000000-0000-0000-0000-0000000000/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/sillygeese-VNET/subnets/subnets/AKSMgmtv2-Subnet",
			wantErr: true,
		},
		{
			name:              "is case insensitive for path keywords",
			input:             "/SubscRiptionS/mySubscRiption/ResourceGroupS/myResourceGroup/ProviDerS/MicrOsofT.NetWorK/VirtualNetwOrkS/myVirtualNetwork/SubNetS/mySubnet",
			wantErr:           false,
			wantSubscription:  "mySubscRiption",
			wantResourceGroup: "myResourceGroup",
			wantVNetName:      "myVirtualNetwork",
			wantSubnetName:    "mySubnet",
		},
		{
			name:    "fails for junk path",
			input:   "what/a/bunch/of/junk",
			wantErr: true,
		},
		{
			name:    "fails for path missing subnets segment",
			input:   "/subscriptions/sam/resourceGroups/red/providers/Microsoft.Network/virtualNetworks/soclose",
			wantErr: true,
		},
		{
			name:              "standard vnet subnet id",
			input:             "/subscriptions/SUB_ID/resourceGroups/RG_NAME/providers/Microsoft.Network/virtualNetworks/VNET_NAME/subnets/SUBNET_NAME",
			wantErr:           false,
			wantSubscription:  "SUB_ID",
			wantResourceGroup: "RG_NAME",
			wantVNetName:      "VNET_NAME",
			wantSubnetName:    "SUBNET_NAME",
		},
		{
			name:              "case-insensitive match for all keywords",
			input:             "/SubscriPtioNS/SUB_ID/REsourceGroupS/RG_NAME/ProViderS/MicrosoFT.NetWorK/VirtualNetWorKS/VNET_NAME/SubneTS/SUBNET_NAME",
			wantErr:           false,
			wantSubscription:  "SUB_ID",
			wantResourceGroup: "RG_NAME",
			wantVNetName:      "VNET_NAME",
			wantSubnetName:    "SUBNET_NAME",
		},
		{
			name:    "missing subscription and resource group",
			input:   "/providers/Microsoft.Network/virtualNetworks/VNET_NAME/subnets/SUBNET_NAME",
			wantErr: true,
		},
		{
			name:    "completely invalid",
			input:   "badVnetSubnetID",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetVnetSubnetIDComponents(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("GetVnetSubnetIDComponents(%q) expected error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetVnetSubnetIDComponents(%q) unexpected error: %v", tt.input, err)
			}
			if got.SubscriptionID != tt.wantSubscription {
				t.Errorf("SubscriptionID = %q, want %q", got.SubscriptionID, tt.wantSubscription)
			}
			if got.ResourceGroupName != tt.wantResourceGroup {
				t.Errorf("ResourceGroupName = %q, want %q", got.ResourceGroupName, tt.wantResourceGroup)
			}
			if got.VNetName != tt.wantVNetName {
				t.Errorf("VNetName = %q, want %q", got.VNetName, tt.wantVNetName)
			}
			if got.SubnetName != tt.wantSubnetName {
				t.Errorf("SubnetName = %q, want %q", got.SubnetName, tt.wantSubnetName)
			}
		})
	}
}

func TestGetSubnetResourceID_Reflexive(t *testing.T) {
	vnetsubnetid := GetSubnetResourceID("sam", "red", "violet", "subaru")
	vnet, err := GetVnetSubnetIDComponents(vnetsubnetid)
	if err != nil {
		t.Fatalf("GetVnetSubnetIDComponents returned unexpected error: %v", err)
	}
	if vnet.SubscriptionID != "sam" {
		t.Errorf("SubscriptionID = %q, want %q", vnet.SubscriptionID, "sam")
	}
	if vnet.ResourceGroupName != "red" {
		t.Errorf("ResourceGroupName = %q, want %q", vnet.ResourceGroupName, "red")
	}
	if vnet.VNetName != "violet" {
		t.Errorf("VNetName = %q, want %q", vnet.VNetName, "violet")
	}
	if vnet.SubnetName != "subaru" {
		t.Errorf("SubnetName = %q, want %q", vnet.SubnetName, "subaru")
	}
}

func TestIsSameVNET(t *testing.T) {
	baseResource := VnetSubnetResource{
		SubscriptionID:    "12345678-1234-1234-1234-123456789012",
		ResourceGroupName: "my-resource-group",
		VNetName:          "my-vnet",
		SubnetName:        "my-subnet",
	}

	tests := []struct {
		name     string
		compare  VnetSubnetResource
		expected bool
	}{
		{
			name: "should return true when all VNET components match",
			compare: VnetSubnetResource{
				SubscriptionID:    "12345678-1234-1234-1234-123456789012",
				ResourceGroupName: "my-resource-group",
				VNetName:          "my-vnet",
				SubnetName:        "different-subnet",
			},
			expected: true,
		},
		{
			name: "should return true when subnet names are different but VNET components match",
			compare: VnetSubnetResource{
				SubscriptionID:    "12345678-1234-1234-1234-123456789012",
				ResourceGroupName: "my-resource-group",
				VNetName:          "my-vnet",
				SubnetName:        "completely-different-subnet",
			},
			expected: true,
		},
		{
			name: "should return false when subscription IDs are different",
			compare: VnetSubnetResource{
				SubscriptionID:    "87654321-4321-4321-4321-210987654321",
				ResourceGroupName: "my-resource-group",
				VNetName:          "my-vnet",
				SubnetName:        "my-subnet",
			},
			expected: false,
		},
		{
			name: "should return false when resource group names are different",
			compare: VnetSubnetResource{
				SubscriptionID:    "12345678-1234-1234-1234-123456789012",
				ResourceGroupName: "different-resource-group",
				VNetName:          "my-vnet",
				SubnetName:        "my-subnet",
			},
			expected: false,
		},
		{
			name: "should return false when VNET names are different",
			compare: VnetSubnetResource{
				SubscriptionID:    "12345678-1234-1234-1234-123456789012",
				ResourceGroupName: "my-resource-group",
				VNetName:          "different-vnet",
				SubnetName:        "my-subnet",
			},
			expected: false,
		},
		{
			name: "should return false when multiple components are different",
			compare: VnetSubnetResource{
				SubscriptionID:    "87654321-4321-4321-4321-210987654321",
				ResourceGroupName: "different-resource-group",
				VNetName:          "different-vnet",
				SubnetName:        "different-subnet",
			},
			expected: false,
		},
		{
			name: "different case resource group name",
			compare: VnetSubnetResource{
				SubscriptionID:    "12345678-1234-1234-1234-123456789012",
				ResourceGroupName: "My-Resource-Group",
				VNetName:          "my-vnet",
				SubnetName:        "my-subnet",
			},
			expected: false,
		},
		{
			name: "different case VNET name",
			compare: VnetSubnetResource{
				SubscriptionID:    "12345678-1234-1234-1234-123456789012",
				ResourceGroupName: "my-resource-group",
				VNetName:          "My-VNet",
				SubnetName:        "my-subnet",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := baseResource.IsSameVNET(tt.compare)
			if got != tt.expected {
				t.Errorf("IsSameVNET() = %v, want %v", got, tt.expected)
			}
		})
	}

	// Empty resource comparisons
	t.Run("empty resource comparisons", func(t *testing.T) {
		emptyResource := VnetSubnetResource{
			SubscriptionID:    "",
			ResourceGroupName: "",
			VNetName:          "",
			SubnetName:        "",
		}

		if got := emptyResource.IsSameVNET(emptyResource); got != true {
			t.Errorf("empty.IsSameVNET(empty) = %v, want true", got)
		}
		if got := emptyResource.IsSameVNET(baseResource); got != false {
			t.Errorf("empty.IsSameVNET(base) = %v, want false", got)
		}
		if got := baseResource.IsSameVNET(emptyResource); got != false {
			t.Errorf("base.IsSameVNET(empty) = %v, want false", got)
		}
	})
}
