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
	"github.com/Azure/skewer"
	"github.com/mitchellh/hashstructure/v2"
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
func TestHasChanged_SimpleCases(t *testing.T) {
	g := NewWithT(t)

	// Case: Has not changed (same int)
	g.Expect(utils.HasChanged(42, 42, nil)).To(BeFalse())

	// Case: Has changed (different int)
	g.Expect(utils.HasChanged(42, 43, nil)).To(BeTrue())

	// Case: Has not changed (same string)
	g.Expect(utils.HasChanged("azure", "azure", nil)).To(BeFalse())

	// Case: Has changed (different string)
	g.Expect(utils.HasChanged("azure", "cloud", nil)).To(BeTrue())
}

func TestHasChanged_SliceOrderWithSlicesAsSets(t *testing.T) {
	g := NewWithT(t)

	a := []int{1, 2, 3}
	b := []int{3, 2, 1}

	// By default, order matters
	g.Expect(utils.HasChanged(a, b, nil)).To(BeTrue())

	// With SlicesAsSets, order does not matter
	opts := &hashstructure.HashOptions{SlicesAsSets: true}
	g.Expect(utils.HasChanged(a, b, opts)).To(BeFalse())
}

func TestExtractVersionFromVMSize(t *testing.T) {
	cases := []struct {
		name           string
		vmSize         *skewer.VMSizeType
		expectedResult string
	}{
		{
			name:           "nil VMSizeType returns empty string",
			vmSize:         nil,
			expectedResult: "",
		},
		{
			name:           "empty version returns default '1'",
			vmSize:         &skewer.VMSizeType{Version: ""},
			expectedResult: "1",
		},
		{
			name:           "version with lowercase 'v' prefix",
			vmSize:         &skewer.VMSizeType{Version: "v2"},
			expectedResult: "2",
		},
		{
			name:           "version with uppercase 'V' prefix",
			vmSize:         &skewer.VMSizeType{Version: "V2"},
			expectedResult: "2",
		},
		{
			name:           "multi-digit version",
			vmSize:         &skewer.VMSizeType{Version: "V123"},
			expectedResult: "123",
		},
		{
			name:           "unexpected version format returns empty string",
			vmSize:         &skewer.VMSizeType{Version: "x2"},
			expectedResult: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			result := utils.ExtractVersionFromVMSize(c.vmSize)
			g.Expect(result).To(Equal(c.expectedResult))
		})
	}
}

func TestGetAlphanumericHash(t *testing.T) {
	g := NewWithT(t)

	t.Run("should return error for non-positive length", func(t *testing.T) {
		_, err := utils.GetAlphanumericHash("test", 0)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("length must be positive"))

		_, err = utils.GetAlphanumericHash("test", -1)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("length must be positive"))
	})

	t.Run("should generate hash of correct length", func(t *testing.T) {
		lengths := []int{1, 6, 10, 15}
		for _, length := range lengths {
			hash, err := utils.GetAlphanumericHash("test-input", length)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(hash).To(HaveLen(length))
		}
	})

	t.Run("should only contain alphanumeric characters", func(t *testing.T) {
		hash, err := utils.GetAlphanumericHash("test-input-with-special-chars!@#$%", 10)
		g.Expect(err).ToNot(HaveOccurred())

		for _, char := range hash {
			g.Expect(char).To(SatisfyAny(
				BeNumerically(">=", '0'), BeNumerically("<=", '9'),
				BeNumerically(">=", 'a'), BeNumerically("<=", 'z'),
			))
		}
	})

	t.Run("should be deterministic", func(t *testing.T) {
		input := "deterministic-test-input"
		length := 8

		hash1, err1 := utils.GetAlphanumericHash(input, length)
		hash2, err2 := utils.GetAlphanumericHash(input, length)

		g.Expect(err1).ToNot(HaveOccurred())
		g.Expect(err2).ToNot(HaveOccurred())
		g.Expect(hash1).To(Equal(hash2))
	})

	t.Run("should produce different hashes for different inputs", func(t *testing.T) {
		length := 6
		hash1, err1 := utils.GetAlphanumericHash("input1", length)
		hash2, err2 := utils.GetAlphanumericHash("input2", length)

		g.Expect(err1).ToNot(HaveOccurred())
		g.Expect(err2).ToNot(HaveOccurred())
		g.Expect(hash1).ToNot(Equal(hash2))
	})

	t.Run("should produce different hashes for different inputs, long output", func(t *testing.T) {
		length := 36
		hash1, err1 := utils.GetAlphanumericHash("input1", length)
		hash2, err2 := utils.GetAlphanumericHash("input2", length)

		g.Expect(err1).ToNot(HaveOccurred())
		g.Expect(err2).ToNot(HaveOccurred())
		g.Expect(hash1).ToNot(Equal(hash2))
	})

	t.Run("should produce different hashes for different inputs, long input", func(t *testing.T) {
		length := 6
		hash1, err1 := utils.GetAlphanumericHash("this-is-a-very-long-nodepool-name-that-exceeds-the-maximum-aks-machine-name-length-limit1", length)
		hash2, err2 := utils.GetAlphanumericHash("this-is-a-very-long-nodepool-name-that-exceeds-the-maximum-aks-machine-name-length-limit2", length)

		g.Expect(err1).ToNot(HaveOccurred())
		g.Expect(err2).ToNot(HaveOccurred())
		g.Expect(hash1).ToNot(Equal(hash2))
	})

	t.Run("should produce different hashes for different inputs, long input 2", func(t *testing.T) {
		length := 6
		hash1, err1 := utils.GetAlphanumericHash("1this-is-a-very-long-nodepool-name-that-exceeds-the-maximum-aks-machine-name-length-limit", length)
		hash2, err2 := utils.GetAlphanumericHash("2this-is-a-very-long-nodepool-name-that-exceeds-the-maximum-aks-machine-name-length-limit", length)

		g.Expect(err1).ToNot(HaveOccurred())
		g.Expect(err2).ToNot(HaveOccurred())
		g.Expect(hash1).ToNot(Equal(hash2))
	})

	t.Run("should handle empty string input", func(t *testing.T) {
		hash, err := utils.GetAlphanumericHash("", 6)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(hash).To(HaveLen(6))
	})

	t.Run("should handle long input strings", func(t *testing.T) {
		longInput := "this-is-a-very-long-nodepool-name-that-exceeds-the-maximum-aks-machine-name-length-limit"
		hash, err := utils.GetAlphanumericHash(longInput, 6)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(hash).To(HaveLen(6))
	})
}
