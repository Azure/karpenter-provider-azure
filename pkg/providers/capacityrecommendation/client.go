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

package capacityrecommendation

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armrecommender"
)

const skuMixPlacementAPIVersion = "2026-05-05-preview"

// SKUMixPlacementCapacityLimit describes the currently available capacity for a SKU, priority, and zone.
type SKUMixPlacementCapacityLimit struct {
	Limit    *int32                                  `json:"limit,omitempty"`
	Name     *string                                 `json:"name,omitempty"`
	Priority *armrecommender.SKUMixPlacementPriority `json:"priority,omitempty"`
	Reason   *string                                 `json:"reason,omitempty"`
	Zone     *string                                 `json:"zone,omitempty"`
}

// SKUMixPlacementResponse includes fields returned by the service that are not yet available in armrecommender.
type SKUMixPlacementResponse struct {
	CapacityLimits           []*SKUMixPlacementCapacityLimit                         `json:"capacityLimits,omitempty"`
	ID                       *string                                                 `json:"id,omitempty"`
	PartialFulfillmentReason *armrecommender.SKUMixPlacementPartialFulfillmentReason `json:"partialFulfillmentReason,omitempty"`
	PlacementChoices         []*armrecommender.SKUMixPlacementDeploymentChoice       `json:"placementChoices,omitempty"`
	ValidUntil               *time.Time                                              `json:"validUntil,omitempty"`
}

// SKUMixPlacementScoresClientPostResponse contains the response from SKUMixPlacementScoresClient.Post.
type SKUMixPlacementScoresClientPostResponse struct {
	SKUMixPlacementResponse
}

// SKUMixPlacementScoresClient temporarily supplements armrecommender until its response model includes capacityLimits.
type SKUMixPlacementScoresClient struct {
	internal       *arm.Client
	subscriptionID string
}

// NewSKUMixPlacementScoresClient creates a client using the standard Azure Resource Manager pipeline.
func NewSKUMixPlacementScoresClient(subscriptionID string, credential azcore.TokenCredential, options *arm.ClientOptions) (*SKUMixPlacementScoresClient, error) {
	client, err := arm.NewClient("github.com/Azure/karpenter-provider-azure", "v0.0.0", credential, options)
	if err != nil {
		return nil, err
	}
	return &SKUMixPlacementScoresClient{
		internal:       client,
		subscriptionID: subscriptionID,
	}, nil
}

// Post generates placement scores for VM SKU mix placement.
func (client *SKUMixPlacementScoresClient) Post(ctx context.Context, location string, request armrecommender.SKUMixPlacementRequest, _ *armrecommender.SKUMixPlacementScoresClientPostOptions) (result SKUMixPlacementScoresClientPostResponse, err error) {
	const operationName = "SKUMixPlacementScoresClient.Post"
	ctx = context.WithValue(ctx, runtime.CtxAPINameKey{}, operationName)
	ctx, endSpan := runtime.StartSpan(ctx, operationName, client.internal.Tracer(), nil)
	defer func() { endSpan(err) }()

	req, err := client.newPostRequest(ctx, location, request)
	if err != nil {
		return SKUMixPlacementScoresClientPostResponse{}, err
	}
	response, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return SKUMixPlacementScoresClientPostResponse{}, err
	}
	if !runtime.HasStatusCode(response, http.StatusOK) {
		err = runtime.NewResponseError(response)
		return SKUMixPlacementScoresClientPostResponse{}, err
	}
	result = SKUMixPlacementScoresClientPostResponse{}
	if err = runtime.UnmarshalAsJSON(response, &result.SKUMixPlacementResponse); err != nil {
		return SKUMixPlacementScoresClientPostResponse{}, err
	}
	return result, nil
}

func (client *SKUMixPlacementScoresClient) newPostRequest(ctx context.Context, location string, body armrecommender.SKUMixPlacementRequest) (*policy.Request, error) {
	if client.subscriptionID == "" {
		return nil, errors.New("subscription ID cannot be empty")
	}
	if location == "" {
		return nil, errors.New("location cannot be empty")
	}
	path := "/subscriptions/" + url.PathEscape(client.subscriptionID) +
		"/providers/Microsoft.Compute/locations/" + url.PathEscape(location) +
		"/skuMixPlacementScores/recommendations/generate"
	req, err := runtime.NewRequest(ctx, http.MethodPost, runtime.JoinPaths(client.internal.Endpoint(), path))
	if err != nil {
		return nil, err
	}
	query := req.Raw().URL.Query()
	query.Set("api-version", skuMixPlacementAPIVersion)
	req.Raw().URL.RawQuery = strings.ReplaceAll(query.Encode(), "+", "%20")
	req.Raw().Header.Set("Accept", "application/json")
	req.Raw().Header.Set("Content-Type", "application/json")
	if err = runtime.MarshalAsJSON(req, body); err != nil {
		return nil, err
	}
	return req, nil
}
