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

package fleet

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2022-08-01/compute" //nolint:staticcheck
	"github.com/Azure/skewer"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
)

func baseRequest() *FleetVMProvisionRequest {
	return &FleetVMProvisionRequest{
		NodeClaimName:     "test-claim-1",
		NodePoolName:      "default",
		CapacityType:      "on-demand",
		NetworkPlugin:     consts.NetworkPluginAzure,
		NetworkPluginMode: consts.NetworkPluginModeOverlay,
		MaxPods:           consts.DefaultOverlayMaxPods,
		AcceptableSKUs:    []string{"Standard_D4s_v3", "Standard_D8s_v3"},
		ResolvedSKUs: map[string]*skewer.SKU{
			"Standard_D2s_v3": testSKU("Standard_D2s_v3"),
			"Standard_D4s_v3": testSKU("Standard_D4s_v3"),
			"Standard_D8s_v3": testSKU("Standard_D8s_v3"),
		},
		AcceptableZones: []string{"2", "1"},
		Tags:            map[string]*string{"env": lo.ToPtr("test")},
		LaunchTemplate: &launchtemplate.Template{
			Tags:                 map[string]*string{"env": lo.ToPtr("test")},
			ImageID:              "/CommunityGalleries/gallery/images/image/versions/1.0.0",
			SubnetID:             "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet1",
			ScriptlessCustomData: "Y3VzdG9tZGF0YQ==",
			StorageProfileSizeGB: 128,
		},
		SSHPublicKey:   "ssh-rsa AAAAB3...",
		AdminUsername:  "azureuser",
		NodeIdentities: []string{"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id1"},
		NSG:            "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg1",
		Location:       "eastus",
	}
}

func testSKU(name string) *skewer.SKU {
	return &skewer.SKU{
		Name: lo.ToPtr(name),
		Capabilities: &[]compute.ResourceSkuCapabilities{
			{Name: lo.ToPtr(skewer.AcceleratedNetworking), Value: lo.ToPtr("True")},
			{Name: lo.ToPtr(skewer.EncryptionAtHost), Value: lo.ToPtr("True")},
		},
	}
}

func batchKey(req *FleetVMProvisionRequest) (string, error) {
	body, err := BuildFleetBody(req, 1, req.Tags)
	if err != nil {
		return "", err
	}
	return DetermineBatchKey(req.NodePoolName, body)
}

func TestDetermineBatchKey_BuildError(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.LaunchTemplate.EncryptionAtHost = lo.ToPtr(true)
	req.ResolvedSKUs = nil

	key, err := batchKey(req)

	g.Expect(err).To(MatchError("building Fleet body: no candidate SKUs support requested encryption at host"))
	g.Expect(key).To(BeEmpty())
}

func TestDetermineBatchKey_IPConfigurationCount(t *testing.T) {
	g := NewWithT(t)
	single := baseRequest()
	multiple := baseRequest()
	multiple.NetworkPluginMode = consts.NetworkPluginModeNone
	multiple.MaxPods = 30

	singleKey, err := batchKey(single)
	g.Expect(err).NotTo(HaveOccurred())
	multipleKey, err := batchKey(multiple)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(multipleKey).NotTo(Equal(singleKey))
	equivalent := baseRequest()
	equivalent.NetworkPluginMode = consts.NetworkPluginModeNone
	equivalent.MaxPods = 30
	equivalentKey, err := batchKey(equivalent)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(equivalentKey).To(Equal(multipleKey))

	multiple.MaxPods = 40
	differentCountKey, err := batchKey(multiple)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(differentCountKey).NotTo(Equal(multipleKey))

	single.MaxPods = 30
	overlayKey, err := batchKey(single)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(overlayKey).To(Equal(singleKey))
}

func TestDetermineBatchKey_FilteredCandidatesMatchEligibleSubset(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.LaunchTemplate.EncryptionAtHost = lo.ToPtr(true)
	req.ResolvedSKUs[req.AcceptableSKUs[0]].Capabilities = nil
	subset := baseRequest()
	subset.LaunchTemplate.EncryptionAtHost = lo.ToPtr(true)
	subset.AcceptableSKUs = []string{"Standard_D8s_v3"}

	key, err := batchKey(req)
	g.Expect(err).ToNot(HaveOccurred())
	subsetKey, err := batchKey(subset)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(key).To(Equal(subsetKey))
	g.Expect(req.AcceptableSKUs).To(Equal([]string{"Standard_D4s_v3", "Standard_D8s_v3"}))
}

func TestDetermineBatchKey_Deterministic(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	key1, err := batchKey(req)
	g.Expect(err).ToNot(HaveOccurred())

	key2, err := batchKey(req)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(key2).To(Equal(key1), "same inputs must produce same key")
}

func TestDetermineBatchKey_Format(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	key, err := batchKey(req)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(key).To(MatchRegexp(`^default/[0-9a-f]{16}$`))
}

func TestDetermineBatchKey_DifferentCapacityType(t *testing.T) {
	g := NewWithT(t)
	req1 := baseRequest()
	req1.CapacityType = "on-demand"

	req2 := baseRequest()
	req2.CapacityType = "spot"

	key1, err := batchKey(req1)
	g.Expect(err).ToNot(HaveOccurred())
	key2, err := batchKey(req2)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(key2).ToNot(Equal(key1), "different capacity types must produce different keys")
}

