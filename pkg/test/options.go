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

package test

import (
	"fmt"

	"github.com/imdario/mergo"
	"github.com/samber/lo"

	azoptions "github.com/Azure/karpenter-provider-azure/pkg/operator/options"
)

type OptionsFields struct {
	ClusterName                    *string
	ClusterEndpoint                *string
	ClusterID                      *string
	KubeletClientTLSBootstrapToken *string
	LinuxAdminUsername             *string
	SSHPublicKey                   *string
	NetworkPlugin                  *string
	NetworkPluginMode              *string
	NetworkPolicy                  *string
	NetworkDataplane               *string
	VMMemoryOverheadPercent        *float64
	NodeIdentities                 []string
	SubnetID                       *string
	NodeResourceGroup              *string
	ProvisionMode                  *string
	NodeBootstrappingServerURL     *string
	VnetGUID                       *string
	KubeletIdentityClientID        *string
	AdditionalTags                 map[string]string
	EnableAzureSDKLogging          *bool
	DiskEncryptionSetID            *string
	ClusterDNSServiceIP            *string
	ManageExistingAKSMachines      *bool
	AKSMachinesPoolName            *string

	// SIG Flags not required by the self hosted offering
	UseSIG                  *bool
	SIGAccessTokenServerURL *string
	SIGSubscriptionID       *string
}

func Options(overrides ...OptionsFields) *azoptions.Options {
	options := OptionsFields{}
	for _, override := range overrides {
		if err := mergo.Merge(&options, override, mergo.WithOverride); err != nil {
			panic(fmt.Sprintf("Failed to merge settings: %s", err))
		}
	}
	return &azoptions.Options{
		ClusterName:                    lo.FromPtrOr(options.ClusterName, "test-cluster"),
		ClusterEndpoint:                lo.FromPtrOr(options.ClusterEndpoint, "https://test-cluster"),
		ClusterID:                      lo.FromPtrOr(options.ClusterID, "00000000"),
		KubeletClientTLSBootstrapToken: lo.FromPtrOr(options.KubeletClientTLSBootstrapToken, "test-token"),
		KubeletIdentityClientID:        lo.FromPtrOr(options.KubeletIdentityClientID, "61f71907-753f-4802-a901-47361c3664f2"),
		SSHPublicKey:                   lo.FromPtrOr(options.SSHPublicKey, "test-ssh-public-key"),
		LinuxAdminUsername:             lo.FromPtrOr(options.LinuxAdminUsername, "azureuser"),
		NetworkPlugin:                  lo.FromPtrOr(options.NetworkPlugin, "azure"),
		NetworkPluginMode:              lo.FromPtrOr(options.NetworkPluginMode, "overlay"),
		NetworkPolicy:                  lo.FromPtrOr(options.NetworkPolicy, "cilium"),
		VnetGUID:                       lo.FromPtrOr(options.VnetGUID, "a519e60a-cac0-40b2-b883-084477fe6f5c"),
		NetworkDataplane:               lo.FromPtrOr(options.NetworkDataplane, "cilium"),
		VMMemoryOverheadPercent:        lo.FromPtrOr(options.VMMemoryOverheadPercent, 0.075),
		NodeIdentities:                 options.NodeIdentities,
		SubnetID:                       lo.FromPtrOr(options.SubnetID, "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/aks-vnet-12345678/subnets/aks-subnet"),
		NodeResourceGroup:              lo.FromPtrOr(options.NodeResourceGroup, "test-resourceGroup"),
		ProvisionMode:                  lo.FromPtrOr(options.ProvisionMode, "aksscriptless"),
		NodeBootstrappingServerURL:     lo.FromPtrOr(options.NodeBootstrappingServerURL, ""),
		EnableAzureSDKLogging:          lo.FromPtrOr(options.EnableAzureSDKLogging, true),
		UseSIG:                         lo.FromPtrOr(options.UseSIG, false),
		SIGSubscriptionID:              lo.FromPtrOr(options.SIGSubscriptionID, "12345678-1234-1234-1234-123456789012"),
		SIGAccessTokenServerURL:        lo.FromPtrOr(options.SIGAccessTokenServerURL, "https://test-sig-access-token-server.com"),
		AdditionalTags:                 options.AdditionalTags,
		DiskEncryptionSetID:            lo.FromPtrOr(options.DiskEncryptionSetID, ""),
		DNSServiceIP:                   lo.FromPtrOr(options.ClusterDNSServiceIP, ""),
		ManageExistingAKSMachines:      lo.FromPtrOr(options.ManageExistingAKSMachines, true),
		AKSMachinesPoolName:            lo.FromPtrOr(options.AKSMachinesPoolName, "aksmanagedap"),
	}
}
