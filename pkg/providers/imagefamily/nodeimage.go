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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	types "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

const (
	ImageExpirationInterval    = time.Hour * 24 * 3
	ImageCacheCleaningInterval = time.Hour * 1

	sharedImageGalleryImageIDFormat = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s/versions/%s"
	communityImageIDFormat          = "/CommunityGalleries/%s/images/%s/versions/%s"
)

type NodeImage struct {
	ID           string
	Requirements scheduling.Requirements
}

type NodeImageProvider interface {
	List(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) ([]NodeImage, error)
}

type provider struct {
	subscription string
	location     string

	imageVersionsClient types.CommunityGalleryImageVersionsAPI
	nodeImageVersions   types.NodeImageVersionsAPI

	nodeImagesCache *cache.Cache
	cm              *pretty.ChangeMonitor
}

func NewProvider(versionsClient types.CommunityGalleryImageVersionsAPI, location, subscription string, nodeImageVersionsClient types.NodeImageVersionsAPI, nodeImagesCache *cache.Cache) *provider {
	return &provider{
		subscription:        subscription,
		location:            location,
		imageVersionsClient: versionsClient,
		nodeImageVersions:   nodeImageVersionsClient,
		nodeImagesCache:     nodeImagesCache,
		cm:                  pretty.NewChangeMonitor(),
	}
}

// Returns the list of available NodeImages for the given AKSNodeClass sorted in priority ordering
func (p *provider) List(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) ([]NodeImage, error) {
	// TODO: refactor to be part of construction, since this is a karpenter setting and won't change across the process.
	useSIG := options.FromContext(ctx).UseSIG

	// CIG has no FIPS images; FIPS images can only be accessed through SIG
	// (this won't be an error since there just aren't any FIPS images for CIG)
	if lo.FromPtr(nodeClass.Spec.FIPSMode) == v1beta1.FIPSModeFIPS && !useSIG {
		return []NodeImage{}, nil
	}

	kubernetesVersion, err := nodeClass.GetKubernetesVersion()
	if err != nil {
		return []NodeImage{}, err
	}

	supportedImages := getSupportedImages(nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, kubernetesVersion, useSIG)

	key, err := p.cacheKey(
		supportedImages,
		kubernetesVersion,
	)
	if err != nil {
		return []NodeImage{}, err
	}

	if nodeImages, ok := p.nodeImagesCache.Get(key); ok {
		return nodeImages.([]NodeImage), nil
	}

	var nodeImages []NodeImage
	if useSIG {
		log.FromContext(ctx).V(1).Info("using SIG to list node images")
		nodeImages, err = p.listSIG(ctx, supportedImages)
		if err != nil {
			return []NodeImage{}, err
		}
	} else {
		nodeImages, err = p.listCIG(ctx, supportedImages)
		if err != nil {
			return []NodeImage{}, err
		}
	}
	p.nodeImagesCache.Set(key, nodeImages, cache.DefaultExpiration)

	return nodeImages, nil
}

func (p *provider) listSIG(ctx context.Context, supportedImages []types.DefaultImageOutput) ([]NodeImage, error) {
	nodeImages := []NodeImage{}
	retrievedLatestImages, err := p.nodeImageVersions.List(ctx, p.location, p.subscription)
	if err != nil {
		return nil, err
	}

	for _, supportedImage := range supportedImages {
		var nextImage *types.NodeImageVersion
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
		imageID := BuildImageIDSIG(options.FromContext(ctx).SIGSubscriptionID, supportedImage.GalleryResourceGroup, supportedImage.GalleryName, supportedImage.ImageDefinition, nextImage.Version)

		nodeImages = append(nodeImages, NodeImage{
			ID:           imageID,
			Requirements: supportedImage.Requirements,
		})
	}
	return nodeImages, nil
}

func (p *provider) listCIG(_ context.Context, supportedImages []types.DefaultImageOutput) ([]NodeImage, error) {
	nodeImages := []NodeImage{}
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

func (p *provider) cacheKey(supportedImages []types.DefaultImageOutput, k8sVersion string) (string, error) {
	// Note: the kubernetes version is part of the cache key here, because we bump images on kubernetes upgrade meaning
	// we want to ensure if there is a kubernetes change we'll get fresh images if there are any.
	hash, err := hashstructure.Hash([]interface{}{
		supportedImages,
		k8sVersion,
	}, hashstructure.FormatV2, &hashstructure.HashOptions{SlicesAsSets: true})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%016x", hash), nil
}

func (p *provider) getCIGImageID(publicGalleryURL, communityImageName string) (string, error) {
	imageVersion, err := p.latestNodeImageVersionCommunity(publicGalleryURL, communityImageName)
	if err != nil {
		return "", err
	}
	return BuildImageIDCIG(publicGalleryURL, communityImageName, imageVersion), nil
}

func (p *provider) latestNodeImageVersionCommunity(publicGalleryURL, communityImageName string) (string, error) {
	pager := p.imageVersionsClient.NewListPager(p.location, publicGalleryURL, communityImageName, nil)
	topImageVersionCandidate := armcompute.CommunityGalleryImageVersion{}
	for pager.More() {
		page, err := pager.NextPage(context.Background())
		if err != nil {
			return "", err
		}
		for _, imageVersion := range page.CommunityGalleryImageVersionList.Value {
			if lo.IsEmpty(topImageVersionCandidate) || imageVersion.Properties.PublishedDate.After(*topImageVersionCandidate.Properties.PublishedDate) {
				topImageVersionCandidate = *imageVersion
			}
		}
	}
	return lo.FromPtr(topImageVersionCandidate.Name), nil
}

// BuildImageIDCIG builds a Community Image Gallery image ID
func BuildImageIDCIG(publicGalleryURL, communityImageName, imageVersion string) string {
	return fmt.Sprintf(communityImageIDFormat, publicGalleryURL, communityImageName, imageVersion)
}

// BuildImageIDSIG builds a Shared Image Gallery image ID
func BuildImageIDSIG(subscriptionID, resourceGroup, galleryName, imageDefinition, imageVersion string) string {
	return fmt.Sprintf(sharedImageGalleryImageIDFormat, subscriptionID, resourceGroup, galleryName, imageDefinition, imageVersion)
}
