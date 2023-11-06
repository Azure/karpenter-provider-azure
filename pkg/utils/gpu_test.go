// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package utils

import (
	"testing"
)

func TestIsNvidiaEnabledSKU(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output bool
	}{
		{"Valid SKU", "standard_nc6s_v2", true},
		{"Valid SKU with Promo", "standard_nc6s_v2_promo", true},
		{"Non-Existent SKU", "non_existent_sku", false},
		{"Uppercase SKU", "STANDARD_NC6s_v2", true},
		{"Mixed Case SKU with Promo", "Standard_Nc6s_v2_Promo", true},
		{"Not supported SKU", "standard_d2_v2", false},
		{"Empty SKU", "", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := IsNvidiaEnabledSKU(test.input)
			if result != test.output {
				t.Errorf("Expected %v, but got %v", test.output, result)
			}
		})
	}
}

func TestIsMarinerEnabledGPUSKU(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output bool
	}{
		{"Valid Mariner SKU", "standard_nc6s_v3", true},
		{"Valid Mariner SKU with Promo", "standard_nc6s_v3_promo", true},
		{"Non-Existent Mariner SKU", "non_existent_sku", false},
		{"Uppercase Mariner SKU", "STANDARD_NC6S_V3", true},
		{"Mixed Case Mariner SKU with Promo", "Standard_Nc6s_V3_Promo", true},
		{"Empty Mariner SKU", "", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := IsMarinerEnabledGPUSKU(test.input)
			if result != test.output {
				t.Errorf("Expected %v, but got %v", test.output, result)
			}
		})
	}
}
