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

	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure"
)

const (
	// auth methods
	authMethodSysMSI           = "system-assigned-msi"
	authMethodWorkloadIdentity = "workload-identity"
)

const (
	// from azure_manager
	vmTypeVMSS = "vmss"
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
	VMType         string `json:"vmType" yaml:"vmType"`

	// AuthMethod determines how to authorize requests for the Azure cloud.
	// Valid options are "system-assigned-msi" and "workload-identity"
	// The default is "workload-identity".
	AuthMethod string `json:"authMethod" yaml:"authMethod"`

	// Managed identity for Kubelet (not to be confused with Azure cloud authorization)
	KubeletIdentityClientID string `json:"kubeletIdentityClientID" yaml:"kubeletIdentityClientID"`

	// Configs only for AKS
	ClusterName       string `json:"clusterName" yaml:"clusterName"`
	NodeResourceGroup string `json:"nodeResourceGroup" yaml:"nodeResourceGroup"`
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

func (cfg *Config) GetAzureClientConfig(authorizer autorest.Authorizer, env *azure.Environment) *ClientConfig {
	azClientConfig := &ClientConfig{
		Location:                cfg.Location,
		SubscriptionID:          cfg.SubscriptionID,
		ResourceManagerEndpoint: env.ResourceManagerEndpoint,
		Authorizer:              authorizer,
	}

	return azClientConfig
}

func (cfg *Config) Build() error {
	cfg.Cloud = strings.TrimSpace(os.Getenv("ARM_CLOUD"))
	cfg.Location = strings.TrimSpace(os.Getenv("LOCATION"))
	cfg.ResourceGroup = strings.TrimSpace(os.Getenv("ARM_RESOURCE_GROUP"))
	cfg.TenantID = strings.TrimSpace(os.Getenv("ARM_TENANT_ID"))
	cfg.SubscriptionID = strings.TrimSpace(os.Getenv("ARM_SUBSCRIPTION_ID"))
	cfg.VMType = strings.ToLower(os.Getenv("ARM_VM_TYPE"))
	cfg.ClusterName = strings.TrimSpace(os.Getenv("AZURE_CLUSTER_NAME"))
	cfg.NodeResourceGroup = strings.TrimSpace(os.Getenv("AZURE_NODE_RESOURCE_GROUP"))
	cfg.AuthMethod = strings.TrimSpace(os.Getenv("ARM_AUTH_METHOD"))
	cfg.KubeletIdentityClientID = strings.TrimSpace(os.Getenv("ARM_KUBELET_IDENTITY_CLIENT_ID"))

	return nil
}

func (cfg *Config) Default() error {
	// Defaulting vmType to vmss.
	if cfg.VMType == "" {
		cfg.VMType = vmTypeVMSS
	}

	if cfg.AuthMethod == "" {
		cfg.AuthMethod = authMethodWorkloadIdentity
	}

	return nil
}

func (cfg *Config) Validate() error {
	// Setup fields and validate all of them are not empty
	fields := []cfgField{
		{cfg.SubscriptionID, "subscription ID"},
		{cfg.NodeResourceGroup, "node resource group"},
		{cfg.VMType, "VM type"},
		// Even though the config doesnt use some of these,
		// its good to validate they were set in the environment
	}

	for _, field := range fields {
		if field.val == "" {
			return fmt.Errorf("%s not set", field.name)
		}
	}

	if cfg.AuthMethod != authMethodSysMSI && cfg.AuthMethod != authMethodWorkloadIdentity {
		return fmt.Errorf("unsupported authorization method: %s", cfg.AuthMethod)
	}

	return nil
}
