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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	fakesync "github.com/Azure/karpenter-provider-azure/pkg/fake/sync"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/aksmachinesheaderbatch"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
	"github.com/samber/lo"
)

// AKSDataStorage contains the shared data stores for both AKS agent pools and machines
type AKSDataStorage struct {
	AgentPools  *fakesync.Map[string, armcontainerservice.AgentPool]
	AKSMachines *fakesync.Map[string, armcontainerservice.Machine]
}

// NewAKSDataStorage creates a new instance of shared data stores
func NewAKSDataStorage() *AKSDataStorage {
	return &AKSDataStorage{
		AgentPools:  &fakesync.Map[string, armcontainerservice.AgentPool]{},
		AKSMachines: &fakesync.Map[string, armcontainerservice.Machine]{},
	}
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

	// BatchMachineErrorFunc, if set, is called during batch creation to determine per-machine
	// errors. It receives a machine name and returns an error code and message; if the error code
	// is non-empty, the machine is treated as failed. Machines with empty error code succeed.
	// When any per-machine errors are returned, the fake produces a batch error response
	// (BatchMachineClientError or BatchMachineInternalServerError) matching the real Azure API format.
	BatchMachineErrorFunc func(machineName string) (errorCode string, errorMessage string)
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

func AKSMachineAPIErrorVMSizeNotSupported(vmSize, subscription, location string) *azcore.ResponseError {
	message := fmt.Sprintf("Virtual Machine size: '%s' is not supported for subscription %s in location '%s'. Please refer to aka.ms/aks/vm-size-selector to find supported VM sizes in location '%s'.", vmSize, subscription, location, location)
	return newResponseError("VMSizeNotSupported", http.StatusBadRequest, message)
}

func AKSMachineAPIErrorVMSizeNotSupportedBadRequest(vmSize, subscription, location string) *azcore.ResponseError {
	message := fmt.Sprintf("Virtual Machine size: '%s' is not supported for subscription %s in location '%s'. Please refer to aka.ms/aks/vm-size-selector to find supported VM sizes in location '%s'.", vmSize, subscription, location, location)
	return newResponseError("BadRequest", http.StatusBadRequest, message)
}

// statusCode is always BadRequest today but kept as a parameter for generality
func newResponseError(errorCode string, statusCode int, message string) *azcore.ResponseError {
	errorBody := fmt.Sprintf(`{"code": "%s", "message": "%s"}`, errorCode, message)
	return &azcore.ResponseError{
		ErrorCode:  errorCode,
		StatusCode: statusCode,
		RawResponse: &http.Response{
			StatusCode: statusCode,
			Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
			Body:       io.NopCloser(strings.NewReader(errorBody)),
		},
	}
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

// AKSMachineAPIErrorValidation creates a sync error (ResponseError) for validation failures.
var AKSMachineAPIErrorValidation = &azcore.ResponseError{
	ErrorCode:  "InvalidParameter",
	StatusCode: http.StatusBadRequest,
}

// AKSMachineAPIProvisioningErrorValidation creates an async provisioning error for validation failures.
func AKSMachineAPIProvisioningErrorValidation() *armcontainerservice.ErrorDetail {
	return &armcontainerservice.ErrorDetail{
		Code:    lo.ToPtr("ValidationError"),
		Message: lo.ToPtr(`Code="ValidationError" Message="The taint key 'invalid/taint/key' is not valid. A taint key must conform to the format [prefix/]name."`),
		Details: []*armcontainerservice.ErrorDetail{
			{
				Code:    lo.ToPtr("InvalidParameter"),
				Message: lo.ToPtr("The taint key 'invalid/taint/key' is not valid. A taint key must conform to the format [prefix/]name."),
			},
		},
	}
}

// assert that the fake implements the interface
var _ azapi.AKSMachinesAPI = &AKSMachinesAPI{}

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
	c.aksDataStorage.AKSMachines.Clear()
	c.AfterPollProvisioningErrorOverride = nil
	c.BatchMachineErrorFunc = nil
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

	// Validate parent AgentPool
	if !c.doesAgentPoolExists(input.ResourceGroupName, input.ResourceName, input.AgentPoolName) {
		return nil, AKSMachineAPIErrorFromAKSMachinesPoolNotFound
	}

	// If batch entries are present, create a machine for each entry.
	// This simulates what the real Azure API does when it reads the BatchPutMachine header.
	if entries := aksmachinesheaderbatch.FakeBatchEntriesFromContext(ctx); len(entries) > 0 {
		return c.createBatchMachines(input, parameters, entries)
	}

	// Non-batch path: single machine creation (original behavior)
	return c.createSingleMachine(input, parameters)
}

// createSingleMachine handles non-batch (single machine) creation.
func (c *AKSMachinesAPI) createSingleMachine(input *AKSMachineCreateOrUpdateInput, parameters armcontainerservice.Machine) (*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse], error) {
	aksMachine := deepCopyMachine(parameters)
	id := MkMachineID(input.ResourceGroupName, input.ResourceName, input.AgentPoolName, input.AKSMachineName)
	aksMachine.ID = &id
	aksMachine.Name = &input.AKSMachineName

	// Check if AKS machine already exists, if so, consider this an update than a create
	existingMachine, ok := c.aksDataStorage.AKSMachines.Load(id)
	if ok {
		return c.updateExistingAKSMachine(input, existingMachine, aksMachine)
	}

	// Default values + update status, for sync phase
	c.setDefaultMachineValues(&aksMachine, input.ResourceGroupName, input.AgentPoolName)
	aksMachine.Properties.ProvisioningState = lo.ToPtr(consts.ProvisioningStateCreating)
	c.aksDataStorage.AKSMachines.Store(id, aksMachine)

	return c.AKSMachineCreateOrUpdateBehavior.Invoke(input, func(input *AKSMachineCreateOrUpdateInput) (*armcontainerservice.MachinesClientCreateOrUpdateResponse, error) {
		// Update status, for async phase
		updatedAKSMachine, pollingError := c.simulateCreateStatusAtAsync(aksMachine)
		c.aksDataStorage.AKSMachines.Store(id, updatedAKSMachine)
		return &armcontainerservice.MachinesClientCreateOrUpdateResponse{Machine: updatedAKSMachine}, pollingError
	})
}

