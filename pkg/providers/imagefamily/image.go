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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"k8s.io/client-go/kubernetes"
	"knative.dev/pkg/logging"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

type Provider struct {
	kubernetesVersionCache *cache.Cache
	cm                     *pretty.ChangeMonitor
	location               string
	kubernetesInterface    kubernetes.Interface
	imageCache             *cache.Cache
	imageVersionsClient    CommunityGalleryImageVersionsAPI
	subscription           string
	NodeImageVersions      NodeImageVersionsAPI
}

const (
	kubernetesVersionCacheKey = "kubernetesVersion"

	// if imageVersion is equal to an "", then we will get the latest version
	autoUpgradeMode = ""

	imageExpirationInterval    = time.Hour * 24 * 3
	imageCacheCleaningInterval = time.Hour * 1

	sharedImageGalleryImageIDFormat = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s/versions/%s"
	communityImageIDFormat          = "/CommunityGalleries/%s/images/%s/versions/%s"
)

func NewProvider(kubernetesInterface kubernetes.Interface, kubernetesVersionCache *cache.Cache, versionsClient CommunityGalleryImageVersionsAPI, location, subscription string, nodeImageVersionsClient NodeImageVersionsAPI) *Provider {
	return &Provider{
		kubernetesVersionCache: kubernetesVersionCache,
		imageCache:             cache.New(imageExpirationInterval, imageCacheCleaningInterval),
		location:               location,
		imageVersionsClient:    versionsClient,
		cm:                     pretty.NewChangeMonitor(),
		kubernetesInterface:    kubernetesInterface,
		subscription:           subscription,
		NodeImageVersions:      nodeImageVersionsClient,
	}
}

func (p *Provider) KubeServerVersion(ctx context.Context) (string, error) {
	if version, ok := p.kubernetesVersionCache.Get(kubernetesVersionCacheKey); ok {
		return version.(string), nil
	}
	serverVersion, err := p.kubernetesInterface.Discovery().ServerVersion()
	if err != nil {
		return "", err
	}
	version := strings.TrimPrefix(serverVersion.GitVersion, "v") // v1.24.9 -> 1.24.9
	p.kubernetesVersionCache.SetDefault(kubernetesVersionCacheKey, version)
	if p.cm.HasChanged("kubernetes-version", version) {
		logging.FromContext(ctx).With("kubernetes-version", version).Debugf("discovered kubernetes version")
	}
	return version, nil
}

// Get returns Image ID for the given instance type. Images may vary due to architecture, accelerator, etc
func (p *Provider) Get(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass, instanceType *cloudprovider.InstanceType, imageFamily ImageFamily) (string, error) {
	defaultImages := imageFamily.DefaultImages()
	for _, defaultImage := range defaultImages {
		if err := instanceType.Requirements.Compatible(defaultImage.Requirements, v1alpha2.AllowUndefinedLabels); err == nil {
			return p.GetLatestImageID(ctx, defaultImage, nodeClass.Spec.GetImageVersion())
		}
	}

	return "", fmt.Errorf("no compatible images found for instance type %s", instanceType.Name)
}

// GetLatestImageID returns the latest image ID for the given image version and image specification
func (p *Provider) GetLatestImageID(ctx context.Context, defaultImage DefaultImageOutput, imageVersion string) (string, error) {
	// Managed Karpenter will use the AKS Managed Shared Image Galleries
	if options.FromContext(ctx).ManagedKarpenter {
		return p.getImageIDSIG(ctx, defaultImage, imageVersion)
	}
	// Self Hosted Karpenter will use the Community Image Galleries, which are public and have lower scaling limits
	return p.getImageIDCIG(ctx, defaultImage.PublicGalleryURL, defaultImage.ImageDefinition, imageVersion)
}

