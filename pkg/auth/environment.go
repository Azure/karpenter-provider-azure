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
	"fmt"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
)

type Environment struct {
	// Environment is the track 1 representation of the Azure environment
	Environment *azclient.Environment
	// Cloud is the track 2 representation of the Azure environment
	Cloud cloud.Configuration
}

// readEnvironmentFromFile reads environment configuration from a JSON file
func readEnvironmentFromFile(filepath string) (*azclient.Environment, error) {
	if filepath == "" {
		return nil, fmt.Errorf("filepath is empty")
	}
	if !strings.HasPrefix(filepath, "/") {
		return nil, fmt.Errorf("filepath must be absolute: %s", filepath)
	}

	// Read file content
	content, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read environment file %s: %w", filepath, err)
	}

	// Parse JSON content into azclient.Environment
	var env azclient.Environment
	if err := json.Unmarshal(content, &env); err != nil {
		return nil, fmt.Errorf("failed to parse environment file %s as JSON: %w", filepath, err)
	}

	// Validate required fields
	if err := validateEnvironment(&env); err != nil {
		return nil, fmt.Errorf("invalid environment configuration in file %s: %w", filepath, err)
	}

	return &env, nil
}

// validateEnvironment validates that required fields are present in the environment
func validateEnvironment(env *azclient.Environment) error {
	if env.ResourceManagerEndpoint == "" {
		return fmt.Errorf("resource manager endpoint is required")
	}
	if env.ActiveDirectoryEndpoint == "" {
		return fmt.Errorf("active directory endpoint is required")
	}
	if env.TokenAudience == "" {
		return fmt.Errorf("token audience is required")
	}
	return nil
}

// mapTrack1ToTrack2Environment converts azclient.Environment (Track1 format) to cloud.Configuration (Track2 format)
func mapTrack1ToTrack2Environment(env *azclient.Environment) (cloud.Configuration, error) {
	if env == nil {
		return cloud.Configuration{}, fmt.Errorf("environment cannot be nil")
	}

	config := cloud.Configuration{
		ActiveDirectoryAuthorityHost: env.ActiveDirectoryEndpoint,
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: env.ResourceManagerEndpoint,
				Audience: env.TokenAudience,
			},
		},
	}

	return config, nil
}

// environmentFromName returns a Track1-style environment from a cloud name.
// This is very similar to https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/cloud.go#L361
// but returns an error rather than defaulting to PublicCloud if the user provides an unknown cloud name.
func environmentFromName(cloudName string) (*azclient.Environment, error) {
	cloudName = strings.ToUpper(strings.TrimSpace(cloudName))
	if cloudConfig, ok := azclient.EnvironmentMapping[cloudName]; ok {
		return cloudConfig, nil
	}
	return nil, fmt.Errorf("unknown cloud name: %s", cloudName)
}

func EnvironmentFromName(cloudName string) (*Environment, error) {
	env, err := environmentFromName(cloudName)
	if err != nil {
		return nil, err
	}

	cloudConfig, err := mapTrack1ToTrack2Environment(env)
	if err != nil {
		return nil, fmt.Errorf("failed to map known cloud %s to Track2 format: %w", cloudName, err)
	}

	return &Environment{
		Environment: env,
		Cloud:       cloudConfig,
	}, nil
}

// ResolveCloudEnvironment resolves the cloud environment using the following precedence:
// 1. File-based environment (AZURE_ENVIRONMENT_FILEPATH)
// 2. Known cloud names (ARM_CLOUD)
// 3. Default (Azure Public Cloud)
func ResolveCloudEnvironment(cfg *Config) (*Environment, error) {
	// 1. Try file-based environment first (highest precedence)
	if cfg.AzureEnvironmentFilepath != "" {
		env, err := readEnvironmentFromFile(cfg.AzureEnvironmentFilepath)
		if err != nil {
			return nil, fmt.Errorf("failed to read environment from file: %w", err)
		}

		cloudConfig, err := mapTrack1ToTrack2Environment(env)
		if err != nil {
			return nil, fmt.Errorf("failed to map environment to Track2 format: %w", err)
		}

		return &Environment{
			Environment: env,
			Cloud:       cloudConfig,
		}, nil
	}

	// 2. Try known cloud names (ARM_CLOUD)
	if cfg.Cloud != "" {
		return EnvironmentFromName(cfg.Cloud)
	}

	// 3. Default to Azure Public Cloud
	return EnvironmentFromName("AzurePublicCloud")
}
