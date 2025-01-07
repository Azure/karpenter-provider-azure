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

package utils_test

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/stretchr/testify/assert"
)

func TestGetZone(t *testing.T) {
	testVMName := "silly-armcompute"
	tc := []struct {
		testName      string
		input         *armcompute.VirtualMachine
		expectedZone  string
		expectedError string
	}{
		{
			testName: "missing name",
			input: &armcompute.VirtualMachine{
				Name: nil,
			},
			expectedError: "virtual machine is missing name",
		},
		{
			testName:      "invalid virtual machine struct",
			input:         nil,
			expectedError: "cannot pass in a nil virtual machine",
		},
		{
			testName: "invalid zones field in virtual machine struct",
			input: &armcompute.VirtualMachine{
				Name: &testVMName,
			},
			expectedError: "virtual machine silly-armcompute zones are nil",
		},
		{
			testName: "happy case",
			input: &armcompute.VirtualMachine{
				Name:     &testVMName,
				Location: to.Ptr("region"),
				Zones:    []*string{to.Ptr("1")},
			},
			expectedZone: "region-1",
		},
		{
			testName: "emptyZones",
			input: &armcompute.VirtualMachine{
				Name:  &testVMName,
				Zones: []*string{},
			},
			expectedError: "virtual machine silly-armcompute does not have any zones specified",
		},
	}

	for _, c := range tc {
		zone, err := utils.GetZone(c.input)
		assert.Equal(t, c.expectedZone, zone, c.testName)
		if err != nil {
			assert.Equal(t, c.expectedError, err.Error(), c.testName)
		}
	}
}
