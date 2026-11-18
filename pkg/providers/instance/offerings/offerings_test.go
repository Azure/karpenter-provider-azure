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

package offerings

import (
	"testing"

	. "github.com/onsi/gomega"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

func TestGetInstanceTypeFromVMSize(t *testing.T) {
	possibleInstanceTypes := []*cloudprovider.InstanceType{
		{Name: "Standard_D2s_v3"},
		{Name: "Standard_D4s_v3"},
		{Name: "Standard_B1s"},
	}

	cases := []struct {
		name                 string
		vmSize               string
		expectedInstanceType *cloudprovider.InstanceType
	}{
		{
			name:                 "Find existing VM size",
			vmSize:               "Standard_D2s_v3",
			expectedInstanceType: possibleInstanceTypes[0],
		},
		{
			name:                 "Find another existing VM size",
			vmSize:               "Standard_B1s",
			expectedInstanceType: possibleInstanceTypes[2],
		},
		{
			name:                 "VM size not found",
			vmSize:               "Standard_NonExistent",
			expectedInstanceType: nil,
		},
		{
			name:                 "Empty VM size",
			vmSize:               "",
			expectedInstanceType: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			result := GetInstanceTypeFromVMSize(c.vmSize, possibleInstanceTypes)
			g.Expect(result).To(Equal(c.expectedInstanceType))
		})
	}
}
