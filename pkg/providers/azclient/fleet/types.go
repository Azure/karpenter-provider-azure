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
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
)

// FleetAPI abstracts the Azure Compute Fleet SDK client for testability.
type FleetAPI interface {
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, fleetName string, resource armcomputefleet.Fleet, options *armcomputefleet.FleetsClientBeginCreateOrUpdateOptions) (*runtime.Poller[armcomputefleet.FleetsClientCreateOrUpdateResponse], error)
	Get(ctx context.Context, resourceGroupName string, fleetName string, options *armcomputefleet.FleetsClientGetOptions) (armcomputefleet.FleetsClientGetResponse, error)
	BeginDelete(ctx context.Context, resourceGroupName string, fleetName string, options *armcomputefleet.FleetsClientBeginDeleteOptions) (*runtime.Poller[armcomputefleet.FleetsClientDeleteResponse], error)
	NewListByResourceGroupPager(resourceGroupName string, options *armcomputefleet.FleetsClientListByResourceGroupOptions) *runtime.Pager[armcomputefleet.FleetsClientListByResourceGroupResponse]
}

// VMAPI abstracts the VM operations needed for tagging and deleting assigned VMs.
type VMAPI interface {
	BeginUpdate(ctx context.Context, resourceGroupName string, vmName string, parameters armcompute.VirtualMachineUpdate, options *armcompute.VirtualMachinesClientBeginUpdateOptions) (*runtime.Poller[armcompute.VirtualMachinesClientUpdateResponse], error)
	BeginDelete(ctx context.Context, resourceGroupName string, vmName string, options *armcompute.VirtualMachinesClientBeginDeleteOptions) (*runtime.Poller[armcompute.VirtualMachinesClientDeleteResponse], error)
}

// FleetVMProvisionRequest represents a single NodeClaim's request to provision a VM via the Fleet batcher.
type FleetVMProvisionRequest struct {
	NodeClaimName       string
	CapacityType        string
	AcceptableSKUs      []string
	AcceptableZones     []string
	Tags                map[string]*string
	NodeClaim           *karpv1.NodeClaim
	NodeClass           *v1beta1.AKSNodeClass
	LaunchTemplate      *launchtemplate.Template
	InstanceTypes       map[string]*cloudprovider.InstanceType
	SSHPublicKey        string
	AdminUsername       string
	NodeIdentities      []string
	DiskEncryptionSetID string
	NSG                 string
	LBBackendPools      []string
	Location            string
}

// FleetBatchResponse is the per-request response returned from the batcher.
type FleetBatchResponse struct {
	SharedState *FleetSharedState
	Error       error
}

// FleetSharedState is shared across all promises in the same batch.
// It stores the assignment results after the Fleet LRO completes.
type FleetSharedState struct {
	// TODO: fields:
	//   once        sync.Once
	//   assignments map[string]*FleetAssignment  // nodeClaimName → assignment
	//   err         error
	//   vmClient    VMAPI
}
