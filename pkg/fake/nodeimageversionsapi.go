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

package fake

import (
	"context"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
)

type NodeImageVersionsAPI struct {
	// OverrideNodeImageVersions allows tests to override the default static data
	// When nil, the default NodeImageVersions slice is used
	OverrideNodeImageVersions []types.NodeImageVersion
	// Error allows tests to simulate API errors.
	// If Error is set to non-nil, it will take precedence over other fake data
	Error error
}

var _ types.NodeImageVersionsAPI = &NodeImageVersionsAPI{}

// Note: use "make az-codegen-nodeimageversions" to generate data for this file
// (will require update of some tests that use this data)
var (
	nodeImageVersions = []types.NodeImageVersion{
		{
			FullName: "AKSAzureLinux-V2fips-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V2fips",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSAzureLinux-V2gen2fips-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V2gen2fips",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSAzureLinux-V3fips-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V3fips",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-1604-2021.11.06",
			OS:       "AKSUbuntu",
			SKU:      "1604",
			Version:  "2021.11.06",
		},
		{
			FullName: "AKSUbuntu-2404gen2containerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "2404gen2containerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-1804gen2fipscontainerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "1804gen2fipscontainerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-2004gen2fipscontainerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "2004gen2fipscontainerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSAzureLinux-V2-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V2",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntuEdgeZone-1804containerd-202505.27.0",
			OS:       "AKSUbuntuEdgeZone",
			SKU:      "1804containerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntuEdgeZone-2204gen2containerd-202505.27.0",
			OS:       "AKSUbuntuEdgeZone",
			SKU:      "2204gen2containerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-2004gen2CVMcontainerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "2004gen2CVMcontainerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSAzureLinux-V2gen2TL-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V2gen2TL",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-1804containerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "1804containerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-2204containerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "2204containerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-2404containerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "2404containerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSWindows-2019-17763.2019.221114",
			OS:       "AKSWindows",
			SKU:      "windows-2019",
			Version:  "17763.2019.221114",
		},
		{
			FullName: "AKSAzureLinux-V2gen2-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V2gen2",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSWindows-2019-containerd-17763.7314.250518",
			OS:       "AKSWindows",
			SKU:      "windows-2019-containerd",
			Version:  "17763.7314.250518",
		},
		{
			FullName: "AKSCBLMariner-V2gen2arm64-202505.27.0",
			OS:       "AKSCBLMariner",
			SKU:      "V2gen2arm64",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSCBLMariner-V2katagen2-202505.27.0",
			OS:       "AKSCBLMariner",
			SKU:      "V2katagen2",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-1804gpu-2022.08.29",
			OS:       "AKSUbuntu",
			SKU:      "1804gpu",
			Version:  "2022.08.29",
		},
		{
			FullName: "AKSUbuntu-1804gen2gpu-2022.08.29",
			OS:       "AKSUbuntu",
			SKU:      "1804gen2gpu",
			Version:  "2022.08.29",
		},
		{
			FullName: "AKSAzureLinux-V3-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V3",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSAzureLinux-V3gen2-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V3gen2",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSCBLMariner-V2gen2-202505.27.0",
			OS:       "AKSCBLMariner",
			SKU:      "V2gen2",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSWindows-2022-containerd-gen2-20348.3692.250518",
			OS:       "AKSWindows",
			SKU:      "windows-2022-containerd-gen2",
			Version:  "20348.3692.250518",
		},
		{
			FullName: "AKSAzureLinux-V3gen2arm64fips-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V3gen2arm64fips",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-2204gen2arm64containerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "2204gen2arm64containerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSWindows-2025-26100.4061.250518",
			OS:       "AKSWindows",
			SKU:      "windows-2025",
			Version:  "26100.4061.250518",
		},
		{
			FullName: "AKSWindows-23H2-gen2-25398.1611.250518",
			OS:       "AKSWindows",
			SKU:      "windows-23H2-gen2",
			Version:  "25398.1611.250518",
		},
		{
			FullName: "AKSAzureLinux-V3gen2TL-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V3gen2TL",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-2204gen2TLcontainerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "2204gen2TLcontainerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-2404gen2CVMcontainerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "2404gen2CVMcontainerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-2204gen2containerd-2022.10.03",
			OS:       "AKSUbuntu",
			SKU:      "2204gen2containerd",
			Version:  "2022.10.03",
		},
		{
			FullName: "AKSUbuntu-2204gen2fipscontainerd-202404.09.0",
			OS:       "AKSUbuntu",
			SKU:      "2204gen2fipscontainerd",
			Version:  "202404.09.0",
		},
		{
			FullName: "AKSAzureLinux-V3gen2arm64-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V3gen2arm64",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSAzureLinux-V3gen2fips-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V3gen2fips",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSCBLMariner-V2fips-202505.27.0",
			OS:       "AKSCBLMariner",
			SKU:      "V2fips",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSCBLMariner-V2gen2TL-202505.27.0",
			OS:       "AKSCBLMariner",
			SKU:      "V2gen2TL",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-1804-2022.08.29",
			OS:       "AKSUbuntu",
			SKU:      "1804",
			Version:  "2022.08.29",
		},
		{
			FullName: "AKSUbuntu-2204fipscontainerd-202404.09.0",
			OS:       "AKSUbuntu",
			SKU:      "2204fipscontainerd",
			Version:  "202404.09.0",
		},
		{
			FullName: "AKSWindows-23H2-25398.1611.250518",
			OS:       "AKSWindows",
			SKU:      "windows-23H2",
			Version:  "25398.1611.250518",
		},
		{
			FullName: "AKSAzureLinux-V3gen2CVM-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V3gen2CVM",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-1804gen2containerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "1804gen2containerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-2204minimalcontainerd-202401.12.0",
			OS:       "AKSUbuntu",
			SKU:      "2204minimalcontainerd",
			Version:  "202401.12.0",
		},
		{
			FullName: "AKSCBLMariner-V2gen2fips-202505.27.0",
			OS:       "AKSCBLMariner",
			SKU:      "V2gen2fips",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-2404gen2TLcontainerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "2404gen2TLcontainerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntuEdgeZone-1804gen2containerd-202505.27.0",
			OS:       "AKSUbuntuEdgeZone",
			SKU:      "1804gen2containerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-1804gen2gpucontainerd-202501.05.0",
			OS:       "AKSUbuntu",
			SKU:      "1804gen2gpucontainerd",
			Version:  "202501.05.0",
		},
		{
			FullName: "AKSWindows-2022-containerd-20348.3692.250518",
			OS:       "AKSWindows",
			SKU:      "windows-2022-containerd",
			Version:  "20348.3692.250518",
		},
		{
			FullName: "AKSUbuntu-2404gen2arm64containerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "2404gen2arm64containerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-1804fipscontainerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "1804fipscontainerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSCBLMariner-V1-202308.28.0",
			OS:       "AKSCBLMariner",
			SKU:      "V1",
			Version:  "202308.28.0",
		},
		{
			FullName: "AKSUbuntu-2004fipscontainerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "2004fipscontainerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSWindows-2025-gen2-26100.4061.250518",
			OS:       "AKSWindows",
			SKU:      "windows-2025-gen2",
			Version:  "26100.4061.250518",
		},
		{
			FullName: "AKSUbuntu-2204gen2containerd-202505.27.0",
			OS:       "AKSUbuntu",
			SKU:      "2204gen2containerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSAzureLinux-V2katagen2-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V2katagen2",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSCBLMariner-V2-202505.27.0",
			OS:       "AKSCBLMariner",
			SKU:      "V2",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-1804gpucontainerd-202501.05.0",
			OS:       "AKSUbuntu",
			SKU:      "1804gpucontainerd",
			Version:  "202501.05.0",
		},
		{
			FullName: "AKSUbuntu-2204gen2minimalcontainerd-202401.12.0",
			OS:       "AKSUbuntu",
			SKU:      "2204gen2minimalcontainerd",
			Version:  "202401.12.0",
		},
		{
			FullName: "AKSAzureLinux-V2gen2arm64-202505.27.0",
			OS:       "AKSAzureLinux",
			SKU:      "V2gen2arm64",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntuEdgeZone-2204containerd-202505.27.0",
			OS:       "AKSUbuntuEdgeZone",
			SKU:      "2204containerd",
			Version:  "202505.27.0",
		},
		{
			FullName: "AKSUbuntu-1804gen2-2022.08.29",
			OS:       "AKSUbuntu",
			SKU:      "1804gen2",
			Version:  "2022.08.29",
		},
		{
			FullName: "AKSCBLMariner-V2katagen2TL-2022.12.15",
			OS:       "AKSCBLMariner",
			SKU:      "V2katagen2TL",
			Version:  "2022.12.15",
		},
	}
)

func (n *NodeImageVersionsAPI) Reset() {
	n.OverrideNodeImageVersions = nil
	n.Error = nil
}

func (n *NodeImageVersionsAPI) List(_ context.Context, _, _ string) (types.NodeImageVersionsResponse, error) {
	// Error takes precedence over other fake data
	if n.Error != nil {
		return types.NodeImageVersionsResponse{}, n.Error
	}

	// Use override data if provided, otherwise use default static data
	dataToUse := nodeImageVersions
	if n.OverrideNodeImageVersions != nil {
		dataToUse = n.OverrideNodeImageVersions
	}

	return types.NodeImageVersionsResponse{
		Values: imagefamily.FilteredNodeImages(dataToUse),
	}, nil
}
