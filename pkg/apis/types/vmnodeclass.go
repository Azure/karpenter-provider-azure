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

// VMNodeClass is the interface that NodeClass CRD types (e.g. AKSNodeClass)
// implement for VM provisioning. This allows the VM provider to support
// multiple NodeClass types without coupling to a specific CRD.
//
// Methods return nil/zero for fields not applicable to the implementing type.
// For example, AKSNodeClass.GetUserData() returns nil because AKS manages
// bootstrapping; a non-AKS NodeClass would return the user-provided script.
type VMNodeClass interface {
	// GetVNETSubnetID returns the subnet for NIC creation. Nil falls back to controller default.
	GetVNETSubnetID() *string
	// GetOSDiskSizeGB returns the OS disk size. Nil uses Azure default.
	GetOSDiskSizeGB() *int32
	// GetImageID returns the VM image ARM resource ID. Nil means resolve via image family.
	GetImageID() *string
	// GetUserData returns raw cloud-init/bootstrap script. Nil means no user-provided bootstrap.
	GetUserData() *string
	// GetTags returns tags to apply to Azure resources.
	GetTags() map[string]string
	// GetEncryptionAtHost returns whether host encryption is enabled.
	GetEncryptionAtHost() bool
	// GetManagedIdentities returns per-NodeClass user-assigned managed identity resource IDs.
	GetManagedIdentities() []string
}
