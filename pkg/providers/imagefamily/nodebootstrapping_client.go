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

package imagefamily

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Azure/aks-middleware/http/client/direct/restlogger"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	types "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/client"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/client/operations"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
	httptransport "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
)

// NodeBootstrappingClient implements the NodeBootstrappingAPI interface using the swagger-generated client.
type NodeBootstrappingClient struct {
	serverURL         string
	subscriptionID    string
	resourceGroupName string
	resourceName      string
	credential        azcore.TokenCredential
}

// NewNodeBootstrappingClient creates a new NodeBootstrappingClient with token caching enabled.
func NewNodeBootstrappingClient(ctx context.Context, subscriptionID string, resourceGroupName string, resourceName string, serverURL string) (*NodeBootstrappingClient, error) {
	return &NodeBootstrappingClient{
		serverURL:         serverURL,
		subscriptionID:    subscriptionID,
		resourceGroupName: resourceGroupName,
		resourceName:      resourceName,
	}, nil
}

// Get implements the NodeBootstrappingAPI interface.
// It retrieves node bootstrapping data (CSE and base64-encoded CustomData), but may omit the TLS bootstrap token.
func (c *NodeBootstrappingClient) Get(
	ctx context.Context,
	parameters *models.ProvisionValues,
) (types.NodeBootstrapping, error) {
	transport := httptransport.New(c.serverURL, "/", []string{"http"})

	// Middleware logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	loggingClient := restlogger.NewLoggingClient(logger)
	transport.Transport = loggingClient.Transport

	// Create the client
	client := client.New(transport, strfmt.Default)

	// Prepare the parameters for the request
	params := operations.NewNodeBootstrappingGetParams()
	params.ResourceGroupName = c.resourceGroupName
	params.ResourceName = c.resourceName
	params.SubscriptionID = c.subscriptionID
	params.Parameters = parameters

	params.WithTimeout(30 * time.Second)
	params.Context = ctx

	resp, err := client.Operations.NodeBootstrappingGet(params)
	if err != nil {
		return types.NodeBootstrapping{}, err
	}

	if resp.Payload == nil {
		return types.NodeBootstrapping{}, fmt.Errorf("no payload in response")
	}
	if resp.Payload.Cse == nil || *resp.Payload.Cse == "" {
		return types.NodeBootstrapping{}, fmt.Errorf("no CSE in response")
	}
	if resp.Payload.CustomData == nil || *resp.Payload.CustomData == "" {
		return types.NodeBootstrapping{}, fmt.Errorf("no CustomData in response")
	}

	return types.NodeBootstrapping{
		CustomDataEncodedDehydratable: *resp.Payload.CustomData,
		CSEDehydratable:               *resp.Payload.Cse,
	}, nil
}
