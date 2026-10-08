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

package instancetype

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/alecthomas/units"
	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"

	"github.com/Azure/skewer"
)

const (
	// AKS accepts osDiskSizeGB values up to 2048, while Azure Compute limits ephemeral
	// OS disks to 2040 GiB. Use the Compute limit for effective provisioning capacity.
	// https://learn.microsoft.com/rest/api/aks/agent-pools/create-or-update
	// https://learn.microsoft.com/azure/virtual-machines/ephemeral-os-disks
	maxEphemeralOSDiskSizeGiB = int64(2040)
	// minEphemeralOSDiskSizeGiB is AKS's raw-capacity threshold for auto-selecting an
	// ephemeral OS disk; below it, auto-sizing falls back to vCPU-based managed defaults.
	minEphemeralOSDiskSizeGiB = int64(128)
)

// OSDiskProfile is the per-SKU OS disk configuration consumed by every provisioning path and capacity model.
type OSDiskProfile struct {
	SizeGB    int32
	Type      armcontainerservice.OSDiskType
	Placement *armcompute.DiffDiskPlacement // not used when the resolved OS disk is managed
}

// ResolveOSDiskProfileFromInstanceType resolves to-be-created instance's OS disk configuration based on instance type and other inputs.
func ResolveOSDiskProfileFromInstanceType(
	ctx context.Context,
	provider Provider,
	instanceTypeName string,
	requestedOSDiskSizeGB *int32,
	requestedOSDiskType v1beta1.OSDiskType,
	trustedLaunch bool,
) (OSDiskProfile, error) {
	sku, err := provider.Get(ctx, instanceTypeName)
	if err != nil {
		return OSDiskProfile{}, err
	}
	return ResolveOSDiskProfileFromSKU(sku, requestedOSDiskSizeGB, requestedOSDiskType, trustedLaunch), nil
}

// ResolveOSDiskProfileFromSKU resolves to-be-created instance's OS disk configuration based on VM size and other inputs.
func ResolveOSDiskProfileFromSKU(
	sku *skewer.SKU,
	requestedOSDiskSizeGB *int32,
	requestedOSDiskType v1beta1.OSDiskType,
	trustedLaunch bool,
) OSDiskProfile {
	if requestedOSDiskType == v1beta1.OSDiskTypeManaged {
		// OSDiskType=Managed, OSDiskSize provided: use size as-is
		if requestedOSDiskSizeGB != nil {
			return OSDiskProfile{
				SizeGB: *requestedOSDiskSizeGB,
				Type:   armcontainerservice.OSDiskTypeManaged,
			}
		}
		// OSDiskType=Managed, OSDiskSize not provided: hardcoded default size per SKU
		return OSDiskProfile{
			SizeGB: defaultManagedOSDiskSizeGB(sku),
			Type:   armcontainerservice.OSDiskTypeManaged,
		}
	}

	// OSDiskType not provided, OSDiskSize provided: use size as-is, and try ephemeral by finding a placement
	if requestedOSDiskSizeGB != nil {
		if placement := selectBestEphemeralOSDiskPlacement(sku, int64(*requestedOSDiskSizeGB), trustedLaunch); placement != nil {
			// Suitable ephemeral OS disk placement found, use it
			return OSDiskProfile{
				SizeGB:    *requestedOSDiskSizeGB,
				Type:      armcontainerservice.OSDiskTypeEphemeral,
				Placement: placement,
			}
		} else {
			// No suitable ephemeral OS disk placement found, fallback to managed OS disk
			return OSDiskProfile{
				SizeGB: *requestedOSDiskSizeGB,
				Type:   armcontainerservice.OSDiskTypeManaged,
			}
		}
	}

	// OSDiskType not provided, OSDiskSize not provided: try ephemeral with maximum size, and use that placement
	largest, ok := getPreferredLargestEphemeralOSDiskPlacement(sku)
	if ok {
		maximizedSizeGiB := largest.sizeBytes / int64(units.GiB)

		// Validate against minimum size requirement.
		if maximizedSizeGiB >= minEphemeralOSDiskSizeGiB {
			// If trusted launch is enabled, reserve 1 GiB for secure boot metadata.
			if trustedLaunch {
				maximizedSizeGiB -= 1
			}

			// Bound to the maximum allowed size.
			maximizedSizeGiB = min(maximizedSizeGiB, maxEphemeralOSDiskSizeGiB)

			// Re-select placement given newly processed maximized size.
			placement := selectBestEphemeralOSDiskPlacement(sku, maximizedSizeGiB, trustedLaunch)
			if placement != nil {
				return OSDiskProfile{
					Type:      armcontainerservice.OSDiskTypeEphemeral,
					SizeGB:    int32(maximizedSizeGiB),
					Placement: placement,
				}
			}
		}
	}

	// Fallback to managed OS disk if no suitable ephemeral OS disk placement is found
	return OSDiskProfile{
		SizeGB: defaultManagedOSDiskSizeGB(sku),
		Type:   armcontainerservice.OSDiskTypeManaged,
	}
}