// createBatchMachines handles batch creation: creates one machine per entry
// using the shared template body + per-machine zones/tags from the batch entries.
// This simulates what the real Azure API does when reading the BatchPutMachine header.
//
// If BatchMachineErrorFunc is set, it is called for each machine to determine per-machine
// errors. Failed machines are NOT created; successful machines are stored normally.
// The error response matches the real Azure batch API format:
//   - If any client error (4xx-style code) is present: returns 400 BatchMachineClientError
//   - If only internal errors (5xx-style): returns 500 BatchMachineInternalServerError
//   - If all succeed: returns success as before
func (c *AKSMachinesAPI) createBatchMachines(input *AKSMachineCreateOrUpdateInput, template armcontainerservice.Machine, entries []aksmachinesheaderbatch.MachineEntry) (*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse], error) {
	// Collect per-machine errors if the error function is set
	var perMachineErrors []fakeBatchMachineError
	failedMachines := make(map[string]bool)

	if c.BatchMachineErrorFunc != nil {
		for _, entry := range entries {
			code, msg := c.BatchMachineErrorFunc(entry.MachineName)
			if code != "" {
				perMachineErrors = append(perMachineErrors, fakeBatchMachineError{
					code:    code,
					message: msg,
					target:  entry.MachineName,
				})
				failedMachines[entry.MachineName] = true
			}
		}
	}

	var primaryMachine armcontainerservice.Machine

	for i, entry := range entries {
		if failedMachines[entry.MachineName] {
			continue
		}

		machine, err := c.createOneBatchMachine(input, template, entry)
		if err != nil {
			return nil, err
		}

		if i == 0 || primaryMachine.ID == nil {
			primaryMachine = machine
		}
	}

	// If there are per-machine errors, build and return a batch error response
	if len(perMachineErrors) > 0 {
		return nil, buildFakeBatchError(perMachineErrors)
	}

	// Enrich input.AKSMachine with the primary entry's zones/tags so that
	// CalledWithInput captures meaningful per-machine data (not the cleared template).
	input.AKSMachine = primaryMachine

	// Return the poller for the primary (first) machine, matching coordinator behavior
	return c.AKSMachineCreateOrUpdateBehavior.Invoke(input, func(input *AKSMachineCreateOrUpdateInput) (*armcontainerservice.MachinesClientCreateOrUpdateResponse, error) {
		return &armcontainerservice.MachinesClientCreateOrUpdateResponse{Machine: primaryMachine}, nil
	})
}

