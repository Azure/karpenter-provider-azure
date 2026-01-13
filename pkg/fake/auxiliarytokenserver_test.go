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
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	. "github.com/onsi/gomega"
)

func Test_AddAuxiliaryTokenPolicyClientOptions(t *testing.T) {
	defaultToken := azcore.AccessToken{
		Token:     "test-token",
		ExpiresOn: time.Now().Add(1 * time.Hour),
		RefreshOn: time.Now().Add(5 * time.Second),
	}
	tests := []struct {
		name       string
		userAgent  string
		statusCode int
	}{
		{
			name:       "default",
			userAgent:  auth.GetUserAgentExtension(),
			statusCode: http.StatusOK,
		},
		{
			name:       "wrong user agent",
			userAgent:  "wrong-user-agent",
			statusCode: http.StatusUnauthorized,
		},
	}
	tokenServer := &AuxiliaryTokenServer{Token: defaultToken}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			request := &http.Request{
				Method: http.MethodGet,
				URL:    &url.URL{Path: "/"},
				Header: http.Header{
					"User-Agent": []string{tt.userAgent},
				},
			}
			resp, err := tokenServer.Do(request)
			if err != nil {
				t.Errorf("Unexpected error %v", err)
				return
			}
			g.Expect(resp.StatusCode).To(Equal(tt.statusCode), "Expected status code %d, got %d", tt.statusCode, resp.StatusCode)
			tokenServer.Reset()
		})
	}
}
