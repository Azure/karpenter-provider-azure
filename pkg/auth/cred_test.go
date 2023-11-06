// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

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
			name: "AAD client ID is MSI",
			cfg: &Config{
				AADClientID:            "msi",
				TenantID:               "00000000-0000-0000-0000-000000000000",
				UserAssignedIdentityID: "12345678-1234-1234-1234-123456789012",
			},
			want:    reflect.TypeOf(&azidentity.ManagedIdentityCredential{}),
			wantErr: false,
		},
		{
			name: "AAD client ID is using MSI extension",
			cfg: &Config{
				UseManagedIdentityExtension: true,
				AADClientID:                 "msi",
				TenantID:                    "00000000-0000-0000-0000-000000000000",
				UserAssignedIdentityID:      "12345678-1234-1234-1234-123456789012",
			},
			want:    reflect.TypeOf(&azidentity.ManagedIdentityCredential{}),
			wantErr: false,
		},
		{
			name: "AADClientID is not MSI",
			cfg: &Config{
				AADClientID:     "test-client-id",
				AADClientSecret: "test-client-secret",
				TenantID:        "00000000-0000-0000-0000-000000000000",
			},
			want:    reflect.TypeOf(&azidentity.ClientSecretCredential{}),
			wantErr: false,
		},
		{
			name: "AADClientID is not MSI and UserAssignedIdentityID is set",
			cfg: &Config{
				AADClientID:            "test-client-id",
				AADClientSecret:        "test-client-secret",
				TenantID:               "00000000-0000-0000-0000-000000000000",
				UserAssignedIdentityID: "12345678-1234-1234-1234-123456789012",
			},
			want:    reflect.TypeOf(&azidentity.ClientSecretCredential{}),
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
