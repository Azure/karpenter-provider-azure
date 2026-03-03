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

package test

import (
	"fmt"

	"dario.cat/mergo"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/samber/lo"
)

// AKSAgentPoolOptions customizes an AKS Agent Pool for testing.
type AKSAgentPoolOptions struct {
	Name          string
	ResourceGroup string
	// SubscriptionID      string
	ClusterName         string
	Count               int32
	VMSize              string
	OrchestratorVersion string
	Tags                map[string]*string
}

// AKSAgentPool creates a test AKS Agent Pool with defaults that can be overridden by AKSAgentPoolOptions.
// Overrides are applied in order, with last-write-wins semantics.
func AKSAgentPool(overrides ...AKSAgentPoolOptions) *armcontainerservice.AgentPool {
	options := AKSAgentPoolOptions{}
	for _, o := range overrides {
		if err := mergo.Merge(&options, o, mergo.WithOverride); err != nil {
			panic(fmt.Sprintf("Failed to merge AKSAgentPool options: %s", err))
		}
	}

	// Provide default values if none are set
	if options.Name == "" {
		options.Name = "default"
	}
	if options.ResourceGroup == "" {
		options.ResourceGroup = "test-resourceGroup"
	}
	if options.ClusterName == "" {
		options.ClusterName = "test-cluster"
	}
	// if options.SubscriptionID == "" {
	// 	options.SubscriptionID = "test-subscription"
	// }
	if options.Count == 0 {
		options.Count = 3
	}
	if options.VMSize == "" {
		options.VMSize = "Standard_D2s_v3"
	}
	if options.OrchestratorVersion == "" {
		options.OrchestratorVersion = "1.28.0"
	}
	if options.Tags == nil {
		options.Tags = map[string]*string{}
	}

	// Create the agent pool ID using the fake helper
	agentPoolID := fake.MkAgentPoolID(options.ResourceGroup, options.ClusterName, options.Name)

	// Construct the AKS Agent Pool
	agentPool := &armcontainerservice.AgentPool{
		ID:   lo.ToPtr(agentPoolID),
		Name: lo.ToPtr(options.Name),
		Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
			Count:               lo.ToPtr(options.Count),
			VMSize:              lo.ToPtr(options.VMSize),
			OrchestratorVersion: lo.ToPtr(options.OrchestratorVersion),
			ProvisioningState:   lo.ToPtr("Succeeded"),
			Tags:                options.Tags,
			Mode:                lo.ToPtr(armcontainerservice.AgentPoolModeMachines),
		},
	}

	return agentPool
}
