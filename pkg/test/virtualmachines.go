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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/imdario/mergo"
	"github.com/samber/lo"
)

// VirtualMachineOptions customizes an Azure Virtual Machine for testing.
type VirtualMachineOptions struct {
	Name         string
	NodepoolName string
	Location     string
	Properties   *armcompute.VirtualMachineProperties
	Tags         map[string]*string
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
	if options.NodepoolName == "" {
		options.NodepoolName = "default"
	}
	if options.Location == "" {
		options.Location = fake.Region
	}
	if options.Properties == nil {
		options.Properties = &armcompute.VirtualMachineProperties{}
	}
	if options.Tags == nil {
		options.Tags = ManagedTags(options.NodepoolName)
	}
	if options.Properties.TimeCreated == nil {
		options.Properties.TimeCreated = lo.ToPtr(time.Now())
	}

	// Construct the basic VM
	vm := &armcompute.VirtualMachine{
		ID:         lo.ToPtr(fmt.Sprintf("/subscriptions/subscriptionID/resourceGroups/test-resourceGroup/providers/Microsoft.Compute/virtualMachines/%s", options.Name)),
		Name:       lo.ToPtr(options.Name),
		Location:   lo.ToPtr(options.Location),
		Properties: options.Properties,
		Tags:       options.Tags,
	}

	return vm
}