// getImageIDSIG retrieves the shared image gallery image URL in the format:
// /subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Compute/galleries/{galleryName}/images/{imageDefinition}/versions/{imageVersion}.
// If imageVersion is an empty string, it fetches the latest version of the image and caches it for imageExpirationInterval (3 days).
func (p *Provider) getImageIDSIG(ctx context.Context, imgStub DefaultImageOutput, imageVersion string) (string, error) {
	key := fmt.Sprintf(sharedImageGalleryImageIDFormat, options.FromContext(ctx).SIGSubscriptionID, imgStub.GalleryResourceGroup, imgStub.GalleryName, imgStub.ImageDefinition, imageVersion)
	if imageID, ok := p.imageCache.Get(key); ok {
		return imageID.(string), nil
	}
	if imageVersion == autoUpgradeMode {
		versions, err := p.NodeImageVersions.List(ctx, p.location, p.subscription)
		if err != nil {
			return "", err
		}
		for _, version := range versions.Values {
			imageID := fmt.Sprintf(sharedImageGalleryImageIDFormat, options.FromContext(ctx).SIGSubscriptionID, imgStub.GalleryResourceGroup, imgStub.GalleryName, version.SKU, version.Version)
			cacheKey := fmt.Sprintf(sharedImageGalleryImageIDFormat, options.FromContext(ctx).SIGSubscriptionID, imgStub.GalleryResourceGroup, imgStub.GalleryName, version.SKU, autoUpgradeMode)
			p.imageCache.Set(
				cacheKey,
				imageID, imageExpirationInterval)
		}
		// return the latest version of the image from the cache after we have caached all of the imageDefinitions
		if imageID, ok := p.imageCache.Get(key); ok {
			return imageID.(string), nil
		}
		return "", fmt.Errorf("failed to get the latest version of the image %s", imgStub.ImageDefinition)
	}
	// if the imageVersion is specified, then we will just return the imageID
	return fmt.Sprintf(sharedImageGalleryImageIDFormat, options.FromContext(ctx).SIGSubscriptionID, imgStub.GalleryResourceGroup, imgStub.GalleryName, imgStub.ImageDefinition, imageVersion), nil
}

// getImageCIG retrieves the community image gallery image URL in the format:
// /CommunityGalleries/{publicGalleryURL}/images/{communityImageName}/versions/{imageVersion}.
// If imageVersion is an empty string, it fetches the latest version of the image and caches it for imageExpirationInterval (3 days).
func (p *Provider) getImageIDCIG(ctx context.Context, publicGalleryURL, communityImageName, imageVersion string) (string, error) {
	key := fmt.Sprintf(communityImageIDFormat, publicGalleryURL, communityImageName, imageVersion)
	if imageID, ok := p.imageCache.Get(key); ok {
		return imageID.(string), nil
	}
	// otherwise resolve it
	if imageVersion == autoUpgradeMode {
		// Try getting the image version using the ListNodeImageVersions api
		versions, err := p.NodeImageVersions.List(context.Background(), p.location, p.subscription)
		if err == nil {
			for _, version := range versions.Values {
				// Do a GET Against the community gallery to see if the image id exists
				img, err := p.imageVersionsClient.Get(ctx, p.location, publicGalleryURL, version.SKU, version.Version, nil)
				if err == nil && lo.FromPtr(img.Identifier.UniqueID) != "" {
					cacheKey := fmt.Sprintf(communityImageIDFormat, publicGalleryURL, version.SKU, autoUpgradeMode)
					cacheValue := fmt.Sprintf(communityImageIDFormat, publicGalleryURL, version.SKU, version.Version)
					p.imageCache.Set(cacheKey, cacheValue, imageExpirationInterval)
				}
			}
		}
		// Check the cache to see if we added it above
		if imageID, ok := p.imageCache.Get(key); ok {
			return imageID.(string), nil
		}
		// Otherwise we want to use the legacy get logic
		imageVer, err := p.latestNodeImageVersionCommmunity(publicGalleryURL, communityImageName)
		if err != nil {
			return "", err
		}
		imageID := BuildImageIDCIG(publicGalleryURL, communityImageName, imageVer)
		p.imageCache.Set(key, imageID, imageExpirationInterval)
		return imageID, nil
	}
	// if the imageVersion is not set to autoUpgradeMode, then we will just return the imageID
	imageID := BuildImageIDCIG(publicGalleryURL, communityImageName, imageVersion)
	return imageID, nil
}

func (p *Provider) latestNodeImageVersionCommmunity(publicGalleryURL, communityImageName string) (string, error) {
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

func BuildImageIDCIG(publicGalleryURL, communityImageName, imageVersion string) string {
	return fmt.Sprintf(communityImageIDFormat, publicGalleryURL, communityImageName, imageVersion)
}
