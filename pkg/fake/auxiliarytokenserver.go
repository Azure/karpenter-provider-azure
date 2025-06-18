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
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
)

type AuxiliaryTokenDoInput struct {
	request *http.Request
}

type AuxiliaryTokenBehavior struct {
	AuxiliaryTokenDoBehavior MockedFunction[AuxiliaryTokenDoInput, *http.Response]
}

// assert that the fake implements the interface
var _ auth.AuxiliaryTokenServer = &AuxiliaryTokenServer{}

type AuxiliaryTokenServer struct {
	AuxiliaryTokenBehavior
}

// Reset must be called between tests otherwise tests will pollute each other.
func (c *AuxiliaryTokenServer) Reset() {
	c.AuxiliaryTokenDoBehavior.Reset()
}

func (c *AuxiliaryTokenServer) Do(req *http.Request) (*http.Response, error) {
	input := &AuxiliaryTokenDoInput{
		request: req,
	}
	return c.AuxiliaryTokenDoBehavior.Invoke(input, func(input *AuxiliaryTokenDoInput) (*http.Response, error) {
		// init response writer
		resp := &http.Response{}
		resp.Header = http.Header{"Content-Type": []string{"application/json"}}

		if input.request.UserAgent() != auth.GetUserAgentExtension() {
			resp.StatusCode = http.StatusUnauthorized
			return resp, nil
		}

		token := azcore.AccessToken{
			Token:     "fake-token",
			ExpiresOn: time.Now().Add(1 * time.Hour),
			RefreshOn: time.Now().Add(5 * time.Second), // Set suggested refresh time for use in acceptance tests
		}
		tokenBytes, _ := json.Marshal(token)
		resp.StatusCode = http.StatusOK
		resp.Body = io.NopCloser(bytes.NewReader(tokenBytes))
		return resp, nil
	})
}
