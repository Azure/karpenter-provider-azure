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

package instance

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/stretchr/testify/assert"
)

func TestCreateNICFromQueryResponseData(t *testing.T) {
	id := "nic_id"
	name := "nic_name"
	tag := "tag1"
	val := "val1"
	tags := map[string]*string{tag: &val}

	tc := []struct {
		testName      string
		data          map[string]interface{}
		expectedError string
		expectedNIC   *armnetwork.Interface
	}{
		{
			testName: "missing id",
			data: map[string]interface{}{
				"name": name,
			},
			expectedError: "network interface is missing id",
			expectedNIC:   nil,
		},
		{
			testName: "missing name",
			data: map[string]interface{}{
				"id": id,
			},
			expectedError: "network interface is missing name",
			expectedNIC:   nil,
		},
		{
			testName: "happy case",
			data: map[string]interface{}{
				"id":   id,
				"name": name,
				"tags": map[string]interface{}{tag: val},
			},
			expectedNIC: &armnetwork.Interface{
				ID:   &id,
				Name: &name,
				Tags: tags,
			},
		},
	}

	for _, c := range tc {
		nic, err := createNICFromQueryResponseData(c.data)
		if nic != nil {
			expected := *c.expectedNIC
			actual := *nic
			assert.Equal(t, *expected.ID, *actual.ID, c.testName)
			assert.Equal(t, *expected.Name, *actual.Name, c.testName)
			for key := range expected.Tags {
				assert.Equal(t, *(expected.Tags[key]), *(actual.Tags[key]), c.testName)
			}
		}
		if err != nil {
			assert.Equal(t, c.expectedError, err.Error(), c.testName)
		}
	}
}

// Currently tested: ID, Name, Tags, Zones
// TODO: Add the below attributes for Properties if needed:
// Priority, InstanceView.HyperVGeneration, TimeCreated
func TestCreateVMFromQueryResponseData(t *testing.T) {
	id := "vm_id"
	name := "vm_name"
	tag := "tag1"
	val := "val1"
	zone := "us-west"
	tags := map[string]*string{tag: &val}
	zones := []*string{&zone}

	tc := []struct {
		testName      string
		data          map[string]interface{}
		expectedError string
		expectedVM    *armcompute.VirtualMachine
	}{
		{
			testName: "missing id",
			data: map[string]interface{}{
				"name": name,
			},
			expectedError: "virtual machine is missing id",
			expectedVM:    nil,
		},
		{
			testName: "missing name",
			data: map[string]interface{}{
				"id": id,
			},
			expectedError: "virtual machine is missing name",
			expectedVM:    nil,
		},
		{
			testName: "happy case",
			data: map[string]interface{}{
				"id":    id,
				"name":  name,
				"tags":  map[string]interface{}{tag: val},
				"zones": []interface{}{zone},
			},
			expectedVM: &armcompute.VirtualMachine{
				ID:    &id,
				Name:  &name,
				Tags:  tags,
				Zones: zones,
			},
		},
	}

	for _, c := range tc {
		vm, err := createVMFromQueryResponseData(c.data)
		if vm != nil {
			expected := *c.expectedVM
			actual := *vm
			assert.Equal(t, *expected.ID, *actual.ID, c.testName)
			assert.Equal(t, *expected.Name, *actual.Name, c.testName)
			for key := range expected.Tags {
				assert.Equal(t, *(expected.Tags[key]), *(actual.Tags[key]), c.testName)
			}
			for i := range expected.Zones {
				assert.Equal(t, *(expected.Zones[i]), *(actual.Zones[i]), c.testName)
			}
		}
		if err != nil {
			assert.Equal(t, c.expectedError, err.Error(), c.testName)
		}
	}
}