// FindMaxEphemeralSizeGBAndPlacement returns the maximum eligible ephemeral OS disk capacity in integer decimal GB.
// The largest eligible placement is selected before its capacity is capped at the 2040-GiB Compute limit.
func FindMaxEphemeralSizeGBAndPlacement(sku *skewer.SKU) (sizeGB int64, placement *armcompute.DiffDiskPlacement) {
	largest, ok := getPreferredLargestEphemeralOSDiskPlacement(sku)
	if !ok {
		return 0, nil
	}

	maxLabelBytes := maxEphemeralOSDiskSizeGiB * int64(units.GiB)
	sizeGB = min(largest.sizeBytes, maxLabelBytes) / int64(units.Gigabyte)
	if sizeGB == 0 {
		return 0, nil
	}
	return sizeGB, lo.ToPtr(largest.placement)
}

type ephemeralOSDiskPlacement struct {
	placement armcompute.DiffDiskPlacement
	sizeBytes int64
}

// selectBestEphemeralOSDiskPlacement attempts to select a suitable ephemeral OS disk placement for the requested size and trusted launch requirement.
// Preferred placements are considered first. See listRankedEligibleEphemeralOSDiskPlacements() for specifics.
// Returns nil if no suitable placement is found.
func selectBestEphemeralOSDiskPlacement(sku *skewer.SKU, requestedOSDiskSizeGiB int64, trustedLaunch bool) *armcompute.DiffDiskPlacement {
	// Validate the request against the maximum allowed size.
	if requestedOSDiskSizeGiB < 0 || requestedOSDiskSizeGiB > maxEphemeralOSDiskSizeGiB {
		return nil
	}

	requiredBytes := requestedOSDiskSizeGiB * int64(units.GiB)
	// Trusted launch requires additional 1 GiB for reservation.
	if trustedLaunch {
		requiredBytes += int64(units.GiB)
	}

	for _, placement := range listRankedEligibleEphemeralOSDiskPlacements(sku) {
		if requiredBytes <= placement.sizeBytes {
			// Found a suitable placement.
			return lo.ToPtr(placement.placement)
		}
	}

	// No suitable placement found.
	return nil
}

// getPreferredLargestEphemeralOSDiskPlacement returns the largest eligible ephemeral OS disk placement for the given SKU.
// Tie-break with order of preference from listRankedEligibleEphemeralOSDiskPlacements().
func getPreferredLargestEphemeralOSDiskPlacement(sku *skewer.SKU) (ephemeralOSDiskPlacement, bool) {
	placements := listRankedEligibleEphemeralOSDiskPlacements(sku)
	if len(placements) == 0 {
		return ephemeralOSDiskPlacement{}, false
	}

	largest := placements[0]
	for _, candidate := range placements[1:] {
		if candidate.sizeBytes > largest.sizeBytes {
			largest = candidate
		}
	}
	return largest, true
}

