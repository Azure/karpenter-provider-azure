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

// Get returns Distro and Image ID for the given instance type. Images may vary due to architecture, accelerator, etc
func (p *Provider) Get(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass, instanceType *cloudprovider.InstanceType, imageFamily ImageFamily) (string, string, error) {
	defaultImages := imageFamily.DefaultImages()
	for _, defaultImage := range defaultImages {
		if err := instanceType.Requirements.Compatible(defaultImage.Requirements, v1alpha2.AllowUndefinedWellKnownAndRestrictedLabels); err == nil {
			imageID, imageRetrievalErr := p.GetLatestImageID(ctx, defaultImage)
			return defaultImage.Distro, imageID, imageRetrievalErr
		}
	}

	return "", "", fmt.Errorf("no compatible images found for instance type %s", instanceType.Name)
}

func (p *Provider) GetLatestImageID(ctx context.Context, defaultImage DefaultImageOutput) (string, error) {
	// Managed Karpenter will use the AKS Managed Shared Image Galleries
	if options.FromContext(ctx).UseSIG {
		return p.getImageIDSIG(ctx, defaultImage)
	}
	// Self Hosted Karpenter will use the Community Image Galleries, which are public and have lower scaling limits
	return p.getImageIDCIG(ctx, defaultImage.PublicGalleryURL, defaultImage.ImageDefinition)
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

func (p *Provider) getImageIDSIG(ctx context.Context, imgStub DefaultImageOutput) (string, error) {
	key := fmt.Sprintf(sharedImageGalleryImageIDFormat, options.FromContext(ctx).SIGSubscriptionID, imgStub.GalleryResourceGroup, imgStub.GalleryName, imgStub.ImageDefinition)
	if imageID, ok := p.imageCache.Get(key); ok {
		return imageID.(string), nil
	}
	versions, err := p.NodeImageVersions.List(ctx, p.location, p.subscription)
	if err != nil {
		return "", err
	}
	for _, version := range versions.Values {
		imageID := fmt.Sprintf(sharedImageGalleryImageIDFormat, options.FromContext(ctx).SIGSubscriptionID, imgStub.GalleryResourceGroup, imgStub.GalleryName, imgStub.ImageDefinition, version.Version)
		p.imageCache.Set(key, imageID, imageExpirationInterval)
	}
	// return the latest version of the image from the cache after we have caached all of the imageDefinitions
	if imageID, ok := p.imageCache.Get(key); ok {
		return imageID.(string), nil
	}
	return "", fmt.Errorf("failed to get the latest version of the image %s", imgStub.ImageDefinition)
}

func (p *Provider) getImageIDCIG(ctx context.Context, publicGalleryURL, communityImageName string) (string, error) {
	key := fmt.Sprintf(communityImageIDFormat, publicGalleryURL, communityImageName)
	if imageID, ok := p.imageCache.Get(key); ok {
		return imageID.(string), nil
	}
	// if the image is not found in the cache, we will refresh the lookup for it
	imageVersion, err := p.latestNodeImageVersionCommmunity(publicGalleryURL, communityImageName)
	if err != nil {
		return "", err
	}
	imageID := BuildImageIDCIG(publicGalleryURL, communityImageName, imageVersion)
	p.imageCache.Set(key, imageID, imageExpirationInterval)
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
