package test

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/imdario/mergo"
	"github.com/samber/lo"
	k8srand "k8s.io/apimachinery/pkg/util/rand"
)

// InterfaceOptions customizes an Azure Network Interface for testing.
type InterfaceOptions struct {
	Name       string
	Location   string
	Properties *armnetwork.InterfacePropertiesFormat
	Tags       map[string]*string
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
	if options.Location == "" {
		options.Location = "eastus"
	}
	if options.Properties == nil {
		options.Properties = &armnetwork.InterfacePropertiesFormat{}
	}

	nic := &armnetwork.Interface{
		ID:       lo.ToPtr(fmt.Sprintf("/subscriptions/subscriptionID/resourceGroups/test-resourceGroup/providers/Microsoft.Network/networkInterfaces/%s", options.Name)),
		Name:     &options.Name,
		Location: &options.Location,
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
				{
					Name: lo.ToPtr("ipConfig"),
					Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
						PrivateIPAllocationMethod: lo.ToPtr(armnetwork.IPAllocationMethodDynamic),
						Subnet:                    &armnetwork.Subnet{ID: lo.ToPtr("/subscriptions/.../resourceGroups/.../providers/Microsoft.Network/virtualNetworks/.../subnets/default")},
					},
				},
			},
		},
		Tags: options.Tags,
	}

	// If the user wants to override the full InterfacePropertiesFormat, apply it here
	if options.Properties != nil {
		nic.Properties = options.Properties
	}

	return nic
}

// RandomName returns a pseudo-random resource name with a given prefix.
func RandomName(prefix string) string {
	// You could make this more robust by including additional random characters.
	return prefix + "-" + k8srand.String(10)
}

func ManagedTags(nodepoolName string) map[string]*string {
	return map[string]*string{
		"karpenter.sh_cluster":  lo.ToPtr("test-cluster"),
		"karpenter.sh_nodepool": lo.ToPtr(nodepoolName),
	}
}
