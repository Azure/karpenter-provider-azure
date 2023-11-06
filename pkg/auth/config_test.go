// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

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
				SubnetID:          "12345",
				SubnetName:        "my-subnet",
				VnetName:          "my-vnet",
				VMType:            "vmss",
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
				SubnetID:          "12345",
				SubnetName:        "my-subnet",
				VnetName:          "my-vnet",
				VMType:            "vm",
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
			name:     "bogus ARM_USE_MANAGED_IDENTITY_EXTENSION",
			expected: nil,
			wantErr:  true,
			env: map[string]string{
				"ARM_RESOURCE_GROUP":                 "my-rg",
				"ARM_SUBSCRIPTION_ID":                "12345",
				"AZURE_NODE_RESOURCE_GROUP":          "my-node-rg",
				"AZURE_SUBNET_ID":                    "12345",
				"AZURE_SUBNET_NAME":                  "my-subnet",
				"AZURE_VNET_NAME":                    "my-vnet",
				"ARM_USE_MANAGED_IDENTITY_EXTENSION": "foo", // this is not a supported value
			},
		},
		{
			name: "valid msi",
			expected: &Config{
				SubscriptionID:              "12345",
				ResourceGroup:               "my-rg",
				NodeResourceGroup:           "my-node-rg",
				SubnetID:                    "12345",
				SubnetName:                  "my-subnet",
				VnetName:                    "my-vnet",
				VMType:                      "vmss",
				UseManagedIdentityExtension: true,
				UserAssignedIdentityID:      "12345",
			},
			wantErr: false,
			env: map[string]string{
				"ARM_RESOURCE_GROUP":                 "my-rg",
				"ARM_SUBSCRIPTION_ID":                "12345",
				"AZURE_NODE_RESOURCE_GROUP":          "my-node-rg",
				"AZURE_SUBNET_ID":                    "12345",
				"AZURE_SUBNET_NAME":                  "my-subnet",
				"AZURE_VNET_NAME":                    "my-vnet",
				"ARM_USE_MANAGED_IDENTITY_EXTENSION": "true",
				"ARM_USER_ASSIGNED_IDENTITY_ID":      "12345",
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
