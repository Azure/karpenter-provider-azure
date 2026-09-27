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

	"dario.cat/mergo"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	opstatus "github.com/awslabs/operatorpkg/status"
	"github.com/blang/semver/v4"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/version"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

const (
	DefaultCIGImageVersion = "202501.02.0"
	DefaultSIGImageVersion = "202410.09.0"
)

func AKSNodeClass(options ...AKSNodeClassOption) *v1beta1.AKSNodeClass {
	result := v1beta1.AKSNodeClass{}
	for _, applyOption := range options {
		applyOption(&result)
	}

	return &v1beta1.AKSNodeClass{
		ObjectMeta: coretest.ObjectMeta(result.ObjectMeta),
		Spec:       result.Spec,
		Status:     result.Status,
	}
}

// AKSNodeClassOption is a function that applies modifications to an AKSNodeClass,
// allowing easy configuration of new instances in tests.
type AKSNodeClassOption func(*v1beta1.AKSNodeClass)

// WithName sets the Name field of the AKSNodeClass.
func WithName(name string) AKSNodeClassOption {
	return func(nodeClass *v1beta1.AKSNodeClass) {
		nodeClass.Name = name
	}
}

// WithOSDiskSizeGB sets the OSDiskSizeGB field of the AKSNodeClass.
func WithOSDiskSizeGB(size int32) AKSNodeClassOption {
	return func(nodeClass *v1beta1.AKSNodeClass) {
		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr(size)
	}
}

// WithImageFamily sets the ImageFamily field of the AKSNodeClass.
func WithImageFamily(imageFamily string) AKSNodeClassOption {
	return func(nodeClass *v1beta1.AKSNodeClass) {
		nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
	}
}

// WithTags sets the Tags field of the AKSNodeClass.
// tags is a sequence of key-value pairs representing the tags to be applied to the AKSNodeClass.
func WithTags(pairs ...string) AKSNodeClassOption {
	if len(pairs)%2 != 0 {
		panic("WithTags requires an even number of arguments representing key-value pairs")
	}

	return func(nodeClass *v1beta1.AKSNodeClass) {
		tags := map[string]string{}
		for i := 0; i < len(pairs); i += 2 {
			tags[pairs[i]] = pairs[i+1]
		}

		nodeClass.Spec.Tags = tags
	}
}

// WithMergedSpec merges the provided spec into the AKSNodeClass, overriding existing fields.
// Non-zero fields in the provided spec will overwrite the corresponding fields in the AKSNodeClass.
func WithMergedSpec(spec v1beta1.AKSNodeClassSpec) AKSNodeClassOption {
	return func(nodeClass *v1beta1.AKSNodeClass) {
		if err := mergo.Merge(&nodeClass.Spec, spec, mergo.WithOverride); err != nil {
			panic(fmt.Sprintf("Failed to merge spec: %s", err))
		}
	}
}

// WithVersionBasedDefaults applies default values to the AKSNodeClass based on the provided Kubernetes version.
// For now, the version is ignored, but we can use it in the future.
func WithVersionBasedDefaults(kubernetesVersion *version.Version) AKSNodeClassOption {
	imageFamily := v1beta1.Ubuntu2204ImageFamily

	if kubernetesVersion != nil && kubernetesVersion.AtLeast(version.MustParse("1.34.0")) {
		imageFamily = v1beta1.Ubuntu2404ImageFamily
	}

	return func(nodeClass *v1beta1.AKSNodeClass) {
		nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
	}
}

// WithAll is a wrapper used internally to combine multiple AKSNodeClassOption functions into a single option.
func WithAll(options ...AKSNodeClassOption) AKSNodeClassOption {
	return func(nodeClass *v1beta1.AKSNodeClass) {
		for _, option := range options {
			option(nodeClass)
		}
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
	nodeClass.Status.KubernetesVersion = lo.ToPtr(testK8sVersion)
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
	nodeClass.StatusConditions().SetTrue(opstatus.ConditionReady)
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeSubnetsReady)
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeValidationSucceeded)
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)

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
	var kubernetesVersion string
	if nodeClass.Status.KubernetesVersion != nil {
		kubernetesVersion = *nodeClass.Status.KubernetesVersion
	}
	imageFamilyNodeImages := getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, nodeClass.IsTrustedLaunchEnabled(), nodeClass.IsKataEnabled(), sigImageVersion, kubernetesVersion)
	nodeClass.Status.Images = translateToStatusNodeImages(imageFamilyNodeImages)
}

func getExpectedTestSIGImages(imageFamily string, fipsMode *v1beta1.FIPSMode, trustedLaunch bool, kataEnabled bool, version string, kubernetesVersion string) []imagefamily.NodeImage {
	images := imagefamily.GetImageFamily(&imageFamily, fipsMode, trustedLaunch, kubernetesVersion, nil).DefaultImages(true, fipsMode, trustedLaunch, kataEnabled)
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
			return corev1.NodeSelectorRequirement{Key: item.Key, Operator: item.Operator, Values: item.Values}
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
