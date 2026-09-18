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

// kubernetesVersionPolicy is the Kubernetes version window in which one explicitly
// version-pinned spec.imageFamily value is usable.
//
// The bounds follow the AKS node OS support windows documented at
// https://learn.microsoft.com/azure/aks/upgrade-os-version - a pinned OS version is
// only usable while AKS still publishes node images for it on the cluster's
// Kubernetes version.
type kubernetesVersionPolicy struct {
	// minimumVersion is the inclusive lower bound.
	minimumVersion semver.Version
	// maximumVersion is the exclusive upper bound, or nil when unbounded.
	maximumVersion *semver.Version
	// fipsMaximumVersion is the exclusive upper bound applied instead of maximumVersion
	// when FIPS is requested, or nil when FIPS does not change the upper bound.
	fipsMaximumVersion *semver.Version
}

// upperBound returns the exclusive upper bound to apply, and reports whether the
// FIPS-specific bound was the one applied. When no FIPS-specific bound is registered,
// FIPS does not change the reported range, so it is not flagged on the error either.
func (p kubernetesVersionPolicy) upperBound(fips bool) (maximumVersion *semver.Version, fipsApplied bool) {
	if fips && p.fipsMaximumVersion != nil {
		return lo.ToPtr(*p.fipsMaximumVersion), true
	}
	if p.maximumVersion == nil {
		return nil, false
	}
	return lo.ToPtr(*p.maximumVersion), false
}

// permits reports whether version falls within [minimumVersion, maximumVersion).
func (p kubernetesVersionPolicy) permits(version semver.Version, maximumVersion *semver.Version) bool {
	if version.LT(p.minimumVersion) {
		return false
	}
	return maximumVersion == nil || version.LT(*maximumVersion)
}

// kubernetesVersionPinnedImageFamilies is the single source of truth for which
// spec.imageFamily values name a specific OS version, and what Kubernetes version
// window each of them supports. Both RequiresKubernetesVersionCompatibility and
// ValidateImageFamilyCompatibility are derived from it, so a family cannot be
// treated as pinned without having bounds, or be bounded without being treated as
// pinned.
//
// Validation is deliberately limited to the entries here: the generic "Ubuntu" family
// (and an unset spec.imageFamily) is a rolling contract to run "an AKS-supported
// Ubuntu", resolved per Kubernetes version by the provisioning path - the image
// resolver for VM-based provisioning, and AKS itself for AKS Machine API provisioning
// - so there is no version range we could enforce here without contradicting that
// resolution. AzureLinux is likewise unpinned.
var kubernetesVersionPinnedImageFamilies = map[string]kubernetesVersionPolicy{
	v1beta1.Ubuntu2204ImageFamily: {
		minimumVersion:     semver.MustParse("1.25.2"),
		maximumVersion:     lo.ToPtr(semver.MustParse("1.37.0")),
		fipsMaximumVersion: lo.ToPtr(semver.MustParse("1.39.0")),
	},
	v1beta1.Ubuntu2404ImageFamily: {
		// Ubuntu2404 has no upper bound, and no FIPS-specific bound today.
		minimumVersion: semver.MustParse("1.32.0"),
	},
}

// kubernetesVersionPolicyFor looks up the policy registered for the image family
// requested by spec.imageFamily, if any.
func kubernetesVersionPolicyFor(nodeClass *v1beta1.AKSNodeClass) (kubernetesVersionPolicy, bool) {
	if nodeClass == nil {
		return kubernetesVersionPolicy{}, false
	}
	policy, found := kubernetesVersionPinnedImageFamilies[lo.FromPtr(nodeClass.Spec.ImageFamily)]
	return policy, found
}

// RequiresKubernetesVersionCompatibility reports whether the image family requested by
// spec.imageFamily is explicitly version pinned, and so needs the discovered cluster
// Kubernetes version in order to be validated.
//
// Callers use this to avoid making Kubernetes version readiness a precondition for
// NodeClasses that no compatibility policy applies to: for those, an unavailable or
// malformed Kubernetes version must not block the rest of validation.
func RequiresKubernetesVersionCompatibility(nodeClass *v1beta1.AKSNodeClass) bool {
	_, found := kubernetesVersionPolicyFor(nodeClass)
	return found
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
	// KubernetesVersion is the discovered cluster Kubernetes version, as discovered (unparsed).
	KubernetesVersion string
	// FIPS reports whether the FIPS-specific bounds were applied.
	FIPS bool
	// MinimumVersion is the inclusive lower bound for the requested image family.
	MinimumVersion semver.Version
	// MaximumVersion is the exclusive upper bound for the requested image family, or nil when unbounded.
	MaximumVersion *semver.Version
}

func (e *ImageFamilyKubernetesVersionIncompatibleError) Error() string {
	fipsSuffix := ""
	if e.FIPS {
		fipsSuffix = " with FIPS"
	}
	supportedRange := fmt.Sprintf(">= %s", e.MinimumVersion)
	if e.MaximumVersion != nil {
		supportedRange = fmt.Sprintf("%s and < %s", supportedRange, e.MaximumVersion)
	}
	return fmt.Sprintf(
		"requested image family %q%s is not supported with discovered Kubernetes version %q; supported range is %s",
		e.RequestedImageFamily,
		fipsSuffix,
		e.KubernetesVersion,
		supportedRange,
	)
}

// ValidateImageFamilyCompatibility verifies that an explicitly version-pinned
// spec.imageFamily is supported by the discovered cluster Kubernetes version.
//
// Only families registered in kubernetesVersionPinnedImageFamilies (Ubuntu2204,
// Ubuntu2404) are in scope. The generic Ubuntu family, an unset image family,
// AzureLinux, and any future family are unrestricted by this helper: what they
// resolve to is the resolver's (and, for AKS Machine API provisioning, the AKS
// RP's) decision, and that decision is expected to remain valid across Kubernetes
// versions.
//
// It returns *ImageFamilyKubernetesVersionIncompatibleError for a pinned family
// outside its supported range.
func ValidateImageFamilyCompatibility(nodeClass *v1beta1.AKSNodeClass, kubernetesVersion string) error {
	if nodeClass == nil {
		return errors.New("AKSNodeClass is required to validate image family compatibility")
	}

	policy, found := kubernetesVersionPolicyFor(nodeClass)
	if !found {
		return nil
	}

	version, err := parseKubernetesVersionTolerant(kubernetesVersion)
	if err != nil {
		return err
	}

	fips := lo.FromPtr(nodeClass.Spec.FIPSMode) == v1beta1.FIPSModeFIPS
	maximumVersion, fipsApplied := policy.upperBound(fips)
	if policy.permits(version, maximumVersion) {
		return nil
	}

	return &ImageFamilyKubernetesVersionIncompatibleError{
		RequestedImageFamily: lo.FromPtr(nodeClass.Spec.ImageFamily),
		KubernetesVersion:    kubernetesVersion,
		FIPS:                 fipsApplied,
		MinimumVersion:       policy.minimumVersion,
		MaximumVersion:       maximumVersion,
	}
}

func parseKubernetesVersionTolerant(kubernetesVersion string) (semver.Version, error) {
	version, err := semver.ParseTolerant(strings.TrimPrefix(kubernetesVersion, "v"))
	if err != nil {
		return semver.Version{}, fmt.Errorf(
			"malformed discovered Kubernetes version %q: expected a semantic version like 1.32.0: %w",
			kubernetesVersion,
			err,
		)
	}
	return version, nil
}
