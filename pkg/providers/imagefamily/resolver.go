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

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	types "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/samber/lo"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

// Resolver resolves VM images for node provisioning.
type Resolver interface {
	ResolveNodeImageFromNodeClass(nodeClass *v1beta1.AKSNodeClass, instanceType *cloudprovider.InstanceType) (string, error)
}

// assert that defaultResolver implements Resolver interface
var _ Resolver = &defaultResolver{}

type defaultResolver struct {
	imageProvider        *provider
	instanceTypeProvider instancetype.Provider
}

// ImageFamily provides image metadata for a specific OS family.
type ImageFamily interface {
	Name() string
	// DefaultImages returns a list of default CommunityImage definitions for this ImageFamily.
	// Our Image Selection logic relies on the ordering of the default images to be ordered from most preferred to least, then we will select the latest image version available for that CommunityImage definition.
	// Our Release pipeline ensures all images are released together within 24 hours of each other for community image gallery, so selecting based on image feature priorities, then by date, and not vice-versa is acceptable.
	// If fipsMode is FIPSModeFIPS, only FIPS-enabled images will be returned
	DefaultImages(useSIG bool, fipsMode *v1beta1.FIPSMode) []types.DefaultImageOutput
}

// NewDefaultResolver constructs a new image Resolver
func NewDefaultResolver(_ client.Client, imageProvider *provider, instanceTypeProvider instancetype.Provider) *defaultResolver {
	return &defaultResolver{
		imageProvider:        imageProvider,
		instanceTypeProvider: instanceTypeProvider,
	}
}

// ResolveNodeImageFromNodeClass resolves the image ID for the given node class and instance type.
func (r *defaultResolver) ResolveNodeImageFromNodeClass(nodeClass *v1beta1.AKSNodeClass, instanceType *cloudprovider.InstanceType) (string, error) {
	nodeImages, err := nodeClass.GetImages()
	if err != nil {
		return "", err
	}
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

// MapToImageDistro maps an image ID to its distro string by matching against DefaultImages.
func MapToImageDistro(imageID string, fipsMode *v1beta1.FIPSMode, imageFamily ImageFamily, useSIG bool) (string, error) {
	var imageInfo types.DefaultImageOutput
	imageInfo.PopulateImageTraitsFromID(imageID)
	for _, defaultImage := range imageFamily.DefaultImages(useSIG, fipsMode) {
		if defaultImage.ImageDefinition == imageInfo.ImageDefinition {
			return defaultImage.Distro, nil
		}
	}
	return "", fmt.Errorf("no distro found for image id %s", imageID)
}

func getSupportedImages(familyName *string, fipsMode *v1beta1.FIPSMode, kubernetesVersion string, useSIG bool) []types.DefaultImageOutput {
	imageFamily := GetImageFamily(familyName, fipsMode, kubernetesVersion)
	return imageFamily.DefaultImages(useSIG, fipsMode)
}

// GetImageFamily returns the ImageFamily implementation for the given family name.
func GetImageFamily(familyName *string, fipsMode *v1beta1.FIPSMode, kubernetesVersion string) ImageFamily {
	switch lo.FromPtr(familyName) {
	case v1beta1.Ubuntu2204ImageFamily:
		return &Ubuntu2204{}
	case v1beta1.Ubuntu2404ImageFamily:
		return &Ubuntu2404{}
	case v1beta1.AzureLinuxImageFamily:
		if UseAzureLinux3(kubernetesVersion) {
			return &AzureLinux3{}
		}
		return &AzureLinux{}
	case v1beta1.UbuntuImageFamily:
		fallthrough
	default:
		return defaultUbuntu(fipsMode, kubernetesVersion)
	}
}

func defaultUbuntu(fipsMode *v1beta1.FIPSMode, kubernetesVersion string) ImageFamily {
	if lo.FromPtr(fipsMode) == v1beta1.FIPSModeFIPS {
		return &Ubuntu2004{}
	}
	if UseUbuntu2404(kubernetesVersion) {
		return &Ubuntu2404{}
	}
	return &Ubuntu2204{}
}
