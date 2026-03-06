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

// Extracted from suite_test.go — FindMaxEphemeralSizeGBAndPlacement table-driven tests.
package instancetype_test

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/skewer"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
)

func TestFindMaxEphemeralSizeGBAndPlacement(t *testing.T) {
	// B20ms:
	// NvmeDiskSizeInMiB == 0
	// CacheDiskBytes == 32212254720 -> 32.21225472 GB .. we should select this as the ephemeral disk size
	// placement == CacheDisk
	// MaxResourceVolumeMB == 163840 MiB -> 171.80 GB,
	// Standard_D128ds_v6:
	// NvmeDiskSizeInMiB == 7208960 -> 7559.142441 GB // SupportedEphemeralOSDiskPlacements == NvmeDisk
	// and this is greater than 0, so we select 7559, placement == NvmeDisk
	// Standard_D16plds_v5:
	// NvmeDiskSizeInMiB == 0
	// CacheDiskBytes == 429496729600 -> 429.4967296, this is greater than zero, so we select this as the ephemeral disk size
	// placement == CacheDisk and size == 429.4967296 GB
	// MaxResourceVolumeMB == 614400 MiB
	// Standard_D2as_v6: -> EphemeralOSDiskSupported is false, it should return 0 and nil for placement
	// Standard_NC24ads_A100_v4:
	// {Name: lo.ToPtr("SupportedEphemeralOSDiskPlacements"), Value: lo.ToPtr("ResourceDisk,CacheDisk")},
	// NvmeDiskSizeInMiB == 915527 -> 959.99964 GB  but no SupportedEphemeralOSDiskPlacements == NvmeDisk so we move to cache disk
	// CacheDiskBytes == 274877906944 -> 274.877906944 GB so we select cache disk + 274
	// MaxResourceVolumeMB == 65536 MiB
	// Standard_D64s_v3:
	// NvmeDiskSizeInMiB == 0
	// CacheDiskBytes == 1717986918400 -> 1717.9869184 GB, this is greater than zero, so we select this as the ephemeral disk size
	// placement == CacheDisk and size == 1717 GB
	// Standard_A0
	// NvmeDiskSizeInMiB == 0
	// CacheDiskBytes == 0, this is zero
	// MaxResourceVolumeMB == 20480 Mib -> 21.474836 GB. Note that this sku doesnt support ephemeral os disk

	tests := []struct {
		name              string
		sku               *skewer.SKU
		expectedSize      int64
		expectedPlacement *armcompute.DiffDiskPlacement
	}{
		{
			name:              "Standard_B20ms",
			sku:               SkewerSKU("Standard_B20ms"),
			expectedSize:      int64(32),
			expectedPlacement: lo.ToPtr(armcompute.DiffDiskPlacementCacheDisk),
		},
		{
			name:              "Standard_D128ds_v6",
			sku:               SkewerSKU("Standard_D128ds_v6"),
			expectedSize:      int64(7559),
			expectedPlacement: lo.ToPtr(armcompute.DiffDiskPlacementNvmeDisk),
		},
		{
			name:              "Standard_D16plds_v5",
			sku:               SkewerSKU("Standard_D16plds_v5"),
			expectedSize:      int64(429),
			expectedPlacement: lo.ToPtr(armcompute.DiffDiskPlacementCacheDisk),
		},
		{
			name:              "Standard_D2as_v6 - does not support ephemeral",
			sku:               SkewerSKU("Standard_D2as_v6"),
			expectedSize:      int64(0),
			expectedPlacement: nil,
		},
		{
			name:              "Standard_NC24ads_A100_v4",
			sku:               SkewerSKU("Standard_NC24ads_A100_v4"),
			expectedSize:      int64(274),
			expectedPlacement: lo.ToPtr(armcompute.DiffDiskPlacementCacheDisk),
		},
		{
			name:              "Standard_D64s_v3",
			sku:               SkewerSKU("Standard_D64s_v3"),
			expectedSize:      int64(1717),
			expectedPlacement: lo.ToPtr(armcompute.DiffDiskPlacementCacheDisk),
		},
		{
			name:              "Standard_A0 - does not support ephemeral",
			sku:               SkewerSKU("Standard_A0"),
			expectedSize:      int64(0),
			expectedPlacement: nil,
		},
		{
			name:              "Standard_D2_v2 - does not support ephemeral",
			sku:               SkewerSKU("Standard_D2_v2"),
			expectedSize:      int64(0),
			expectedPlacement: nil,
		},
		// TODO: codegen
		// {name: "Standard_D2pls_v5", sku: SkewerSKU("Standard_D2pls_v5"), expectedSize: int64(0), expectedPlacement: nil},
		// {name: "Standard_D2lds_v5", sku: SkewerSKU("Standard_D2lds_v5"), expectedSize: int64(80), expectedPlacement: lo.ToPtr(armcompute.DiffDiskPlacementResourceDisk)},
		{
			name:              "Nil SKU",
			sku:               nil,
			expectedSize:      int64(0),
			expectedPlacement: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sizeGB, placement := instancetype.FindMaxEphemeralSizeGBAndPlacement(tt.sku)
			if sizeGB != tt.expectedSize {
				t.Errorf("sizeGB = %d, want %d", sizeGB, tt.expectedSize)
			}
			if tt.expectedPlacement == nil {
				if placement != nil {
					t.Errorf("placement = %v, want nil", *placement)
				}
			} else {
				if placement == nil {
					t.Fatalf("placement is nil, want %v", *tt.expectedPlacement)
					return
				}
				if *placement != *tt.expectedPlacement {
					t.Errorf("placement = %v, want %v", *placement, *tt.expectedPlacement)
				}
			}
		})
	}
}
