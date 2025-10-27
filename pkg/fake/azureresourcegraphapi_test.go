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
