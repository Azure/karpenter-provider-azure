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

	"github.com/Azure/go-autorest/autorest"
)

type cfgField struct {
	val  string
	name string
}

// ClientConfig contains all essential information to create an Azure client.
type ClientConfig struct {
	CloudName               string
	Location                string
	SubscriptionID          string
	ResourceManagerEndpoint string
	Authorizer              autorest.Authorizer
	UserAgent               string
}

// Config holds the configuration parsed from the --cloud-config flag
type Config struct {
	Cloud                    string `json:"cloud" yaml:"cloud"`
	Location                 string `json:"location" yaml:"location"`
	TenantID                 string `json:"tenantId" yaml:"tenantId"`
	SubscriptionID           string `json:"subscriptionId" yaml:"subscriptionId"`
	ResourceGroup            string `json:"resourceGroup" yaml:"resourceGroup"`
	AzureEnvironmentFilepath string `json:"azureEnvironmentFilepath" yaml:"azureEnvironmentFilepath"`
}

// BuildAzureConfig returns a Config object for the Azure clients
func BuildAzureConfig() (*Config, error) {
	cfg := &Config{}

	if err := cfg.Build(); err != nil {
		return nil, err
	}
	if err := cfg.Default(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (cfg *Config) Build() error {
	// May require more than this behind the scenes: https://github.com/Azure/azure-sdk-for-go/blob/main/sdk/azidentity/README.md#defaultazurecredential
	cfg.Cloud = strings.TrimSpace(os.Getenv("ARM_CLOUD"))
	cfg.Location = strings.TrimSpace(os.Getenv("LOCATION"))
	cfg.ResourceGroup = strings.TrimSpace(os.Getenv("ARM_RESOURCE_GROUP"))
	cfg.TenantID = strings.TrimSpace(os.Getenv("ARM_TENANT_ID"))
	cfg.SubscriptionID = strings.TrimSpace(os.Getenv("ARM_SUBSCRIPTION_ID"))
	cfg.AzureEnvironmentFilepath = strings.TrimSpace(os.Getenv("AZURE_ENVIRONMENT_FILEPATH"))

	return nil
}

func (cfg *Config) Default() error {
	// Default is AzurePublicCloud if not set
	if cfg.Cloud == "" && cfg.AzureEnvironmentFilepath == "" {
		cfg.Cloud = "AzurePublicCloud"
	}

	return nil
}

func (cfg *Config) Validate() error {
	// Validate that ARM_CLOUD and AZURE_ENVIRONMENT_FILEPATH are not both set
	if cfg.Cloud != "" && cfg.AzureEnvironmentFilepath != "" {
		return fmt.Errorf("ARM_CLOUD and AZURE_ENVIRONMENT_FILEPATH cannot both be set - please use only one cloud configuration method")
	}

	// Setup fields and validate all of them are not empty
	fields := []cfgField{
		{cfg.SubscriptionID, "subscription ID"},
		// Even though the config doesnt use some of these,
		// its good to validate they were set in the environment
	}

	for _, field := range fields {
		if field.val == "" {
			return fmt.Errorf("%s not set", field.name)
		}
	}

	return nil
}

func (cfg *Config) String() string {
	json, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Sprintf("couldn't marshal Config JSON: %s", err)
	}

	return string(json)
}
