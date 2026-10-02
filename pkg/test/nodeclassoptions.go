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
	"fmt"

	"dario.cat/mergo"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/util/version"
)

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
// kubernetesVersion is used to determine which default values should be applied.
func WithVersionBasedDefaults(kubernetesVersion *version.Version) AKSNodeClassOption {
	// Image family default switches to Ubuntu 24.04 for Kubernetes versions 1.34.0 and above.
	imageFamilyOption := WithImageFamily(v1beta1.Ubuntu2204ImageFamily)
	if kubernetesVersion != nil && kubernetesVersion.AtLeast(version.MustParse("1.34.0")) {
		imageFamilyOption = WithImageFamily(v1beta1.Ubuntu2404ImageFamily)
	}

	// Combine all version-based default options into a single option.
	return WithAll(
		imageFamilyOption,
	)
}

// WithAll is a wrapper used to combine multiple AKSNodeClassOption functions into a single option.
func WithAll(options ...AKSNodeClassOption) AKSNodeClassOption {
	// Short circuit if there's only one option.
	if len(options) == 1 {
		return options[0]
	}

	return func(nodeClass *v1beta1.AKSNodeClass) {
		for _, option := range options {
			option(nodeClass)
		}
	}
}
