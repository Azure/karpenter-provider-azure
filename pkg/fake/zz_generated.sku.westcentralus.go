//go:build !ignore_autogenerated

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

package fake

import (
	"github.com/samber/lo"
	// nolint SA1019 - deprecated package
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2022-08-01/compute"
)

// generated at 2024-12-13T00:09:53Z

func init() {
	// ResourceSkus is a list of selected VM SKUs for a given region
	ResourceSkus["westcentralus"] = []compute.ResourceSku{
		{
			Name:         lo.ToPtr("Standard_A0"),
			Tier:         lo.ToPtr("Standard"),
			Kind:         lo.ToPtr(""),
			Size:         lo.ToPtr("A0"),
			Family:       lo.ToPtr("standardA0_A7Family"),
			ResourceType: lo.ToPtr("virtualMachines"),
			APIVersions:  &[]string{},
			Costs:        &[]compute.ResourceSkuCosts{},
			Restrictions: &[]compute.ResourceSkuRestrictions{
				{
					Type:   compute.ResourceSkuRestrictionsType("Location"),
					Values: &[]string{"westcentralus"},
					RestrictionInfo: &compute.ResourceSkuRestrictionInfo{
						Locations: &[]string{
							"westcentralus",
						},
						Zones: &[]string{},
					},
					ReasonCode: "NotAvailableForSubscription",
				},
			},
			Capabilities: &[]compute.ResourceSkuCapabilities{
				{Name: lo.ToPtr("MaxResourceVolumeMB"), Value: lo.ToPtr("20480")},
				{Name: lo.ToPtr("OSVhdSizeMB"), Value: lo.ToPtr("1047552")},
				{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("1")},
				{Name: lo.ToPtr("MemoryPreservingMaintenanceSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("HyperVGenerations"), Value: lo.ToPtr("V1")},
				{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("0.75")},
				{Name: lo.ToPtr("MaxDataDiskCount"), Value: lo.ToPtr("1")},
				{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("x64")},
				{Name: lo.ToPtr("LowPriorityCapable"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("PremiumIO"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("VMDeploymentTypes"), Value: lo.ToPtr("IaaS,PaaS")},
				{Name: lo.ToPtr("vCPUsAvailable"), Value: lo.ToPtr("1")},
				{Name: lo.ToPtr("ACUs"), Value: lo.ToPtr("50")},
				{Name: lo.ToPtr("vCPUsPerCore"), Value: lo.ToPtr("1")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedIOPS"), Value: lo.ToPtr("1000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedReadBytesPerSecond"), Value: lo.ToPtr("20971520")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedWriteBytesPerSecond"), Value: lo.ToPtr("10485760")},
				{Name: lo.ToPtr("UncachedDiskIOPS"), Value: lo.ToPtr("1600")},
				{Name: lo.ToPtr("UncachedDiskBytesPerSecond"), Value: lo.ToPtr("24000000")},
				{Name: lo.ToPtr("EphemeralOSDiskSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("EncryptionAtHostSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("CapacityReservationSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("AcceleratedNetworkingEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("RdmaEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("MaxNetworkInterfaces"), Value: lo.ToPtr("2")},
			},
			Locations:    &[]string{"westcentralus"},
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{Location: lo.ToPtr("westcentralus"), Zones: &[]string{}}},
		},
		{
			Name:         lo.ToPtr("Standard_B1s"),
			Tier:         lo.ToPtr("Standard"),
			Kind:         lo.ToPtr(""),
			Size:         lo.ToPtr("B1s"),
			Family:       lo.ToPtr("standardBSFamily"),
			ResourceType: lo.ToPtr("virtualMachines"),
			APIVersions:  &[]string{},
			Costs:        &[]compute.ResourceSkuCosts{},
			Restrictions: &[]compute.ResourceSkuRestrictions{},
			Capabilities: &[]compute.ResourceSkuCapabilities{
				{Name: lo.ToPtr("MaxResourceVolumeMB"), Value: lo.ToPtr("4096")},
				{Name: lo.ToPtr("OSVhdSizeMB"), Value: lo.ToPtr("1047552")},
				{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("1")},
				{Name: lo.ToPtr("MemoryPreservingMaintenanceSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("HyperVGenerations"), Value: lo.ToPtr("V1,V2")},
				{Name: lo.ToPtr("SupportedEphemeralOSDiskPlacements"), Value: lo.ToPtr("ResourceDisk,CacheDisk")},
				{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("1")},
				{Name: lo.ToPtr("MaxDataDiskCount"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("x64")},
				{Name: lo.ToPtr("LowPriorityCapable"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("PremiumIO"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("VMDeploymentTypes"), Value: lo.ToPtr("IaaS")},
				{Name: lo.ToPtr("vCPUsAvailable"), Value: lo.ToPtr("1")},
				{Name: lo.ToPtr("vCPUsPerCore"), Value: lo.ToPtr("1")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedIOPS"), Value: lo.ToPtr("400")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedReadBytesPerSecond"), Value: lo.ToPtr("22528000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedWriteBytesPerSecond"), Value: lo.ToPtr("22528000")},
				{Name: lo.ToPtr("CachedDiskBytes"), Value: lo.ToPtr("32212254720")},
				{Name: lo.ToPtr("UncachedDiskIOPS"), Value: lo.ToPtr("320")},
				{Name: lo.ToPtr("UncachedDiskBytesPerSecond"), Value: lo.ToPtr("22500000")},
				{Name: lo.ToPtr("EphemeralOSDiskSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("EncryptionAtHostSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("CapacityReservationSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("AcceleratedNetworkingEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("RdmaEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("MaxNetworkInterfaces"), Value: lo.ToPtr("2")},
			},
			Locations:    &[]string{"westcentralus"},
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{Location: lo.ToPtr("westcentralus"), Zones: &[]string{}}},
		},
		{
			Name:         lo.ToPtr("Standard_D16plds_v5"),
			Tier:         lo.ToPtr("Standard"),
			Kind:         lo.ToPtr(""),
			Size:         lo.ToPtr("D16plds_v5"),
			Family:       lo.ToPtr("standardDPLDSv5Family"),
			ResourceType: lo.ToPtr("virtualMachines"),
			APIVersions:  &[]string{},
			Costs:        &[]compute.ResourceSkuCosts{},
			Restrictions: &[]compute.ResourceSkuRestrictions{},
			Capabilities: &[]compute.ResourceSkuCapabilities{
				{Name: lo.ToPtr("MaxResourceVolumeMB"), Value: lo.ToPtr("614400")},
				{Name: lo.ToPtr("OSVhdSizeMB"), Value: lo.ToPtr("1047552")},
				{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("16")},
				{Name: lo.ToPtr("MemoryPreservingMaintenanceSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("HyperVGenerations"), Value: lo.ToPtr("V2")},
				{Name: lo.ToPtr("SupportedEphemeralOSDiskPlacements"), Value: lo.ToPtr("ResourceDisk,CacheDisk")},
				{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("32")},
				{Name: lo.ToPtr("MaxDataDiskCount"), Value: lo.ToPtr("32")},
				{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("Arm64")},
				{Name: lo.ToPtr("LowPriorityCapable"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("PremiumIO"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("VMDeploymentTypes"), Value: lo.ToPtr("IaaS")},
				{Name: lo.ToPtr("vCPUsAvailable"), Value: lo.ToPtr("16")},
				{Name: lo.ToPtr("vCPUsPerCore"), Value: lo.ToPtr("1")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedIOPS"), Value: lo.ToPtr("75000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedReadBytesPerSecond"), Value: lo.ToPtr("1000000000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedWriteBytesPerSecond"), Value: lo.ToPtr("1000000000")},
				{Name: lo.ToPtr("CachedDiskBytes"), Value: lo.ToPtr("429496729600")},
				{Name: lo.ToPtr("UncachedDiskIOPS"), Value: lo.ToPtr("25600")},
				{Name: lo.ToPtr("UncachedDiskBytesPerSecond"), Value: lo.ToPtr("600000000")},
				{Name: lo.ToPtr("EphemeralOSDiskSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("EncryptionAtHostSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("CapacityReservationSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("TrustedLaunchDisabled"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("AcceleratedNetworkingEnabled"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("RdmaEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("MaxNetworkInterfaces"), Value: lo.ToPtr("4")},
			},
			Locations:    &[]string{"westcentralus"},
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{Location: lo.ToPtr("westcentralus"), Zones: &[]string{}}},
		},
		{
			Name:         lo.ToPtr("Standard_D2as_v6"),
			Tier:         lo.ToPtr("Standard"),
			Kind:         lo.ToPtr(""),
			Size:         lo.ToPtr("D2as_v6"),
			Family:       lo.ToPtr("standardDav6Family"),
			ResourceType: lo.ToPtr("virtualMachines"),
			APIVersions:  &[]string{},
			Costs:        &[]compute.ResourceSkuCosts{},
			Restrictions: &[]compute.ResourceSkuRestrictions{},
			Capabilities: &[]compute.ResourceSkuCapabilities{
				{Name: lo.ToPtr("MaxResourceVolumeMB"), Value: lo.ToPtr("0")},
				{Name: lo.ToPtr("OSVhdSizeMB"), Value: lo.ToPtr("1047552")},
				{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("MemoryPreservingMaintenanceSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("HyperVGenerations"), Value: lo.ToPtr("V2")},
				{Name: lo.ToPtr("DiskControllerTypes"), Value: lo.ToPtr("NVMe")},
				{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("8")},
				{Name: lo.ToPtr("MaxDataDiskCount"), Value: lo.ToPtr("4")},
				{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("x64")},
				{Name: lo.ToPtr("LowPriorityCapable"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("PremiumIO"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("VMDeploymentTypes"), Value: lo.ToPtr("IaaS")},
				{Name: lo.ToPtr("vCPUsAvailable"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("ACUs"), Value: lo.ToPtr("230")},
				{Name: lo.ToPtr("vCPUsPerCore"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedIOPS"), Value: lo.ToPtr("9000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedReadBytesPerSecond"), Value: lo.ToPtr("125000000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedWriteBytesPerSecond"), Value: lo.ToPtr("125000000")},
				{Name: lo.ToPtr("UncachedDiskIOPS"), Value: lo.ToPtr("4000")},
				{Name: lo.ToPtr("UncachedDiskBytesPerSecond"), Value: lo.ToPtr("90000000")},
				{Name: lo.ToPtr("EphemeralOSDiskSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("EncryptionAtHostSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("CapacityReservationSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("AcceleratedNetworkingEnabled"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("RdmaEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("MaxNetworkInterfaces"), Value: lo.ToPtr("2")},
			},
			Locations:    &[]string{"westcentralus"},
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{Location: lo.ToPtr("westcentralus"), Zones: &[]string{}}},
		},
		{
			Name:         lo.ToPtr("Standard_D2s_v3"),
			Tier:         lo.ToPtr("Standard"),
			Kind:         lo.ToPtr(""),
			Size:         lo.ToPtr("D2s_v3"),
			Family:       lo.ToPtr("standardDSv3Family"),
			ResourceType: lo.ToPtr("virtualMachines"),
			APIVersions:  &[]string{},
			Costs:        &[]compute.ResourceSkuCosts{},
			Restrictions: &[]compute.ResourceSkuRestrictions{},
			Capabilities: &[]compute.ResourceSkuCapabilities{
				{Name: lo.ToPtr("MaxResourceVolumeMB"), Value: lo.ToPtr("16384")},
				{Name: lo.ToPtr("OSVhdSizeMB"), Value: lo.ToPtr("1047552")},
				{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("MemoryPreservingMaintenanceSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("HyperVGenerations"), Value: lo.ToPtr("V1,V2")},
				{Name: lo.ToPtr("SupportedEphemeralOSDiskPlacements"), Value: lo.ToPtr("ResourceDisk,CacheDisk")},
				{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("8")},
				{Name: lo.ToPtr("MaxDataDiskCount"), Value: lo.ToPtr("4")},
				{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("x64")},
				{Name: lo.ToPtr("LowPriorityCapable"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("PremiumIO"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("VMDeploymentTypes"), Value: lo.ToPtr("IaaS")},
				{Name: lo.ToPtr("vCPUsAvailable"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("ACUs"), Value: lo.ToPtr("160")},
				{Name: lo.ToPtr("vCPUsPerCore"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedIOPS"), Value: lo.ToPtr("4000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedReadBytesPerSecond"), Value: lo.ToPtr("32768000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedWriteBytesPerSecond"), Value: lo.ToPtr("32768000")},
				{Name: lo.ToPtr("CachedDiskBytes"), Value: lo.ToPtr("53687091200")},
				{Name: lo.ToPtr("UncachedDiskIOPS"), Value: lo.ToPtr("3200")},
				{Name: lo.ToPtr("UncachedDiskBytesPerSecond"), Value: lo.ToPtr("48000000")},
				{Name: lo.ToPtr("EphemeralOSDiskSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("EncryptionAtHostSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("CapacityReservationSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("AcceleratedNetworkingEnabled"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("RdmaEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("MaxNetworkInterfaces"), Value: lo.ToPtr("2")},
			},
			Locations:    &[]string{"westcentralus"},
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{Location: lo.ToPtr("westcentralus"), Zones: &[]string{}}},
		},
		{
			Name:         lo.ToPtr("Standard_D2_v2"),
			Tier:         lo.ToPtr("Standard"),
			Kind:         lo.ToPtr(""),
			Size:         lo.ToPtr("D2_v2"),
			Family:       lo.ToPtr("standardDv2Family"),
			ResourceType: lo.ToPtr("virtualMachines"),
			APIVersions:  &[]string{},
			Costs:        &[]compute.ResourceSkuCosts{},
			Restrictions: &[]compute.ResourceSkuRestrictions{},
			Capabilities: &[]compute.ResourceSkuCapabilities{
				{Name: lo.ToPtr("MaxResourceVolumeMB"), Value: lo.ToPtr("102400")},
				{Name: lo.ToPtr("OSVhdSizeMB"), Value: lo.ToPtr("1047552")},
				{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("MemoryPreservingMaintenanceSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("HyperVGenerations"), Value: lo.ToPtr("V1")},
				{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("7")},
				{Name: lo.ToPtr("MaxDataDiskCount"), Value: lo.ToPtr("8")},
				{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("x64")},
				{Name: lo.ToPtr("LowPriorityCapable"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("PremiumIO"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("VMDeploymentTypes"), Value: lo.ToPtr("IaaS,PaaS")},
				{Name: lo.ToPtr("vCPUsAvailable"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("ACUs"), Value: lo.ToPtr("210")},
				{Name: lo.ToPtr("vCPUsPerCore"), Value: lo.ToPtr("1")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedIOPS"), Value: lo.ToPtr("6000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedReadBytesPerSecond"), Value: lo.ToPtr("98304000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedWriteBytesPerSecond"), Value: lo.ToPtr("49152000")},
				{Name: lo.ToPtr("UncachedDiskIOPS"), Value: lo.ToPtr("6400")},
				{Name: lo.ToPtr("UncachedDiskBytesPerSecond"), Value: lo.ToPtr("96000000")},
				{Name: lo.ToPtr("EphemeralOSDiskSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("EncryptionAtHostSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("CapacityReservationSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("AcceleratedNetworkingEnabled"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("RdmaEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("MaxNetworkInterfaces"), Value: lo.ToPtr("2")},
			},
			Locations:    &[]string{"westcentralus"},
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{Location: lo.ToPtr("westcentralus"), Zones: &[]string{}}},
		},
		{
			Name:         lo.ToPtr("Standard_D2_v3"),
			Tier:         lo.ToPtr("Standard"),
			Kind:         lo.ToPtr(""),
			Size:         lo.ToPtr("D2_v3"),
			Family:       lo.ToPtr("standardDv3Family"),
			ResourceType: lo.ToPtr("virtualMachines"),
			APIVersions:  &[]string{},
			Costs:        &[]compute.ResourceSkuCosts{},
			Restrictions: &[]compute.ResourceSkuRestrictions{},
			Capabilities: &[]compute.ResourceSkuCapabilities{
				{Name: lo.ToPtr("MaxResourceVolumeMB"), Value: lo.ToPtr("51200")},
				{Name: lo.ToPtr("OSVhdSizeMB"), Value: lo.ToPtr("1047552")},
				{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("MemoryPreservingMaintenanceSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("HyperVGenerations"), Value: lo.ToPtr("V1")},
				{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("8")},
				{Name: lo.ToPtr("MaxDataDiskCount"), Value: lo.ToPtr("4")},
				{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("x64")},
				{Name: lo.ToPtr("LowPriorityCapable"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("PremiumIO"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("VMDeploymentTypes"), Value: lo.ToPtr("IaaS,PaaS")},
				{Name: lo.ToPtr("vCPUsAvailable"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("ACUs"), Value: lo.ToPtr("160")},
				{Name: lo.ToPtr("vCPUsPerCore"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedIOPS"), Value: lo.ToPtr("3000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedReadBytesPerSecond"), Value: lo.ToPtr("49152000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedWriteBytesPerSecond"), Value: lo.ToPtr("24576000")},
				{Name: lo.ToPtr("UncachedDiskIOPS"), Value: lo.ToPtr("3200")},
				{Name: lo.ToPtr("UncachedDiskBytesPerSecond"), Value: lo.ToPtr("48000000")},
				{Name: lo.ToPtr("EphemeralOSDiskSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("EncryptionAtHostSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("CapacityReservationSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("AcceleratedNetworkingEnabled"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("RdmaEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("MaxNetworkInterfaces"), Value: lo.ToPtr("2")},
			},
			Locations:    &[]string{"westcentralus"},
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{Location: lo.ToPtr("westcentralus"), Zones: &[]string{}}},
		},
		{
			Name:         lo.ToPtr("Standard_D2_v5"),
			Tier:         lo.ToPtr("Standard"),
			Kind:         lo.ToPtr(""),
			Size:         lo.ToPtr("D2_v5"),
			Family:       lo.ToPtr("standardDv5Family"),
			ResourceType: lo.ToPtr("virtualMachines"),
			APIVersions:  &[]string{},
			Costs:        &[]compute.ResourceSkuCosts{},
			Restrictions: &[]compute.ResourceSkuRestrictions{},
			Capabilities: &[]compute.ResourceSkuCapabilities{
				{Name: lo.ToPtr("MaxResourceVolumeMB"), Value: lo.ToPtr("0")},
				{Name: lo.ToPtr("OSVhdSizeMB"), Value: lo.ToPtr("1047552")},
				{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("MemoryPreservingMaintenanceSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("HyperVGenerations"), Value: lo.ToPtr("V1,V2")},
				{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("8")},
				{Name: lo.ToPtr("MaxDataDiskCount"), Value: lo.ToPtr("4")},
				{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("x64")},
				{Name: lo.ToPtr("LowPriorityCapable"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("PremiumIO"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("VMDeploymentTypes"), Value: lo.ToPtr("IaaS")},
				{Name: lo.ToPtr("vCPUsAvailable"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("vCPUsPerCore"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedIOPS"), Value: lo.ToPtr("9000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedReadBytesPerSecond"), Value: lo.ToPtr("125000000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedWriteBytesPerSecond"), Value: lo.ToPtr("125000000")},
				{Name: lo.ToPtr("UncachedDiskIOPS"), Value: lo.ToPtr("3750")},
				{Name: lo.ToPtr("UncachedDiskBytesPerSecond"), Value: lo.ToPtr("85000000")},
				{Name: lo.ToPtr("EphemeralOSDiskSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("EncryptionAtHostSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("CapacityReservationSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("AcceleratedNetworkingEnabled"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("RdmaEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("MaxNetworkInterfaces"), Value: lo.ToPtr("2")},
			},
			Locations:    &[]string{"westcentralus"},
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{Location: lo.ToPtr("westcentralus"), Zones: &[]string{}}},
		},
		{
			Name:         lo.ToPtr("Standard_D4s_v3"),
			Tier:         lo.ToPtr("Standard"),
			Kind:         lo.ToPtr(""),
			Size:         lo.ToPtr("D4s_v3"),
			Family:       lo.ToPtr("standardDSv3Family"),
			ResourceType: lo.ToPtr("virtualMachines"),
			APIVersions:  &[]string{},
			Costs:        &[]compute.ResourceSkuCosts{},
			Restrictions: &[]compute.ResourceSkuRestrictions{},
			Capabilities: &[]compute.ResourceSkuCapabilities{
				{Name: lo.ToPtr("MaxResourceVolumeMB"), Value: lo.ToPtr("32768")},
				{Name: lo.ToPtr("OSVhdSizeMB"), Value: lo.ToPtr("1047552")},
				{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("4")},
				{Name: lo.ToPtr("MemoryPreservingMaintenanceSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("HyperVGenerations"), Value: lo.ToPtr("V1,V2")},
				{Name: lo.ToPtr("SupportedEphemeralOSDiskPlacements"), Value: lo.ToPtr("ResourceDisk,CacheDisk")},
				{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("16")},
				{Name: lo.ToPtr("MaxDataDiskCount"), Value: lo.ToPtr("8")},
				{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("x64")},
				{Name: lo.ToPtr("LowPriorityCapable"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("PremiumIO"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("VMDeploymentTypes"), Value: lo.ToPtr("IaaS")},
				{Name: lo.ToPtr("vCPUsAvailable"), Value: lo.ToPtr("4")},
				{Name: lo.ToPtr("ACUs"), Value: lo.ToPtr("160")},
				{Name: lo.ToPtr("vCPUsPerCore"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedIOPS"), Value: lo.ToPtr("8000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedReadBytesPerSecond"), Value: lo.ToPtr("65536000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedWriteBytesPerSecond"), Value: lo.ToPtr("65536000")},
				{Name: lo.ToPtr("CachedDiskBytes"), Value: lo.ToPtr("107374182400")},
				{Name: lo.ToPtr("UncachedDiskIOPS"), Value: lo.ToPtr("6400")},
				{Name: lo.ToPtr("UncachedDiskBytesPerSecond"), Value: lo.ToPtr("96000000")},
				{Name: lo.ToPtr("EphemeralOSDiskSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("EncryptionAtHostSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("CapacityReservationSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("AcceleratedNetworkingEnabled"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("RdmaEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("MaxNetworkInterfaces"), Value: lo.ToPtr("2")},
			},
			Locations:    &[]string{"westcentralus"},
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{Location: lo.ToPtr("westcentralus"), Zones: &[]string{}}},
		},
		{
			Name:         lo.ToPtr("Standard_D64s_v3"),
			Tier:         lo.ToPtr("Standard"),
			Kind:         lo.ToPtr(""),
			Size:         lo.ToPtr("D64s_v3"),
			Family:       lo.ToPtr("standardDSv3Family"),
			ResourceType: lo.ToPtr("virtualMachines"),
			APIVersions:  &[]string{},
			Costs:        &[]compute.ResourceSkuCosts{},
			Restrictions: &[]compute.ResourceSkuRestrictions{},
			Capabilities: &[]compute.ResourceSkuCapabilities{
				{Name: lo.ToPtr("MaxResourceVolumeMB"), Value: lo.ToPtr("524288")},
				{Name: lo.ToPtr("OSVhdSizeMB"), Value: lo.ToPtr("1047552")},
				{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("64")},
				{Name: lo.ToPtr("MemoryPreservingMaintenanceSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("HyperVGenerations"), Value: lo.ToPtr("V1,V2")},
				{Name: lo.ToPtr("SupportedEphemeralOSDiskPlacements"), Value: lo.ToPtr("ResourceDisk,CacheDisk")},
				{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("256")},
				{Name: lo.ToPtr("MaxDataDiskCount"), Value: lo.ToPtr("32")},
				{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("x64")},
				{Name: lo.ToPtr("LowPriorityCapable"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("PremiumIO"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("VMDeploymentTypes"), Value: lo.ToPtr("IaaS")},
				{Name: lo.ToPtr("vCPUsAvailable"), Value: lo.ToPtr("64")},
				{Name: lo.ToPtr("ACUs"), Value: lo.ToPtr("160")},
				{Name: lo.ToPtr("vCPUsPerCore"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedIOPS"), Value: lo.ToPtr("128000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedReadBytesPerSecond"), Value: lo.ToPtr("1048576000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedWriteBytesPerSecond"), Value: lo.ToPtr("1048576000")},
				{Name: lo.ToPtr("CachedDiskBytes"), Value: lo.ToPtr("1717986918400")},
				{Name: lo.ToPtr("UncachedDiskIOPS"), Value: lo.ToPtr("80000")},
				{Name: lo.ToPtr("UncachedDiskBytesPerSecond"), Value: lo.ToPtr("1200000000")},
				{Name: lo.ToPtr("EphemeralOSDiskSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("EncryptionAtHostSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("CapacityReservationSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("AcceleratedNetworkingEnabled"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("RdmaEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("MaxNetworkInterfaces"), Value: lo.ToPtr("8")},
			},
			Locations:    &[]string{"westcentralus"},
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{Location: lo.ToPtr("westcentralus"), Zones: &[]string{}}},
		},
		{
			Name:         lo.ToPtr("Standard_DS2_v2"),
			Tier:         lo.ToPtr("Standard"),
			Kind:         lo.ToPtr(""),
			Size:         lo.ToPtr("DS2_v2"),
			Family:       lo.ToPtr("standardDSv2Family"),
			ResourceType: lo.ToPtr("virtualMachines"),
			APIVersions:  &[]string{},
			Costs:        &[]compute.ResourceSkuCosts{},
			Restrictions: &[]compute.ResourceSkuRestrictions{},
			Capabilities: &[]compute.ResourceSkuCapabilities{
				{Name: lo.ToPtr("MaxResourceVolumeMB"), Value: lo.ToPtr("14336")},
				{Name: lo.ToPtr("OSVhdSizeMB"), Value: lo.ToPtr("1047552")},
				{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("MemoryPreservingMaintenanceSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("HyperVGenerations"), Value: lo.ToPtr("V1,V2")},
				{Name: lo.ToPtr("SupportedEphemeralOSDiskPlacements"), Value: lo.ToPtr("ResourceDisk,CacheDisk")},
				{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("7")},
				{Name: lo.ToPtr("MaxDataDiskCount"), Value: lo.ToPtr("8")},
				{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("x64")},
				{Name: lo.ToPtr("LowPriorityCapable"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("PremiumIO"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("VMDeploymentTypes"), Value: lo.ToPtr("IaaS")},
				{Name: lo.ToPtr("vCPUsAvailable"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("ACUs"), Value: lo.ToPtr("210")},
				{Name: lo.ToPtr("vCPUsPerCore"), Value: lo.ToPtr("1")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedIOPS"), Value: lo.ToPtr("8000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedReadBytesPerSecond"), Value: lo.ToPtr("65536000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedWriteBytesPerSecond"), Value: lo.ToPtr("65536000")},
				{Name: lo.ToPtr("CachedDiskBytes"), Value: lo.ToPtr("92341796864")},
				{Name: lo.ToPtr("UncachedDiskIOPS"), Value: lo.ToPtr("6400")},
				{Name: lo.ToPtr("UncachedDiskBytesPerSecond"), Value: lo.ToPtr("96000000")},
				{Name: lo.ToPtr("EphemeralOSDiskSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("EncryptionAtHostSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("CapacityReservationSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("AcceleratedNetworkingEnabled"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("RdmaEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("MaxNetworkInterfaces"), Value: lo.ToPtr("2")},
			},
			Locations:    &[]string{"westcentralus"},
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{Location: lo.ToPtr("westcentralus"), Zones: &[]string{}}},
		},
		{
			Name:         lo.ToPtr("Standard_F16s_v2"),
			Tier:         lo.ToPtr("Standard"),
			Kind:         lo.ToPtr(""),
			Size:         lo.ToPtr("F16s_v2"),
			Family:       lo.ToPtr("standardFSv2Family"),
			ResourceType: lo.ToPtr("virtualMachines"),
			APIVersions:  &[]string{},
			Costs:        &[]compute.ResourceSkuCosts{},
			Restrictions: &[]compute.ResourceSkuRestrictions{},
			Capabilities: &[]compute.ResourceSkuCapabilities{
				{Name: lo.ToPtr("MaxResourceVolumeMB"), Value: lo.ToPtr("131072")},
				{Name: lo.ToPtr("OSVhdSizeMB"), Value: lo.ToPtr("1047552")},
				{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("16")},
				{Name: lo.ToPtr("MemoryPreservingMaintenanceSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("HyperVGenerations"), Value: lo.ToPtr("V1,V2")},
				{Name: lo.ToPtr("SupportedEphemeralOSDiskPlacements"), Value: lo.ToPtr("ResourceDisk,CacheDisk")},
				{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("32")},
				{Name: lo.ToPtr("MaxDataDiskCount"), Value: lo.ToPtr("32")},
				{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("x64")},
				{Name: lo.ToPtr("LowPriorityCapable"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("PremiumIO"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("VMDeploymentTypes"), Value: lo.ToPtr("IaaS")},
				{Name: lo.ToPtr("vCPUsAvailable"), Value: lo.ToPtr("16")},
				{Name: lo.ToPtr("ACUs"), Value: lo.ToPtr("195")},
				{Name: lo.ToPtr("vCPUsPerCore"), Value: lo.ToPtr("2")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedIOPS"), Value: lo.ToPtr("32000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedReadBytesPerSecond"), Value: lo.ToPtr("262144000")},
				{Name: lo.ToPtr("CombinedTempDiskAndCachedWriteBytesPerSecond"), Value: lo.ToPtr("262144000")},
				{Name: lo.ToPtr("CachedDiskBytes"), Value: lo.ToPtr("274877906944")},
				{Name: lo.ToPtr("UncachedDiskIOPS"), Value: lo.ToPtr("25600")},
				{Name: lo.ToPtr("UncachedDiskBytesPerSecond"), Value: lo.ToPtr("384000000")},
				{Name: lo.ToPtr("EphemeralOSDiskSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("EncryptionAtHostSupported"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("CapacityReservationSupported"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("AcceleratedNetworkingEnabled"), Value: lo.ToPtr("True")},
				{Name: lo.ToPtr("RdmaEnabled"), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr("MaxNetworkInterfaces"), Value: lo.ToPtr("4")},
			},
			Locations:    &[]string{"westcentralus"},
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{Location: lo.ToPtr("westcentralus"), Zones: &[]string{}}},
		},
	}
}
