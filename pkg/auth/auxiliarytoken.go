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
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type AuxiliaryTokenServer interface {
	Do(req *http.Request) (*http.Response, error)
}

var _ policy.Policy = &AuxiliaryTokenPolicy{}

// AuxiliaryTokenPolicy provides a custom policy used to authenticate
// with shared node image galleries.
type AuxiliaryTokenPolicy struct {
	Token  azcore.AccessToken
	url    string
	scope  string
	client AuxiliaryTokenServer
	lock   sync.Mutex
}

func (p *AuxiliaryTokenPolicy) GetAuxiliaryToken() error {
	p.lock.Lock()
	defer p.lock.Unlock()
	// If the token is uninitialized or close to expiration, fetch a new one
	currentTime := time.Now()
	if p.Token.ExpiresOn.IsZero() || p.Token.RefreshOn.Before(currentTime) || p.Token.ExpiresOn.Before(currentTime.Add(5*time.Minute)) {
		newToken, err := getAuxiliaryToken(p.client, p.url, p.scope)
		if err != nil {
			return err
		}
		p.Token = newToken
	}
	return nil
}

func (p *AuxiliaryTokenPolicy) Do(req *policy.Request) (*http.Response, error) {
	err := p.GetAuxiliaryToken()
	if err != nil {
		log.FromContext(req.Raw().Context()).Error(err, "Failed to get auxiliary token")
		return nil, err
	}
	req.Raw().Header.Add("x-ms-authorization-auxiliary", "Bearer "+p.Token.Token)
	return req.Next()
}

func NewAuxiliaryTokenPolicy(client AuxiliaryTokenServer, url string, scope string) *AuxiliaryTokenPolicy {
	auxPolicy := AuxiliaryTokenPolicy{
		Token:  azcore.AccessToken{},
		url:    url,
		scope:  scope,
		client: client,
		lock:   sync.Mutex{},
	}
	return &auxPolicy
}

func getAuxiliaryToken(client AuxiliaryTokenServer, url string, scope string) (azcore.AccessToken, error) {
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
