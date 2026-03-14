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
