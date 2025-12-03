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
	"encoding/base64"
	"fmt"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
)

type NodeBootstrappingGetInput struct {
	Params *models.ProvisionValues
}

type NodeBootstrappingBehavior struct {
	NodeBootstrappingGetBehavior MockedFunction[NodeBootstrappingGetInput, types.NodeBootstrapping]
}

// NodeBootstrappingAPI implements a fake version of the imagefamily.types.NodeBootstrappingAPI
// for testing purposes.
type NodeBootstrappingAPI struct {
	NodeBootstrappingBehavior
	SimulateDown bool
}

// Ensure NodeBootstrappingAPI implements the types.NodeBootstrappingAPI interface
var _ types.NodeBootstrappingAPI = &NodeBootstrappingAPI{}

// Reset must be called between tests otherwise tests will pollute each other.
func (n *NodeBootstrappingAPI) Reset() {
	n.NodeBootstrappingGetBehavior.Reset()
}

// Get implements the NodeBootstrappingAPI interface for testing
func (n *NodeBootstrappingAPI) Get(ctx context.Context, params *models.ProvisionValues) (types.NodeBootstrapping, error) {
	input := &NodeBootstrappingGetInput{
		Params: params,
	}
	return n.NodeBootstrappingGetBehavior.Invoke(input, func(input *NodeBootstrappingGetInput) (types.NodeBootstrapping, error) {
		if n.SimulateDown {
			return types.NodeBootstrapping{}, fmt.Errorf("InternalServerError; NodeBootstrappingAPI is down")
		}
		if err := validateProvisionProfile(input.Params.ProvisionProfile); err != nil {
			return types.NodeBootstrapping{}, fmt.Errorf("MissingRequiredProperty; ConvertProvisionProfile failed with error: %s", err.Error())
		}

		if err := validateProvisionHelperValues(input.Params.ProvisionHelperValues); err != nil {
			return types.NodeBootstrapping{}, fmt.Errorf("MissingRequiredProperty; ConvertProvisionProfile failed with error: %s", err.Error())
		}

		return types.NodeBootstrapping{
			CSEDehydratable:               fmt.Sprintf("CORRECT_CSE_WITH_OMITTED_TLS_BOOTSTRAP_TOKEN_{{.TokenID}}.{{.TokenSecret}}: %v", *input.Params),
			CustomDataEncodedDehydratable: base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("CORRECT_CUSTOM_DATA_WITH_OMITTED_TLS_BOOTSTRAP_TOKEN_{{.TokenID}}.{{.TokenSecret}}: %v", *input.Params))),
		}, nil
	})
}

// nolint: gocyclo
func validateProvisionProfile(p *models.ProvisionProfile) error {
	if p == nil {
		return fmt.Errorf("ProvisionProfile cannot be empty")
	}

	if p.Name == nil {
		return fmt.Errorf("missing field: Name cannot be empty")
	}

	if p.VMSize == nil {
		return fmt.Errorf("missing field: VMSize cannot be empty")
	}

	if p.OsType == nil {
		return fmt.Errorf("missing field: OsType cannot be empty")
	}

	if p.OsSku == nil {
		return fmt.Errorf("missing field: OsSku cannot be empty")
	}

	if p.StorageProfile == nil {
		return fmt.Errorf("missing field: StorageProfile cannot be empty")
	}

	if p.Distro == nil {
		return fmt.Errorf("missing field: Distro cannot be empty")
	}

	if p.OrchestratorVersion == nil {
		return fmt.Errorf("missing field: OrchestratorVersion cannot be empty")
	}

	if p.VnetCidrs == nil {
		return fmt.Errorf("missing field: VnetCidrs cannot be empty")
	}

	if p.VnetSubnetID == nil {
		return fmt.Errorf("missing field: VnetSubnetID cannot be empty")
	}

	if p.Mode == nil {
		return fmt.Errorf("missing field: Mode cannot be empty")
	}

	if p.Architecture == nil {
		return fmt.Errorf("missing field: Architecture cannot be empty")
	}

	if p.MaxPods == nil {
		return fmt.Errorf("missing field: MaxPods cannot be empty")
	}

	return nil
}

func validateProvisionHelperValues(p *models.ProvisionHelperValues) error {
	if p == nil {
		return fmt.Errorf("ProvisionHelperValues cannot be empty")
	}

	if p.SkuCPU == nil {
		return fmt.Errorf("missing field: SkuCPU cannot be empty")
	}

	if p.SkuMemory == nil {
		return fmt.Errorf("missing field: SkuMemory cannot be empty")
	}

	return nil
}
