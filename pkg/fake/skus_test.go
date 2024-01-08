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
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
)

// TestSKUExistenceEastUS tests that we are not regressing in our codegen
func TestSKUExistenceEastUS(t *testing.T) {
	expectedSKUs := sets.New(
		"Standard_A0",
		"Standard_B1s",
		"Standard_D2s_v3",
		"Standard_D2_v2",
		"Standard_D2_v3",
		"Standard_D2_v5",
		"Standard_D4s_v3",
		"Standard_D64s_v3",
		"Standard_DC8s_v3",
		"Standard_DS2_v2",
		"Standard_F16s_v2",
		"Standard_M8-2ms",
		"Standard_NC24ads_A100_v4",
		"Standard_NC6s_v3",
		"Standard_NC16as_T4_v3",
	)

	generatedSKUs := ResourceSkus["eastus"]

	skuSets := make(sets.Set[string])
	for _, sku := range generatedSKUs {
		skuName := *sku.Name
		skuSets.Insert(skuName)
	}

	for _, expectedSKU := range expectedSKUs.UnsortedList() {
		if exists := skuSets.Has(expectedSKU); !exists {
			t.Errorf("SKU not found %v", expectedSKU)
		}
	}
}

// TestSKUExistenceWestCentralUS tests that we are not regressing in our codegen
func TestSKUExistenceWestCentralUS(t *testing.T) {
	expectedSKUs := sets.New( 
		"Standard_A0",
		"Standard_B1s",
		"Standard_D2s_v3",
		"Standard_D2_v2",
		"Standard_D2_v3",
		"Standard_D2_v5",
		"Standard_D4s_v3",
		"Standard_D64s_v3",
		"Standard_DS2_v2",
		"Standard_F16s_v2",
	)

	generatedSKUs := ResourceSkus["westcentralus"]

	skuSets := make(sets.Set[string])
	for _, sku := range generatedSKUs {
		skuName := *sku.Name
		skuSets.Insert(skuName)
	}

	for _, expectedSKU := range expectedSKUs.UnsortedList() {
		if exists := skuSets.Has(expectedSKU); !exists {
			t.Errorf("SKU not found %v", expectedSKU)
		}
	}
}
