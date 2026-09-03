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
	"strings"

	"github.com/blang/semver/v4"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

// Kubernetes version bounds for the version-pinned Ubuntu image families.
//
// These follow the AKS node OS support windows documented at
// https://learn.microsoft.com/azure/aks/upgrade-os-version - a pinned OS version
// is only usable while AKS still publishes node images for it on the cluster's
// Kubernetes version.
//
// The bounds apply *only* when spec.imageFamily explicitly pins an Ubuntu
// version. The generic "Ubuntu" family (and leaving spec.imageFamily unset) is a
// contract to run "an AKS-supported Ubuntu", which is resolved per Kubernetes
// version - and may be resolved differently by different provisioning modes - so
// it is intentionally never rejected here.
var (
	ubuntu2204MinimumVersion     = semver.MustParse("1.25.2")
	ubuntu2204MaximumVersion     = semver.MustParse("1.37.0")
	ubuntu2204FIPSMaximumVersion = semver.MustParse("1.39.0")
	ubuntu2404MinimumVersion     = semver.MustParse("1.32.0")
)

// versionPinnedImageFamilies are the spec.imageFamily values that name a specific OS
// version, and are therefore the only families whose usability depends on the cluster's
// Kubernetes version.
//
// Validation is deliberately limited to these: the generic "Ubuntu" family (and an unset
// spec.imageFamily) is a rolling contract to run "an AKS-supported Ubuntu", resolved per
// Kubernetes version by the provisioning path - the image resolver for VM-based
// provisioning, and AKS itself for AKS Machine API provisioning - so there is no version
// range we could enforce here without contradicting that resolution. AzureLinux is
// likewise unpinned.
var versionPinnedImageFamilies = []string{
	v1beta1.Ubuntu2204ImageFamily,
	v1beta1.Ubuntu2404ImageFamily,
}

// RequiresKubernetesVersionCompatibility reports whether the image family requested by
// spec.imageFamily is explicitly version pinned, and so needs the discovered cluster
// Kubernetes version in order to be validated.
//
// Callers use this to avoid making Kubernetes version readiness a precondition for
// NodeClasses that no compatibility policy applies to: for those, an unavailable or
// malformed Kubernetes version must not block the rest of validation.
func RequiresKubernetesVersionCompatibility(nodeClass *v1beta1.AKSNodeClass) bool {
	if nodeClass == nil {
		return false
	}
	return lo.Contains(versionPinnedImageFamilies, lo.FromPtr(nodeClass.Spec.ImageFamily))
}

// MalformedDiscoveredKubernetesVersionError indicates the discovered cluster
// Kubernetes version could not be parsed as a semantic version.
type MalformedDiscoveredKubernetesVersionError struct {
	kubernetesVersion string
	cause             error
}

func (e *MalformedDiscoveredKubernetesVersionError) Error() string {
	return fmt.Sprintf("malformed discovered Kubernetes version %q: expected a semantic version like 1.32.0: %v", e.kubernetesVersion, e.cause)
}

func (e *MalformedDiscoveredKubernetesVersionError) Unwrap() error {
	return e.cause
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
// Only explicitly pinned Ubuntu families (Ubuntu2204, Ubuntu2404) are in scope.
// The generic Ubuntu family, an unset image family, AzureLinux, and any future
// family are unrestricted by this helper: what they resolve to is the
// resolver's (and, for AKS Machine API provisioning, the AKS RP's) decision, and
// that decision is expected to remain valid across Kubernetes versions.
//
// It returns *ImageFamilyKubernetesVersionIncompatibleError for a pinned family
// outside its supported range, and *MalformedDiscoveredKubernetesVersionError
// when the discovered version cannot be parsed.
func ValidateImageFamilyCompatibility(nodeClass *v1beta1.AKSNodeClass, kubernetesVersion string) error {
	if nodeClass == nil {
		return fmt.Errorf("AKSNodeClass is required to validate image family compatibility")
	}

	requestedImageFamily := lo.FromPtr(nodeClass.Spec.ImageFamily)
	if !RequiresKubernetesVersionCompatibility(nodeClass) {
		return nil
	}

	version, err := parseKubernetesVersionTolerant(kubernetesVersion)
	if err != nil {
		return err
	}

	fips := lo.FromPtr(nodeClass.Spec.FIPSMode) == v1beta1.FIPSModeFIPS

	switch requestedImageFamily {
	case v1beta1.Ubuntu2204ImageFamily:
		maximumVersion := ubuntu2204MaximumVersion
		if fips {
			maximumVersion = ubuntu2204FIPSMaximumVersion
		}
		if version.LT(ubuntu2204MinimumVersion) || !version.LT(maximumVersion) {
			return &ImageFamilyKubernetesVersionIncompatibleError{
				RequestedImageFamily: requestedImageFamily,
				KubernetesVersion:    kubernetesVersion,
				FIPS:                 fips,
				MinimumVersion:       ubuntu2204MinimumVersion,
				MaximumVersion:       lo.ToPtr(maximumVersion),
			}
		}
	case v1beta1.Ubuntu2404ImageFamily:
		// Ubuntu2404 has no FIPS-specific bound today, so FIPS is left unset to
		// avoid implying the reported range depends on it.
		if version.LT(ubuntu2404MinimumVersion) {
			return &ImageFamilyKubernetesVersionIncompatibleError{
				RequestedImageFamily: requestedImageFamily,
				KubernetesVersion:    kubernetesVersion,
				MinimumVersion:       ubuntu2404MinimumVersion,
			}
		}
	}

	return nil
}

func parseKubernetesVersionTolerant(kubernetesVersion string) (semver.Version, error) {
	version, err := semver.ParseTolerant(strings.TrimPrefix(kubernetesVersion, "v"))
	if err != nil {
		return semver.Version{}, &MalformedDiscoveredKubernetesVersionError{
			kubernetesVersion: kubernetesVersion,
			cause:             err,
		}
	}
	return version, nil
}
