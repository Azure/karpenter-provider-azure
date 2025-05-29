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
	"fmt"
	"os"
	"strings"

	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"

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
	Cloud          string `json:"cloud" yaml:"cloud"`
	Location       string `json:"location" yaml:"location"`
	TenantID       string `json:"tenantId" yaml:"tenantId"`
	SubscriptionID string `json:"subscriptionId" yaml:"subscriptionId"`
	ResourceGroup  string `json:"resourceGroup" yaml:"resourceGroup"`
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

func (cfg *Config) GetAzureClientConfig(authorizer autorest.Authorizer, env *azclient.Environment) *ClientConfig {
	azClientConfig := &ClientConfig{
		Location:                cfg.Location,
		SubscriptionID:          cfg.SubscriptionID,
		ResourceManagerEndpoint: env.ResourceManagerEndpoint,
		Authorizer:              authorizer,
		UserAgent:               GetUserAgentExtension(),
	}

	return azClientConfig
}

func (cfg *Config) Build() error {
	// May require more than this behind the scenes: https://github.com/Azure/azure-sdk-for-go/blob/main/sdk/azidentity/README.md#defaultazurecredential
	cfg.Cloud = strings.TrimSpace(os.Getenv("ARM_CLOUD"))
	cfg.Location = strings.TrimSpace(os.Getenv("LOCATION"))
	cfg.ResourceGroup = strings.TrimSpace(os.Getenv("ARM_RESOURCE_GROUP"))
	cfg.TenantID = strings.TrimSpace(os.Getenv("ARM_TENANT_ID"))
	cfg.SubscriptionID = strings.TrimSpace(os.Getenv("ARM_SUBSCRIPTION_ID"))

	return nil
}

func (cfg *Config) Default() error {
	// Nothing to default, for now.
	return nil
}

func (cfg *Config) Validate() error {
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
