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
	"os"
	"reflect"
	"testing"
)

func TestBuildAzureConfig(t *testing.T) {
	tests := []struct {
		name     string
		expected *Config
		wantErr  bool
		env      map[string]string
	}{
		{
			name:     "required env vars not present",
			expected: nil,
			wantErr:  true,
		},
		{
			name: "default",
			expected: &Config{
				SubscriptionID:    "12345",
				ResourceGroup:     "my-rg",
				NodeResourceGroup: "my-node-rg",
				VMType:            "vmss",
				AuthMethod:        "credential-from-environment",
			},
			wantErr: false,
			env: map[string]string{
				"ARM_RESOURCE_GROUP":        "my-rg",
				"ARM_SUBSCRIPTION_ID":       "12345",
				"AZURE_NODE_RESOURCE_GROUP": "my-node-rg",
				"AZURE_SUBNET_ID":           "12345",
				"AZURE_SUBNET_NAME":         "my-subnet",
				"AZURE_VNET_NAME":           "my-vnet",
			},
		},
		{
			name: "vmType=vm", // tests setVMType()
			expected: &Config{
				SubscriptionID:    "12345",
				ResourceGroup:     "my-rg",
				NodeResourceGroup: "my-node-rg",
				VMType:            "vm",
				AuthMethod:        "credential-from-environment",
			},
			wantErr: false,
			env: map[string]string{
				"ARM_RESOURCE_GROUP":        "my-rg",
				"ARM_SUBSCRIPTION_ID":       "12345",
				"AZURE_NODE_RESOURCE_GROUP": "my-node-rg",
				"AZURE_SUBNET_ID":           "12345",
				"AZURE_SUBNET_NAME":         "my-subnet",
				"AZURE_VNET_NAME":           "my-vnet",
				"ARM_VM_TYPE":               "vm",
			},
		},
		{
			name:     "bogus ARM_AUTH_METHOD",
			expected: nil,
			wantErr:  true,
			env: map[string]string{
				"ARM_RESOURCE_GROUP":        "my-rg",
				"ARM_SUBSCRIPTION_ID":       "12345",
				"AZURE_NODE_RESOURCE_GROUP": "my-node-rg",
				"AZURE_SUBNET_ID":           "12345",
				"AZURE_SUBNET_NAME":         "my-subnet",
				"AZURE_VNET_NAME":           "my-vnet",
				"ARM_AUTH_METHOD":           "foo", // this is not a supported value
			},
		},
		{
			name: "auth method msi",
			expected: &Config{
				SubscriptionID:    "12345",
				ResourceGroup:     "my-rg",
				NodeResourceGroup: "my-node-rg",
				VMType:            "vmss",
				AuthMethod:        "system-assigned-msi",
			},
			wantErr: false,
			env: map[string]string{
				"ARM_RESOURCE_GROUP":        "my-rg",
				"ARM_SUBSCRIPTION_ID":       "12345",
				"AZURE_NODE_RESOURCE_GROUP": "my-node-rg",
				"AZURE_SUBNET_ID":           "12345",
				"AZURE_SUBNET_NAME":         "my-subnet",
				"AZURE_VNET_NAME":           "my-vnet",
				"ARM_AUTH_METHOD":           "system-assigned-msi",
			},
		},
		{
			name: "auth method credential from environment",
			expected: &Config{
				SubscriptionID:    "12345",
				ResourceGroup:     "my-rg",
				NodeResourceGroup: "my-node-rg",
				VMType:            "vmss",
				AuthMethod:        "credential-from-environment",
			},
			wantErr: false,
			env: map[string]string{
				"ARM_RESOURCE_GROUP":        "my-rg",
				"ARM_SUBSCRIPTION_ID":       "12345",
				"AZURE_NODE_RESOURCE_GROUP": "my-node-rg",
				"AZURE_SUBNET_ID":           "12345",
				"AZURE_SUBNET_NAME":         "my-subnet",
				"AZURE_VNET_NAME":           "my-vnet",
				"ARM_AUTH_METHOD":           "credential-from-environment",
			},
		},
		{
			name: "valid kubelet identity",
			expected: &Config{
				SubscriptionID:          "12345",
				ResourceGroup:           "my-rg",
				NodeResourceGroup:       "my-node-rg",
				VMType:                  "vmss",
				AuthMethod:              "system-assigned-msi",
				KubeletIdentityClientID: "11111111-2222-3333-4444-555555555555",
			},
			wantErr: false,
			env: map[string]string{
				"ARM_RESOURCE_GROUP":             "my-rg",
				"ARM_SUBSCRIPTION_ID":            "12345",
				"AZURE_NODE_RESOURCE_GROUP":      "my-node-rg",
				"AZURE_SUBNET_ID":                "12345",
				"AZURE_SUBNET_NAME":              "my-subnet",
				"AZURE_VNET_NAME":                "my-vnet",
				"ARM_AUTH_METHOD":                "system-assigned-msi",
				"ARM_KUBELET_IDENTITY_CLIENT_ID": "11111111-2222-3333-4444-555555555555",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				err := os.Setenv(k, v)
				if err != nil {
					t.Errorf("error setting environment %v = %s", tt.env, err)
					return
				}
			}
			got, err := BuildAzureConfig()
			if (err != nil) != tt.wantErr {
				t.Errorf("BuildAzureConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if equal := reflect.DeepEqual(got, tt.expected); !equal {
				t.Errorf("BuildAzureConfig() = %v, want %v", got, tt.expected)
			}
			for k := range tt.env {
				err := os.Unsetenv(k)
				if err != nil {
					t.Errorf("error unsetting environment %v = %s", tt.env, err)
					return
				}
			}
		})
	}
}
