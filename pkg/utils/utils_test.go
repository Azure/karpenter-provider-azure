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

	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	. "github.com/onsi/gomega"
)

func TestIsAKSManagedVNET(t *testing.T) {
	cases := []struct {
		name           string
		subnetID       string
		nrg            string
		expectedError  string
		expectedResult bool
	}{
		{
			name:           "Not a BYO vnet",
			subnetID:       "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/MC_rg/providers/Microsoft.Network/virtualNetworks/aks-vnet-18484614/subnets/aks-subnet",
			nrg:            "MC_rg",
			expectedError:  "",
			expectedResult: true,
		},
		{
			name:           "Not a BYO vnet (different casing)",
			subnetID:       "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/MC_rg/providers/Microsoft.Network/virtualNetworks/AKS-VNET-18484614/subnets/aks-subnet",
			nrg:            "mc_rg",
			expectedError:  "",
			expectedResult: true,
		},
		{
			name:           "BYO vnet in the MC RG",
			subnetID:       "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/MC_rg/providers/Microsoft.Network/virtualNetworks/myvnet/subnets/aks-subnet",
			nrg:            "mc_rg",
			expectedError:  "",
			expectedResult: false,
		},
		{
			name:           "A BYO subnet in the managed vnet",
			subnetID:       "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/MC_rg/providers/Microsoft.Network/virtualNetworks/AKS-VNET-18484614/subnets/my-subnet",
			nrg:            "MC_rg",
			expectedError:  "",
			expectedResult: false,
		},
		{
			name:           "BYO vnet in a different RG",
			subnetID:       "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myrg/providers/Microsoft.Network/virtualNetworks/aks-vnet-18484614/subnets/aks-subnet",
			nrg:            "MC_rg",
			expectedError:  "",
			expectedResult: false,
		},
		{
			name:           "not a subnet errors",
			subnetID:       "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/MC_rg/providers/Microsoft.Compute/virtualMachines/myVM",
			expectedError:  "invalid vnet subnet id",
			expectedResult: false,
		},
		{
			name:           "not a valid ARM ID errors",
			subnetID:       "not a valid ID",
			expectedError:  "invalid vnet subnet id",
			expectedResult: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			byo, err := utils.IsAKSManagedVNET(c.nrg, c.subnetID)
			if c.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(c.expectedError)))
			} else {
				g.Expect(byo).To(Equal(c.expectedResult))
			}
		})
	}
}
