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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
)

func TestReadEnvironmentFromFile(t *testing.T) {
	// Create a valid environment for successful test cases
	validEnv := &azclient.Environment{
		Name:                    "AzureStackCloud",
		ResourceManagerEndpoint: "https://management.azurestack.local/",
		ActiveDirectoryEndpoint: "https://login.microsoftonline.com/",
		TokenAudience:           "https://management.azurestack.local/",
		GraphEndpoint:           "https://graph.azurestack.local/",
	}

	tests := []struct {
		name           string
		setupFile      func(t *testing.T) string
		expectedEnv    *azclient.Environment
		wantErr        bool
		expectedErrMsg string
	}{
		{
			name: "valid environment file",
			setupFile: func(t *testing.T) string {
				tmpDir := t.TempDir()
				envFile := filepath.Join(tmpDir, "environment.json")
				envData, err := json.Marshal(validEnv)
				if err != nil {
					t.Fatalf("Failed to marshal environment: %v", err)
				}
				if err := os.WriteFile(envFile, envData, 0600); err != nil {
					t.Fatalf("Failed to write environment file: %v", err)
				}
				return envFile
			},
			expectedEnv: validEnv,
			wantErr:     false,
		},
		{
			name: "empty path",
			setupFile: func(t *testing.T) string {
				return ""
			},
			expectedEnv:    nil,
			wantErr:        true,
			expectedErrMsg: "path is empty",
		},
		{
			name: "relative path",
			setupFile: func(t *testing.T) string {
				return "relative/path.json"
			},
			expectedEnv:    nil,
			wantErr:        true,
			expectedErrMsg: "path must be absolute",
		},
		{
			name: "non-existent file",
			setupFile: func(t *testing.T) string {
				return "/non/existent/file.json"
			},
			expectedEnv:    nil,
			wantErr:        true,
			expectedErrMsg: "failed to read environment file",
		},
		{
			name: "invalid JSON",
			setupFile: func(t *testing.T) string {
				tmpDir := t.TempDir()
				envFile := filepath.Join(tmpDir, "invalid.json")
				if err := os.WriteFile(envFile, []byte("invalid json content"), 0600); err != nil {
					t.Fatalf("Failed to write invalid JSON file: %v", err)
				}
				return envFile
			},
			expectedEnv:    nil,
			wantErr:        true,
			expectedErrMsg: "failed to parse environment file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			filepath := tt.setupFile(t)
			env, err := readEnvironmentFromFile(filepath)

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				if tt.expectedErrMsg != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.expectedErrMsg))
				}
				g.Expect(env).To(BeNil())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(env).ToNot(BeNil())
				g.Expect(env.Name).To(Equal(tt.expectedEnv.Name))
				g.Expect(env.ResourceManagerEndpoint).To(Equal(tt.expectedEnv.ResourceManagerEndpoint))
				g.Expect(env.ActiveDirectoryEndpoint).To(Equal(tt.expectedEnv.ActiveDirectoryEndpoint))
				g.Expect(env.TokenAudience).To(Equal(tt.expectedEnv.TokenAudience))
				g.Expect(env.GraphEndpoint).To(Equal(tt.expectedEnv.GraphEndpoint))
			}
		})
	}
}

func TestMapTrack1ToTrack2Environment(t *testing.T) {
	tests := []struct {
		name                    string
		env                     *azclient.Environment
		wantErr                 bool
		expectedErrMsg          string
		expectedActiveDirectory string
		expectedRMEndpoint      string
		expectedRMAudience      string
		shouldHaveGraph         bool
	}{
		{
			name: "valid environment mapping",
			env: &azclient.Environment{
				Name:                    "AzureStackCloud",
				ResourceManagerEndpoint: "https://management.azurestack.local/",
				ActiveDirectoryEndpoint: "https://login.microsoftonline.com/",
				TokenAudience:           "https://management.azurestack.local/",
			},
			wantErr:                 false,
			expectedActiveDirectory: "https://login.microsoftonline.com/",
			expectedRMEndpoint:      "https://management.azurestack.local/",
			expectedRMAudience:      "https://management.azurestack.local/",
		},
		{
			name:           "nil environment",
			env:            nil,
			wantErr:        true,
			expectedErrMsg: "environment cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			config, err := mapTrack1ToTrack2Environment(tt.env)

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				if tt.expectedErrMsg != "" {
					g.Expect(err.Error()).To(Equal(tt.expectedErrMsg))
				}
				g.Expect(config).To(Equal(cloud.Configuration{}))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(config.ActiveDirectoryAuthorityHost).To(Equal(tt.expectedActiveDirectory))

				rmService, exists := config.Services[cloud.ResourceManager]
				g.Expect(exists).To(BeTrue(), "ResourceManager service should exist in configuration")
				g.Expect(rmService.Endpoint).To(Equal(tt.expectedRMEndpoint))
				g.Expect(rmService.Audience).To(Equal(tt.expectedRMAudience))
			}
		})
	}
}

