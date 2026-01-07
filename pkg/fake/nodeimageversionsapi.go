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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/samber/lo"
)

type NodeImageVersionsAPI struct {
	// OverrideNodeImageVersions allows tests to override the default static data
	// When nil, the default NodeImageVersions slice is used
	OverrideNodeImageVersions []*armcontainerservice.NodeImageVersion
	// Error allows tests to simulate API errors.
	// If Error is set to non-nil, it will take precedence over other fake data
	Error error
}

var _ types.NodeImageVersionsAPI = &NodeImageVersionsAPI{}

// Note: use "make az-codegen-nodeimageversions" to generate data for this file
// (will require update of some tests that use this data)
var (
	nodeImageVersions = []*armcontainerservice.NodeImageVersion{
		{
			FullName: lo.ToPtr("AKSAzureLinux-V2fips-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V2fips"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V2gen2fips-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V2gen2fips"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V3fips-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V3fips"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-1604-2021.11.06"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("1604"),
			Version:  lo.ToPtr("2021.11.06"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2404gen2containerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2404gen2containerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-1804gen2fipscontainerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("1804gen2fipscontainerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2004gen2fipscontainerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2004gen2fipscontainerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V2-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V2"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntuEdgeZone-1804containerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntuEdgeZone"),
			SKU:      lo.ToPtr("1804containerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntuEdgeZone-2204gen2containerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntuEdgeZone"),
			SKU:      lo.ToPtr("2204gen2containerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2004gen2CVMcontainerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2004gen2CVMcontainerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V2gen2TL-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V2gen2TL"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-1804containerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("1804containerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2204containerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2204containerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2404containerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2404containerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSWindows-2019-17763.2019.221114"),
			OS:       lo.ToPtr("AKSWindows"),
			SKU:      lo.ToPtr("windows-2019"),
			Version:  lo.ToPtr("17763.2019.221114"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V2gen2-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V2gen2"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSWindows-2019-containerd-17763.7314.250518"),
			OS:       lo.ToPtr("AKSWindows"),
			SKU:      lo.ToPtr("windows-2019-containerd"),
			Version:  lo.ToPtr("17763.7314.250518"),
		},
		{
			FullName: lo.ToPtr("AKSCBLMariner-V2gen2arm64-202505.27.0"),
			OS:       lo.ToPtr("AKSCBLMariner"),
			SKU:      lo.ToPtr("V2gen2arm64"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSCBLMariner-V2katagen2-202505.27.0"),
			OS:       lo.ToPtr("AKSCBLMariner"),
			SKU:      lo.ToPtr("V2katagen2"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-1804gpu-2022.08.29"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("1804gpu"),
			Version:  lo.ToPtr("2022.08.29"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-1804gen2gpu-2022.08.29"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("1804gen2gpu"),
			Version:  lo.ToPtr("2022.08.29"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V3-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V3"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V3gen2-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V3gen2"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSCBLMariner-V2gen2-202505.27.0"),
			OS:       lo.ToPtr("AKSCBLMariner"),
			SKU:      lo.ToPtr("V2gen2"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSWindows-2022-containerd-gen2-20348.3692.250518"),
			OS:       lo.ToPtr("AKSWindows"),
			SKU:      lo.ToPtr("windows-2022-containerd-gen2"),
			Version:  lo.ToPtr("20348.3692.250518"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V3gen2arm64fips-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V3gen2arm64fips"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2204gen2arm64containerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2204gen2arm64containerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSWindows-2025-26100.4061.250518"),
			OS:       lo.ToPtr("AKSWindows"),
			SKU:      lo.ToPtr("windows-2025"),
			Version:  lo.ToPtr("26100.4061.250518"),
		},
		{
			FullName: lo.ToPtr("AKSWindows-23H2-gen2-25398.1611.250518"),
			OS:       lo.ToPtr("AKSWindows"),
			SKU:      lo.ToPtr("windows-23H2-gen2"),
			Version:  lo.ToPtr("25398.1611.250518"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V3gen2TL-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V3gen2TL"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2204gen2TLcontainerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2204gen2TLcontainerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2404gen2CVMcontainerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2404gen2CVMcontainerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2204gen2containerd-2022.10.03"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2204gen2containerd"),
			Version:  lo.ToPtr("2022.10.03"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2204gen2fipscontainerd-202404.09.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2204gen2fipscontainerd"),
			Version:  lo.ToPtr("202404.09.0"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V3gen2arm64-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V3gen2arm64"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V3gen2fips-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V3gen2fips"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSCBLMariner-V2fips-202505.27.0"),
			OS:       lo.ToPtr("AKSCBLMariner"),
			SKU:      lo.ToPtr("V2fips"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSCBLMariner-V2gen2TL-202505.27.0"),
			OS:       lo.ToPtr("AKSCBLMariner"),
			SKU:      lo.ToPtr("V2gen2TL"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-1804-2022.08.29"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("1804"),
			Version:  lo.ToPtr("2022.08.29"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2204fipscontainerd-202404.09.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2204fipscontainerd"),
			Version:  lo.ToPtr("202404.09.0"),
		},
		{
			FullName: lo.ToPtr("AKSWindows-23H2-25398.1611.250518"),
			OS:       lo.ToPtr("AKSWindows"),
			SKU:      lo.ToPtr("windows-23H2"),
			Version:  lo.ToPtr("25398.1611.250518"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V3gen2CVM-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V3gen2CVM"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-1804gen2containerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("1804gen2containerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2204minimalcontainerd-202401.12.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2204minimalcontainerd"),
			Version:  lo.ToPtr("202401.12.0"),
		},
		{
			FullName: lo.ToPtr("AKSCBLMariner-V2gen2fips-202505.27.0"),
			OS:       lo.ToPtr("AKSCBLMariner"),
			SKU:      lo.ToPtr("V2gen2fips"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2404gen2TLcontainerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2404gen2TLcontainerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntuEdgeZone-1804gen2containerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntuEdgeZone"),
			SKU:      lo.ToPtr("1804gen2containerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-1804gen2gpucontainerd-202501.05.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("1804gen2gpucontainerd"),
			Version:  lo.ToPtr("202501.05.0"),
		},
		{
			FullName: lo.ToPtr("AKSWindows-2022-containerd-20348.3692.250518"),
			OS:       lo.ToPtr("AKSWindows"),
			SKU:      lo.ToPtr("windows-2022-containerd"),
			Version:  lo.ToPtr("20348.3692.250518"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2404gen2arm64containerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2404gen2arm64containerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-1804fipscontainerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("1804fipscontainerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSCBLMariner-V1-202308.28.0"),
			OS:       lo.ToPtr("AKSCBLMariner"),
			SKU:      lo.ToPtr("V1"),
			Version:  lo.ToPtr("202308.28.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2004fipscontainerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2004fipscontainerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSWindows-2025-gen2-26100.4061.250518"),
			OS:       lo.ToPtr("AKSWindows"),
			SKU:      lo.ToPtr("windows-2025-gen2"),
			Version:  lo.ToPtr("26100.4061.250518"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2204gen2containerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2204gen2containerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V2katagen2-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V2katagen2"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSCBLMariner-V2-202505.27.0"),
			OS:       lo.ToPtr("AKSCBLMariner"),
			SKU:      lo.ToPtr("V2"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-1804gpucontainerd-202501.05.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("1804gpucontainerd"),
			Version:  lo.ToPtr("202501.05.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-2204gen2minimalcontainerd-202401.12.0"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("2204gen2minimalcontainerd"),
			Version:  lo.ToPtr("202401.12.0"),
		},
		{
			FullName: lo.ToPtr("AKSAzureLinux-V2gen2arm64-202505.27.0"),
			OS:       lo.ToPtr("AKSAzureLinux"),
			SKU:      lo.ToPtr("V2gen2arm64"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntuEdgeZone-2204containerd-202505.27.0"),
			OS:       lo.ToPtr("AKSUbuntuEdgeZone"),
			SKU:      lo.ToPtr("2204containerd"),
			Version:  lo.ToPtr("202505.27.0"),
		},
		{
			FullName: lo.ToPtr("AKSUbuntu-1804gen2-2022.08.29"),
			OS:       lo.ToPtr("AKSUbuntu"),
			SKU:      lo.ToPtr("1804gen2"),
			Version:  lo.ToPtr("2022.08.29"),
		},
		{
			FullName: lo.ToPtr("AKSCBLMariner-V2katagen2TL-2022.12.15"),
			OS:       lo.ToPtr("AKSCBLMariner"),
			SKU:      lo.ToPtr("V2katagen2TL"),
			Version:  lo.ToPtr("2022.12.15"),
		},
	}
)

func (n *NodeImageVersionsAPI) Reset() {
	n.OverrideNodeImageVersions = nil
	n.Error = nil
}

func (n *NodeImageVersionsAPI) List(_ context.Context, _ string) ([]*armcontainerservice.NodeImageVersion, error) {
	// Error takes precedence over other fake data
	if n.Error != nil {
		return nil, n.Error
	}

	// Use override data if provided, otherwise use default static data
	dataToUse := nodeImageVersions
	if n.OverrideNodeImageVersions != nil {
		dataToUse = n.OverrideNodeImageVersions
	}

	return imagefamily.FilteredNodeImages(dataToUse), nil
}
