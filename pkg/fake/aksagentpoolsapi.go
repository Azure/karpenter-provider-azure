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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/samber/lo"
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
	AgentPoolCreateOrUpdateBehavior MockedLRO[AgentPoolCreateOrUpdateInput, armcontainerservice.AgentPoolsClientCreateOrUpdateResponse]
}

// XPMT: TODO: check API: all these
var AKSAgentPoolsAPIErrorFromAKSMachineNotFound = &azcore.ResponseError{
	ErrorCode:  "NotFound",
	StatusCode: http.StatusNotFound,
}
var AKSAgentPoolsAPIErrorFromAKSAgentPoolNotFound = &azcore.ResponseError{
	ErrorCode:  "NotFound",
	StatusCode: http.StatusNotFound,
}
var AKSAgentPoolsAPIErrorFromServer = &azcore.ResponseError{
	ErrorCode: "SomeRandomError",
}

// assert that the fake implements the interface
var _ instance.AKSAgentPoolsAPI = &AKSAgentPoolsAPI{}

type AKSAgentPoolsAPI struct {
	AgentPoolsBehavior
	sharedStores *SharedAKSDataStores
}

func NewAKSAgentPoolsAPI(sharedStores *SharedAKSDataStores) *AKSAgentPoolsAPI {
	return &AKSAgentPoolsAPI{
		sharedStores: sharedStores,
	}
}

// Reset must be called between tests otherwise tests will pollute each other.
func (c *AKSAgentPoolsAPI) Reset() {
	c.AgentPoolDeleteMachinesBehavior.Reset()
	c.AgentPoolGetBehavior.Reset()
	c.AgentPoolCreateOrUpdateBehavior.Reset()
	c.sharedStores.AgentPools.Range(func(k, v any) bool {
		c.sharedStores.AgentPools.Delete(k)
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
		agentPool, ok := c.sharedStores.AgentPools.Load(agentPoolID)
		if !ok {
			return armcontainerservice.AgentPoolsClientGetResponse{}, AKSAgentPoolsAPIErrorFromAKSAgentPoolNotFound
		}
		return armcontainerservice.AgentPoolsClientGetResponse{
			AgentPool: agentPool.(armcontainerservice.AgentPool),
		}, nil
	})
}

func (c *AKSAgentPoolsAPI) BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, parameters armcontainerservice.AgentPool, options *armcontainerservice.AgentPoolsClientBeginCreateOrUpdateOptions) (*runtime.Poller[armcontainerservice.AgentPoolsClientCreateOrUpdateResponse], error) {
	input := &AgentPoolCreateOrUpdateInput{
		ResourceGroupName: resourceGroupName,
		ResourceName:      resourceName,
		AgentPoolName:     agentPoolName,
		Parameters:        parameters,
		Options:           options,
	}

	agentPool := input.Parameters
	agentPoolID := MkAgentPoolID(input.ResourceGroupName, input.ResourceName, input.AgentPoolName)

	// Set some default values
	if agentPool.ID == nil {
		agentPool.ID = &agentPoolID
	}
	if agentPool.Name == nil {
		agentPool.Name = &input.AgentPoolName
	}

	agentPool.Properties.ProvisioningState = lo.ToPtr("Creating")

	c.sharedStores.AgentPools.Store(agentPoolID, agentPool)

	// This does not handle the case where the agent pool already exists; it will overwrite it.
	return c.AgentPoolCreateOrUpdateBehavior.Invoke(input, func(input *AgentPoolCreateOrUpdateInput) (*armcontainerservice.AgentPoolsClientCreateOrUpdateResponse, error) {
		agentPool.Properties.ProvisioningState = lo.ToPtr("Succeeded")
		c.sharedStores.AgentPools.Store(agentPoolID, agentPool)
		return &armcontainerservice.AgentPoolsClientCreateOrUpdateResponse{AgentPool: agentPool}, nil
	})
}

func (c *AKSAgentPoolsAPI) BeginDeleteMachines(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachines armcontainerservice.AgentPoolDeleteMachinesParameter, options *armcontainerservice.AgentPoolsClientBeginDeleteMachinesOptions) (*runtime.Poller[armcontainerservice.AgentPoolsClientDeleteMachinesResponse], error) {
	input := &AgentPoolDeleteMachinesInput{
		ResourceGroupName: resourceGroupName,
		ResourceName:      resourceName,
		AgentPoolName:     agentPoolName,
		AKSMachines:       aksMachines,
		Options:           options,
	}
	// Check if agent pool exists before deleting machines
	agentPoolID := MkAgentPoolID(input.ResourceGroupName, input.ResourceName, input.AgentPoolName)
	_, ok := c.sharedStores.AgentPools.Load(agentPoolID)
	if !ok {
		return nil, AKSAgentPoolsAPIErrorFromAKSAgentPoolNotFound
	}

	return c.AgentPoolDeleteMachinesBehavior.Invoke(input, func(input *AgentPoolDeleteMachinesInput) (*armcontainerservice.AgentPoolsClientDeleteMachinesResponse, error) {
		// Delete machines from shared storage
		for _, aksMachineName := range input.AKSMachines.MachineNames {
			if aksMachineName != nil {
				id := MkMachineID(input.ResourceGroupName, input.ResourceName, input.AgentPoolName, *aksMachineName)
				if c.sharedStores != nil && c.sharedStores.AKSMachines != nil {
					c.sharedStores.AKSMachines.Delete(id)
				}
			}
		}

		return &armcontainerservice.AgentPoolsClientDeleteMachinesResponse{}, nil
	})
}

func MkAgentPoolID(resourceGroupName string, clusterName string, agentPoolName string) string {
	const idFormat = "/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s/agentPools/%s"
	return fmt.Sprintf(idFormat, resourceGroupName, clusterName, agentPoolName)
}
