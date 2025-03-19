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

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

type NodeImage struct {
	ID           string
	Requirements scheduling.Requirements
}

type NodeImages []NodeImage

type NodeImageProvider interface {
	List(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass) (NodeImages, error)
}

func (p *Provider) List(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass) (NodeImages, error) {
	supportedImages := getSupportedImages(nodeClass.Spec.ImageFamily)
	if options.FromContext(ctx).UseSIG {
		return p.listSIG(ctx, supportedImages)
	}

	return p.listCIG(ctx, supportedImages)
}

func (p *Provider) listSIG(ctx context.Context, supportedImages []DefaultImageOutput) (NodeImages, error) {
	nodeImages := NodeImages{}
	retrievedLatestImages, err := p.NodeImageVersions.List(ctx, p.location, p.subscription)
	if err != nil {
		return nil, err
	}

	for _, supportedImage := range supportedImages {
		var nextImage *NodeImageVersion
		for _, retrievedLatestImage := range retrievedLatestImages.Values {
			if supportedImage.ImageDefinition == retrievedLatestImage.SKU {
				nextImage = &retrievedLatestImage
				break
			}
		}
		if nextImage == nil {
			// Unable to find given image version
			continue
		}
		imageID := fmt.Sprintf(sharedImageGalleryImageIDFormat, options.FromContext(ctx).SIGSubscriptionID, supportedImage.GalleryResourceGroup, supportedImage.GalleryName, supportedImage.ImageDefinition, nextImage.Version)

		nodeImages = append(nodeImages, NodeImage{
			ID:           imageID,
			Requirements: supportedImage.Requirements,
		})
	}
	return nodeImages, nil
}

func (p *Provider) listCIG(_ context.Context, supportedImages []DefaultImageOutput) (NodeImages, error) {
	nodeImages := NodeImages{}
	for _, supportedImage := range supportedImages {
		cigImageID, err := p.getCIGImageID(supportedImage.PublicGalleryURL, supportedImage.ImageDefinition)
		if err != nil {
			return nil, err
		}

		nodeImages = append(nodeImages, NodeImage{
			ID:           cigImageID,
			Requirements: supportedImage.Requirements,
		})
	}
	return nodeImages, nil
}
