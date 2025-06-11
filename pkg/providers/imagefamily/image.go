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
	types "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

// TODO: Remove this provider, by refactoring it into the nodeimage.go provider, and resolver.go as needed
// This is part of a shift to have the new controllers for k8s and node image version which populate status.
// At the current point the provisioning logic isn't update itself, just the status being populated.
// As part of the change to the actual provisioning, this provider will be refactored as mentioned above.
// The logic the nodeimage.go provider is dependent upon will refactor into its file, and the runtime logic for
// creation will refactor into resolver.go but dropping API retrievals for the data stored in the status instead.
type Provider struct {
	kubernetesVersionCache    *cache.Cache
	cm                        *pretty.ChangeMonitor
	location                  string
	kubernetesInterface       kubernetes.Interface
	imageCache                *cache.Cache
	nodeImagesCache           *cache.Cache
	imageVersionsClient       types.CommunityGalleryImageVersionsAPI
	subscription              string
	NodeImageVersions         types.NodeImageVersionsAPI
	NodeBootstrappingProvider types.NodeBootstrappingAPI
}

const (
	imageExpirationInterval    = time.Hour * 24 * 3
	imageCacheCleaningInterval = time.Hour * 1

	sharedImageGalleryImageIDFormat = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s/versions/%s"
	communityImageIDFormat          = "/CommunityGalleries/%s/images/%s/versions/%s"
)

func NewProvider(kubernetesInterface kubernetes.Interface, kubernetesVersionCache *cache.Cache, versionsClient types.CommunityGalleryImageVersionsAPI, location, subscription string, nodeImageVersionsClient types.NodeImageVersionsAPI, nodeBootstrappingClient types.NodeBootstrappingAPI) *Provider {
	return &Provider{
		kubernetesVersionCache:    kubernetesVersionCache,
		imageCache:                cache.New(imageExpirationInterval, imageCacheCleaningInterval),
		nodeImagesCache:           cache.New(imageExpirationInterval, imageCacheCleaningInterval),
		location:                  location,
		imageVersionsClient:       versionsClient,
		cm:                        pretty.NewChangeMonitor(),
		kubernetesInterface:       kubernetesInterface,
		subscription:              subscription,
		NodeImageVersions:         nodeImageVersionsClient,
		NodeBootstrappingProvider: nodeBootstrappingClient,
	}
}

// TODO (charliedmcb): refactor this into resolver.go
// resolveNodeImage returns Distro and Image ID for the given instance type. Images may vary due to architecture, accelerator, etc
//
// Preconditions:
// - nodeImages is sorted by priority order
func (r *defaultResolver) resolveNodeImage(nodeImages []v1beta1.NodeImage, instanceType *cloudprovider.InstanceType) (string, error) {
	// nodeImages are sorted by priority order, so we can return the first one that matches
	for _, availableImage := range nodeImages {
		if err := instanceType.Requirements.Compatible(
			scheduling.NewNodeSelectorRequirements(availableImage.Requirements...),
			v1beta1.AllowUndefinedWellKnownAndRestrictedLabels,
		); err == nil {
			return availableImage.ID, nil
		}
	}
	return "", fmt.Errorf("no compatible images found for instance type %s", instanceType.Name)
}

// TODO (charliedmcb): refactor this into nodeimage.go and create new provider
func (p *Provider) getCIGImageID(publicGalleryURL, communityImageName string) (string, error) {
	imageVersion, err := p.latestNodeImageVersionCommunity(publicGalleryURL, communityImageName)
	if err != nil {
		return "", err
	}
	return BuildImageIDCIG(publicGalleryURL, communityImageName, imageVersion), nil
}

// TODO (charliedmcb): refactor this into nodeimage.go and create new provider
func (p *Provider) latestNodeImageVersionCommunity(publicGalleryURL, communityImageName string) (string, error) {
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

// TODO (charliedmcb): refactor this into nodeimage.go and create new provider
func BuildImageIDCIG(publicGalleryURL, communityImageName, imageVersion string) string {
	return fmt.Sprintf(communityImageIDFormat, publicGalleryURL, communityImageName, imageVersion)
}

func (p *Provider) Reset() {
	p.imageCache.Flush()
	p.nodeImagesCache.Flush()
}
