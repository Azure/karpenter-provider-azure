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

	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
)

// TODO
type NodeBootstrappingAPI struct {
	// Customize these fields to mock specific responses
	CustomData string
	CSE        string
}

var _ imagefamily.NodeBootstrappingAPI = &NodeBootstrappingAPI{}

func (n NodeBootstrappingAPI) Get(_ context.Context, _ *models.ProvisionValues) (string, string, error) {
	return n.CustomData, n.CSE, nil
}
