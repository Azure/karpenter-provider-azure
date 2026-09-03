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

var (
	ubuntu2204MinimumVersion     = semver.MustParse("1.25.2")
	ubuntu2204MaximumVersion     = semver.MustParse("1.37.0")
	ubuntu2204FIPSMaximumVersion = semver.MustParse("1.39.0")
	ubuntu2404MinimumVersion     = semver.MustParse("1.32.0")
)

// ValidateImageFamilyCompatibility verifies that the AKSNodeClass resolves to an
// image family supported by the discovered cluster Kubernetes version.
func ValidateImageFamilyCompatibility(nodeClass *v1beta1.AKSNodeClass, kubernetesVersion string) error {
	if nodeClass == nil {
		return fmt.Errorf("AKSNodeClass is required to validate image family compatibility")
	}

	version, err := parseKubernetesVersionTolerant(kubernetesVersion)
	if err != nil {
		return err
	}

	effectiveImageFamily := GetImageFamily(
		nodeClass.Spec.ImageFamily,
		nodeClass.Spec.FIPSMode,
		nodeClass.IsTrustedLaunchEnabled(),
		version.String(),
		nil,
	)

	switch effectiveImageFamily.Name() {
	case v1beta1.Ubuntu2204ImageFamily:
		maximumVersion := ubuntu2204MaximumVersion
		descriptionSuffix := ""
		if lo.FromPtr(nodeClass.Spec.FIPSMode) == v1beta1.FIPSModeFIPS {
			maximumVersion = ubuntu2204FIPSMaximumVersion
			descriptionSuffix = " with FIPS"
		}

		if version.LT(ubuntu2204MinimumVersion) || !version.LT(maximumVersion) {
			return fmt.Errorf("effective image family %q%s is not supported with discovered Kubernetes version %q; supported range is >= %s and < %s", effectiveImageFamily.Name(), descriptionSuffix, kubernetesVersion, ubuntu2204MinimumVersion, maximumVersion)
		}
	case v1beta1.Ubuntu2404ImageFamily:
		if version.LT(ubuntu2404MinimumVersion) {
			return fmt.Errorf("effective image family %q is not supported with discovered Kubernetes version %q; supported range is >= %s", effectiveImageFamily.Name(), kubernetesVersion, ubuntu2404MinimumVersion)
		}
	}

	return nil
}

func parseKubernetesVersionTolerant(kubernetesVersion string) (semver.Version, error) {
	version, err := semver.ParseTolerant(strings.TrimPrefix(kubernetesVersion, "v"))
	if err != nil {
		return semver.Version{}, fmt.Errorf("malformed discovered Kubernetes version %q: expected a semantic version like 1.32.0: %w", kubernetesVersion, err)
	}
	return version, nil
}
