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
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
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
			g := NewWithT(t)
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
				g.Expect("Unexpected nil resource data").To(Equal(c.expectedError))
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
			g := NewWithT(t)
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
				g.Expect("Unexpected nil resource data").To(Equal(c.expectedError))
			} else {
				g.Expect(data).To(HaveLen(len(c.expectedVMNames)), "Unexpected number of VMs returned")

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
					g.Expect(returnedNames).To(ContainElement(expectedName), "Expected VM not found in results")
				}
			}

			virtualMachinesAPI.Reset()
		})
	}
}

func TestResourceListQueryOwnershipFilters(t *testing.T) {
	g := NewWithT(t)
	vmQuery := instance.GetVMListQueryBuilder("test-rg").String()
	legacyNICQuery := instance.GetNICListQueryBuilder("test-rg").String()
	machineNICQuery := instance.GetAKSMachineNICListQueryBuilder("test-rg", "aksmanagedap").String()

	g.Expect(vmQuery).To(Equal(`Resources | where type == "microsoft.compute/virtualmachines" | where resourceGroup == "test-rg" | where tags has_cs "karpenter.sh_nodepool" | where not(tags has_cs "karpenter.azure.com_aksmachine_nodeclaim")`))
	g.Expect(legacyNICQuery).To(Equal(`Resources | where type == "microsoft.network/networkinterfaces" | where resourceGroup == "test-rg" | where tags has_cs "karpenter.sh_nodepool" | where not(tags has_cs "karpenter.azure.com_aksmachine_nodeclaim")`))
	g.Expect(machineNICQuery).To(Equal(`Resources | where type == "microsoft.network/networkinterfaces" | where resourceGroup == "test-rg" | where tags has_cs "karpenter.sh_nodepool" | where (not(tags has_cs "karpenter.azure.com_aksmachine_nodeclaim") and isempty(tostring(tags["aks-managed-poolName"]))) or (isnotempty(tostring(tags["aks-managed-poolName"])) and tostring(tags["aks-managed-poolName"]) == "aksmanagedap") | order by id asc | project id, name, tags`))
}

