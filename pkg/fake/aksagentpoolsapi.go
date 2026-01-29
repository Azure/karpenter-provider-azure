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
	"io"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
)

type AgentPoolDeleteMachinesInput struct {
	ResourceGroupName string
	ResourceName      string
	AgentPoolName     string
	AKSMachines       armcontainerservice.AgentPoolDeleteMachinesParameter
	Options           *armcontainerservice.AgentPoolsClientBeginDeleteMachinesOptions
}

type AgentPoolGetInput struct {
	ResourceGroupName string
	ResourceName      string
	AgentPoolName     string
	Options           *armcontainerservice.AgentPoolsClientGetOptions
}

type AgentPoolCreateOrUpdateInput struct {
	ResourceGroupName string
	ResourceName      string
	AgentPoolName     string
	Parameters        armcontainerservice.AgentPool
	Options           *armcontainerservice.AgentPoolsClientBeginCreateOrUpdateOptions
}

type AgentPoolsBehavior struct {
	AgentPoolDeleteMachinesBehavior MockedLRO[AgentPoolDeleteMachinesInput, armcontainerservice.AgentPoolsClientDeleteMachinesResponse]
	AgentPoolGetBehavior            MockedFunction[AgentPoolGetInput, armcontainerservice.AgentPoolsClientGetResponse]
}

var AKSAgentPoolsAPIErrorFromAKSAgentPoolNotFound = &azcore.ResponseError{
	ErrorCode:  "NotFound",
	StatusCode: http.StatusNotFound,
}

// AKSAgentPoolsAPIErrorFromAKSMachineNotFound creates the specific error for when machines cannot be found during delete
func AKSAgentPoolsAPIErrorFromAKSMachineNotFound(agentPoolName string, validMachines []string) error {
	message := fmt.Sprintf("Cannot find any valid machines to delete. Please check your input machine names. The valid machines to delete in agent pool '%s' are: %s.",
		agentPoolName, strings.Join(validMachines, ", "))

	errorBody := fmt.Sprintf(`{"error": {"code": "InvalidParameter", "message": "%s"}}`, message)
	return &azcore.ResponseError{
		ErrorCode:  "InvalidParameter",
		StatusCode: http.StatusBadRequest,
		RawResponse: &http.Response{
			Body: io.NopCloser(strings.NewReader(errorBody)),
		},
	}
}

// assert that the fake implements the interface
var _ instance.AKSAgentPoolsAPI = &AKSAgentPoolsAPI{}

type AKSAgentPoolsAPI struct {
	AgentPoolsBehavior
	aksDataStorage *AKSDataStorage
}

func NewAKSAgentPoolsAPI(aksDataStorage *AKSDataStorage) *AKSAgentPoolsAPI {
	return &AKSAgentPoolsAPI{
		aksDataStorage: aksDataStorage,
	}
}

// Reset must be called between tests otherwise tests will pollute each other.
func (c *AKSAgentPoolsAPI) Reset() {
	c.AgentPoolDeleteMachinesBehavior.Reset()
	c.AgentPoolGetBehavior.Reset()
	c.aksDataStorage.AgentPools.Range(func(k, v any) bool {
		c.aksDataStorage.AgentPools.Delete(k)
		return true
	})
}

func (c *AKSAgentPoolsAPI) Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.AgentPoolsClientGetOptions) (armcontainerservice.AgentPoolsClientGetResponse, error) {
	input := &AgentPoolGetInput{
		ResourceGroupName: resourceGroupName,
		ResourceName:      resourceName,
		AgentPoolName:     agentPoolName,
		Options:           options,
	}

	return c.AgentPoolGetBehavior.Invoke(input, func(input *AgentPoolGetInput) (armcontainerservice.AgentPoolsClientGetResponse, error) {
		agentPoolID := MkAgentPoolID(input.ResourceGroupName, input.ResourceName, input.AgentPoolName)
		agentPool, ok := c.aksDataStorage.AgentPools.Load(agentPoolID)
		if !ok {
			return armcontainerservice.AgentPoolsClientGetResponse{}, AKSAgentPoolsAPIErrorFromAKSAgentPoolNotFound
		}
		return armcontainerservice.AgentPoolsClientGetResponse{
			AgentPool: agentPool.(armcontainerservice.AgentPool),
		}, nil
	})
}

