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
	"time"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/patrickmn/go-cache"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

// TODO: Remove this file, by refactoring remaining code into nodeimage.go (keeping for a follow up, as to not
// combine code change, and moving bulk in the same PR, for cleaniness of reviewing)
//
// This is part of a shift to have the new controllers for k8s and node image version which populate status.
// At the current point the provisioning logic isn't update itself, just the status being populated.
// As part of the change to the actual provisioning, this provider will be refactored as mentioned above.
// The logic the nodeimage.go provider is dependent upon will refactor into its file, and the runtime logic for
// creation will refactor into resolver.go but dropping API retrievals for the data stored in the status instead.
type Provider struct {
	subscription string
	location     string

	imageVersionsClient types.CommunityGalleryImageVersionsAPI
	nodeImageVersions   types.NodeImageVersionsAPI

	nodeImagesCache *cache.Cache
	cm              *pretty.ChangeMonitor
}

const (
	imageExpirationInterval    = time.Hour * 24 * 3
	imageCacheCleaningInterval = time.Hour * 1

	sharedImageGalleryImageIDFormat = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s/versions/%s"
	communityImageIDFormat          = "/CommunityGalleries/%s/images/%s/versions/%s"
)

func NewProvider(versionsClient types.CommunityGalleryImageVersionsAPI, location, subscription string, nodeImageVersionsClient types.NodeImageVersionsAPI) *Provider {
	return &Provider{
		subscription:        subscription,
		location:            location,
		imageVersionsClient: versionsClient,
		nodeImageVersions:   nodeImageVersionsClient,
		nodeImagesCache:     cache.New(imageExpirationInterval, imageCacheCleaningInterval),
		cm:                  pretty.NewChangeMonitor(),
	}
}

func (p *Provider) Reset() {
	p.nodeImagesCache.Flush()
}
