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

package fake

import (
	"context"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
)

func TestAzureResourceGraphAPI_Resources_VM(t *testing.T) {
	resourceGroup := "test_managed_cluster_rg"
	subscriptionID := "test_sub"
	virtualMachinesAPI := &VirtualMachinesAPI{}
	azureResourceGraphAPI := NewAzureResourceGraphAPI(resourceGroup, virtualMachinesAPI, nil)
	cases := []struct {
		testName      string
		vmNames       []string
		tags          map[string]*string
		expectedError string
	}{
		{
			testName:      "happy case",
			vmNames:       []string{"A", "B", "C"},
			tags:          map[string]*string{launchtemplate.NodePoolTagKey: lo.ToPtr("default")},
			expectedError: "",
		},
		{
			testName:      "no tags",
			vmNames:       []string{"A", "B", "C"},
			tags:          nil,
			expectedError: "Unexpected nil resource data",
		},
		{
			testName:      "wrong tags",
			vmNames:       []string{"A", "B", "C"},
			tags:          map[string]*string{"dummy tag": lo.ToPtr("default")},
			expectedError: "Unexpected nil resource data",
		},
	}
	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			for _, name := range c.vmNames {
				_, err := instance.CreateVirtualMachine(context.Background(), virtualMachinesAPI, resourceGroup, name, armcompute.VirtualMachine{Tags: c.tags, Zones: []*string{lo.ToPtr("1")}})
				if err != nil {
					t.Errorf("Unexpected error %v", err)
					return
				}
			}
			queryRequest := instance.NewQueryRequest(&subscriptionID, instance.GetVMListQueryBuilder(resourceGroup).String())
			data, err := instance.GetResourceData(context.Background(), azureResourceGraphAPI, *queryRequest)
			if err != nil {
				t.Errorf("Unexpected error %v", err)
				return
			}
			if data == nil {
				assert.Equal(t, c.expectedError, "Unexpected nil resource data")
			}
			if c.expectedError == "" {
				if len(data) != len(c.vmNames) {
					t.Errorf("Not all VMs were returned")
					return
				}
				for _, vm := range data {
					err := checkVM(vm, resourceGroup)
					if err != nil {
						t.Error(err)
					}
				}
			}
			virtualMachinesAPI.Reset()
		})
	}
}

func checkVM(vm instance.Resource, rg string) error {
	name, ok := vm["name"]
	if !ok {
		return fmt.Errorf("VM is missing name")
	}
	expectedID := MkVMID(rg, name.(string))
	id, ok := vm["id"]
	if !ok {
		return fmt.Errorf("VM is missing id")
	}
	if expectedID != id {
		return fmt.Errorf("VM id is incorrect")
	}
	properties, ok := vm["properties"]
	if !ok {
		return fmt.Errorf("VM is missing properties")
	}
	_, ok = properties.(instance.Resource)["timeCreated"]
	if !ok {
		return fmt.Errorf("VM is missing timeCreated property")
	}
	return nil
}

func TestAzureResourceGraphAPI_Resources_VM_WithKarpenterAKSMachineTagFiltering(t *testing.T) {
	resourceGroup := "test_managed_cluster_rg"
	subscriptionID := "test_sub"
	virtualMachinesAPI := &VirtualMachinesAPI{}
	azureResourceGraphAPI := NewAzureResourceGraphAPI(resourceGroup, virtualMachinesAPI, nil)

	cases := []struct {
		testName        string
		vmConfigs       []vmConfig
		expectedVMNames []string
		expectedError   string
	}{
		{
			testName: "exclude VMs with KarpenterAKSMachine tag",
			vmConfigs: []vmConfig{
				{
					name: "karpenter-vm",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey: lo.ToPtr("default"),
					},
				},
				{
					name: "aks-machine-vm",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey:                     lo.ToPtr("default"),
						launchtemplate.KarpenterAKSMachineNodeClaimTagKey: lo.ToPtr("test-claim"),
					},
				},
			},
			expectedVMNames: []string{"karpenter-vm"},
			expectedError:   "",
		},
		{
			testName: "include all VMs when none have KarpenterAKSMachine tag",
			vmConfigs: []vmConfig{
				{
					name: "vm1",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey: lo.ToPtr("default"),
					},
				},
				{
					name: "vm2",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey: lo.ToPtr("default"),
					},
				},
			},
			expectedVMNames: []string{"vm1", "vm2"},
			expectedError:   "",
		},
		{
			testName: "exclude all VMs when all have KarpenterAKSMachine tag",
			vmConfigs: []vmConfig{
				{
					name: "aks-vm1",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey:                     lo.ToPtr("default"),
						launchtemplate.KarpenterAKSMachineNodeClaimTagKey: lo.ToPtr("claim1"),
					},
				},
				{
					name: "aks-vm2",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey:                     lo.ToPtr("default"),
						launchtemplate.KarpenterAKSMachineNodeClaimTagKey: lo.ToPtr("claim2"),
					},
				},
			},
			expectedVMNames: []string{},
			expectedError:   "",
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			// Create VMs with different tag configurations
			for _, vmConfig := range c.vmConfigs {
				_, err := instance.CreateVirtualMachine(context.Background(), virtualMachinesAPI, resourceGroup, vmConfig.name, armcompute.VirtualMachine{
					Tags:  vmConfig.tags,
					Zones: []*string{lo.ToPtr("1")},
				})
				if err != nil {
					t.Errorf("Unexpected error creating VM %s: %v", vmConfig.name, err)
					return
				}
			}

			// Query for VMs
			queryRequest := instance.NewQueryRequest(&subscriptionID, instance.GetVMListQueryBuilder(resourceGroup).String())
			data, err := instance.GetResourceData(context.Background(), azureResourceGraphAPI, *queryRequest)

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if c.expectedError != "" {
				assert.Equal(t, c.expectedError, "Unexpected nil resource data")
			} else {
				assert.Equal(t, len(c.expectedVMNames), len(data), "Unexpected number of VMs returned")

				// Verify the correct VMs are returned
				returnedNames := make([]string, 0, len(data))
				for _, vm := range data {
					name, ok := vm["name"]
					if !ok {
						t.Error("VM is missing name")
						continue
					}
					returnedNames = append(returnedNames, name.(string))
				}

				for _, expectedName := range c.expectedVMNames {
					assert.Contains(t, returnedNames, expectedName, "Expected VM not found in results")
				}
			}

			virtualMachinesAPI.Reset()
		})
	}
}

