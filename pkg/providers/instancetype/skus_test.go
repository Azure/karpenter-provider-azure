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

package instancetype

import (
	"testing"
)

func TestGetKarpenterWorkingSKUs(t *testing.T) {
	for _, sku := range getKarpenterWorkingSKUs() {
		for _, aksRestrictedSKU := range aksRestrictedVMSKUs {
			if *aksRestrictedSKU.Name == *sku.Name {
				t.Errorf("AKS restricted SKU %s should not be in the list of SKUs", *aksRestrictedSKU.Name)
			}
		}
		for _, karpenterRestrictedSKU := range karpenterRestrictedVMSKUs {
			if *karpenterRestrictedSKU.Name == *sku.Name {
				t.Errorf("Karpenter restricted SKU %s should not be in the list of SKUs", *karpenterRestrictedSKU.Name)
			}
		}
	}
}
