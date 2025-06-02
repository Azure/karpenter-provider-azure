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
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	armopts "github.com/Azure/karpenter-provider-azure/pkg/utils/opts"
	"github.com/stretchr/testify/assert"
)

func Test_AddAuxiliaryTokenPolicyClientOptions(t *testing.T) {
	tests := []struct {
		name      string
		expected  azcore.AccessToken
		wantErr   bool
		errString string
		url       string
		scope     string
	}{
		{
			name:      "url is not set",
			wantErr:   true,
			errString: "access token server URL is not set",
			url:       "",
			scope:     "anything",
		},
		{
			name:      "scope is not set",
			wantErr:   true,
			errString: "access token scope is not set",
			url:       "anything",
			scope:     "",
		},
		{
			name:    "default",
			wantErr: false,
			url:     "http://test-url.com",
			scope:   "test-scope",
		},
	}
	tokenServer := &AuxiliaryTokenServer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientOpts := armopts.DefaultArmOpts()
			vmClientOpts := *clientOpts
			auxPolicy, err := auth.NewAuxiliaryTokenPolicy(context.Background(), tokenServer, tt.url, tt.scope)
			if (err != nil) != tt.wantErr {
				t.Errorf("getAuxiliaryToken() error = %v, wantErr: %v", err, tt.wantErr)
				return
			}
			vmClientOpts.ClientOptions.PerRetryPolicies = append(vmClientOpts.ClientOptions.PerRetryPolicies, auxPolicy)
			if tt.wantErr {
				assert.ErrorContains(t, err, tt.errString)
			} else {
				assert.NotEqual(t, clientOpts.ClientOptions.PerRetryPolicies, vmClientOpts.ClientOptions.PerRetryPolicies)
			}
		})
		tokenServer.Reset()
	}
}