// createOneBatchMachine builds and stores a single machine from a batch entry.
func (c *AKSMachinesAPI) createOneBatchMachine(input *AKSMachineCreateOrUpdateInput, template armcontainerservice.Machine, entry aksmachinesheaderbatch.MachineEntry) (armcontainerservice.Machine, error) {
	machine := template
	// Shallow-copy Properties to avoid mutating the shared template across loop iterations.
	if machine.Properties != nil {
		props := *machine.Properties
		machine.Properties = &props
	}
	machine.Name = lo.ToPtr(entry.MachineName)
	id := MkMachineID(input.ResourceGroupName, input.ResourceName, input.AgentPoolName, entry.MachineName)
	machine.ID = &id

	// Apply per-machine zones from the batch entry
	if len(entry.Zones) > 0 {
		zones := make([]*string, len(entry.Zones))
		for j := range entry.Zones {
			zones[j] = lo.ToPtr(entry.Zones[j])
		}
		machine.Zones = zones
	}

	// Apply per-machine tags from the batch entry
	if len(entry.Tags) > 0 {
		if machine.Properties == nil {
			machine.Properties = &armcontainerservice.MachineProperties{}
		}
		tags := make(map[string]*string, len(entry.Tags))
		for k, v := range entry.Tags {
			tags[k] = lo.ToPtr(v)
		}
		machine.Properties.Tags = tags
	}

	// Check if AKS machine already exists — if so, check for immutable property conflicts
	if existing, ok := c.aksDataStorage.AKSMachines.Load(id); ok {
		if c.doImmutablePropertiesChanged(&existing, &machine) {
			return armcontainerservice.Machine{}, AKSMachineAPIErrorFromAKSMachineImmutablePropertyChangeAttempted
		}
	}

	c.setDefaultMachineValues(&machine, input.ResourceGroupName, input.AgentPoolName)
	machine.Properties.ProvisioningState = lo.ToPtr("Creating")
	c.aksDataStorage.AKSMachines.Store(id, machine)

	updatedMachine, _ := c.simulateCreateStatusAtAsync(machine)
	c.aksDataStorage.AKSMachines.Store(id, updatedMachine)

	return updatedMachine, nil
}

// fakeBatchMachineError represents a per-machine error for building fake batch error responses.
type fakeBatchMachineError struct {
	code    string
	message string
	target  string
}

// batchErrorDetailJSON is the JSON shape for a per-machine error detail in batch API responses.
type batchErrorDetailJSON struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Target  string `json:"target"`
}

