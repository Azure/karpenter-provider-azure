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

	"dario.cat/mergo"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/samber/lo"
)

// AKSMachineOptions customizes an AKS Machine for testing.
type AKSMachineOptions struct {
	Name                 string
	MachinesPoolName     string
	ClusterResourceGroup string
	ClusterName          string
	Location             string
	VMSize               string
	Priority             *armcontainerservice.ScaleSetPriority
	Zones                []*string
	Properties           *armcontainerservice.MachineProperties
	NodepoolName         string
}

// AKSMachine creates a test AKS Machine with defaults that can be overridden by AKSMachineOptions.
// This implementation matches the setDefaultMachineValues pattern from the fake API.
// Overrides are applied in order, with last-write-wins semantics.
//
//nolint:gocyclo
func AKSMachine(overrides ...AKSMachineOptions) *armcontainerservice.Machine {
	options := AKSMachineOptions{}
	for _, o := range overrides {
		if err := mergo.Merge(&options, o, mergo.WithOverride); err != nil {
			panic(fmt.Sprintf("Failed to merge AKSMachine options: %s", err))
		}
	}

	// Provide default values if none are set
	if options.ClusterResourceGroup == "" {
		options.ClusterResourceGroup = "test-resourceGroup"
	}
	if options.ClusterName == "" {
		options.ClusterName = "test-cluster"
	}
	if options.Name == "" {
		options.Name = RandomName("aks-machine")
	}
	if options.MachinesPoolName == "" {
		options.MachinesPoolName = "default"
	}
	if options.Location == "" {
		options.Location = fake.Region
	}
	if options.VMSize == "" {
		options.VMSize = "Standard_D2s_v3"
	}
	if options.Priority == nil {
		options.Priority = lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular)
	}
	if options.Zones == nil {
		options.Zones = []*string{lo.ToPtr("1")}
	}
	if options.Properties == nil {
		options.Properties = &armcontainerservice.MachineProperties{}
	}

	// Set default properties if not provided - matching setDefaultMachineValues pattern
	if options.Properties.Hardware == nil {
		options.Properties.Hardware = &armcontainerservice.MachineHardwareProfile{
			VMSize: lo.ToPtr(options.VMSize),
		}
	}
	if options.Properties.Network == nil {
		options.Properties.Network = &armcontainerservice.MachineNetworkProperties{}
	}
	if options.Properties.OperatingSystem == nil {
		options.Properties.OperatingSystem = &armcontainerservice.MachineOSProfile{
			OSType: lo.ToPtr(armcontainerservice.OSTypeLinux),
		}
	}
	if options.Properties.Kubernetes == nil {
		options.Properties.Kubernetes = &armcontainerservice.MachineKubernetesProfile{
			OrchestratorVersion: lo.ToPtr("1.28.0"),
		}
	}
	if options.Properties.ProvisioningState == nil {
		options.Properties.ProvisioningState = lo.ToPtr("Succeeded")
	}

	// Set Priority field (required field that was missing) - must be set AFTER default Priority is established
	if options.Properties.Priority == nil {
		options.Properties.Priority = options.Priority
	}

	// Set Status with creation timestamp (required field) - matching setDefaultMachineValues
	if options.Properties.Status == nil {
		options.Properties.Status = &armcontainerservice.MachineStatus{}
	}
	if options.Properties.Status.CreationTimestamp == nil {
		options.Properties.Status.CreationTimestamp = lo.ToPtr(time.Now())
	}

	// Set ResourceID (required field) - simulates VM resource ID following AKS naming convention
	// vmName = aks-<machinesPoolName>-<aksMachineName>-########-vm
	if options.Properties.ResourceID == nil {
		// Generate a VM name following AKS convention: aks-{agentPoolName}-{machineName}-{randomId}-vm{id}
		vmName := fmt.Sprintf("aks-%s-%s-12345678-vm", options.MachinesPoolName, options.Name)
		vmResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s", "test-subscription", options.ClusterResourceGroup, vmName)
		options.Properties.ResourceID = lo.ToPtr(vmResourceID)
	}

	// Set NodeImageVersion - matching setDefaultMachineValues default
	if options.Properties.NodeImageVersion == nil {
		// Default node image version if none provided
		options.Properties.NodeImageVersion = lo.ToPtr("AKSUbuntu-2204gen2containerd-2023.11.15")
	}

	if options.NodepoolName == "" {
		options.NodepoolName = "default"
	}
	if options.Properties.Tags == nil {
		options.Properties.Tags = ManagedTagsAKSMachine(options.NodepoolName, "some-nodeclaim", (*options.Properties.Status.CreationTimestamp).Add(-1*time.Minute))
	}

	// Construct the AKS Machine
	machineID := fake.MkMachineID(options.ClusterResourceGroup, options.ClusterName, options.MachinesPoolName, options.Name)
	machine := &armcontainerservice.Machine{
		ID:         lo.ToPtr(machineID),
		Name:       lo.ToPtr(options.Name),
		Zones:      options.Zones,
		Properties: options.Properties,
	}

	return machine
}
