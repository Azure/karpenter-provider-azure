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

package imagefamily

import (
	"fmt"
	"strings"

	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

// marketplaceImageIDFormat is the Status.Images ID format for Azure Marketplace images. The trailing
// /Versions/{version} lets existing version-suffix handling (drift partial updates) apply unchanged.
const marketplaceImageIDFormat = "/Publishers/%s/ArtifactTypes/VMImage/Offers/%s/Skus/%s/Versions/%s"

// MarketplaceImage describes an Azure Marketplace image by publisher/offer/sku, with the scheduling
// requirements (architecture, Hyper-V generation) an instance type must satisfy to use it.
type MarketplaceImage struct {
	Publisher    string
	Offer        string
	SKU          string
	Requirements scheduling.Requirements
}

// GetMarketplaceImages maps an image family to Azure Marketplace images, ordered most preferred first
// (Gen2 amd64). The version is resolved separately to the latest available.
func GetMarketplaceImages(familyName *string, kubernetesVersion string) []MarketplaceImage {
	switch lo.FromPtr(familyName) {
	case v1beta1.Ubuntu2204ImageFamily:
		return ubuntuMarketplaceImages("ubuntu-22_04-lts")
	case v1beta1.AzureLinuxImageFamily:
		if UseAzureLinux3(kubernetesVersion) {
			return azureLinux3MarketplaceImages()
		}
		return azureLinux2MarketplaceImages()
	case v1beta1.UbuntuImageFamily, v1beta1.Ubuntu2404ImageFamily:
		fallthrough
	default:
		return ubuntuMarketplaceImages("ubuntu-24_04-lts")
	}
}

// ubuntuMarketplaceImages returns the marketplace images for an Ubuntu offer.
func ubuntuMarketplaceImages(offer string) []MarketplaceImage {
	return []MarketplaceImage{
		{Publisher: "Canonical", Offer: offer, SKU: "server", Requirements: marketplaceImageRequirements(karpv1.ArchitectureAmd64, v1beta1.HyperVGenerationV2)},
		{Publisher: "Canonical", Offer: offer, SKU: "server-gen1", Requirements: marketplaceImageRequirements(karpv1.ArchitectureAmd64, v1beta1.HyperVGenerationV1)},
		{Publisher: "Canonical", Offer: offer, SKU: "server-arm64", Requirements: marketplaceImageRequirements(karpv1.ArchitectureArm64, v1beta1.HyperVGenerationV2)},
	}
}

// azureLinux3MarketplaceImages returns the marketplace images for Azure Linux 3.
func azureLinux3MarketplaceImages() []MarketplaceImage {
	return []MarketplaceImage{
		{Publisher: "MicrosoftCBLMariner", Offer: "azure-linux-3", SKU: "azure-linux-3-gen2", Requirements: marketplaceImageRequirements(karpv1.ArchitectureAmd64, v1beta1.HyperVGenerationV2)},
		{Publisher: "MicrosoftCBLMariner", Offer: "azure-linux-3", SKU: "azure-linux-3", Requirements: marketplaceImageRequirements(karpv1.ArchitectureAmd64, v1beta1.HyperVGenerationV1)},
		{Publisher: "MicrosoftCBLMariner", Offer: "azure-linux-3", SKU: "azure-linux-3-arm64", Requirements: marketplaceImageRequirements(karpv1.ArchitectureArm64, v1beta1.HyperVGenerationV2)},
	}
}

// azureLinux2MarketplaceImages returns the marketplace images for Azure Linux 2 (CBL-Mariner).
func azureLinux2MarketplaceImages() []MarketplaceImage {
	return []MarketplaceImage{
		{Publisher: "MicrosoftCBLMariner", Offer: "cbl-mariner", SKU: "cbl-mariner-2-gen2", Requirements: marketplaceImageRequirements(karpv1.ArchitectureAmd64, v1beta1.HyperVGenerationV2)},
		{Publisher: "MicrosoftCBLMariner", Offer: "cbl-mariner", SKU: "cbl-mariner-2", Requirements: marketplaceImageRequirements(karpv1.ArchitectureAmd64, v1beta1.HyperVGenerationV1)},
		{Publisher: "MicrosoftCBLMariner", Offer: "cbl-mariner", SKU: "cbl-mariner-2-arm64", Requirements: marketplaceImageRequirements(karpv1.ArchitectureArm64, v1beta1.HyperVGenerationV2)},
	}
}

func marketplaceImageRequirements(arch, hyperVGeneration string) scheduling.Requirements {
	return scheduling.NewRequirements(
		scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, arch),
		scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, hyperVGeneration),
	)
}

// BuildImageIDMarketplace builds the marketplace image ID used in Status.Images.
func BuildImageIDMarketplace(publisher, offer, sku, version string) string {
	return fmt.Sprintf(marketplaceImageIDFormat, publisher, offer, sku, version)
}

// IsMarketplaceImageID reports whether imageID is a marketplace image ID produced by BuildImageIDMarketplace.
func IsMarketplaceImageID(imageID string) bool {
	_, err := ParseMarketplaceImageID(imageID)
	return err == nil
}

// ParseMarketplaceImageID parses a BuildImageIDMarketplace image ID back into publisher/offer/sku/version.
func ParseMarketplaceImageID(imageID string) (MarketplaceImageReference, error) {
	parts := strings.Split(imageID, "/")
	// "", "Publishers", {publisher}, "ArtifactTypes", "VMImage", "Offers", {offer}, "Skus", {sku}, "Versions", {version}
	if len(parts) != 11 || parts[0] != "" ||
		parts[1] != "Publishers" || parts[3] != "ArtifactTypes" || parts[4] != "VMImage" ||
		parts[5] != "Offers" || parts[7] != "Skus" || parts[9] != "Versions" {
		return MarketplaceImageReference{}, fmt.Errorf("image ID %q is not a marketplace image ID", imageID)
	}
	return MarketplaceImageReference{
		Publisher: parts[2],
		Offer:     parts[6],
		SKU:       parts[8],
		Version:   parts[10],
	}, nil
}

// MarketplaceImageReference holds the components of a marketplace image ID.
type MarketplaceImageReference struct {
	Publisher string
	Offer     string
	SKU       string
	Version   string
}

// URN returns the standard Azure marketplace image URN (Publisher:Offer:Sku:Version), matching how a
// marketplace VM image reference renders into NodeClaim status (see utils.ImageReferenceToString).
func (m MarketplaceImageReference) URN() string {
	return fmt.Sprintf("%s:%s:%s:%s", m.Publisher, m.Offer, m.SKU, m.Version)
}
