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

package customscripts

import (
	"context"

	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
)

// NodeBootstrappingAPI defines the interface for retrieving node bootstrapping data
type NodeBootstrappingAPI interface {
	Get(ctx context.Context, parameters *models.ProvisionValues) (NodeBootstrapping, error)
}

type NodeBootstrapping struct {
	// CustomDataEncodedDehydratable is the base64 encoded custom data, which might contains template strings for TLS bootstrap token in the format of `{{.TokenID}}.{{.TokenSecret}}`
	// It is to be used in VM creation
	CustomDataEncodedDehydratable string
	// CSEDehydratable is CSE script, which might contains template strings for TLS bootstrap token in the format of `{{.TokenID}}.{{.TokenSecret}}`
	// It is to be used in VM CSE creation
	CSEDehydratable string
}
