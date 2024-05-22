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
	"reflect"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

func TestNewCredential(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *Config
		want       reflect.Type
		wantErr    bool
		wantErrStr string
	}{
		{
			name:       "nil Config",
			cfg:        nil,
			want:       nil,
			wantErr:    true,
			wantErrStr: "failed to create credential, nil config provided",
		},
		{
			name: "unsupported auth method",
			cfg: &Config{
				AuthMethod: "unsupported",
			},
			want:       nil,
			wantErr:    true,
			wantErrStr: "cred: unsupported auth method: unsupported",
		},
		{
			name:       "empty auth method",
			cfg:        &Config{},
			want:       nil,
			wantErr:    true,
			wantErrStr: "cred: unsupported auth method: ",
		},
		{
			name: "auth method system-assigned-msi",
			cfg: &Config{
				AuthMethod: authMethodSysMSI,
			},
			want:    reflect.TypeOf(&azidentity.ManagedIdentityCredential{}),
			wantErr: false,
		},
		{
			name: "auth method credential-from-environment",
			cfg: &Config{
				AuthMethod: authMethodCredFromEnv,
			},
			want:    reflect.TypeOf(&azidentity.DefaultAzureCredential{}),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewCredential(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && err.Error() != tt.wantErrStr {
				t.Errorf("NewCredential() error = %v, wantErrStr %v", err, tt.wantErrStr)
				return
			}
			if reflect.TypeOf(got) != tt.want {
				t.Errorf("NewCredential() = %v, want %v", reflect.TypeOf(got), tt.want)
			}
		})
	}
}
