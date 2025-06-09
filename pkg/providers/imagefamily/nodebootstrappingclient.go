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
	"sync"
	"time"

	"github.com/Azure/aks-middleware/http/client/direct/restlogger"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	types "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/client"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/client/operations"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
	httptransport "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
)

// This simple, short-lived caching is mainly to prevent excessive token fetches on a large scale up, rather than guaranteeing the optimal number of calls and 100% valid token.
//
// As of this time, for managed identity, token caching exists in the implementation beneath MSAL GetToken(...) as below. This means our caching is not needed if MSAL GetToken(...) caching is valid.
// https://github.com/AzureAD/microsoft-authentication-library-for-go/blob/b4b8bfc9569042572ccb82b648ea509075fadb74/apps/managedidentity/managedidentity.go#L318
// However, this is never made clear in the interface of this layer nor its documentation, thus relying on that assumption may not be perfect.
// Alternatively, we could try to pair our cache implementation with what's in Azure clients as below. However, the cost makes it arguably not worth it, given the current circumstances.
// https://github.com/Azure/azure-sdk-for-go/blob/f72e2ad4f23b02eba6387dc31580c0e66333f2ae/sdk/internal/temporal/resource.go#L78-L140
type tokenCache struct {
	mu          sync.Mutex         // Mutex to ensure thread-safety
	token       azcore.AccessToken // The cached token
	refreshTime time.Time          // Time after which we should refresh the token
}

func (t *tokenCache) getTokenAtTime(ctx context.Context, credential azcore.TokenCredential, curTime time.Time) (azcore.AccessToken, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if we have a cached token that is still valid
	if t.token.Token != "" && curTime.Before(t.refreshTime) {
		return t.token, nil
	}

	// Token is expired or not present, get a new one
	tokenObj, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return azcore.AccessToken{}, fmt.Errorf("failed to get token: %w", err)
	}

	// Store the token with its expiration
	t.token = tokenObj

	// Ensure TTL is <= 1 minute
	defaultRefreshTime := curTime.Add(1 * time.Minute)
	if !tokenObj.RefreshOn.IsZero() && tokenObj.RefreshOn.Before(defaultRefreshTime) {
		t.refreshTime = tokenObj.RefreshOn
	} else if tokenObj.ExpiresOn.Before(defaultRefreshTime) {
		t.refreshTime = tokenObj.ExpiresOn
	} else {
		t.refreshTime = defaultRefreshTime
	}

	return t.token, nil
}

func (t *tokenCache) getToken(ctx context.Context, credential azcore.TokenCredential) (azcore.AccessToken, error) {
	return t.getTokenAtTime(ctx, credential, time.Now())
}

// NodeBootstrappingClient implements the NodeBootstrappingAPI interface using the swagger-generated client.
type NodeBootstrappingClient struct {
	serverURL         string
	subscriptionID    string
	resourceGroupName string
	resourceName      string
	credential        azcore.TokenCredential
	tokenCache        *tokenCache // Cache for Azure AD tokens to improve performance
}

// NewNodeBootstrappingClient creates a new NodeBootstrappingClient with token caching enabled.
func NewNodeBootstrappingClient(ctx context.Context, subscriptionID string, resourceGroupName string, resourceName string, credential azcore.TokenCredential, serverURL string) (*NodeBootstrappingClient, error) {
	return &NodeBootstrappingClient{
		serverURL:         serverURL,
		subscriptionID:    subscriptionID,
		resourceGroupName: resourceGroupName,
		resourceName:      resourceName,
		credential:        credential,
		tokenCache:        &tokenCache{},
	}, nil
}

// Get implements the NodeBootstrappingAPI interface.
// It retrieves node bootstrapping data (CSE and base64-encoded CustomData), but may omit the TLS bootstrap token.
func (c *NodeBootstrappingClient) Get(
	ctx context.Context,
	parameters *models.ProvisionValues,
) (types.NodeBootstrapping, error) {
	transport := httptransport.New(c.serverURL, "/", []string{"http"})

	// Add Authorization Bearer token header using cached token if available
	token, err := c.tokenCache.getToken(ctx, c.credential)
	if err != nil {
		return types.NodeBootstrapping{}, fmt.Errorf("failed to get token: %w", err)
	}
	transport.DefaultAuthentication = httptransport.BearerToken(token.Token)

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
