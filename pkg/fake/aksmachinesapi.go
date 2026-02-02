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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/samber/lo"
)

// AKSDataStorage contains the shared data stores for both AKS agent pools and machines
type AKSDataStorage struct {
	AgentPools  *sync.Map
	AKSMachines *sync.Map
}

// NewAKSDataStorage creates a new instance of shared data stores
func NewAKSDataStorage() *AKSDataStorage {
	return &AKSDataStorage{
		AgentPools:  &sync.Map{},
		AKSMachines: &sync.Map{},
	}
}

type VMImageIDContextKey string

const VMImageIDKey VMImageIDContextKey = "vmimageid"

// This is not really the real one being used, which is the header.
// But the header cannot be extracted due to azure-sdk-for-go being restrictive. This is good enough.
func GetVMImageIDFromContext(ctx context.Context) string {
	if ctx.Value(VMImageIDKey) != nil {
		return ctx.Value(VMImageIDKey).(string)
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
	AfterPollProvisioningErrorOverride *armcontainerservice.ErrorDetail
}

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

func AKSMachineAPIProvisioningErrorSkuNotAvailable(sku string, location string) *armcontainerservice.ErrorDetail {
	return &armcontainerservice.ErrorDetail{
		Code:    lo.ToPtr("SkuNotAvailable"),
		Message: lo.ToPtr(fmt.Sprintf("Code=\"SkuNotAvailable\" Message=\"The requested VM size for resource 'Following SKUs have failed for Capacity Restrictions: %s' is currently not available in location '%s'. Please try another size or deploy to a different location or different zone. See https://aka.ms/azureskunotavailable for details.\" Target=\"sku.name\"", sku, location)),
		Details: []*armcontainerservice.ErrorDetail{
			{
				Code:    lo.ToPtr("SkuNotAvailable"),
				Message: lo.ToPtr(fmt.Sprintf("The requested VM size for resource 'Following SKUs have failed for Capacity Restrictions: %s' is currently not available in location '%s'. Please try another size or deploy to a different location or different zone. See https://aka.ms/azureskunotavailable for details.", sku, location)),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorLowPriorityCoresQuota(location string) *armcontainerservice.ErrorDetail {
	return &armcontainerservice.ErrorDetail{
		Code:    lo.ToPtr("QuotaExceeded"),
		Message: lo.ToPtr(fmt.Sprintf("Code=\"OperationNotAllowed\" Message=\"Operation could not be completed as it results in exceeding approved LowPriorityCores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: 3, Current Usage: 0, Additional Required: 6, (Minimum) New Limit Required: 6.\"", location)),
		Details: []*armcontainerservice.ErrorDetail{
			{
				Code:    lo.ToPtr("OperationNotAllowed"),
				Message: lo.ToPtr(fmt.Sprintf("Operation could not be completed as it results in exceeding approved LowPriorityCores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: 3, Current Usage: 0, Additional Required: 6, (Minimum) New Limit Required: 6.", location)),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorOverconstrainedZonalAllocation() *armcontainerservice.ErrorDetail {
	return &armcontainerservice.ErrorDetail{
		Code:    lo.ToPtr("OverconstrainedZonalAllocationRequest"),
		Message: lo.ToPtr("original error: Code=\"OverconstrainedZonalAllocationRequest\" Message=\"Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive. Please remove some constraints and try again. Constraints applied are:\\n  - Availability Zone\\n  - Low Priority VMs\\n  - Networking Constraints (such as Accelerated Networking or IPv6)\\n  - Preemptible VMs (VM might be preempted by another VM with a higher priority)\\n  - VM Size\\n\" Target=\"6\""),
		Details: []*armcontainerservice.ErrorDetail{
			{
				Code:    lo.ToPtr("OverconstrainedZonalAllocationRequest"),
				Message: lo.ToPtr("Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive. Please remove some constraints and try again. Constraints applied are:\n  - Availability Zone\n  - Low Priority VMs\n  - Networking Constraints (such as Accelerated Networking or IPv6)\n  - Preemptible VMs (VM might be preempted by another VM with a higher priority)\n  - VM Size\n"),
				Target:  lo.ToPtr("6"),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorOverconstrainedAllocation() *armcontainerservice.ErrorDetail {
	return &armcontainerservice.ErrorDetail{
		Code:    lo.ToPtr("OverconstrainedAllocationRequest"),
		Message: lo.ToPtr("Code=\"OverconstrainedAllocationRequest\" Message=\"Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive. Please remove some constraints and try again. Constraints applied are:\\n  - Differencing (Ephemeral) Disks\\n  - Networking Constraints (such as Accelerated Networking or IPv6)\\n  - Subscription Pinning\\n  - VM Size\\n\" Target=\"0\""),
		Details: []*armcontainerservice.ErrorDetail{
			{
				Code:    lo.ToPtr("OverconstrainedAllocationRequest"),
				Message: lo.ToPtr("Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive. Please remove some constraints and try again. Constraints applied are:\n  - Differencing (Ephemeral) Disks\n  - Networking Constraints (such as Accelerated Networking or IPv6)\n  - Subscription Pinning\n  - VM Size\n"),
				Target:  lo.ToPtr("0"),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorAllocationFailed() *armcontainerservice.ErrorDetail {
	return &armcontainerservice.ErrorDetail{
		Code:    lo.ToPtr("AllocationFailed"),
		Message: lo.ToPtr("original error: Code=\"AllocationFailed\" Message=\"Allocation failed. If you are trying to add a new VM to a Virtual Machine Scale Set with a single placement group or update/resize an existing VM in a Virtual Machine Scale Set with a single placement group, please note that such allocation is scoped to a single cluster, and it is possible that the cluster is out of capacity. Please read more about improving likelihood of allocation success at http://aka.ms/allocation-guidance.\" Target=\"243\""),
		Details: []*armcontainerservice.ErrorDetail{
			{
				Code:    lo.ToPtr("AllocationFailed"),
				Message: lo.ToPtr("Allocation failed. If you are trying to add a new VM to a Virtual Machine Scale Set with a single placement group or update/resize an existing VM in a Virtual Machine Scale Set with a single placement group, please note that such allocation is scoped to a single cluster, and it is possible that the cluster is out of capacity. Please read more about improving likelihood of allocation success at http://aka.ms/allocation-guidance."),
				Target:  lo.ToPtr("243"),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorVMFamilyQuotaExceeded(location string, familyName string, currentLimit int32, currentUsage int32, additionalRequired int32, newLimitRequired int32) *armcontainerservice.ErrorDetail {
	return &armcontainerservice.ErrorDetail{
		Code:    lo.ToPtr("QuotaExceeded"),
		Message: lo.ToPtr(fmt.Sprintf("Code=\"OperationNotAllowed\" Message=\"Operation could not be completed as it results in exceeding approved %s Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: %d, Current Usage: %d, Additional Required: %d, (Minimum) New Limit Required: %d.\"", familyName, location, currentLimit, currentUsage, additionalRequired, newLimitRequired)),
		Details: []*armcontainerservice.ErrorDetail{
			{
				Code:    lo.ToPtr("OperationNotAllowed"),
				Message: lo.ToPtr(fmt.Sprintf("Operation could not be completed as it results in exceeding approved %s Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: %d, Current Usage: %d, Additional Required: %d, (Minimum) New Limit Required: %d.", familyName, location, currentLimit, currentUsage, additionalRequired, newLimitRequired)),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorTotalRegionalCoresQuota(location string) *armcontainerservice.ErrorDetail {
	return &armcontainerservice.ErrorDetail{
		Code:    lo.ToPtr("QuotaExceeded"),
		Message: lo.ToPtr(fmt.Sprintf("Code=\"OperationNotAllowed\" Message=\"Operation could not be completed as it results in exceeding approved Total Regional Cores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: 10, Current Usage: 8, Additional Required: 8, (Minimum) New Limit Required: 16.\"", location)),
		Details: []*armcontainerservice.ErrorDetail{
			{
				Code:    lo.ToPtr("OperationNotAllowed"),
				Message: lo.ToPtr(fmt.Sprintf("Operation could not be completed as it results in exceeding approved Total Regional Cores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: 10, Current Usage: 8, Additional Required: 8, (Minimum) New Limit Required: 16.", location)),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorZoneAllocationFailed(sku string, zone string) *armcontainerservice.ErrorDetail {
	return &armcontainerservice.ErrorDetail{
		Code:    lo.ToPtr("ZonalAllocationFailed"),
		Message: lo.ToPtr(fmt.Sprintf("Code=\"ZonalAllocationFailed\" Message=\"Allocation failed. We do not have sufficient capacity for the requested VM size %s in zone %s. Read more about improving likelihood of allocation success at http://aka.ms/allocation-guidance\"", sku, zone)),
		Details: []*armcontainerservice.ErrorDetail{
			{
				Code:    lo.ToPtr("ZonalAllocationFailed"),
				Message: lo.ToPtr(fmt.Sprintf("Allocation failed. We do not have sufficient capacity for the requested VM size %s in zone %s. Read more about improving likelihood of allocation success at http://aka.ms/allocation-guidance", sku, zone)),
			},
		},
	}
}

func AKSMachineAPIProvisioningErrorAny() *armcontainerservice.ErrorDetail {
	return &armcontainerservice.ErrorDetail{
		Code:    lo.ToPtr("SomeRandomError"),
		Message: lo.ToPtr("An unexpected error occurred."),
		Details: []*armcontainerservice.ErrorDetail{
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
	aksDataStorage *AKSDataStorage
}

// NewAKSMachinesAPI creates a new AKSMachinesAPI instance with shared data stores
func NewAKSMachinesAPI(aksDataStorage *AKSDataStorage) *AKSMachinesAPI {
	return &AKSMachinesAPI{
		aksDataStorage: aksDataStorage,
	}
}

// Reset must be called between tests otherwise tests will pollute each other.
func (c *AKSMachinesAPI) Reset() {
	c.AKSMachineCreateOrUpdateBehavior.Reset()
	c.AKSMachineGetBehavior.Reset()
	c.AKSMachineNewListPagerBehavior.Reset()
	c.aksDataStorage.AKSMachines.Range(func(k, v any) bool {
		c.aksDataStorage.AKSMachines.Delete(k)
		return true
	})
	c.AfterPollProvisioningErrorOverride = nil
}

func (c *AKSMachinesAPI) BeginCreateOrUpdate(
	ctx context.Context,
	resourceGroupName string,
	resourceName string,
	agentPoolName string,
	aksMachineName string,
	parameters armcontainerservice.Machine,
	options *armcontainerservice.MachinesClientBeginCreateOrUpdateOptions,
) (*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse], error) {
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
	aksMachine.ID = &id
	aksMachine.Name = &input.AKSMachineName

	// Validate parent AgentPool
	if !c.doesAgentPoolExists(input.ResourceGroupName, input.ResourceName, input.AgentPoolName) {
		return nil, AKSMachineAPIErrorFromAKSMachinesPoolNotFound
	}

	// Check if AKS machine already exists, if so, consider this an update than a create
	existingMachine, ok := c.aksDataStorage.AKSMachines.Load(id)
	if ok {
		return c.updateExistingAKSMachine(input, existingMachine.(armcontainerservice.Machine), aksMachine)
	}

	// Default values + update status, for sync phase
	vmImageID := GetVMImageIDFromContext(ctx)
	c.setDefaultMachineValues(&aksMachine, vmImageID, input.ResourceGroupName, input.AgentPoolName)
	aksMachine.Properties.ProvisioningState = lo.ToPtr("Creating")
	c.aksDataStorage.AKSMachines.Store(id, aksMachine)

	return c.AKSMachineCreateOrUpdateBehavior.Invoke(input, func(input *AKSMachineCreateOrUpdateInput) (*armcontainerservice.MachinesClientCreateOrUpdateResponse, error) {
		// Update status, for async phase
		updatedAKSMachine, pollingError := c.simulateCreateStatusAtAsync(aksMachine)
		c.aksDataStorage.AKSMachines.Store(id, updatedAKSMachine)
		return &armcontainerservice.MachinesClientCreateOrUpdateResponse{Machine: updatedAKSMachine}, pollingError
	})
}

func (c *AKSMachinesAPI) updateExistingAKSMachine(input *AKSMachineCreateOrUpdateInput, existing armcontainerservice.Machine, aksMachine armcontainerservice.Machine) (*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse], error) {
	// Check ETag for optimistic concurrency control
	if input.Options != nil && input.Options.IfMatch != nil {
		if existing.Properties == nil || existing.Properties.ETag == nil ||
			*existing.Properties.ETag != *input.Options.IfMatch {
			return nil, &azcore.ResponseError{
				StatusCode: http.StatusPreconditionFailed,
				ErrorCode:  "ConditionNotMet",
			}
		}
	}

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

	// Update ETag after successful update
	if existing.Properties != nil {
		existing.Properties.ETag = lo.ToPtr(fmt.Sprintf(`"etag-%d"`, time.Now().UnixNano()))
	}

	// Write the updated machine
	c.aksDataStorage.AKSMachines.Store(*existing.ID, existing)
	return c.AKSMachineCreateOrUpdateBehavior.Invoke(input, func(input *AKSMachineCreateOrUpdateInput) (*armcontainerservice.MachinesClientCreateOrUpdateResponse, error) {
		return &armcontainerservice.MachinesClientCreateOrUpdateResponse{Machine: existing}, nil
	})
}

func (c *AKSMachinesAPI) simulateCreateStatusAtAsync(aksMachine armcontainerservice.Machine) (armcontainerservice.Machine, error) {
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

	return aksMachine, pollingError
}

func (c *AKSMachinesAPI) Get(
	ctx context.Context,
	resourceGroupName string,
	resourceName string,
	agentPoolName string,
	aksMachineName string,
	options *armcontainerservice.MachinesClientGetOptions,
) (armcontainerservice.MachinesClientGetResponse, error) {
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
		aksMachine, ok := c.aksDataStorage.AKSMachines.Load(MkMachineID(input.ResourceGroupName, input.ResourceName, input.AgentPoolName, input.AKSMachineName))
		if ok {
			return armcontainerservice.MachinesClientGetResponse{
				Machine: aksMachine.(armcontainerservice.Machine),
			}, nil
		} else {
			return armcontainerservice.MachinesClientGetResponse{}, AKSMachineAPIErrorFromAKSMachineNotFound
		}
	})
}

func (c *AKSMachinesAPI) NewListPager(
	resourceGroupName string,
	resourceName string,
	agentPoolName string,
	options *armcontainerservice.MachinesClientListOptions,
) *runtime.Pager[armcontainerservice.MachinesClientListResponse] {
	input := &AKSMachineListInput{
		ResourceGroupName: resourceGroupName,
		ResourceName:      resourceName,
		AgentPoolName:     agentPoolName,
		Options:           options,
	}
	pager, _ := c.AKSMachineNewListPagerBehavior.Invoke(input, func(input *AKSMachineListInput) (*runtime.Pager[armcontainerservice.MachinesClientListResponse], error) {
		// For this fake implementation, return a simple pager that lists all AKS machines
		pager := runtime.NewPager(runtime.PagingHandler[armcontainerservice.MachinesClientListResponse]{
			More: func(page armcontainerservice.MachinesClientListResponse) bool {
				return false // Single page for fake implementation
			},
			Fetcher: func(ctx context.Context, page *armcontainerservice.MachinesClientListResponse) (armcontainerservice.MachinesClientListResponse, error) {
				// Check if the agent pool exists when fetching the page
				if !c.doesAgentPoolExists(input.ResourceGroupName, input.ResourceName, input.AgentPoolName) {
					// AKS machines pool not found. Return ARM not found error to match real API behavior.
					return armcontainerservice.MachinesClientListResponse{}, AKSMachineAPIErrorFromAKSMachinesPoolNotFound
				}

				var aksMachines []*armcontainerservice.Machine
				expectedIDPrefix := fmt.Sprintf("/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s/agentPools/%s/machines/",
					input.ResourceGroupName, input.ResourceName, input.AgentPoolName)
				c.aksDataStorage.AKSMachines.Range(func(key, value any) bool {
					machineID := key.(string)
					if strings.HasPrefix(machineID, expectedIDPrefix) {
						aksMachine := value.(armcontainerservice.Machine)
						aksMachines = append(aksMachines, &aksMachine)
					}
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
	if c.aksDataStorage == nil || c.aksDataStorage.AgentPools == nil {
		return false // No store means agent pool doesn't exist
	}

	agentPoolID := MkAgentPoolID(resourceGroupName, resourceName, agentPoolName)
	_, ok := c.aksDataStorage.AgentPools.Load(agentPoolID)
	return ok // Return true ONLY if agent pool is actually found
}

// validateMachinePropertyChanges checks if the immutable properties of an AKS machine are being changed
//
//nolint:gocyclo
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
//
//nolint:gocyclo
func (c *AKSMachinesAPI) setDefaultMachineValues(machine *armcontainerservice.Machine, vmImageID string, resourceGroupName string, agentPoolName string) {
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
		vmResourceID := fmt.Sprintf("/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s", resourceGroupName, vmName)
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

	// Set ETag for optimistic concurrency control
	if machine.Properties.ETag == nil {
		machine.Properties.ETag = lo.ToPtr(fmt.Sprintf(`"etag-%d"`, time.Now().UnixNano()))
	}
}