// listRankedEligibleEphemeralOSDiskPlacements returns a list of eligible ephemeral OS disk placements for the given SKU, ranked by preference.
func listRankedEligibleEphemeralOSDiskPlacements(sku *skewer.SKU) []ephemeralOSDiskPlacement {
	if sku == nil {
		return nil
	}
	if !sku.IsEphemeralOSDiskSupported() {
		return nil
	}

	// Determine the maximum size for each placement.
	cacheBytes, _ := sku.MaxCachedDiskBytes()
	resourceMiB, _ := sku.MaxResourceVolumeMB()
	nvmeMiB, _ := sku.GetCapabilityIntegerQuantity("NvmeDiskSizeInMiB")
	maxMiBWithoutOverflow := int64(math.MaxInt64) / int64(units.MiB)
	cacheBytes = max(cacheBytes, 0)
	resourceBytes := min(max(resourceMiB, 0), maxMiBWithoutOverflow) * int64(units.MiB)
	nvmeBytes := min(max(nvmeMiB, 0), maxMiBWithoutOverflow) * int64(units.MiB)

	// Determine which ephemeral OS disk placements are supported by the SKU.
	var cacheSupported, resourceSupported, nvmeSupported bool
	value, err := sku.GetCapabilityString("SupportedEphemeralOSDiskPlacements")
	if err != nil {
		var notFound *skewer.ErrCapabilityNotFound
		if errors.As(err, &notFound) {
			// Older SKU payloads omit placement metadata. Preserve their historical
			// CacheDisk/ResourceDisk inference, but never override explicit metadata.
			cacheSupported = cacheBytes > 0
			resourceSupported = resourceBytes > 0
		}
	} else {
		for _, placement := range strings.Split(value, ",") {
			switch {
			case strings.EqualFold(strings.TrimSpace(placement), string(armcompute.DiffDiskPlacementCacheDisk)):
				cacheSupported = true
			case strings.EqualFold(strings.TrimSpace(placement), string(armcompute.DiffDiskPlacementResourceDisk)):
				resourceSupported = true
			case strings.EqualFold(strings.TrimSpace(placement), string(armcompute.DiffDiskPlacementNvmeDisk)):
				nvmeSupported = true
			}
		}
	}

	// Append the eligible ephemeral OS disk placements to the list.
	// Order of preference: CacheDisk > ResourceDisk > NvmeDisk.
	candidates := []ephemeralOSDiskPlacement{}
	candidates = appendEphemeralOSDiskPlacementIfEligible(candidates, cacheSupported, armcompute.DiffDiskPlacementCacheDisk, cacheBytes)
	candidates = appendEphemeralOSDiskPlacementIfEligible(candidates, resourceSupported, armcompute.DiffDiskPlacementResourceDisk, resourceBytes)
	candidates = appendEphemeralOSDiskPlacementIfEligible(candidates, nvmeSupported, armcompute.DiffDiskPlacementNvmeDisk, nvmeBytes)
	return candidates
}

// defaultManagedOSDiskSizeGB returns the managed OS disk size by vCPU count, mirroring AKS defaulting.
// https://learn.microsoft.com/azure/aks/concepts-storage#default-os-disk-sizing
func defaultManagedOSDiskSizeGB(sku *skewer.SKU) int32 {
	if sku == nil {
		return 128
	}
	vcpus, err := sku.VCPU()
	if err != nil {
		return 128
	}
	switch {
	case vcpus < 8:
		return 128
	case vcpus < 16:
		return 256
	case vcpus < 64:
		return 512
	default:
		return 1024
	}
}

func appendEphemeralOSDiskPlacementIfEligible(candidates []ephemeralOSDiskPlacement, supported bool, placement armcompute.DiffDiskPlacement, sizeBytes int64) []ephemeralOSDiskPlacement {
	if supported && sizeBytes > 0 {
		return append(candidates, ephemeralOSDiskPlacement{placement: placement, sizeBytes: sizeBytes})
	}
	return candidates
}
