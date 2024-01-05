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
	"strconv"
	"strings"

	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure"
)

const (
	// auth methods
	authMethodPrincipal = "principal"
	authMethodCLI       = "cli"
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

	// AuthMethod determines how to authorize requests for the Azure
	// cloud. Valid options are "principal" (= the traditional
	// service principle approach) and "cli" (= load az command line
	// config file). The default is "principal".
	AuthMethod string `json:"authMethod" yaml:"authMethod"`

	// Settings for a service principal.
	AADClientID                 string `json:"aadClientId" yaml:"aadClientId"`
	AADClientSecret             string `json:"aadClientSecret" yaml:"aadClientSecret"`
	AADClientCertPath           string `json:"aadClientCertPath" yaml:"aadClientCertPath"`
	AADClientCertPassword       string `json:"aadClientCertPassword" yaml:"aadClientCertPassword"`
	UseNewCredWorkflow          bool   `json:"useNewCredWorkflow" yaml:"useNewCredWorkflow"`
	UseManagedIdentityExtension bool   `json:"useManagedIdentityExtension" yaml:"useManagedIdentityExtension"`
	UserAssignedIdentityID      string `json:"userAssignedIdentityID" yaml:"userAssignedIdentityID"`

	//Configs only for AKS
	ClusterName string `json:"clusterName" yaml:"clusterName"`
	//Config only for AKS
	NodeResourceGroup string `json:"nodeResourceGroup" yaml:"nodeResourceGroup"`
	//SubnetId is the resource ID of the subnet that VM network interfaces should use
	SubnetID   string `json:"subnetId" yaml:"subnetId"`
	VnetName   string `json:"vnetName" yaml:"vnetName"`
	SubnetName string `json:"subnetName" yaml:"subnetName"`
}

func (cfg *Config) PrepareConfig() error {
	cfg.BaseVars()
	err := cfg.prepareID()
	if err != nil {
		return err
	}
	return nil
}

func (cfg *Config) BaseVars() {
	cfg.Cloud = os.Getenv("ARM_CLOUD")
	cfg.Location = os.Getenv("LOCATION")
	cfg.ResourceGroup = os.Getenv("ARM_RESOURCE_GROUP")
	cfg.TenantID = os.Getenv("ARM_TENANT_ID")
	cfg.SubscriptionID = os.Getenv("ARM_SUBSCRIPTION_ID")
	cfg.AADClientID = os.Getenv("ARM_CLIENT_ID")
	cfg.AADClientSecret = os.Getenv("ARM_CLIENT_SECRET")
	cfg.VMType = strings.ToLower(os.Getenv("ARM_VM_TYPE"))
	cfg.AADClientCertPath = os.Getenv("ARM_CLIENT_CERT_PATH")
	cfg.AADClientCertPassword = os.Getenv("ARM_CLIENT_CERT_PASSWORD")
	cfg.ClusterName = os.Getenv("AZURE_CLUSTER_NAME")
	cfg.NodeResourceGroup = os.Getenv("AZURE_NODE_RESOURCE_GROUP")
	cfg.SubnetID = os.Getenv("AZURE_SUBNET_ID")
	cfg.SubnetName = os.Getenv("AZURE_SUBNET_NAME")
	cfg.VnetName = os.Getenv("AZURE_VNET_NAME")
	// cfg.VnetGuid = os.Getenv("AZURE_VNET_GUID") // This field needs to be resolved inside of karpenter, so we will get it in the azClient initialization
}

func (cfg *Config) prepareID() error {
	useNewCredWorkflowFromEnv := os.Getenv("ARM_USE_NEW_CRED_WORKFLOW")
	if len(useNewCredWorkflowFromEnv) > 0 {
		shouldUse, err := strconv.ParseBool(useNewCredWorkflowFromEnv)
		if err != nil {
			return err
		}
		cfg.UseNewCredWorkflow = shouldUse
	}
	useManagedIdentityExtensionFromEnv := os.Getenv("ARM_USE_MANAGED_IDENTITY_EXTENSION")
	if len(useManagedIdentityExtensionFromEnv) > 0 {
		shouldUse, err := strconv.ParseBool(useManagedIdentityExtensionFromEnv)
		if err != nil {
			return err
		}
		cfg.UseManagedIdentityExtension = shouldUse
	}
	userAssignedIdentityIDFromEnv := os.Getenv("ARM_USER_ASSIGNED_IDENTITY_ID")
	if userAssignedIdentityIDFromEnv != "" {
		cfg.UserAssignedIdentityID = userAssignedIdentityIDFromEnv
	}
	return nil
}

// BuildAzureConfig returns a Config object for the Azure clients
func BuildAzureConfig() (*Config, error) {
	var err error
	cfg := &Config{}
	err = cfg.PrepareConfig()
	if err != nil {
		return nil, err
	}
	cfg.TrimSpace()
	setVMType(cfg)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func setVMType(cfg *Config) {
	// Defaulting vmType to vmss.
	if cfg.VMType == "" {
		cfg.VMType = vmTypeVMSS
	}
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

// TrimSpace removes all leading and trailing white spaces.
func (cfg *Config) TrimSpace() {
	cfg.Cloud = strings.TrimSpace(cfg.Cloud)
	cfg.TenantID = strings.TrimSpace(cfg.TenantID)
	cfg.SubscriptionID = strings.TrimSpace(cfg.SubscriptionID)
	cfg.ResourceGroup = strings.TrimSpace(cfg.ResourceGroup)
	cfg.VMType = strings.TrimSpace(cfg.VMType)
	cfg.AADClientID = strings.TrimSpace(cfg.AADClientID)
	cfg.AADClientSecret = strings.TrimSpace(cfg.AADClientSecret)
	cfg.AADClientCertPath = strings.TrimSpace(cfg.AADClientCertPath)
	cfg.AADClientCertPassword = strings.TrimSpace(cfg.AADClientCertPassword)
	cfg.ClusterName = strings.TrimSpace(cfg.ClusterName)
	cfg.NodeResourceGroup = strings.TrimSpace(cfg.NodeResourceGroup)
	cfg.SubnetID = strings.TrimSpace(cfg.SubnetID)
	cfg.SubnetName = strings.TrimSpace(cfg.SubnetName)
	cfg.VnetName = strings.TrimSpace(cfg.VnetName)
}

func (cfg *Config) validate() error {
	// Setup fields and validate all of them are not empty
	fields := []cfgField{
		{cfg.SubscriptionID, "subscription ID"},
		{cfg.NodeResourceGroup, "node resource group"},
		{cfg.VMType, "VM type"},
		// Even though the config doesnt use some of these,
		// its good to validate they were set in the environment
		{cfg.SubnetID, "subnet ID"},
		{cfg.SubnetName, "subnet name"},
		{cfg.VnetName, "vnet name"},
	}

	for _, field := range fields {
		if field.val == "" {
			return fmt.Errorf("%s not set", field.name)
		}
	}

	if cfg.UseManagedIdentityExtension {
		return nil
	}

	if cfg.AuthMethod != "" && cfg.AuthMethod != authMethodPrincipal && cfg.AuthMethod != authMethodCLI {
		return fmt.Errorf("unsupported authorization method: %s", cfg.AuthMethod)
	}

	return nil
}
