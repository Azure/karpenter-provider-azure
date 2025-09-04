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
	"path/filepath"
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

// readEnvironmentFromFile reads environment configuration from a JSON file.
// The expected file format is the same one that CloudProvider expects:
// https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/cloud.go#L153.
// This also happens to be the original Track1 SDK format.
func readEnvironmentFromFile(path string) (*azclient.Environment, error) {
	if path == "" {
		return nil, fmt.Errorf("path is empty")
	}

	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("path must be absolute: %s", path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read environment file %s: %w", path, err)
	}

	var env azclient.Environment
	if err := json.Unmarshal(content, &env); err != nil {
		return nil, fmt.Errorf("failed to parse environment file %s as JSON: %w", path, err)
	}

	if err := validateEnvironment(&env); err != nil {
		return nil, fmt.Errorf("invalid environment configuration in file %s: %w", path, err)
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

// mapTrack1ToTrack2Environment converts azclient.Environment (Track1 and CloudProvider format, written to all nodes automatically by AKS)
// to cloud.Configuration (Track2 format).
// This is similar to what CloudProvider does here: https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/cloud.go#L121
// but we don't use that as it couples loading the file and mapping track1 to track2, in addition to allowing partial overrides
// which is less than ideal.
// TODO: We could move this upstream to azure-sdk-for-go-extensions or refactor how CloudProvider parses and share that.
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

// IsPublic returns if the specified configuration is public.
// This takes the track2 format rather than being a method on Environment because
// usage in api/sdk contexts use the track2 format and may not have access to the
// auth.Environment struct.
func IsPublic(env cloud.Configuration) bool {
	endpointA := strings.TrimRight(env.Services[cloud.ResourceManager].Endpoint, "/")
	endpointB := strings.TrimRight(cloud.AzurePublic.Services[cloud.ResourceManager].Endpoint, "/")
	return strings.EqualFold(endpointA, endpointB) // Shouldn't differ by case but let's be safe
}

// ResolveCloudEnvironment resolves the cloud environment using the following precedence:
// 1. File-based environment (AZURE_ENVIRONMENT_FILEPATH)
// 2. Known cloud names (ARM_CLOUD)
// 3. Default (Azure Public Cloud)
func ResolveCloudEnvironment(cfg *Config) (*Environment, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cfg is nil")
	}

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

	// 3. Default to Azure Public Cloud -- this code shouldn't be hit regularly
	// as we already default cfg.Cloud to AzurePublicCloud in the Config.Default method.
	return EnvironmentFromName("AzurePublicCloud")
}
