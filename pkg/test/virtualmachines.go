package test

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/imdario/mergo"
	"github.com/samber/lo"
)

// VirtualMachineOptions customizes an Azure Virtual Machine for testing.
type VirtualMachineOptions struct {
	Name       string
	Location   string
	Properties *armcompute.VirtualMachineProperties
	Tags       map[string]*string
}

// VirtualMachine creates a test Azure Virtual Machine with defaults that can be overridden by VirtualMachineOptions.
// Overrides are applied in order, with last-write-wins semantics.
func VirtualMachine(overrides ...VirtualMachineOptions) *armcompute.VirtualMachine {
	options := VirtualMachineOptions{}
	for _, o := range overrides {
		if err := mergo.Merge(&options, o, mergo.WithOverride); err != nil {
			panic(fmt.Sprintf("Failed to merge VirtualMachine options: %s", err))
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
		options.Properties = &armcompute.VirtualMachineProperties{}
	}

	// Construct the basic VM
	vm := &armcompute.VirtualMachine{
		ID:         lo.ToPtr(fmt.Sprintf("/subscriptions/subscriptionID/resourceGroups/test-resourceGroup/providers/Microsoft.Compute/virtualMachines/%s", options.Name)),
		Name:       lo.ToPtr(options.Name),
		Location:   lo.ToPtr(options.Location),
		Properties: &armcompute.VirtualMachineProperties{
			// Minimal default properties can be set here if you like:
			// HardwareProfile: &armcompute.HardwareProfile{
			// 	VMSize: lo.ToPtr(armcompute.VirtualMachineSizeTypesStandardDS1V2),
			// },
		},
		Tags: options.Tags,
	}

	// If the user wants to override the entire VirtualMachineProperties, apply it here
	if options.Properties != nil {
		vm.Properties = options.Properties
	}

	return vm
}
