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
	"errors"
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

// kubernetesVersionPolicy is the Kubernetes version window in which an explicitly
// version-pinned spec.imageFamily value is usable.
//
// The bounds follow the AKS node OS support windows documented at
// https://learn.microsoft.com/azure/aks/upgrade-os-version - a pinned OS version is
// only usable while AKS still publishes node images for it on the cluster's
// Kubernetes version.
type kubernetesVersionPolicy struct {
	description string
	// minimumVersion is the inclusive lower bound.
	minimumVersion semver.Version
	// maximumVersion is the exclusive upper bound, or nil when unbounded.
	maximumVersion *semver.Version
}

// permits reports whether version falls within [minimumVersion, maximumVersion).
func (p kubernetesVersionPolicy) permits(version semver.Version) bool {
	if version.LT(p.minimumVersion) {
		return false
	}

	return p.maximumVersion == nil || version.LT(*p.maximumVersion)
}

// kubernetesVersionPinnedImageFamilies is the single source of truth for which
// spec.imageFamily values name a specific OS version, and what Kubernetes version
// window each of them supports.
//
// Generic Ubuntu and an unset image family are intentionally excluded: their OS
// version is selected by the provisioning path, not pinned by the user.
var kubernetesVersionPinnedImageFamilies = map[string]kubernetesVersionPolicy{
	v1beta1.Ubuntu2204ImageFamily: {
		description:    "Ubuntu2204",
		minimumVersion: semver.MustParse("1.25.2"),
		maximumVersion: lo.ToPtr(semver.MustParse("1.37.0")),
	},
	// Technically this should have an upper bound of v1.38 as documented, but we need to first add both
	// Ubuntu2604 and v1.39 support first before adding that.
	// See https://learn.microsoft.com/en-us/azure/aks/upgrade-os-version#supported-os-versions
	v1beta1.Ubuntu2404ImageFamily: {
		description:    "Ubuntu2404",
		minimumVersion: semver.MustParse("1.32.0"),
	},
}

// kubernetesFIPSVersionPinnedImageFamilies is a similar source of truth for FIPS-specific bounds on image families.
var kubernetesFIPSVersionPinnedImageFamilies = map[string]kubernetesVersionPolicy{
	v1beta1.Ubuntu2204ImageFamily: {
		description:    "Ubuntu2204 FIPS",
		minimumVersion: semver.MustParse("1.25.2"),
		maximumVersion: lo.ToPtr(semver.MustParse("1.39.0")),
	},
}

// kubernetesVersionPolicyFor looks up the policy registered for the image family
// requested by spec.imageFamily, if any.
func kubernetesVersionPolicyFor(nodeClass *v1beta1.AKSNodeClass) (kubernetesVersionPolicy, bool) {
	usesFIPS := lo.FromPtr(nodeClass.Spec.FIPSMode) == v1beta1.FIPSModeFIPS
	if usesFIPS {
		if policy, found := kubernetesFIPSVersionPinnedImageFamilies[lo.FromPtr(nodeClass.Spec.ImageFamily)]; found {
			return policy, found
		}
	}

	policy, found := kubernetesVersionPinnedImageFamilies[lo.FromPtr(nodeClass.Spec.ImageFamily)]
	return policy, found
}

// ImageFamilyKubernetesVersionIncompatibleError indicates the image family
// explicitly requested by spec.imageFamily pins an Ubuntu version that the
// discovered cluster Kubernetes version does not support.
//
// This is a static, deterministic incompatibility: it can only change when the
// NodeClass spec changes or the cluster Kubernetes version changes, both of
// which trigger a fresh reconciliation.
type ImageFamilyKubernetesVersionIncompatibleError struct {
	// RequestedImageFamily is the value of spec.imageFamily that was validated.
	RequestedImageFamily string
	// RequestedKubernetesVersion is the discovered cluster Kubernetes version, as discovered (unparsed).
	RequestedKubernetesVersion string
	// policy is the Kubernetes version policy that was applied to the requested image family.
	policy kubernetesVersionPolicy
}

func (e *ImageFamilyKubernetesVersionIncompatibleError) Error() string {
	supportedRange := fmt.Sprintf(">= %s", e.policy.minimumVersion)
	if e.policy.maximumVersion != nil {
		supportedRange = fmt.Sprintf("%s and < %s", supportedRange, e.policy.maximumVersion)
	}
	return fmt.Sprintf(
		"requested image family %s is not supported with discovered Kubernetes version %q; supported range is %s",
		e.policy.description,
		e.RequestedKubernetesVersion,
		supportedRange,
	)
}

// ValidateImageFamilyCompatibility verifies that an explicitly version-pinned
// spec.imageFamily is supported by the discovered cluster Kubernetes version.
//
// Only families registered in kubernetesVersionPinnedImageFamilies (Ubuntu2204,
// Ubuntu2404) are in scope. The generic Ubuntu family, an unset image family,
// AzureLinux, and any future family are unrestricted by this helper. Image
// resolution remains the provisioning path's responsibility.
//
// It returns *ImageFamilyKubernetesVersionIncompatibleError for a pinned family
// outside its supported range.
func ValidateImageFamilyCompatibility(nodeClass *v1beta1.AKSNodeClass) error {
	if nodeClass == nil {
		return errors.New("AKSNodeClass is required to validate image family compatibility")
	}

	policy, found := kubernetesVersionPolicyFor(nodeClass)
	if !found {
		return nil
	}

	kubernetesVersion, err := nodeClass.GetKubernetesVersion()
	if err != nil {
		return fmt.Errorf("getting kubernetes version: %w", err)
	}

	version, err := semver.ParseTolerant(kubernetesVersion)
	if err != nil {
		return fmt.Errorf("malformed Kubernetes version %q: %w", kubernetesVersion, err)
	}

	// AKS build suffixes describe a released Kubernetes version, not a prerelease.
	// Keep genuine prereleases (alpha, beta, rc) subject to normal semver ordering.
	if len(version.Pre) > 0 && strings.HasPrefix(version.Pre[0].VersionStr, "aks") {
		version.Pre = nil
	}

	if policy.permits(version) {
		return nil
	}

	return &ImageFamilyKubernetesVersionIncompatibleError{
		RequestedImageFamily:       lo.FromPtr(nodeClass.Spec.ImageFamily),
		RequestedKubernetesVersion: kubernetesVersion,
		policy:                     policy,
	}
}
