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
	"context"
	"fmt"
	"strings"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

type NodeImage struct {
	ID           string
	BaseID       string
	Version      string
	Requirements scheduling.Requirements
}

type NodeImages []NodeImage

type NodeImageProvider interface {
	List(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass) (NodeImages, error)
}

func (p *Provider) List(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass) (NodeImages, error) {
	supportedImages := getSupportedImages(nodeClass.Spec.ImageFamily)
	if options.FromContext(ctx).UseSIG {
		nodeImages := NodeImages{}
		retrievedLatestImages, err := p.NodeImageVersions.List(ctx, p.location, p.subscription)
		if err != nil {
			return nil, err
		}

		for _, nextSupportedImage := range supportedImages {
			var nextImage *NodeImageVersion
			for _, retrievedLatestImage := range retrievedLatestImages.Values {
				if nextSupportedImage.ImageDefinition == retrievedLatestImage.SKU {
					nextImage = &retrievedLatestImage
					break
				}
			}
			if nextImage == nil {
				// Unable to find given image version
				continue
			}
			imageID := fmt.Sprintf(sharedImageGalleryImageIDFormat, options.FromContext(ctx).SIGSubscriptionID, nextSupportedImage.GalleryResourceGroup, nextSupportedImage.GalleryName, nextSupportedImage.ImageDefinition, nextImage.Version)
			imageIDBase, _ := splitImageID(imageID)

			nodeImages = append(nodeImages, NodeImage{
				ID:           imageID,
				BaseID:       imageIDBase,
				Version:      nextImage.Version,
				Requirements: nextSupportedImage.Requirements,
			})
		}
		return nodeImages, nil
	}

	// CIG path
	nodeImages := NodeImages{}
	for _, nextSupportedImage := range supportedImages {
		cigImageID, err := p.getCIGImageID(nextSupportedImage.PublicGalleryURL, nextSupportedImage.ImageDefinition)
		if err != nil {
			return nil, err
		}
		baseID, version := splitImageID(cigImageID)

		nodeImages = append(nodeImages, NodeImage{
			ID:           cigImageID,
			BaseID:       baseID,
			Version:      version,
			Requirements: nextSupportedImage.Requirements,
		})
	}
	return nodeImages, nil
}

// Splits the imageID into components of:
// - baseID: imageID without the version suffix
// - version: version of the imageID
func splitImageID(imageID string) (baseID, version string) {
	imageIDParts := strings.Split(imageID, "/")
	baseID = strings.Join(imageIDParts[0:len(imageIDParts)-2], "/")
	version = imageIDParts[len(imageIDParts)-1]
	return baseID, version
}