// buildFakeBatchError constructs an *azcore.ResponseError matching the real Azure batch error format.
// Mimics the JoinBatchPutMachineErrors logic tested in the wiki:
//   - If any error code looks like a client error → 400 BatchMachineClientError with details[] at top level
//   - If only internal errors → 500 BatchMachineInternalServerError with details[] JSON-encoded in message
func buildFakeBatchError(errors []fakeBatchMachineError) *azcore.ResponseError {
	details := make([]batchErrorDetailJSON, len(errors))
	hasClientError := false
	for i, e := range errors {
		details[i] = batchErrorDetailJSON{Code: e.code, Message: e.message, Target: e.target}
		// Client errors are non-Internal* codes (e.g., InvalidParameter, SkuNotAvailable, etc.)
		if !strings.HasPrefix(e.code, "Internal") {
			hasClientError = true
		}
	}

	var bodyJSON []byte
	var statusCode int
	var errorCode string

	if hasClientError {
		statusCode = http.StatusBadRequest
		errorCode = "BatchMachineClientError"
		body := struct {
			Code    string                 `json:"code"`
			Message string                 `json:"message"`
			Details []batchErrorDetailJSON `json:"details"`
		}{
			Code:    errorCode,
			Message: "batch client error",
			Details: details,
		}
		bodyJSON, _ = json.Marshal(body)
	} else {
		statusCode = http.StatusInternalServerError
		errorCode = "BatchMachineInternalServerError"
		innerJSON, _ := json.Marshal(struct {
			Details []batchErrorDetailJSON `json:"details"`
		}{Details: details})
		body := struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{
			Code:    errorCode,
			Message: string(innerJSON),
		}
		bodyJSON, _ = json.Marshal(body)
	}

	return &azcore.ResponseError{
		ErrorCode:  errorCode,
		StatusCode: statusCode,
		RawResponse: &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(bytes.NewReader(bodyJSON)),
		},
	}
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
		aksMachine.Properties.ProvisioningState = lo.ToPtr(consts.ProvisioningStateFailed)
		if aksMachine.Properties.Status == nil {
			aksMachine.Properties.Status = &armcontainerservice.MachineStatus{}
		}
		aksMachine.Properties.Status.ProvisioningError = c.AfterPollProvisioningErrorOverride
		pollingError = AKSMachineAPIErrorAny
	} else {
		aksMachine.Properties.ProvisioningState = lo.ToPtr(consts.ProvisioningStateSucceeded)
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
				Machine: aksMachine,
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
				c.aksDataStorage.AKSMachines.Range(func(machineID string, aksMachine armcontainerservice.Machine) bool {
					if strings.HasPrefix(machineID, expectedIDPrefix) {
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

// setDefaultMachineValues sets comprehensive default values for AKS machine creation
// Note: this may not be accurate. But likely sufficient for testing.
func (c *AKSMachinesAPI) setDefaultMachineValues(machine *armcontainerservice.Machine, resourceGroupName string, agentPoolName string) {
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
		machine.Properties.ProvisioningState = lo.ToPtr(consts.ProvisioningStateSucceeded)
	}

	// Set Priority - default to Regular if not set
	if machine.Properties.Priority == nil {
		machine.Properties.Priority = lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular)
	}

	// Set ResourceID - simulates VM resource ID
	// vmName = aks-<machinesPoolName>-<aksMachineName>-########-vm
	if machine.Properties.ResourceID == nil {
		vmName := fmt.Sprintf("aks-%s-%s-12345678-vm", agentPoolName, *machine.Name)
		vmResourceID := fmt.Sprintf("/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s", resourceGroupName, vmName)
		machine.Properties.ResourceID = lo.ToPtr(vmResourceID)
	}

	// NodeImageVersion is now set directly on the machine template by the caller.
	// Only apply default if not provided.
	if machine.Properties.NodeImageVersion == nil {
		// Default node image version if none provided
		machine.Properties.NodeImageVersion = lo.ToPtr("AKSUbuntu-2204gen2containerd-2023.11.15")
	}

	// Set ETag for optimistic concurrency control
	if machine.Properties.ETag == nil {
		machine.Properties.ETag = lo.ToPtr(fmt.Sprintf(`"etag-%d"`, time.Now().UnixNano()))
	}
}

// deepCopyMachine returns a fully independent copy of an AKS Machine via JSON
// round-trip, simulating the serialization boundary of a real HTTP call.
func deepCopyMachine(src armcontainerservice.Machine) armcontainerservice.Machine {
	data, err := json.Marshal(src)
	if err != nil {
		panic(fmt.Sprintf("fake: failed to marshal Machine for deep copy: %v", err))
	}
	var dst armcontainerservice.Machine
	if err := json.Unmarshal(data, &dst); err != nil {
		panic(fmt.Sprintf("fake: failed to unmarshal Machine for deep copy: %v", err))
	}
	return dst
}
