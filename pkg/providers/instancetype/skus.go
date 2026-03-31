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
	_ "embed"

	"github.com/Azure/skewer"
	"github.com/samber/lo"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/util/sets"
)

// SKUEntry represents a single VM SKU entry in the known_skus.yaml file.
type SKUEntry struct {
	Name         string `yaml:"name"`
	Family       string `yaml:"family"`
	DiscoveredOn string `yaml:"discoveredOn"`
}

//go:embed known_skus.yaml
var allAzureVMSkusString string

var allAzureVMSkus = func() []skewer.SKU {
	var entries []SKUEntry
	if err := yaml.Unmarshal([]byte(allAzureVMSkusString), &entries); err != nil {
		panic("failed to parse embedded known_skus.yaml: " + err.Error())
	}
	skus := make([]skewer.SKU, len(entries))
	for i, e := range entries {
		skus[i] = skewer.SKU{Name: lo.ToPtr(e.Name)}
	}
	return skus
}()

// GetKarpenterWorkingSKUs returns a the list of SKUs that are
// allowed to be used by Karpenter. This is a subset of the
// SKUs that are available in Azure.
func GetKarpenterWorkingSKUs() []skewer.SKU {
	workingSKUs := []skewer.SKU{}
	for _, sku := range allAzureVMSkus {
		var exclude bool
		// If we find this SKU in the AKS restricted list, exclude it
		for _, aksRestrictedSKU := range AKSRestrictedVMSizes.UnsortedList() {
			if aksRestrictedSKU == sku.GetName() {
				exclude = true
			}
		}
		// If it's not in the AKS restricted list, it may be in the Karpenter restricted list
		if !exclude {
			for _, karpenterRestrictedSKU := range karpenterRestrictedVMSKUs.UnsortedList() {
				if karpenterRestrictedSKU == sku.GetName() {
					exclude = true
				}
			}
		}
		// If it's not in any of the restricted lists, we register it as a working VM SKU
		if !exclude {
			workingSKUs = append(workingSKUs, sku)
		}
	}
	return workingSKUs
}

var (
	// TODO: some of these sizes are no longer in allVMSkus so we probably don't need to explicitly exclude them here.
	// AKSRestrictedVMSizes are low-performance VM sizes
	// that are not allowed for use in AKS node pools.
	AKSRestrictedVMSizes = sets.New(
		"Standard_A0",
		"Standard_A1",
		"Standard_A1_v2",
		"Standard_B1s",
		"Standard_B1ms",
		"Standard_F1",
		"Standard_F1s",
		"Basic_A0",
		"Basic_A1",
		"Basic_A2",
		"Basic_A3",
		"Basic_A4",
	)
	// karpenterRestrictedVMSKUs are VMS SKUs that are known to
	// be problematic with karpenter-provider-azure.
	karpenterRestrictedVMSKUs = sets.New[string](
		"Standard_E64i_v3",
		"Standard_E64is_v3",
	)
)