//nolint:gocyclo // Table coverage mirrors the legacy and temporary Machine-inclusive ARG query branches.
func TestAzureResourceGraphAPI_Resources_NIC_WithKarpenterAKSMachineTagFiltering(t *testing.T) {
	resourceGroup := "test_managed_cluster_rg"
	subscriptionID := "test_sub"

	cases := []struct {
		testName              string
		includeAKSMachineNICs bool
		emptyAKSMachinePool   bool
		nicConfigs            []nicConfig
		expectedNICNames      []string
		expectedError         string
	}{
		{
			testName:              "include detached Machine NICs in Machine API mode",
			includeAKSMachineNICs: true,
			nicConfigs: []nicConfig{
				{
					name: "karpenter-nic",
					tags: map[string]*string{
						launchtemplate.NodePoolTagKey: lo.ToPtr("default"),
					},
				},
				{
					name: "aks-machine-nic",
					tags: testManagedMachineTags("test-claim"),
				},
			},
			expectedNICNames: []string{"karpenter-nic", "aks-machine-nic"},
			expectedError:    "",
		},
		{
			testName:              "exclude Machine NICs in VM mode",
			includeAKSMachineNICs: false,
			nicConfigs: []nicConfig{
				{name: "karpenter-nic", tags: map[string]*string{launchtemplate.NodePoolTagKey: lo.ToPtr("default")}},
				{name: "aks-machine-nic", tags: testManagedMachineTags("test-claim")},
			},
			expectedNICNames: []string{"karpenter-nic"},
			expectedError:    "",
		},
		{
			testName:              "exclude NICs without tags",
			includeAKSMachineNICs: true,
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
			testName:              "exclude Machine NICs from other resource groups",
			includeAKSMachineNICs: true,
			nicConfigs: []nicConfig{
				{name: "local-machine-nic", tags: testManagedMachineTags("local-claim")},
				{name: "remote-machine-nic", resourceGroup: "other-resource-group", tags: testManagedMachineTags("remote-claim")},
			},
			expectedNICNames: []string{"local-machine-nic"},
			expectedError:    "",
		},
		{
			testName:              "exclude Machine NICs from other Machine pools",
			includeAKSMachineNICs: true,
			nicConfigs: []nicConfig{
				{name: "configured-pool-nic", tags: testManagedMachineTags("configured-claim")},
				{name: "other-pool-nic", tags: testManagedMachineTagsForPool("other-claim", "otherpool")},
			},
			expectedNICNames: []string{"configured-pool-nic"},
			expectedError:    "",
		},
		{
			testName:              "exclude Machine NICs when the configured Machine pool is empty",
			includeAKSMachineNICs: true,
			emptyAKSMachinePool:   true,
			nicConfigs: []nicConfig{
				{name: "unmanaged-machine-nic", tags: testManagedMachineTags("unmanaged-claim")},
			},
			expectedNICNames: []string{},
			expectedError:    "",
		},
		{
			testName:              "preserve VM-mode NIC discovery",
			includeAKSMachineNICs: false,
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
			testName:              "include all detached Machine NICs in Machine API mode",
			includeAKSMachineNICs: true,
			nicConfigs: []nicConfig{
				{name: "aks-nic1", tags: testManagedMachineTags("claim1")},
				{name: "aks-nic2", tags: testManagedMachineTags("claim2")},
			},
			expectedNICNames: []string{"aks-nic1", "aks-nic2"},
			expectedError:    "",
		},
		{
			testName:              "include attached and detached Machine NICs for ownership evaluation",
			includeAKSMachineNICs: true,
			nicConfigs: []nicConfig{
				{
					name: "detached-machine-nic",
					tags: testManagedMachineTags("claim-detached"),
				},
				{
					name: "attached-machine-nic",
					tags: testManagedMachineTags("claim-attached"),
					properties: &armnetwork.InterfacePropertiesFormat{
						VirtualMachine: &armnetwork.SubResource{ID: lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/live-vm")},
					},
				},
			},
			expectedNICNames: []string{"detached-machine-nic", "attached-machine-nic"},
			expectedError:    "",
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			g := NewWithT(t)
			networkInterfacesAPI := &NetworkInterfacesAPI{}
			machinePoolName := "aksmanagedap"
			if c.emptyAKSMachinePool {
				machinePoolName = ""
			}
			azureResourceGraphAPI := NewAzureResourceGraphAPI(resourceGroup, nil, networkInterfacesAPI, machinePoolName)
			// Create NICs with different tag configurations
			for _, nicConfig := range c.nicConfigs {
				nic := armnetwork.Interface{
					Location:   lo.ToPtr("southcentralus"),
					Tags:       nicConfig.tags,
					Properties: nicConfig.properties,
				}
				nicResourceGroup := resourceGroup
				if nicConfig.resourceGroup != "" {
					nicResourceGroup = nicConfig.resourceGroup
				}
				_, err := networkInterfacesAPI.BeginCreateOrUpdate(context.Background(), nicResourceGroup, nicConfig.name, nic, nil)
				if err != nil {
					t.Errorf("Unexpected error creating NIC %s: %v", nicConfig.name, err)
					return
				}
			}

			// Query for NICs
			query := instance.GetNICListQueryBuilder(resourceGroup)
			if c.includeAKSMachineNICs {
				query = instance.GetAKSMachineNICListQueryBuilder(resourceGroup, machinePoolName)
			}
			queryRequest := instance.NewQueryRequest(&subscriptionID, query.String())
			data, err := instance.GetResourceData(context.Background(), azureResourceGraphAPI, *queryRequest)

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			if c.includeAKSMachineNICs {
				for _, nic := range data {
					g.Expect(nic).To(HaveLen(3), "Machine-inclusive ARG query projects exactly the GC ownership fields")
					g.Expect(nic).To(And(HaveKey("id"), HaveKey("name"), HaveKey("tags")))
				}
			}

			if c.expectedError != "" {
				g.Expect("Unexpected nil resource data").To(Equal(c.expectedError))
			} else {
				g.Expect(data).To(HaveLen(len(c.expectedNICNames)), "Unexpected number of NICs returned")

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
					g.Expect(returnedNames).To(ContainElement(expectedName), "Expected NIC not found in results")
				}
			}
		})
	}
}

// Helper types for test configuration
type vmConfig struct {
	name string
	tags map[string]*string
}

type nicConfig struct {
	name          string
	resourceGroup string
	tags          map[string]*string
	properties    *armnetwork.InterfacePropertiesFormat
}

func testManagedMachineTags(nodeClaimName string) map[string]*string {
	return testManagedMachineTagsForPool(nodeClaimName, "aksmanagedap")
}

func testManagedMachineTagsForPool(nodeClaimName, machinePoolName string) map[string]*string {
	return map[string]*string{
		launchtemplate.NodePoolTagKey:                     lo.ToPtr("default"),
		launchtemplate.KarpenterAKSMachineNodeClaimTagKey: lo.ToPtr(nodeClaimName),
		launchtemplate.AKSManagedPoolNameTagKey:           lo.ToPtr(machinePoolName),
	}
}
