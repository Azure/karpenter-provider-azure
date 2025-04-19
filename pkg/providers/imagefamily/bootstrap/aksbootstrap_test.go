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

package bootstrap

import (
	"fmt"
	"testing"
)

func TestKubeBinaryURL(t *testing.T) {
	cases := []struct {
		name     string
		version  string
		expected string
	}{
		{
			name:     "Test version 1.24.x",
			version:  "1.24.5",
			expected: fmt.Sprintf("%s/kubernetes/v1.24.5/binaries/kubernetes-node-linux-amd64.tar.gz", globalAKSMirror),
		},
		{
			name:     "Test version 1.25.x",
			version:  "1.25.2",
			expected: fmt.Sprintf("%s/kubernetes/v1.25.2/binaries/kubernetes-node-linux-amd64.tar.gz", globalAKSMirror),
		},
		{
			name:     "Test version 1.26.x",
			version:  "1.26.0",
			expected: fmt.Sprintf("%s/kubernetes/v1.26.0/binaries/kubernetes-node-linux-amd64.tar.gz", globalAKSMirror),
		},
		{
			name:     "Test version 1.27.x",
			version:  "1.27.1",
			expected: fmt.Sprintf("%s/kubernetes/v1.27.1/binaries/kubernetes-node-linux-amd64.tar.gz", globalAKSMirror),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual := kubeBinaryURL(tc.version, "amd64")
			if actual != tc.expected {
				t.Errorf("Expected %s but got %s", tc.expected, actual)
			}
		})
	}
}

func TestGetCredentialProviderURL(t *testing.T) {
	tests := []struct {
		version string
		arch    string
		url     string
	}{
		{
			version: "1.32.0",
			arch:    "amd64",
			url:     fmt.Sprintf("%s/cloud-provider-azure/v1.32.3/binaries/azure-acr-credential-provider-linux-amd64-v1.32.3.tar.gz", globalAKSMirror),
		},
		{
			version: "1.31.0",
			arch:    "amd64",
			url:     fmt.Sprintf("%s/cloud-provider-azure/v1.31.4/binaries/azure-acr-credential-provider-linux-amd64-v1.31.4.tar.gz", globalAKSMirror),
		},
		{
			version: "1.30.2",
			arch:    "amd64",
			url:     fmt.Sprintf("%s/cloud-provider-azure/v1.30.10/binaries/azure-acr-credential-provider-linux-amd64-v1.30.10.tar.gz", globalAKSMirror),
		},
		{
			version: "1.30.0",
			arch:    "amd64",
			url:     fmt.Sprintf("%s/cloud-provider-azure/v1.30.10/binaries/azure-acr-credential-provider-linux-amd64-v1.30.10.tar.gz", globalAKSMirror),
		},
		{
			version: "1.30.0",
			arch:    "arm64",
			url:     fmt.Sprintf("%s/cloud-provider-azure/v1.30.10/binaries/azure-acr-credential-provider-linux-arm64-v1.30.10.tar.gz", globalAKSMirror),
		},
		{
			version: "1.29.2",
			arch:    "amd64",
			url:     "",
		},
		{
			version: "1.29.0",
			arch:    "amd64",
			url:     "",
		},
		{
			version: "1.29.0",
			arch:    "arm64",
			url:     "",
		},
		{
			version: "1.28.7",
			arch:    "amd64",
			url:     "",
		},
	}
	for _, tt := range tests {
		url := CredentialProviderURL(tt.version, tt.arch)
		if url != tt.url {
			t.Errorf("for version %s expected %s, got %s", tt.version, tt.url, url)
		}
	}
}