func TestDetermineBatchKey_DifferentSKUs(t *testing.T) {
	g := NewWithT(t)
	req1 := baseRequest()
	req1.AcceptableSKUs = []string{"Standard_D4s_v3"}

	req2 := baseRequest()
	req2.AcceptableSKUs = []string{"Standard_D8s_v3"}

	key1, err := batchKey(req1)
	g.Expect(err).ToNot(HaveOccurred())
	key2, err := batchKey(req2)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(key2).ToNot(Equal(key1), "different SKU sets must produce different keys")
}

func TestDetermineBatchKey_SKUOrderIrrelevant(t *testing.T) {
	g := NewWithT(t)
	req1 := baseRequest()
	req1.AcceptableSKUs = []string{"Standard_D8s_v3", "Standard_D4s_v3"}

	req2 := baseRequest()
	req2.AcceptableSKUs = []string{"Standard_D4s_v3", "Standard_D8s_v3"}

	key1, err := batchKey(req1)
	g.Expect(err).ToNot(HaveOccurred())
	key2, err := batchKey(req2)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(key2).To(Equal(key1), "SKU order must not affect the key")
}

func TestDetermineBatchKey_ZoneOrderIrrelevant(t *testing.T) {
	g := NewWithT(t)
	req1 := baseRequest()
	req1.AcceptableZones = []string{"3", "1", "2"}

	req2 := baseRequest()
	req2.AcceptableZones = []string{"1", "2", "3"}

	key1, err := batchKey(req1)
	g.Expect(err).ToNot(HaveOccurred())
	key2, err := batchKey(req2)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(key2).To(Equal(key1), "zone order must not affect the key")
}

func TestDetermineBatchKey_EncryptionAtHostAffectsKey(t *testing.T) {
	g := NewWithT(t)
	req1 := baseRequest()
	// nil EncryptionAtHost

	req2 := baseRequest()
	req2.LaunchTemplate.EncryptionAtHost = lo.ToPtr(true)

	key1, err := batchKey(req1)
	g.Expect(err).ToNot(HaveOccurred())
	key2, err := batchKey(req2)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(key2).ToNot(Equal(key1), "EncryptionAtHost nil vs true must produce different keys")
}

func TestDetermineBatchKey_NilFleetBody(t *testing.T) {
	g := NewWithT(t)
	_, err := DetermineBatchKey("pool", nil)
	g.Expect(err).To(HaveOccurred())
}

func TestDetermineBatchKey_LaunchTemplateAffectsKey(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*launchtemplate.Template)
	}{
		{
			name: "image version",
			mutate: func(template *launchtemplate.Template) {
				template.ImageID = "/CommunityGalleries/gallery/images/image/versions/2.0.0"
			},
		},
		{
			name: "nil vs false encryption at host",
			mutate: func(template *launchtemplate.Template) {
				template.EncryptionAtHost = lo.ToPtr(false)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			req1 := baseRequest()
			req2 := baseRequest()
			tt.mutate(req2.LaunchTemplate)

			key1, err := batchKey(req1)
			g.Expect(err).ToNot(HaveOccurred())
			key2, err := batchKey(req2)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(key2).ToNot(Equal(key1))
		})
	}
}

func TestDetermineBatchKey_SKUCapabilitiesAffectKey(t *testing.T) {
	for _, capability := range []string{skewer.AcceleratedNetworking, skewer.EncryptionAtHost} {
		t.Run(capability, func(t *testing.T) {
			g := NewWithT(t)
			req1 := baseRequest()
			req2 := baseRequest()
			req1.LaunchTemplate.EncryptionAtHost = lo.ToPtr(true)
			req2.LaunchTemplate.EncryptionAtHost = lo.ToPtr(true)
			sku := req2.ResolvedSKUs[req2.AcceptableSKUs[1]]
			for i := range *sku.Capabilities {
				if *(*sku.Capabilities)[i].Name == capability {
					(*sku.Capabilities)[i].Value = lo.ToPtr("False")
				}
			}

			key1, err := batchKey(req1)
			g.Expect(err).ToNot(HaveOccurred())
			key2, err := batchKey(req2)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(key2).ToNot(Equal(key1))
		})
	}
}

func TestDetermineBatchKey_NodeClaimNameDoesNotAffectKey(t *testing.T) {
	g := NewWithT(t)
	req1 := baseRequest()
	req1.NodeClaimName = "claim-a"

	req2 := baseRequest()
	req2.NodeClaimName = "claim-b"

	key1, err := batchKey(req1)
	g.Expect(err).ToNot(HaveOccurred())
	key2, err := batchKey(req2)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(key2).To(Equal(key1), "NodeClaimName is per-VM identity, must not affect batch key")
}

func TestDetermineBatchKey_TagsAffectKey(t *testing.T) {
	g := NewWithT(t)
	req1 := baseRequest()
	req2 := baseRequest()
	req2.Tags["env"] = lo.ToPtr("production")

	key1, err := batchKey(req1)
	g.Expect(err).NotTo(HaveOccurred())
	key2, err := batchKey(req2)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(key2).NotTo(Equal(key1))
}
