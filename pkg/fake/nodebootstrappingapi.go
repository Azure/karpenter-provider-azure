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
	"github.com/samber/lo"
)

// NodeBootstrappingAPI implements a fake version of the imagefamily.types.NodeBootstrappingAPI
// for testing purposes.
type NodeBootstrappingAPI struct {
	SimulateDown                   bool
	SimulateSecureTLSBootstrapping bool
}

// Ensure NodeBootstrappingAPI implements the types.NodeBootstrappingAPI interface
var _ types.NodeBootstrappingAPI = &NodeBootstrappingAPI{}

// Get implements the NodeBootstrappingAPI interface for testing
func (n *NodeBootstrappingAPI) Get(ctx context.Context, params *models.ProvisionValues) (types.NodeBootstrapping, error) {
	if n.SimulateDown {
		return types.NodeBootstrapping{}, fmt.Errorf("InternalServerError; NodeBootstrappingAPI is down")
	}
	if err := validateProvisionProfile(params.ProvisionProfile); err != nil {
		return types.NodeBootstrapping{}, fmt.Errorf("MissingRequiredProperty; ConvertProvisionProfile failed with error: %s", err.Error())
	}

	if err := validateProvisionHelperValues(params.ProvisionHelperValues); err != nil {
		return types.NodeBootstrapping{}, fmt.Errorf("MissingRequiredProperty; ConvertProvisionProfile failed with error: %s", err.Error())
	}

	// assume that server-side enablement of secure TLS bootstrapping won't provide a placelolder for the token
	tokenPart := lo.Ternary(n.SimulateSecureTLSBootstrapping,
		"NO_TLS_BOOTSTRAP_TOKEN",                                    // without token placeholder
		"OMITTED_TLS_BOOTSTRAP_TOKEN_{{.TokenID}}.{{.TokenSecret}}", // with token placeholder
	)

	return types.NodeBootstrapping{
		CSEDehydratable:               fmt.Sprintf("CORRECT_CSE_WITH_%s: %v", tokenPart, *params),
		CustomDataEncodedDehydratable: base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("CORRECT_CUSTOM_DATA_WITH_%s: %v", tokenPart, *params))),
	}, nil
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
