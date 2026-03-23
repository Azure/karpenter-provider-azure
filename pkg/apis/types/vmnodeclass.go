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

// Package types defines shared interfaces for NodeClass CRD types.
package types

// VMNodeClass is the interface that both AKSNodeClass and AzureNodeClass
// satisfy for VM provisioning. The VM provider operates on this interface
// rather than on a specific CRD type, keeping both NodeClass types as
// first-class citizens without adapter conversion.
//
// This interface covers the fields needed by the VM creation path.
// AKS-specific functionality (image family resolution, kubelet config,
// k8s version validation) is handled by the AKS CloudProvider code path
// and accesses AKSNodeClass-specific fields directly — those do NOT need
// to be on this interface.
type VMNodeClass interface {
	// GetVNETSubnetID returns the subnet for NIC creation. May be nil (falls back to controller default).
	GetVNETSubnetID() *string
	// GetOSDiskSizeGB returns the OS disk size. May be nil (Azure default).
	GetOSDiskSizeGB() *int32
	// GetImageID returns the VM image ARM resource ID. May be nil (AKS modes resolve via image family).
	GetImageID() *string
	// GetUserData returns the raw cloud-init/bootstrap script. May be nil.
	// The provider base64-encodes this before setting osProfile.CustomData.
	GetUserData() *string
	// GetTags returns tags to apply to Azure resources.
	GetTags() map[string]string
	// GetEncryptionAtHost returns whether host encryption is enabled.
	GetEncryptionAtHost() bool
	// GetManagedIdentities returns per-NodeClass user-assigned managed identity resource IDs.
	GetManagedIdentities() []string
}
