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

package utils

import (
	"fmt"
	"regexp"
	"strings"
)

// WARNING: not supporting CIG images yet.
func GetAKSMachineNodeImageVersionFromImageID(imageID string) (string, error) {
	if strings.HasPrefix(imageID, "/CommunityGalleries") {
		// Requires AKS machine API support
		return "", fmt.Errorf("CIG images are not supported yet for AKS machines, consider not using PROVISION_MODE=aksmachineapi: %s", imageID)
	} else {
		return GetAKSMachineNodeImageVersionFromSIGImageID(imageID)
	}
}

// Convert from "/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03"
// to "AKSUbuntu-2204gen2containerd-2022.10.03".
func GetAKSMachineNodeImageVersionFromSIGImageID(imageID string) (string, error) {
	matches := regexp.MustCompile(`(?i)/subscriptions/(\S+)/resourceGroups/(\S+)/providers/Microsoft.Compute/galleries/(\S+)/images/(\S+)/versions/(\S+)`).FindStringSubmatch(imageID)
	if matches == nil {
		return "", fmt.Errorf("incorrect SIG image ID id=%s", imageID)
	}

	// SubscriptionID := matches[1]
	// ResourceGroup := matches[2]
	Gallery := matches[3]
	Definition := matches[4]
	Version := matches[5]

	prefix := Gallery
	osVersion := Definition
	// if strings.Contains(prefix, windowsPrefix) {		// TODO(Windows)
	// 	osVersion = extractOsVersionForWindows(Definition)
	// }

	return strings.Join([]string{prefix, osVersion, Version}, "-"), nil
}
