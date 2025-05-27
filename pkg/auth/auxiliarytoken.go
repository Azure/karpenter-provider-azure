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

// AuxiliaryTokenPolicy provides a custom policy used to authenticate
// with shared node image galleries.
type AuxiliaryTokenPolicy struct {
	Token string
}

func (p *AuxiliaryTokenPolicy) Do(req *policy.Request) (*http.Response, error) {
	req.Raw().Header.Add("x-ms-authorization-auxiliary", "Bearer "+p.Token)
	return req.Next()
}

func GetAuxiliaryTokenPolicy(ctx context.Context, url string, scope string) (policy.Policy, error) {
	token, err := getAuxiliaryToken(url, scope)
	if err != nil {
		return nil, fmt.Errorf("failed to get auxiliary token: %w", err)
	}
	auxPolicy := AuxiliaryTokenPolicy{Token: token.Token}
	log.FromContext(ctx).V(1).Info("Will use auxiliary token policy for creating virtual machines")
	return &auxPolicy, nil
}

func getAuxiliaryToken(url string, scope string) (azcore.AccessToken, error) {
	if url == "" {
		return azcore.AccessToken{}, fmt.Errorf("access token server URL is not set")
	}
	if scope == "" {
		return azcore.AccessToken{}, fmt.Errorf("access token scope is not set")
	}

	// Construct the request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return azcore.AccessToken{}, err
	}
	q := req.URL.Query()
	q.Add("scope", scope)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("User-Agent", GetUserAgentExtension())

	// Send the request
	client := &http.Client{Timeout: 10 * time.Second}
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
