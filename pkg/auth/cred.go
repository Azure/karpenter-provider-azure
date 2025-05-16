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

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// expireEarlyTokenCredential is a wrapper around the azcore.TokenCredential that
// returns an earlier ExpiresOn timestamp to avoid conditions like clockSkew, or a race
// condition during polling.
// See: https://github.com/hashicorp/terraform-provider-azurerm/issues/20834 for more details
type expireEarlyTokenCredential struct {
	cred azcore.TokenCredential
}

func GetAuxiliaryToken(ctx context.Context, url string, scope string) (azcore.AccessToken, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	if url == "" {
		return azcore.AccessToken{}, fmt.Errorf("access token server URL is not set")
	}
	if scope == "" {
		return azcore.AccessToken{}, fmt.Errorf("access token scope is not set")
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return azcore.AccessToken{}, err
	}
	q := req.URL.Query()
	q.Add("scope", scope)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("User-Agent", GetUserAgentExtension())

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		return azcore.AccessToken{}, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return azcore.AccessToken{}, fmt.Errorf("error: %s", resp.Status)
	}

	// Decode the response body into the AccessToken struct
	var token azcore.AccessToken
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return azcore.AccessToken{}, fmt.Errorf("error decoding json: %w", err)
	}
	return token, nil
}

func NewTokenWrapper(cred azcore.TokenCredential) azcore.TokenCredential {
	return &expireEarlyTokenCredential{
		cred: cred,
	}
}

func (w *expireEarlyTokenCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	token, err := w.cred.GetToken(ctx, options)
	if err != nil {
		return azcore.AccessToken{}, err
	}

	twoHoursFromNow := time.Now().Add(2 * time.Hour)
	// IMDS may have the MI token already, and have an expiration of less than 2h when we receive the token. We don't want to set that value beyond the ExpiresOn time and potentially miss a refresh
	// So we just return earlier here. See discussion here: https://github.com/Azure/karpenter-provider-azure/pull/391/files#r1648633051
	if token.ExpiresOn.Before(twoHoursFromNow) {
		return token, nil
	}
	log.FromContext(ctx).V(1).Info("adjusting token ExpiresOn")
	// If the token expires in more than 2 hours, this means we are taking in a new token with a fresh 24h expiration time or one already in the cache that hasn't been modified by us, so we want to set that to two hours so
	// we can refresh it early to avoid the polling bugs mentioned in the above issue
	token.ExpiresOn = twoHoursFromNow
	return token, nil
}
