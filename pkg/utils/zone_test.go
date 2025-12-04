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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/stretchr/testify/assert"
)

func TestMakeAKSLabelZoneFromVM(t *testing.T) {
	tc := []struct {
		testName      string
		input         *armcompute.VirtualMachine
		expectedZone  string
		expectedError string
	}{
		{
			testName:      "invalid virtual machine struct",
			input:         nil,
			expectedError: "cannot pass in a nil virtual machine",
		},
		{
			testName: "happy case",
			input: &armcompute.VirtualMachine{
				Location: to.Ptr("region"),
				Zones:    []*string{to.Ptr("1")},
			},
			expectedZone: "region-1",
		},
		{
			testName: "missing Location",
			input: &armcompute.VirtualMachine{
				Zones: []*string{to.Ptr("1")},
			},
			expectedError: "virtual machine is missing location",
		},
		{
			testName: "multiple zones",
			input: &armcompute.VirtualMachine{
				Zones: []*string{to.Ptr("1"), to.Ptr("2")},
			},
			expectedError: "virtual machine has multiple zones",
		},
		{
			testName: "empty Zones",
			input: &armcompute.VirtualMachine{
				Zones: []*string{},
			},
			expectedZone: "",
		},
		{
			testName:     "nil Zones",
			input:        &armcompute.VirtualMachine{},
			expectedZone: "",
		},
	}

	for _, c := range tc {
		zone, err := utils.MakeAKSLabelZoneFromVM(c.input)
		assert.Equal(t, c.expectedZone, zone, c.testName)
		if err == nil && c.expectedError != "" {
			assert.Fail(t, "expected error but got nil", c.testName)
		}
		if err != nil {
			assert.Equal(t, c.expectedError, err.Error(), c.testName)
		}
	}
}
