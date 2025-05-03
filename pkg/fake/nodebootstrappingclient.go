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
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/client/operations"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
	"github.com/go-openapi/runtime"
)

// NodeBootstrappingClient is a fake implementation of the NodeBootstrapping client for testing
type NodeBootstrappingClient struct {
	operations.ClientService
	GetFunc          func(params *operations.NodeBootstrappingGetParams) (*operations.NodeBootstrappingGetOK, error)
	Called           bool
	CalledWithParams map[string]string
	ExpectedParams   map[string]string
}

// NewNodeBootstrappingClient creates a new fake NodeBootstrapping client
func NewNodeBootstrappingClient() *NodeBootstrappingClient {
	return &NodeBootstrappingClient{
		CalledWithParams: make(map[string]string),
		ExpectedParams:   make(map[string]string),
		GetFunc: func(params *operations.NodeBootstrappingGetParams) (*operations.NodeBootstrappingGetOK, error) {
			cse := "test-cse-script"
			customData := "test-custom-data"
			return &operations.NodeBootstrappingGetOK{
				Payload: &models.NodeBootstrapping{
					Cse:        &cse,
					CustomData: &customData,
				},
			}, nil
		},
	}
}

// NodeBootstrappingGet implements the NodeBootstrappingGet operation
func (c *NodeBootstrappingClient) NodeBootstrappingGet(params *operations.NodeBootstrappingGetParams) (*operations.NodeBootstrappingGetOK, error) {
	c.Called = true
	if params.Parameters != nil && params.Parameters.ProvisionProfile != nil {
		c.CalledWithParams["nodeName"] = *params.Parameters.ProvisionProfile.Name
		// Extract nodePool from custom labels if present
		if params.Parameters.ProvisionProfile.CustomNodeLabels != nil {
			if nodePool, ok := params.Parameters.ProvisionProfile.CustomNodeLabels["karpenter.sh/nodepool"]; ok {
				c.CalledWithParams["nodePool"] = nodePool
			}
		}
	}

	// Instead of making an actual HTTP request, just return the mock response
	// This avoids the "no Host in request URL" error
	return c.GetFunc(params)
}

// SetTransport is required by the interface but not used in the fake
func (c *NodeBootstrappingClient) SetTransport(transport runtime.ClientTransport) {}
