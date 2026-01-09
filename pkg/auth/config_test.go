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
	"testing"

	. "github.com/onsi/gomega"
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
				Cloud:          "AzurePublicCloud",
				SubscriptionID: "12345",
				ResourceGroup:  "my-rg",
			},
			wantErr: false,
			env: map[string]string{
				"ARM_RESOURCE_GROUP":  "my-rg",
				"ARM_SUBSCRIPTION_ID": "12345",
				"AZURE_SUBNET_ID":     "12345",
				"AZURE_SUBNET_NAME":   "my-subnet",
				"AZURE_VNET_NAME":     "my-vnet",
			},
		},
		{
			name: "vmType=vm", // tests setVMType()
			expected: &Config{
				Cloud:          "AzurePublicCloud",
				SubscriptionID: "12345",
				ResourceGroup:  "my-rg",
			},
			wantErr: false,
			env: map[string]string{
				"ARM_RESOURCE_GROUP":  "my-rg",
				"ARM_SUBSCRIPTION_ID": "12345",
				"AZURE_SUBNET_ID":     "12345",
				"AZURE_SUBNET_NAME":   "my-subnet",
				"AZURE_VNET_NAME":     "my-vnet",
				"ARM_VM_TYPE":         "vm",
			},
		},
		{
			name:     "both ARM_CLOUD and AZURE_ENVIRONMENT_FILEPATH set",
			expected: nil,
			wantErr:  true,
			env: map[string]string{
				"ARM_RESOURCE_GROUP":         "my-rg",
				"ARM_SUBSCRIPTION_ID":        "12345",
				"AZURE_SUBNET_ID":            "12345",
				"AZURE_SUBNET_NAME":          "my-subnet",
				"AZURE_VNET_NAME":            "my-vnet",
				"ARM_CLOUD":                  "AzurePublicCloud",
				"AZURE_ENVIRONMENT_FILEPATH": "/etc/kubernetes/AzureStackCloud.json",
			},
		},
		{
			name: "only ARM_CLOUD set",
			expected: &Config{
				SubscriptionID: "12345",
				ResourceGroup:  "my-rg",
				Cloud:          "AzurePublicCloud",
			},
			wantErr: false,
			env: map[string]string{
				"ARM_RESOURCE_GROUP":  "my-rg",
				"ARM_SUBSCRIPTION_ID": "12345",
				"AZURE_SUBNET_ID":     "12345",
				"AZURE_SUBNET_NAME":   "my-subnet",
				"AZURE_VNET_NAME":     "my-vnet",
				"ARM_CLOUD":           "AzurePublicCloud",
			},
		},
		{
			name: "only AZURE_ENVIRONMENT_FILEPATH set",
			expected: &Config{
				SubscriptionID:           "12345",
				ResourceGroup:            "my-rg",
				AzureEnvironmentFilepath: "/etc/kubernetes/AzureStackCloud.json",
			},
			wantErr: false,
			env: map[string]string{
				"ARM_RESOURCE_GROUP":         "my-rg",
				"ARM_SUBSCRIPTION_ID":        "12345",
				"AZURE_SUBNET_ID":            "12345",
				"AZURE_SUBNET_NAME":          "my-subnet",
				"AZURE_VNET_NAME":            "my-vnet",
				"AZURE_ENVIRONMENT_FILEPATH": "/etc/kubernetes/AzureStackCloud.json",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			for k, v := range tt.env {
				err := os.Setenv(k, v)
				g.Expect(err).ToNot(HaveOccurred(), "error setting environment variable %s = %s", k, v)
			}

			got, err := BuildAzureConfig()

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(got).To(BeNil())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(got).To(Equal(tt.expected))
			}

			for k := range tt.env {
				err := os.Unsetenv(k)
				g.Expect(err).ToNot(HaveOccurred(), "error unsetting environment variable %s", k)
			}
		})
	}
}
