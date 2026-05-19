// Portions Copyright (c) Microsoft Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package imagefamily

import (
	"strings"

	"github.com/blang/semver/v4"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

// UseAzureLinux3 checks if the Kubernetes version is 1.32.0 or higher,
// which is when Azure Linux 3 support starts
func UseAzureLinux3(kubernetesVersion string) bool {
	// Parse version, stripping any 'v' prefix if present
	version, err := semver.ParseTolerant(strings.TrimPrefix(kubernetesVersion, "v"))
	if err != nil {
		// If we can't parse the version, default to AzureLinux (false)
		return false
	}
	return version.GE(semver.Version{Major: 1, Minor: 32})
}

// UseUbuntu2404 is when AKS starts defaulting support for Ubuntu2404
func UseUbuntu2404(kubernetesVersion string) bool {
	// Parse version, stripping any 'v' prefix if present
	version, err := semver.ParseTolerant(strings.TrimPrefix(kubernetesVersion, "v"))
	if err != nil {
		// If we can't parse the version, default to Ubuntu2204 (false)
		return false
	}
	return version.GE(semver.Version{Major: 1, Minor: 34})
}

// ResolvesToUbuntu2004 returns true if the given image-family + FIPS-mode
// combination would resolve to the Ubuntu2004 ImageFamily implementation
// in defaultUbuntu (see resolver.go).
//
// Today, Ubuntu2004 is reachable only when the legacy/unset Ubuntu image
// family is selected together with FIPS mode. Callers outside of the
// resolver use this to make decisions that depend on whether a NodeClass
// will ultimately be backed by 20.04 (e.g. the LocalDNS state reconciler,
// since LocalDNS is unsupported on 20.04).
//
// NOTE: this helper duplicates the rule that lives in defaultUbuntu — it is
// intentionally not used from defaultUbuntu itself, to keep that function's
// existing logic flow untouched. If the rule in defaultUbuntu ever changes,
// update this helper to match.
func ResolvesToUbuntu2004(familyName *string, fipsMode *v1beta1.FIPSMode) bool {
	family := lo.FromPtr(familyName)
	isUbuntuLegacyOrUnset := family == "" || family == v1beta1.UbuntuImageFamily
	return isUbuntuLegacyOrUnset && lo.FromPtr(fipsMode) == v1beta1.FIPSModeFIPS
}
