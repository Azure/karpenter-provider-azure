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
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/samber/lo"
)

// SharedAKSDataStores contains the shared data stores for both AKS agent pools and machines
type SharedAKSDataStores struct {
	AgentPools  *sync.Map
	AKSMachines *sync.Map
}

// NewSharedAKSDataStores creates a new instance of shared data stores
func NewSharedAKSDataStores() *SharedAKSDataStores {
	return &SharedAKSDataStores{
		AgentPools:  &sync.Map{},
		AKSMachines: &sync.Map{},
	}
}

// extractVMNameFromResourceID extracts the VM name from a VM resource ID
// e.g., "/subscriptions/.../virtualMachines/vmName" -> "vmName"
func extractVMNameFromResourceID(resourceID string) string {
	parts := strings.Split(resourceID, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

// This is not really the real one being used, which is the header.
// But the header cannot be extracted due to azure-sdk-for-go being restrictive. This is good enough.
func GetVMImageIDFromContext(ctx context.Context) string {
	if ctx.Value("vmimageid") != nil {
		return ctx.Value("vmimageid").(string)
	}
	return ""
}

type AKSMachineCreateOrUpdateInput struct {
	ResourceGroupName string
	ResourceName      string
	AgentPoolName     string
	AKSMachineName    string
	AKSMachine        armcontainerservice.Machine
	Options           *armcontainerservice.MachinesClientBeginCreateOrUpdateOptions
}

type AKSMachineGetInput struct {
	ResourceGroupName string
	ResourceName      string
	AgentPoolName     string
	AKSMachineName    string
	Options           *armcontainerservice.MachinesClientGetOptions
}

type AKSMachineListInput struct {
	ResourceGroupName string
	ResourceName      string
	AgentPoolName     string
	Options           *armcontainerservice.MachinesClientListOptions
}

type AKSMachinesBehavior struct {
	AKSMachineCreateOrUpdateBehavior   MockedLRO[AKSMachineCreateOrUpdateInput, armcontainerservice.MachinesClientCreateOrUpdateResponse]
	AKSMachineGetBehavior              MockedFunction[AKSMachineGetInput, armcontainerservice.MachinesClientGetResponse]
	AKSMachineNewListPagerBehavior     MockedFunction[AKSMachineListInput, *runtime.Pager[armcontainerservice.MachinesClientListResponse]]
	AfterPollProvisioningErrorOverride *armcontainerservice.CloudErrorBody
}

// XPMT: TODO: check API: all these
var AKSMachineAPIErrorFromAKSMachineNotFound = &azcore.ResponseError{
	ErrorCode:  "NotFound",
	StatusCode: http.StatusNotFound,
}
var AKSMachineAPIErrorFromAKSMachinesPoolNotFound = &azcore.ResponseError{
	ErrorCode:  "NotFound",
	StatusCode: http.StatusNotFound,
}
var AKSMachineAPIErrorFromAKSMachineImmutablePropertyChangeAttempted = &azcore.ResponseError{
	ErrorCode:  "OperationNotAllowed",
	StatusCode: http.StatusBadRequest,
}
var AKSMachineAPIErrorAny = &azcore.ResponseError{
	ErrorCode: "SomeRandomError",
}

func AKSMachineAPIProvisioningErrorSkuNotAvailable(sku string, location string) *armcontainerservice.CloudErrorBody {
	return &armcontainerservice.CloudErrorBody{
		Code:    lo.ToPtr("SkuNotAvailable"),
		Message: lo.ToPtr(fmt.Sprintf("Code=\"SkuNotAvailable\" Message=\"The requested VM size for resource 'Following SKUs have failed for Capacity Restrictions: %s' is currently not available in location '%s'. Please try another size or deploy to a different location or different zone. See https://aka.ms/azureskunotavailable for details.\" Target=\"sku.name\"", sku, location)),
		Details: []*armcontainerservice.CloudErrorBody{
			{
				Code:    lo.ToPtr("SkuNotAvailable"),
				Message: lo.ToPtr(fmt.Sprintf("The requested VM size for resource 'Following SKUs have failed for Capacity Restrictions: %s' is currently not available in location '%s'. Please try another size or deploy to a different location or different zone. See https://aka.ms/azureskunotavailable for details.", sku, location)),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorLowPriorityCoresQuota(location string) *armcontainerservice.CloudErrorBody {
	return &armcontainerservice.CloudErrorBody{
		Code:    lo.ToPtr("QuotaExceeded"),
		Message: lo.ToPtr(fmt.Sprintf("Code=\"OperationNotAllowed\" Message=\"Operation could not be completed as it results in exceeding approved LowPriorityCores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: 3, Current Usage: 0, Additional Required: 6, (Minimum) New Limit Required: 6.\"", location)),
		Details: []*armcontainerservice.CloudErrorBody{
			{
				Code:    lo.ToPtr("OperationNotAllowed"),
				Message: lo.ToPtr(fmt.Sprintf("Operation could not be completed as it results in exceeding approved LowPriorityCores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: 3, Current Usage: 0, Additional Required: 6, (Minimum) New Limit Required: 6.", location)),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorOverconstrainedZonalAllocation() *armcontainerservice.CloudErrorBody {
	return &armcontainerservice.CloudErrorBody{
		Code:    lo.ToPtr("OverconstrainedZonalAllocationRequest"),
		Message: lo.ToPtr("original error: Code=\"OverconstrainedZonalAllocationRequest\" Message=\"Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive. Please remove some constraints and try again. Constraints applied are:\\n  - Availability Zone\\n  - Low Priority VMs\\n  - Networking Constraints (such as Accelerated Networking or IPv6)\\n  - Preemptible VMs (VM might be preempted by another VM with a higher priority)\\n  - VM Size\\n\" Target=\"6\""),
		Details: []*armcontainerservice.CloudErrorBody{
			{
				Code:    lo.ToPtr("OverconstrainedZonalAllocationRequest"),
				Message: lo.ToPtr("Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive. Please remove some constraints and try again. Constraints applied are:\n  - Availability Zone\n  - Low Priority VMs\n  - Networking Constraints (such as Accelerated Networking or IPv6)\n  - Preemptible VMs (VM might be preempted by another VM with a higher priority)\n  - VM Size\n"),
				Target:  lo.ToPtr("6"),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorOverconstrainedAllocation() *armcontainerservice.CloudErrorBody {
	return &armcontainerservice.CloudErrorBody{
		Code:    lo.ToPtr("OverconstrainedAllocationRequest"),
		Message: lo.ToPtr("Code=\"OverconstrainedAllocationRequest\" Message=\"Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive. Please remove some constraints and try again. Constraints applied are:\\n  - Differencing (Ephemeral) Disks\\n  - Networking Constraints (such as Accelerated Networking or IPv6)\\n  - Subscription Pinning\\n  - VM Size\\n\" Target=\"0\""),
		Details: []*armcontainerservice.CloudErrorBody{
			{
				Code:    lo.ToPtr("OverconstrainedAllocationRequest"),
				Message: lo.ToPtr("Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive. Please remove some constraints and try again. Constraints applied are:\n  - Differencing (Ephemeral) Disks\n  - Networking Constraints (such as Accelerated Networking or IPv6)\n  - Subscription Pinning\n  - VM Size\n"),
				Target:  lo.ToPtr("0"),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorAllocationFailed() *armcontainerservice.CloudErrorBody {
	return &armcontainerservice.CloudErrorBody{
		Code:    lo.ToPtr("AllocationFailed"),
		Message: lo.ToPtr("original error: Code=\"AllocationFailed\" Message=\"Allocation failed. If you are trying to add a new VM to a Virtual Machine Scale Set with a single placement group or update/resize an existing VM in a Virtual Machine Scale Set with a single placement group, please note that such allocation is scoped to a single cluster, and it is possible that the cluster is out of capacity. Please read more about improving likelihood of allocation success at http://aka.ms/allocation-guidance.\" Target=\"243\""),
		Details: []*armcontainerservice.CloudErrorBody{
			{
				Code:    lo.ToPtr("AllocationFailed"),
				Message: lo.ToPtr("Allocation failed. If you are trying to add a new VM to a Virtual Machine Scale Set with a single placement group or update/resize an existing VM in a Virtual Machine Scale Set with a single placement group, please note that such allocation is scoped to a single cluster, and it is possible that the cluster is out of capacity. Please read more about improving likelihood of allocation success at http://aka.ms/allocation-guidance."),
				Target:  lo.ToPtr("243"),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorVMFamilyQuotaExceeded(location string, familyName string, currentLimit int32, currentUsage int32, additionalRequired int32, newLimitRequired int32) *armcontainerservice.CloudErrorBody {
	return &armcontainerservice.CloudErrorBody{
		Code:    lo.ToPtr("QuotaExceeded"),
		Message: lo.ToPtr(fmt.Sprintf("Code=\"OperationNotAllowed\" Message=\"Operation could not be completed as it results in exceeding approved %s Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: %d, Current Usage: %d, Additional Required: %d, (Minimum) New Limit Required: %d.\"", familyName, location, currentLimit, currentUsage, additionalRequired, newLimitRequired)),
		Details: []*armcontainerservice.CloudErrorBody{
			{
				Code:    lo.ToPtr("OperationNotAllowed"),
				Message: lo.ToPtr(fmt.Sprintf("Operation could not be completed as it results in exceeding approved %s Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: %d, Current Usage: %d, Additional Required: %d, (Minimum) New Limit Required: %d.", familyName, location, currentLimit, currentUsage, additionalRequired, newLimitRequired)),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorTotalRegionalCoresQuota(location string) *armcontainerservice.CloudErrorBody {
	return &armcontainerservice.CloudErrorBody{
		Code:    lo.ToPtr("QuotaExceeded"),
		Message: lo.ToPtr(fmt.Sprintf("Code=\"OperationNotAllowed\" Message=\"Operation could not be completed as it results in exceeding approved Total Regional Cores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: 10, Current Usage: 8, Additional Required: 8, (Minimum) New Limit Required: 16.\"", location)),
		Details: []*armcontainerservice.CloudErrorBody{
			{
				Code:    lo.ToPtr("OperationNotAllowed"),
				Message: lo.ToPtr(fmt.Sprintf("Operation could not be completed as it results in exceeding approved Total Regional Cores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: 10, Current Usage: 8, Additional Required: 8, (Minimum) New Limit Required: 16.", location)),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorZoneAllocationFailed(zone string) *armcontainerservice.CloudErrorBody {
	return &armcontainerservice.CloudErrorBody{
		Code:    lo.ToPtr("ZoneAllocationFailed"),
		Message: lo.ToPtr(fmt.Sprintf("The requested VM size for resource group 'MC_test_test_westus2' cannot be provisioned in availability zone '%s' due to capacity constraints. Please try a different size or retry later.", zone)),
		Details: []*armcontainerservice.CloudErrorBody{
			{
				Code:    lo.ToPtr("ZoneAllocationFailed"),
				Message: lo.ToPtr(fmt.Sprintf("Allocation failed: Zone allocation failed in zone '%s'", zone)),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorAny() *armcontainerservice.CloudErrorBody {
	return &armcontainerservice.CloudErrorBody{
		Code:    lo.ToPtr("SomeRandomError"),
		Message: lo.ToPtr("An unexpected error occurred."),
		Details: []*armcontainerservice.CloudErrorBody{
			{
				Code:    lo.ToPtr("SomeRandomError"),
				Message: lo.ToPtr("An unexpected error occurred."),
			},
		},
	}
}

// assert that the fake implements the interface
var _ instance.AKSMachinesAPI = &AKSMachinesAPI{}

type AKSMachinesAPI struct {
	AKSMachinesBehavior
	sharedStores *SharedAKSDataStores
}

// NewAKSMachinesAPI creates a new AKSMachinesAPI instance with shared data stores
func NewAKSMachinesAPI(sharedStores *SharedAKSDataStores) *AKSMachinesAPI {
	return &AKSMachinesAPI{
		sharedStores: sharedStores,
	}
}

// Reset must be called between tests otherwise tests will pollute each other.
func (c *AKSMachinesAPI) Reset() {
	c.AKSMachineCreateOrUpdateBehavior.Reset()
	c.AKSMachineGetBehavior.Reset()
	c.AKSMachineNewListPagerBehavior.Reset()
	c.sharedStores.AKSMachines.Range(func(k, v any) bool {
		c.sharedStores.AKSMachines.Delete(k)
		return true
	})
	c.AfterPollProvisioningErrorOverride = nil
}

func (c *AKSMachinesAPI) BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, parameters armcontainerservice.Machine, options *armcontainerservice.MachinesClientBeginCreateOrUpdateOptions) (*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse], error) {
	input := &AKSMachineCreateOrUpdateInput{
		ResourceGroupName: resourceGroupName,
		ResourceName:      resourceName,
		AgentPoolName:     agentPoolName,
		AKSMachineName:    aksMachineName,
		AKSMachine:        parameters,
		Options:           options,
	}
	aksMachine := input.AKSMachine
	id := MkMachineID(input.ResourceGroupName, input.ResourceName, input.AgentPoolName, input.AKSMachineName)
	// Basic defaults
	aksMachine.ID = &id
	aksMachine.Name = &input.AKSMachineName

	if !c.doesAgentPoolExists(input.ResourceGroupName, input.ResourceName, input.AgentPoolName) {
		return nil, AKSMachineAPIErrorFromAKSMachinesPoolNotFound
	}

	// Extract vmImageID from context
	vmImageID := GetVMImageIDFromContext(ctx)

	// Check if AKS machine already exists
	existingMachine, ok := c.sharedStores.AKSMachines.Load(id)
	if ok {
		existing := existingMachine.(armcontainerservice.Machine)

		// Validate immutable properties not violated
		if c.doImmutablePropertiesChanged(&existing, &aksMachine) {
			return nil, AKSMachineAPIErrorFromAKSMachineImmutablePropertyChangeAttempted
		}

		// Patch with new values
		if aksMachine.Properties != nil && aksMachine.Properties.Tags != nil {
			if existing.Properties == nil {
				existing.Properties = &armcontainerservice.MachineProperties{}
			}
			existing.Properties.Tags = aksMachine.Properties.Tags
		}

		// Write the updated machine
		c.sharedStores.AKSMachines.Store(id, existing)
		return c.AKSMachineCreateOrUpdateBehavior.Invoke(input, func(input *AKSMachineCreateOrUpdateInput) (*armcontainerservice.MachinesClientCreateOrUpdateResponse, error) {
			return &armcontainerservice.MachinesClientCreateOrUpdateResponse{Machine: existing}, nil
		})
	}

	// More defaults
	c.setDefaultMachineValues(&aksMachine, vmImageID, input.AgentPoolName)

	aksMachine.Properties.ProvisioningState = lo.ToPtr("Creating")

	c.sharedStores.AKSMachines.Store(id, aksMachine)
	return c.AKSMachineCreateOrUpdateBehavior.Invoke(input, func(input *AKSMachineCreateOrUpdateInput) (*armcontainerservice.MachinesClientCreateOrUpdateResponse, error) {
		var pollingError error
		if c.AfterPollProvisioningErrorOverride != nil {
			aksMachine.Properties.ProvisioningState = lo.ToPtr("Failed")
			if aksMachine.Properties.Status == nil {
				aksMachine.Properties.Status = &armcontainerservice.MachineStatus{}
			}
			aksMachine.Properties.Status.ProvisioningError = c.AfterPollProvisioningErrorOverride
			pollingError = AKSMachineAPIErrorAny
		} else {
			aksMachine.Properties.ProvisioningState = lo.ToPtr("Succeeded")
		}

		c.sharedStores.AKSMachines.Store(id, aksMachine)
		return &armcontainerservice.MachinesClientCreateOrUpdateResponse{Machine: aksMachine}, pollingError
	})
}

func (c *AKSMachinesAPI) Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, options *armcontainerservice.MachinesClientGetOptions) (armcontainerservice.MachinesClientGetResponse, error) {
	input := &AKSMachineGetInput{
		ResourceGroupName: resourceGroupName,
		ResourceName:      resourceName,
		AgentPoolName:     agentPoolName,
		AKSMachineName:    aksMachineName,
		Options:           options,
	}

	return c.AKSMachineGetBehavior.Invoke(input, func(input *AKSMachineGetInput) (armcontainerservice.MachinesClientGetResponse, error) {
		// Validate that the agent pool exists before attempting to get machines
		if !c.doesAgentPoolExists(input.ResourceGroupName, input.ResourceName, input.AgentPoolName) {
			return armcontainerservice.MachinesClientGetResponse{}, AKSMachineAPIErrorFromAKSMachinesPoolNotFound
		}

		// First try direct lookup using the standard machine ID format
		aksMachine, ok := c.sharedStores.AKSMachines.Load(MkMachineID(input.ResourceGroupName, input.ResourceName, input.AgentPoolName, input.AKSMachineName))
		if ok {
			return armcontainerservice.MachinesClientGetResponse{
				Machine: aksMachine.(armcontainerservice.Machine),
			}, nil
		}

		// If not found, try to find by VM name
		c.sharedStores.AKSMachines.Range(func(key, value interface{}) bool {
			storedMachine := value.(armcontainerservice.Machine)

			// Check if the input matches the AKS machine name
			if storedMachine.Name != nil && *storedMachine.Name == input.AKSMachineName {
				aksMachine = storedMachine
				ok = true
				return false
			}

			// Check if the input matches the VM name from ResourceID
			if storedMachine.Properties != nil && storedMachine.Properties.ResourceID != nil {
				vmResourceID := *storedMachine.Properties.ResourceID
				// Extract VM name from resource ID: /subscriptions/.../virtualMachines/vmName
				if vmName := extractVMNameFromResourceID(vmResourceID); vmName == input.AKSMachineName {
					aksMachine = storedMachine
					ok = true
					return false
				}
			}

			return true
		})

		if !ok {
			return armcontainerservice.MachinesClientGetResponse{}, AKSMachineAPIErrorFromAKSMachineNotFound
		}
		return armcontainerservice.MachinesClientGetResponse{
			Machine: aksMachine.(armcontainerservice.Machine),
		}, nil
	})
}

func (c *AKSMachinesAPI) NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse] {
	input := &AKSMachineListInput{
		ResourceGroupName: resourceGroupName,
		ResourceName:      resourceName,
		AgentPoolName:     agentPoolName,
		Options:           options,
	}
	pager, _ := c.AKSMachineNewListPagerBehavior.Invoke(input, func(input *AKSMachineListInput) (*runtime.Pager[armcontainerservice.MachinesClientListResponse], error) {
		// Validate that the agent pool exists before attempting to list machines
		if !c.doesAgentPoolExists(input.ResourceGroupName, input.ResourceName, input.AgentPoolName) {
			// XPMT: TODO: check API: see if this is the expected behavior
			return nil, AKSMachineAPIErrorFromAKSMachinesPoolNotFound
		}

		// For this fake implementation, return a simple pager that lists all AKS machines
		pager := runtime.NewPager(runtime.PagingHandler[armcontainerservice.MachinesClientListResponse]{
			More: func(page armcontainerservice.MachinesClientListResponse) bool {
				return false // Single page for fake implementation
			},
			Fetcher: func(ctx context.Context, page *armcontainerservice.MachinesClientListResponse) (armcontainerservice.MachinesClientListResponse, error) {
				var aksMachines []*armcontainerservice.Machine
				c.sharedStores.AKSMachines.Range(func(key, value any) bool {
					aksMachine := value.(armcontainerservice.Machine)
					aksMachines = append(aksMachines, &aksMachine)
					return true
				})

				return armcontainerservice.MachinesClientListResponse{
					MachineListResult: armcontainerservice.MachineListResult{
						Value: aksMachines,
					},
				}, nil
			},
		})
		return pager, nil
	})
	return pager
}

// doesAgentPoolExists checks if the agent pool exists
func (c *AKSMachinesAPI) doesAgentPoolExists(resourceGroupName, resourceName, agentPoolName string) bool {
	if c.sharedStores == nil || c.sharedStores.AgentPools == nil {
		return false // No store means agent pool doesn't exist
	}

	agentPoolID := MkAgentPoolID(resourceGroupName, resourceName, agentPoolName)
	_, ok := c.sharedStores.AgentPools.Load(agentPoolID)
	return ok // Return true ONLY if agent pool is actually found
}

// validateMachinePropertyChanges checks if the immutable properties of an AKS machine are being changed
func (c *AKSMachinesAPI) doImmutablePropertiesChanged(existing, incoming *armcontainerservice.Machine) bool {
	if existing.Properties == nil || incoming.Properties == nil {
		return false // Skip validation if properties are missing
	}

	// Check VM size changes (not allowed)
	if existing.Properties.Hardware != nil && incoming.Properties.Hardware != nil {
		if existing.Properties.Hardware.VMSize != nil && incoming.Properties.Hardware.VMSize != nil {
			if *existing.Properties.Hardware.VMSize != *incoming.Properties.Hardware.VMSize {
				return true
			}
		}
	}

	// Check priority changes (not allowed)
	if existing.Properties.Priority != nil && incoming.Properties.Priority != nil {
		if *existing.Properties.Priority != *incoming.Properties.Priority {
			return true
		}
	}

	// Check zone changes (not allowed)
	if len(existing.Zones) > 0 && len(incoming.Zones) > 0 {
		if *existing.Zones[0] != *incoming.Zones[0] {
			return true
		}
	}

	return false
}

func MkMachineID(resourceGroupName string, clusterName string, agentPoolName string, aksMachineName string) string {
	const idFormat = "/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s/agentPools/%s/machines/%s"
	return fmt.Sprintf(idFormat, resourceGroupName, clusterName, agentPoolName, aksMachineName)
}

// Convert from "/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03"
// to "AKSUbuntu-2204gen2containerd-2022.10.03".
func getAKSMachineNodeImageVersionFromSIGImageID(imageID string) (string, error) {
	matches := regexp.MustCompile(`(?i)/subscriptions/(\S+)/resourceGroups/(\S+)/providers/Microsoft.Compute/galleries/(\S+)/images/(\S+)/versions/(\S+)`).FindStringSubmatch(imageID)
	if matches == nil {
		return "", fmt.Errorf("incorrect SIG image ID id=%s", imageID)
	}

	// SubscriptionID := matches[1]
	// ResourceGroup := matches[2]
	Gallery := matches[3]
	Definition := matches[4]
	Version := matches[5]

	prefix := Gallery
	osVersion := Definition
	// if strings.Contains(prefix, windowsPrefix) {		// TODO(Windows)
	// 	osVersion = extractOsVersionForWindows(Definition)
	// }

	return strings.Join([]string{prefix, osVersion, Version}, "-"), nil
}

// setDefaultMachineValues sets comprehensive default values for AKS machine creation
// Note: this may not be accurate. But likely sufficient for testing.
func (c *AKSMachinesAPI) setDefaultMachineValues(machine *armcontainerservice.Machine, vmImageID string, agentPoolName string) {
	if machine.Properties == nil {
		machine.Properties = &armcontainerservice.MachineProperties{}
	}

	// Set Status with creation timestamp
	if machine.Properties.Status == nil {
		machine.Properties.Status = &armcontainerservice.MachineStatus{}
	}
	if machine.Properties.Status.CreationTimestamp == nil {
		machine.Properties.Status.CreationTimestamp = lo.ToPtr(time.Now())
	}

	// Set ProvisioningState
	if machine.Properties.ProvisioningState == nil {
		machine.Properties.ProvisioningState = lo.ToPtr("Succeeded")
	}

	// Set Priority - default to Regular if not set
	if machine.Properties.Priority == nil {
		machine.Properties.Priority = lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular)
	}

	// Set ResourceID - simulates VM resource ID
	// vmName = aks-<machinesPoolName>-<aksMachineName>-########-vm#
	if machine.Properties.ResourceID == nil {
		vmName := fmt.Sprintf("aks-%s-%s-12345678-vm0", agentPoolName, *machine.Name)
		vmResourceID := fmt.Sprintf("/subscriptions/subscriptionID/resourceGroups/test-resourceGroup/providers/Microsoft.Compute/virtualMachines/%s", vmName)
		machine.Properties.ResourceID = lo.ToPtr(vmResourceID)
	}

	// Set NodeImageVersion from vmImageID header
	if vmImageID != "" {
		nodeImageVersion, err := getAKSMachineNodeImageVersionFromSIGImageID(vmImageID)
		if err == nil && nodeImageVersion != "" {
			machine.Properties.NodeImageVersion = lo.ToPtr(nodeImageVersion)
		}
	}
	if machine.Properties.NodeImageVersion == nil {
		// Default node image version if none provided
		machine.Properties.NodeImageVersion = lo.ToPtr("AKSUbuntu-2204gen2containerd-2023.11.15")
	}
}
