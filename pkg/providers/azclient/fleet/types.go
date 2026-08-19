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

package fleet

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
)

// FleetVMProvisionRequest represents a single NodeClaim's VM provisioning request to be batched.
type FleetVMProvisionRequest struct {
	NodeClaimName       string // Per-VM identity, not part of batch key
	NodePoolName        string
	CapacityType        string // "spot" or "on-demand"
	AcceptableSKUs      []string
	AcceptableZones     []string
	Tags                map[string]*string
	LaunchTemplate      *launchtemplate.Template
	SSHPublicKey        string
	AdminUsername       string
	NodeIdentities      []string
	DiskEncryptionSetID string
	NSG                 string
	LBBackendPools      []string
	Location            string
	Extensions          []*armcompute.VirtualMachineExtension
}
