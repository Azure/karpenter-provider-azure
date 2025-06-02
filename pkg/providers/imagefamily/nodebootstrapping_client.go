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
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/client"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/client/operations"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
	httptransport "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
)

type tokenCache struct {
	mu           sync.Mutex         // Mutex to ensure thread-safety
	token        azcore.AccessToken // The cached token
	refreshAfter time.Time          // Time after which we should refresh the token
}

// getToken returns a cached token if valid, otherwise fetches a new one using the provided credential.
// The method is thread-safe and can be called concurrently from multiple goroutines.
// Inspired by https://github.com/Azure/azure-sdk-for-go/blob/f72e2ad4f23b02eba6387dc31580c0e66333f2ae/sdk/internal/temporal/resource.go#L78-L140
//
// NOTE: as of this time, for managed identity, token caching exists in the implementation beneath GetToken(...):
// https://github.com/AzureAD/microsoft-authentication-library-for-go/blob/b4b8bfc9569042572ccb82b648ea509075fadb74/apps/managedidentity/managedidentity.go#L318
// However, this is never made clear in the interface of this layer nor its documentation, thus relying on that assumption may not be perfect, which is a reason why this layer of caching is still implemented.
// In addition, all azure-sdk-for-go clients also implement their caching as shown above, which means there are at least two layers of caching in most clients.
// And for the potential issue in https://github.com/Azure/karpenter-provider-azure/pull/391, this implementation, in parity with Azure clients, should also receive the fix from that PR (and beyond).
func (t *tokenCache) getToken(ctx context.Context, credential azcore.TokenCredential, scopes []string) (azcore.AccessToken, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if we have a cached token that is still valid
	if t.token.Token != "" && time.Now().Before(t.refreshAfter) {
		// Return the cached token if it's still valid
		return t.token, nil
	}

	// Token is expired or not present, get a new one
	tokenObj, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: scopes,
	})
	if err != nil {
		return azcore.AccessToken{}, fmt.Errorf("failed to get token: %w", err)
	}

	// Store the token with its expiration
	t.token = tokenObj

	// Inspired by https://github.com/Azure/azure-sdk-for-go/blob/f72e2ad4f23b02eba6387dc31580c0e66333f2ae/sdk/azcore/runtime/policy_bearer_token.go#L54-L61
	if tokenObj.RefreshOn.IsZero() {
		defaultRefreshBuffer := 5 * time.Minute
		t.refreshAfter = tokenObj.ExpiresOn.Add(-defaultRefreshBuffer)
	} else {
		// Use the RefreshOn time if provided, otherwise define our own buffer
		t.refreshAfter = tokenObj.RefreshOn
	}

	return t.token, nil
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
// It retrieves node bootstrapping data (CSE and base64-encoded CustomData), but omits the TLS bootstrap token.
func (c *NodeBootstrappingClient) Get(
	ctx context.Context,
	parameters *models.ProvisionValues,
) (string, string, error) {
	transport := httptransport.New(c.serverURL, "/", []string{"http"})

	// Add Authorization Bearer token header using cached token if available
	// This reduces the frequency of token acquisition calls, which can be expensive
	scopes := []string{"https://management.azure.com/.default"}
	token, err := c.tokenCache.getToken(ctx, c.credential, scopes)
	if err != nil {
		return "", "", fmt.Errorf("failed to get token: %w", err)
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
		return "", "", err
	}

	if resp.Payload == nil {
		return "", "", fmt.Errorf("no payload in response")
	}
	if resp.Payload.Cse == nil || *resp.Payload.Cse == "" {
		return "", "", fmt.Errorf("no CSE in response")
	}
	if resp.Payload.CustomData == nil || *resp.Payload.CustomData == "" {
		return "", "", fmt.Errorf("no CustomData in response")
	}

	return *resp.Payload.CustomData, *resp.Payload.Cse, nil
}