func TestResolveCloudEnvironment(t *testing.T) {
	// Create a valid environment for file-based test
	validEnv := &azclient.Environment{
		Name:                    "AzureStackCloud",
		ResourceManagerEndpoint: "https://management.azurestack.local/",
		ActiveDirectoryEndpoint: "https://login.microsoftonline.com/",
		TokenAudience:           "https://management.azurestack.local/",
	}

	tests := []struct {
		name           string
		setupConfig    func(t *testing.T) *Config
		expectedName   string
		wantErr        bool
		expectedErrMsg string
	}{
		{
			name: "known cloud resolution",
			setupConfig: func(t *testing.T) *Config {
				return &Config{
					Cloud: "AzurePublicCloud",
				}
			},
			expectedName: "AzurePublicCloud",
			wantErr:      false,
		},
		{
			name: "file-based environment resolution",
			setupConfig: func(t *testing.T) *Config {
				tmpDir := t.TempDir()
				envFile := filepath.Join(tmpDir, "environment.json")
				envData, err := json.Marshal(validEnv)
				if err != nil {
					t.Fatalf("Failed to marshal environment: %v", err)
				}
				if err := os.WriteFile(envFile, envData, 0600); err != nil {
					t.Fatalf("Failed to write environment file: %v", err)
				}
				return &Config{
					AzureEnvironmentFilepath: envFile,
				}
			},
			expectedName: "AzureStackCloud",
			wantErr:      false,
		},
		{
			name: "default cloud resolution",
			setupConfig: func(t *testing.T) *Config {
				return &Config{
					// No Cloud or AzureEnvironmentFilepath set
				}
			},
			expectedName: "AzurePublicCloud",
			wantErr:      false,
		},
		{
			name: "unknown cloud name",
			setupConfig: func(t *testing.T) *Config {
				return &Config{
					Cloud: "UnknownCloud",
				}
			},
			expectedName:   "",
			wantErr:        true,
			expectedErrMsg: "unknown cloud name: UNKNOWNCLOUD",
		},
		{
			name: "invalid environment file",
			setupConfig: func(t *testing.T) *Config {
				return &Config{
					AzureEnvironmentFilepath: "/non/existent/file.json",
				}
			},
			expectedName:   "",
			wantErr:        true,
			expectedErrMsg: "failed to read environment from file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			cfg := tt.setupConfig(t)
			env, err := ResolveCloudEnvironment(cfg)

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				if tt.expectedErrMsg != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.expectedErrMsg))
				}
				g.Expect(env).To(BeNil())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(env.Environment).ToNot(BeNil())
				g.Expect(env.Environment.Name).To(Equal(tt.expectedName))
				g.Expect(env.Cloud.ActiveDirectoryAuthorityHost).ToNot(BeEmpty())

				// Verify ResourceManager service is configured
				rmService, exists := env.Cloud.Services[cloud.ResourceManager]
				g.Expect(exists).To(BeTrue(), "ResourceManager service should exist")
				g.Expect(rmService.Endpoint).ToNot(BeEmpty())
				g.Expect(rmService.Audience).ToNot(BeEmpty())
			}
		})
	}
}

func TestValidateEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		env     *azclient.Environment
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid environment with all required fields",
			env: &azclient.Environment{
				Name:                    "AzureStackCloud",
				ResourceManagerEndpoint: "https://management.azurestack.local/",
				ActiveDirectoryEndpoint: "https://login.microsoftonline.com/",
				TokenAudience:           "https://management.azurestack.local/",
				GraphEndpoint:           "https://graph.azurestack.local/",
			},
			wantErr: false,
		},
		{
			name: "missing resource manager endpoint",
			env: &azclient.Environment{
				Name:                    "AzureStackCloud",
				ActiveDirectoryEndpoint: "https://login.microsoftonline.com/",
				TokenAudience:           "https://management.azurestack.local/",
				GraphEndpoint:           "https://graph.azurestack.local/",
			},
			wantErr: true,
			errMsg:  "resource manager endpoint is required",
		},
		{
			name: "missing active directory endpoint",
			env: &azclient.Environment{
				Name:                    "AzureStackCloud",
				ResourceManagerEndpoint: "https://management.azurestack.local/",
				TokenAudience:           "https://management.azurestack.local/",
				GraphEndpoint:           "https://graph.azurestack.local/",
			},
			wantErr: true,
			errMsg:  "active directory endpoint is required",
		},
		{
			name: "missing token audience",
			env: &azclient.Environment{
				Name:                    "AzureStackCloud",
				ResourceManagerEndpoint: "https://management.azurestack.local/",
				ActiveDirectoryEndpoint: "https://login.microsoftonline.com/",
				GraphEndpoint:           "https://graph.azurestack.local/",
			},
			wantErr: true,
			errMsg:  "token audience is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			err := validateEnvironment(tt.env)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				if tt.errMsg != "" {
					g.Expect(err.Error()).To(Equal(tt.errMsg))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
