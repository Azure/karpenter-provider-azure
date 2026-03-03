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

	"dario.cat/mergo"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/samber/lo"
)

// InterfaceOptions customizes an Azure Network Interface for testing.
type InterfaceOptions struct {
	Name         string
	NodepoolName string
	Location     string
	Properties   *armnetwork.InterfacePropertiesFormat
	Tags         map[string]*string
}

// Interface creates a test Azure Network Interface with defaults that can be overridden by InterfaceOptions.
// Overrides are applied in order, with last-write-wins semantics.
func Interface(overrides ...InterfaceOptions) *armnetwork.Interface {
	options := InterfaceOptions{}
	for _, o := range overrides {
		if err := mergo.Merge(&options, o, mergo.WithOverride); err != nil {
			panic(fmt.Sprintf("Failed to merge Interface options: %s", err))
		}
	}

	// Provide default values if none are set
	if options.Name == "" {
		options.Name = RandomName("aks")
	}
	if options.NodepoolName == "" {
		options.NodepoolName = "default"
	}
	if options.Location == "" {
		options.Location = fake.Region
	}
	if options.Tags == nil {
		options.Tags = ManagedTags(options.NodepoolName)
	}
	if options.Properties == nil {
		options.Properties = &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
				{
					Name: lo.ToPtr("ipConfig"),
					Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
						PrivateIPAllocationMethod: lo.ToPtr(armnetwork.IPAllocationMethodDynamic),
						Subnet:                    &armnetwork.Subnet{ID: lo.ToPtr("/subscriptions/.../resourceGroups/.../providers/Microsoft.Network/virtualNetworks/.../subnets/default")},
					},
				},
			},
		}
	}

	nic := &armnetwork.Interface{
		ID:         lo.ToPtr(fmt.Sprintf("/subscriptions/subscriptionID/resourceGroups/test-resourceGroup/providers/Microsoft.Network/networkInterfaces/%s", options.Name)),
		Name:       &options.Name,
		Location:   &options.Location,
		Properties: options.Properties,
		Tags:       options.Tags,
	}

	return nic
}