// Already procedural, and is a fake
//
//nolint:gocyclo
func (c *AKSAgentPoolsAPI) BeginDeleteMachines(
	ctx context.Context,
	resourceGroupName string,
	resourceName string,
	agentPoolName string,
	aksMachines armcontainerservice.AgentPoolDeleteMachinesParameter,
	options *armcontainerservice.AgentPoolsClientBeginDeleteMachinesOptions,
) (*runtime.Poller[armcontainerservice.AgentPoolsClientDeleteMachinesResponse], error) {
	input := &AgentPoolDeleteMachinesInput{
		ResourceGroupName: resourceGroupName,
		ResourceName:      resourceName,
		AgentPoolName:     agentPoolName,
		AKSMachines:       aksMachines,
		Options:           options,
	}
	// Check if agent pool exists before deleting machines
	agentPoolID := MkAgentPoolID(input.ResourceGroupName, input.ResourceName, input.AgentPoolName)
	_, ok := c.aksDataStorage.AgentPools.Load(agentPoolID)
	if !ok {
		return nil, AKSAgentPoolsAPIErrorFromAKSAgentPoolNotFound
	}

	return c.AgentPoolDeleteMachinesBehavior.Invoke(input, func(input *AgentPoolDeleteMachinesInput) (*armcontainerservice.AgentPoolsClientDeleteMachinesResponse, error) {
		// First, validate that all machines exist and collect valid/invalid machines
		var validMachines []string
		var invalidMachines []string
		var allValidMachinesInPool []string

		// Collect all existing machines in the agent pool for error message
		if c.aksDataStorage != nil && c.aksDataStorage.AKSMachines != nil {
			c.aksDataStorage.AKSMachines.Range(func(key, value interface{}) bool {
				machineID := key.(string)
				// Check if this machine belongs to the same agent pool
				expectedPrefix := fmt.Sprintf("/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s/agentPools/%s/machines/",
					input.ResourceGroupName, input.ResourceName, input.AgentPoolName)
				if strings.HasPrefix(machineID, expectedPrefix) {
					machineName := strings.TrimPrefix(machineID, expectedPrefix)
					allValidMachinesInPool = append(allValidMachinesInPool, machineName)
				}
				return true
			})
		}

		// Check if requested machines exist
		for _, aksMachineName := range input.AKSMachines.MachineNames {
			if aksMachineName != nil {
				id := MkMachineID(input.ResourceGroupName, input.ResourceName, input.AgentPoolName, *aksMachineName)
				if c.aksDataStorage != nil && c.aksDataStorage.AKSMachines != nil {
					if _, exists := c.aksDataStorage.AKSMachines.Load(id); exists {
						validMachines = append(validMachines, *aksMachineName)
					} else {
						invalidMachines = append(invalidMachines, *aksMachineName)
					}
				}
			}
		}

		// If any machines are invalid, return the InvalidParameter error
		if len(invalidMachines) > 0 {
			return nil, AKSAgentPoolsAPIErrorFromAKSMachineNotFound(input.AgentPoolName, allValidMachinesInPool)
		}

		// Delete only the valid machines
		for _, validMachine := range validMachines {
			id := MkMachineID(input.ResourceGroupName, input.ResourceName, input.AgentPoolName, validMachine)
			if c.aksDataStorage != nil && c.aksDataStorage.AKSMachines != nil {
				c.aksDataStorage.AKSMachines.Delete(id)
			}
		}

		return &armcontainerservice.AgentPoolsClientDeleteMachinesResponse{}, nil
	})
}

func MkAgentPoolID(resourceGroupName string, clusterName string, agentPoolName string) string {
	const idFormat = "/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s/agentPools/%s"
	return fmt.Sprintf(idFormat, resourceGroupName, clusterName, agentPoolName)
}
