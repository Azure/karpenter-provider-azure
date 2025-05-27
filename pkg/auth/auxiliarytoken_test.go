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
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

func TestGetAuxiliaryToken(t *testing.T) {
	tests := []struct {
		name     string
		expected azcore.AccessToken
		wantErr  bool
		url      string
		scope    string
	}{
		{
			name:    "url is not set",
			wantErr: true,
			url:     "",
			scope:   "anything",
		},
		{
			name:    "scope is not set",
			wantErr: true,
			url:     "anything",
			scope:   "",
		},
		// Uncomment this test when the server is mocked
		// {
		// 	name:    "default",
		// 	wantErr: false,
		// 	url:     "http://test-url.com",
		// 	scope:   "test-scope",
		// },
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// TODO: Mock the GetAuxiliaryToken function to return a valid token
			_, err := getAuxiliaryToken(tt.url, tt.scope)
			if (err != nil) != tt.wantErr {
				t.Errorf("getAuxiliaryToken() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}
