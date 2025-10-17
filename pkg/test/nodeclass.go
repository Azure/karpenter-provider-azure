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

package test

import (
	"context"
	"fmt"
	"sort"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	imagefamilytypes "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	opstatus "github.com/awslabs/operatorpkg/status"
	"github.com/blang/semver/v4"
	"github.com/imdario/mergo"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

const (
	DefaultCIGImageVersion = "202501.02.0"
	DefaultSIGImageVersion = "202410.09.0"
)

func AKSNodeClass(overrides ...v1beta1.AKSNodeClass) *v1beta1.AKSNodeClass {
	options := v1beta1.AKSNodeClass{}
	for _, override := range overrides {
		if err := mergo.Merge(&options, override, mergo.WithOverride); err != nil {
			panic(fmt.Sprintf("Failed to merge settings: %s", err))
		}
	}
	// In reality, these default values will be set via the defaulting done by the API server. The reason we provide them here is
	// we sometimes reference a test.AKSNodeClass without applying it, and in that case we need to set the default values ourselves
	if options.Spec.OSDiskSizeGB == nil {
		options.Spec.OSDiskSizeGB = lo.ToPtr[int32](128)
	}
	if options.Spec.ImageFamily == nil {
		options.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)
	}
	return &v1beta1.AKSNodeClass{
		ObjectMeta: coretest.ObjectMeta(options.ObjectMeta),
		Spec:       options.Spec,
		Status:     options.Status,
	}
}

// TODO: Pass in test.Options if we want to use more options within this func
func ApplyDefaultStatus(nodeClass *v1beta1.AKSNodeClass, env *coretest.Environment, useSIG bool) {
	if useSIG {
		ApplySIGImages(nodeClass)
	} else {
		ApplyCIGImages(nodeClass)
	}
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)

	testK8sVersion := lo.Must(semver.ParseTolerant(lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String())).String()
	nodeClass.Status.KubernetesVersion = testK8sVersion
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
	nodeClass.StatusConditions().SetTrue(opstatus.ConditionReady)
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeSubnetsReady)

	conditions := []opstatus.Condition{}
	for _, condition := range nodeClass.GetConditions() {
		// Using the magic number 1, as it appears the Generation is always equal to 1 on the NodeClass in testing. If that appears to not be the case,
		// than we should add some function for allows bumps as needed to match.
		condition.ObservedGeneration = 1
		conditions = append(conditions, condition)
	}
	nodeClass.SetConditions(conditions)
}

func ApplyCIGImages(nodeClass *v1beta1.AKSNodeClass) {
	ApplyCIGImagesWithVersion(nodeClass, DefaultCIGImageVersion)
}

func ApplyCIGImagesWithVersion(nodeClass *v1beta1.AKSNodeClass, cigImageVersion string) {
	nodeClass.Status.Images = []v1beta1.NodeImage{
		{
			ID: fmt.Sprintf("/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2containerd/versions/%s", cigImageVersion),
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      corev1.LabelArchStable,
					Operator: "In",
					Values:   []string{"amd64"},
				},
				{
					Key:      v1beta1.LabelSKUHyperVGeneration,
					Operator: "In",
					Values:   []string{"2"},
				},
			},
		},
		{
			ID: fmt.Sprintf("/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204containerd/versions/%s", cigImageVersion),
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      corev1.LabelArchStable,
					Operator: "In",
					Values:   []string{"amd64"},
				},
				{
					Key:      v1beta1.LabelSKUHyperVGeneration,
					Operator: "In",
					Values:   []string{"1"},
				},
			},
		},
		{
			ID: fmt.Sprintf("/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2arm64containerd/versions/%s", cigImageVersion),
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      corev1.LabelArchStable,
					Operator: "In",
					Values:   []string{"arm64"},
				},
				{
					Key:      v1beta1.LabelSKUHyperVGeneration,
					Operator: "In",
					Values:   []string{"2"},
				},
			},
		},
	}
}

func ApplySIGImages(nodeClass *v1beta1.AKSNodeClass) {
	ApplySIGImagesWithVersion(nodeClass, DefaultSIGImageVersion)
}

func ApplySIGImagesWithVersion(nodeClass *v1beta1.AKSNodeClass, sigImageVersion string) {
	imageFamilyNodeImages := getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, sigImageVersion, nodeClass.Status.KubernetesVersion)
	nodeClass.Status.Images = translateToStatusNodeImages(imageFamilyNodeImages)
}

func getExpectedTestSIGImages(imageFamily string, fipsMode *v1beta1.FIPSMode, version string, kubernetesVersion string) []imagefamily.NodeImage {
	var images []imagefamilytypes.DefaultImageOutput
	if imageFamily == v1beta1.Ubuntu2204ImageFamily {
		images = imagefamily.Ubuntu2204{}.DefaultImages(true, fipsMode)
	} else if imageFamily == v1beta1.AzureLinuxImageFamily {
		if imagefamily.UseAzureLinux3(kubernetesVersion) {
			images = imagefamily.AzureLinux3{}.DefaultImages(true, fipsMode)
		} else {
			images = imagefamily.AzureLinux{}.DefaultImages(true, fipsMode)
		}
	}
	nodeImages := []imagefamily.NodeImage{}
	for _, image := range images {
		nodeImages = append(nodeImages, imagefamily.NodeImage{
			ID:           fmt.Sprintf("/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s/versions/%s", image.GalleryResourceGroup, image.GalleryName, image.ImageDefinition, version),
			Requirements: image.Requirements,
		})
	}
	return nodeImages
}

func translateToStatusNodeImages(imageFamilyNodeImages []imagefamily.NodeImage) []v1beta1.NodeImage {
	return lo.Map(imageFamilyNodeImages, func(nodeImage imagefamily.NodeImage, _ int) v1beta1.NodeImage {
		reqs := lo.Map(nodeImage.Requirements.NodeSelectorRequirements(), func(item karpv1.NodeSelectorRequirementWithMinValues, _ int) corev1.NodeSelectorRequirement {
			return item.NodeSelectorRequirement
		})

		// sorted for consistency
		sort.Slice(reqs, func(i, j int) bool {
			if len(reqs[i].Key) != len(reqs[j].Key) {
				return len(reqs[i].Key) < len(reqs[j].Key)
			}
			return reqs[i].Key < reqs[j].Key
		})
		return v1beta1.NodeImage{
			ID:           nodeImage.ID,
			Requirements: reqs,
		}
	})
}

func AKSNodeClassFieldIndexer(ctx context.Context) func(cache.Cache) error {
	return func(c cache.Cache) error {
		return c.IndexField(ctx, &karpv1.NodeClaim{}, "spec.nodeClassRef.name", func(obj client.Object) []string {
			nc := obj.(*karpv1.NodeClaim)
			if nc.Spec.NodeClassRef == nil {
				return []string{""}
			}
			return []string{nc.Spec.NodeClassRef.Name}
		})
	}
}