func TestAzureResourceGraphAPI_Resources_NIC_WithKarpenterAKSMachineTagFiltering(t *testing.T) {
	resourceGroup := "test_managed_cluster_rg"
	subscriptionID := "test_sub"
	networkInterfacesAPI := &NetworkInterfacesAPI{}
	azureResourceGraphAPI := NewAzureResourceGraphAPI(resourceGroup, nil, networkInterfacesAPI)

	cases := []struct {
		testName         string
		nicConfigs       []nicConfig
		expectedNICNames []string
		expectedError    string
	}{
		{
			testName: "exclude NICs with KarpenterAKSMachine tag",
			nicConfigs: []nicConfig{
				{
					name: "karpenter-nic",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey: lo.ToPtr("default"),
					},
				},
				{
					name: "aks-machine-nic",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey:                     lo.ToPtr("default"),
						launchtemplate.KarpenterAKSMachineNodeClaimTagKey: lo.ToPtr("test-claim"),
					},
				},
			},
			expectedNICNames: []string{"karpenter-nic"},
			expectedError:    "",
		},
		{
			testName: "exclude NICs without tags",
			nicConfigs: []nicConfig{
				{
					name: "karpenter-nic",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey: lo.ToPtr("default"),
					},
				},
				{
					name: "aks-machine-nic-no-tags",
					tags: map[string]*string{},
				},
			},
			expectedNICNames: []string{"karpenter-nic"},
			expectedError:    "",
		},
		{
			testName: "include all NICs when none have KarpenterAKSMachine tag",
			nicConfigs: []nicConfig{
				{
					name: "nic1",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey: lo.ToPtr("default"),
					},
				},
				{
					name: "nic2",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey: lo.ToPtr("default"),
					},
				},
			},
			expectedNICNames: []string{"nic1", "nic2"},
			expectedError:    "",
		},
		{
			testName: "exclude all NICs when all have KarpenterAKSMachine tag",
			nicConfigs: []nicConfig{
				{
					name: "aks-nic1",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey:                     lo.ToPtr("default"),
						launchtemplate.KarpenterAKSMachineNodeClaimTagKey: lo.ToPtr("claim1"),
					},
				},
				{
					name: "aks-nic2",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey:                     lo.ToPtr("default"),
						launchtemplate.KarpenterAKSMachineNodeClaimTagKey: lo.ToPtr("claim2"),
					},
				},
			},
			expectedNICNames: []string{},
			expectedError:    "",
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			// Create NICs with different tag configurations
			for _, nicConfig := range c.nicConfigs {
				nic := armnetwork.Interface{
					Tags: nicConfig.tags,
				}
				_, err := networkInterfacesAPI.BeginCreateOrUpdate(context.Background(), resourceGroup, nicConfig.name, nic, nil)
				if err != nil {
					t.Errorf("Unexpected error creating NIC %s: %v", nicConfig.name, err)
					return
				}
			}

			// Query for NICs
			queryRequest := instance.NewQueryRequest(&subscriptionID, instance.GetNICListQueryBuilder(resourceGroup).String())
			data, err := instance.GetResourceData(context.Background(), azureResourceGraphAPI, *queryRequest)

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if c.expectedError != "" {
				assert.Equal(t, c.expectedError, "Unexpected nil resource data")
			} else {
				assert.Equal(t, len(c.expectedNICNames), len(data), "Unexpected number of NICs returned")

				// Verify the correct NICs are returned
				returnedNames := make([]string, 0, len(data))
				for _, nic := range data {
					name, ok := nic["name"]
					if !ok {
						t.Error("NIC is missing name")
						continue
					}
					returnedNames = append(returnedNames, name.(string))
				}

				for _, expectedName := range c.expectedNICNames {
					assert.Contains(t, returnedNames, expectedName, "Expected NIC not found in results")
				}
			}

			networkInterfacesAPI.Reset()
		})
	}
}

// Helper types for test configuration
type vmConfig struct {
	name string
	tags map[string]*string
}

type nicConfig struct {
	name string
	tags map[string]*string
}
