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

package utils_test

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	. "github.com/onsi/gomega"
)

func TestGetAKSMachineNodeImageVersionFromImageID(t *testing.T) {
	cases := []struct {
		name           string
		imageID        string
		expectedError  string
		expectedResult string
	}{
		{
			name:           "CIG image should return error",
			imageID:        "/CommunityGalleries/myGallery/images/myImage/versions/1.0.0",
			expectedError:  "CIG images are not supported yet for AKS machines, consider not using PROVISION_MODE=aksmachineapi: /CommunityGalleries/myGallery/images/myImage/versions/1.0.0",
			expectedResult: "",
		},
		{
			name:           "Valid SIG image ID",
			imageID:        "/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			expectedError:  "",
			expectedResult: "AKSUbuntu-2204gen2containerd-2022.10.03",
		},
		{
			name:           "Invalid SIG image ID",
			imageID:        "/subscriptions/invalid/format",
			expectedError:  "incorrect SIG image ID id=/subscriptions/invalid/format",
			expectedResult: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := utils.GetAKSMachineNodeImageVersionFromImageID(c.imageID)
			if c.expectedError != "" {
				g.Expect(err).To(MatchError(c.expectedError))
				g.Expect(result).To(Equal(""))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(result).To(Equal(c.expectedResult))
			}
		})
	}
}

func TestGetAKSMachineNodeImageVersionFromSIGImageID(t *testing.T) {
	cases := []struct {
		name           string
		imageID        string
		expectedError  string
		expectedResult string
	}{
		{
			name:           "Valid SIG image ID with standard format",
			imageID:        "/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			expectedError:  "",
			expectedResult: "AKSUbuntu-2204gen2containerd-2022.10.03",
		},
		{
			name:           "Valid SIG image ID with different gallery name",
			imageID:        "/subscriptions/12345678-abcd-efgh-ijkl-123456789012/resourceGroups/MyResourceGroup/providers/Microsoft.Compute/galleries/MyGallery/images/ubuntu1804/versions/1.0.0",
			expectedError:  "",
			expectedResult: "MyGallery-ubuntu1804-1.0.0",
		},
		{
			name:           "Valid SIG image ID with case insensitive matching",
			imageID:        "/SUBSCRIPTIONS/10945678-1234-1234-1234-123456789012/RESOURCEGROUPS/AKS-Ubuntu/PROVIDERS/Microsoft.Compute/GALLERIES/AKSUbuntu/IMAGES/2204gen2containerd/VERSIONS/2022.10.03",
			expectedError:  "",
			expectedResult: "AKSUbuntu-2204gen2containerd-2022.10.03",
		},
		{
			name:           "Valid SIG image ID with complex version",
			imageID:        "/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03-build.1",
			expectedError:  "",
			expectedResult: "AKSUbuntu-2204gen2containerd-2022.10.03-build.1",
		},
		{
			name:           "Invalid SIG image ID - missing subscription",
			imageID:        "/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			expectedError:  "incorrect SIG image ID id=/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			expectedResult: "",
		},
		{
			name:           "Invalid SIG image ID - missing version",
			imageID:        "/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd",
			expectedError:  "incorrect SIG image ID id=/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd",
			expectedResult: "",
		},
		{
			name:           "Invalid SIG image ID - wrong provider",
			imageID:        "/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Storage/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			expectedError:  "incorrect SIG image ID id=/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Storage/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			expectedResult: "",
		},
		{
			name:           "Invalid SIG image ID - completely malformed",
			imageID:        "not-a-valid-resource-id",
			expectedError:  "incorrect SIG image ID id=not-a-valid-resource-id",
			expectedResult: "",
		},
		{
			name:           "Empty image ID",
			imageID:        "",
			expectedError:  "incorrect SIG image ID id=",
			expectedResult: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := utils.GetAKSMachineNodeImageVersionFromSIGImageID(c.imageID)
			if c.expectedError != "" {
				g.Expect(err).To(MatchError(c.expectedError))
				g.Expect(result).To(Equal(""))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(result).To(Equal(c.expectedResult))
			}
		})
	}
}
