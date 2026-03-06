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

package instancetype_test

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/skewer"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
)

// TestFindMaxEphemeralSizeGBAndPlacement tests the pure algorithm for calculating
// ephemeral disk size and placement without any provisioning or API calls.
func TestFindMaxEphemeralSizeGBAndPlacement(t *testing.T) {
	tests := []struct {
		name              string
		skuName           string
		wantSize          int64
		wantPlacement     *armcompute.DiffDiskPlacement
		description       string
	}{
		{
			name:              "Standard_B20ms",
			skuName:           "Standard_B20ms",
			wantSize:          32,
			wantPlacement:     lo.ToPtr(armcompute.DiffDiskPlacementCacheDisk),
			description:       "CacheDiskBytes=32212254720 (~32GB) should be selected as ephemeral disk",
		},
		{
			name:              "Standard_D128ds_v6",
			skuName:           "Standard_D128ds_v6",
			wantSize:          7559,
			wantPlacement:     lo.ToPtr(armcompute.DiffDiskPlacementNvmeDisk),
			description:       "NvmeDiskSizeInMiB=7208960 (~7559GB) with NvmeDisk placement support",
		},
		{
			name:              "Standard_D16plds_v5",
			skuName:           "Standard_D16plds_v5",
			wantSize:          429,
			wantPlacement:     lo.ToPtr(armcompute.DiffDiskPlacementCacheDisk),
			description:       "CacheDiskBytes=429496729600 (~429GB) should be selected",
		},
		{
			name:              "Standard_D2as_v6 does not support ephemeral",
			skuName:           "Standard_D2as_v6",
			wantSize:          0,
			wantPlacement:     nil,
			description:       "EphemeralOSDiskSupported is false, should return 0 and nil",
		},
		{
			name:              "Standard_NC24ads_A100_v4",
			skuName:           "Standard_NC24ads_A100_v4",
			wantSize:          274,
			wantPlacement:     lo.ToPtr(armcompute.DiffDiskPlacementCacheDisk),
			description:       "Has NVMe but SupportedEphemeralOSDiskPlacements does not include NvmeDisk, falls back to CacheDisk with 274GB",
		},
		{
			name:              "Standard_D64s_v3",
			skuName:           "Standard_D64s_v3",
			wantSize:          1717,
			wantPlacement:     lo.ToPtr(armcompute.DiffDiskPlacementCacheDisk),
			description:       "CacheDiskBytes=1717986918400 (~1717GB) should be selected",
		},
		{
			name:              "Standard_A0 does not support ephemeral",
			skuName:           "Standard_A0",
			wantSize:          0,
			wantPlacement:     nil,
			description:       "No ephemeral OS disk support",
		},
		{
			name:              "Standard_D2_v2 does not support ephemeral",
			skuName:           "Standard_D2_v2",
			wantSize:          0,
			wantPlacement:     nil,
			description:       "No ephemeral OS disk support",
		},
		{
			name:              "Nil SKU",
			skuName:           "",
			wantSize:          0,
			wantPlacement:     nil,
			description:       "Nil SKU should return 0 and nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sku *skewer.SKU
			if tt.skuName != "" {
				sku = SkewerSKU(tt.skuName)
			}

			gotSize, gotPlacement := instancetype.FindMaxEphemeralSizeGBAndPlacement(sku)

			if gotSize != tt.wantSize {
				t.Errorf("FindMaxEphemeralSizeGBAndPlacement() size = %v, want %v", gotSize, tt.wantSize)
			}

			// Compare placement pointers - both nil or both equal
			if (gotPlacement == nil) != (tt.wantPlacement == nil) {
				t.Errorf("FindMaxEphemeralSizeGBAndPlacement() placement = %v, want %v", gotPlacement, tt.wantPlacement)
			} else if gotPlacement != nil && tt.wantPlacement != nil && *gotPlacement != *tt.wantPlacement {
				t.Errorf("FindMaxEphemeralSizeGBAndPlacement() placement = %v, want %v", *gotPlacement, *tt.wantPlacement)
			}
		})
	}
}
