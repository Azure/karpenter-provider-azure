package fake

import (
	"context"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
)

type NodeImageVersionsAPI struct {
}

var _ imagefamily.NodeImageVersionsAPI = &NodeImageVersionsAPI{}

func (n NodeImageVersionsAPI) List(_ context.Context, _, _ string) (imagefamily.NodeImageVersionsResponse, error) {
	return imagefamily.NodeImageVersionsResponse{
		Values: []imagefamily.NodeImageVersion{
			{
				FullName: "AKSUbuntu-1804gpucontainerd-202410.09.0",
				OS:       "AKSUbuntu",
				SKU:      "1804gpucontainerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSWindows-2019-17763.2019.221114",
				OS:       "AKSWindows",
				SKU:      "windows-2019",
				Version:  "17763.2019.221114",
			},
			{
				FullName: "AKSAzureLinux-V3-202409.23.0",
				OS:       "AKSAzureLinux",
				SKU:      "V3",
				Version:  "202409.23.0",
			},
			{
				FullName: "AKSUbuntu-2204gen2containerd-202410.09.0",
				OS:       "AKSUbuntu",
				SKU:      "2204gen2containerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntu-1804gpu-2022.08.29",
				OS:       "AKSUbuntu",
				SKU:      "1804gpu",
				Version:  "2022.08.29",
			},
			{
				FullName: "AKSWindows-2022-containerd-gen2-20348.2762.241009",
				OS:       "AKSWindows",
				SKU:      "windows-2022-containerd-gen2",
				Version:  "20348.2762.241009",
			},
			{
				FullName: "AKSCBLMariner-V2katagen2TL-2022.12.15",
				OS:       "AKSCBLMariner",
				SKU:      "V2katagen2TL",
				Version:  "2022.12.15",
			},
			{
				FullName: "AKSUbuntu-2004gen2fipscontainerd-202410.09.0",
				OS:       "AKSUbuntu",
				SKU:      "2004gen2fipscontainerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSAzureLinux-V2fips-202410.09.0",
				OS:       "AKSAzureLinux",
				SKU:      "V2fips",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSAzureLinux-V2gen2fips-202410.09.0",
				OS:       "AKSAzureLinux",
				SKU:      "V2gen2fips",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntuEdgeZone-1804gen2containerd-202410.09.0",
				OS:       "AKSUbuntuEdgeZone",
				SKU:      "1804gen2containerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSAzureLinux-V2katagen2-202410.09.0",
				OS:       "AKSAzureLinux",
				SKU:      "V2katagen2",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSAzureLinux-V3gen2-202409.23.0",
				OS:       "AKSAzureLinux",
				SKU:      "V3gen2",
				Version:  "202409.23.0",
			},
			{
				FullName: "AKSUbuntu-2204gen2arm64containerd-202410.09.0",
				OS:       "AKSUbuntu",
				SKU:      "2204gen2arm64containerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntu-1804gen2gpucontainerd-202410.09.0",
				OS:       "AKSUbuntu",
				SKU:      "1804gen2gpucontainerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSAzureLinux-V2gen2TL-202410.09.0",
				OS:       "AKSAzureLinux",
				SKU:      "V2gen2TL",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSCBLMariner-V2-202410.09.0",
				OS:       "AKSCBLMariner",
				SKU:      "V2",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSCBLMariner-V2fips-202410.09.0",
				OS:       "AKSCBLMariner",
				SKU:      "V2fips",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntu-1804containerd-202410.09.0",
				OS:       "AKSUbuntu",
				SKU:      "1804containerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntuEdgeZone-1804containerd-202410.09.0",
				OS:       "AKSUbuntuEdgeZone",
				SKU:      "1804containerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntuEdgeZone-2204gen2containerd-202410.09.0",
				OS:       "AKSUbuntuEdgeZone",
				SKU:      "2204gen2containerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntu-1804fipscontainerd-202410.09.0",
				OS:       "AKSUbuntu",
				SKU:      "1804fipscontainerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSAzureLinux-V2gen2arm64-202410.09.0",
				OS:       "AKSAzureLinux",
				SKU:      "V2gen2arm64",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSAzureLinux-V2gen2-202410.09.0",
				OS:       "AKSAzureLinux",
				SKU:      "V2gen2",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntu-2204gen2fipscontainerd-202404.09.0",
				OS:       "AKSUbuntu",
				SKU:      "2204gen2fipscontainerd",
				Version:  "202404.09.0",
			},
			{
				FullName: "AKSWindows-2019-containerd-17763.6414.241010",
				OS:       "AKSWindows",
				SKU:      "windows-2019-containerd",
				Version:  "17763.6414.241010",
			},
			{
				FullName: "AKSUbuntu-2204gen2TLcontainerd-202410.09.0",
				OS:       "AKSUbuntu",
				SKU:      "2204gen2TLcontainerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntu-1804-2022.08.29",
				OS:       "AKSUbuntu",
				SKU:      "1804",
				Version:  "2022.08.29",
			},
			{
				FullName: "AKSUbuntuEdgeZone-2204containerd-202410.09.0",
				OS:       "AKSUbuntuEdgeZone",
				SKU:      "2204containerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSCBLMariner-V2gen2-202410.09.0",
				OS:       "AKSCBLMariner",
				SKU:      "V2gen2",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSCBLMariner-V2gen2fips-202410.09.0",
				OS:       "AKSCBLMariner",
				SKU:      "V2gen2fips",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSCBLMariner-V2gen2arm64-202410.09.0",
				OS:       "AKSCBLMariner",
				SKU:      "V2gen2arm64",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSCBLMariner-V2gen2TL-202410.09.0",
				OS:       "AKSCBLMariner",
				SKU:      "V2gen2TL",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntu-2404gen2arm64containerd-202405.20.0",
				OS:       "AKSUbuntu",
				SKU:      "2404gen2arm64containerd",
				Version:  "202405.20.0",
			},
			{
				FullName: "AKSAzureLinux-V3gen2arm64-202409.23.0",
				OS:       "AKSAzureLinux",
				SKU:      "V3gen2arm64",
				Version:  "202409.23.0",
			},
			{
				FullName: "AKSAzureLinux-V3fips-202409.23.0",
				OS:       "AKSAzureLinux",
				SKU:      "V3fips",
				Version:  "202409.23.0",
			},
			{
				FullName: "AKSUbuntu-2004gen2CVMcontainerd-202410.09.0",
				OS:       "AKSUbuntu",
				SKU:      "2004gen2CVMcontainerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSCBLMariner-V1-202308.28.0",
				OS:       "AKSCBLMariner",
				SKU:      "V1",
				Version:  "202308.28.0",
			},
			{
				FullName: "AKSUbuntu-1804gen2containerd-202410.09.0",
				OS:       "AKSUbuntu",
				SKU:      "1804gen2containerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntu-1804gen2gpu-2022.08.29",
				OS:       "AKSUbuntu",
				SKU:      "1804gen2gpu",
				Version:  "2022.08.29",
			},
			{
				FullName: "AKSUbuntu-2204gen2minimalcontainerd-202401.12.0",
				OS:       "AKSUbuntu",
				SKU:      "2204gen2minimalcontainerd",
				Version:  "202401.12.0",
			},
			{
				FullName: "AKSWindows-23H2-gen2-25398.1189.241009",
				OS:       "AKSWindows",
				SKU:      "windows-23H2-gen2",
				Version:  "25398.1189.241009",
			},
			{
				FullName: "AKSCBLMariner-V2katagen2-202410.09.0",
				OS:       "AKSCBLMariner",
				SKU:      "V2katagen2",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntu-1604-2021.11.06",
				OS:       "AKSUbuntu",
				SKU:      "1604",
				Version:  "2021.11.06",
			},
			{
				FullName: "AKSUbuntu-2204fipscontainerd-202404.09.0",
				OS:       "AKSUbuntu",
				SKU:      "2204fipscontainerd",
				Version:  "202404.09.0",
			},
			{
				FullName: "AKSUbuntu-2204minimalcontainerd-202401.12.0",
				OS:       "AKSUbuntu",
				SKU:      "2204minimalcontainerd",
				Version:  "202401.12.0",
			},
			{
				FullName: "AKSWindows-2022-containerd-20348.2762.241009",
				OS:       "AKSWindows",
				SKU:      "windows-2022-containerd",
				Version:  "20348.2762.241009",
			},
			{
				FullName: "AKSUbuntu-2204containerd-202410.09.0",
				OS:       "AKSUbuntu",
				SKU:      "2204containerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntu-2004fipscontainerd-202410.09.0",
				OS:       "AKSUbuntu",
				SKU:      "2004fipscontainerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSUbuntu-1804gen2fipscontainerd-202410.09.0",
				OS:       "AKSUbuntu",
				SKU:      "1804gen2fipscontainerd",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSWindows-23H2-25398.1189.241009",
				OS:       "AKSWindows",
				SKU:      "windows-23H2",
				Version:  "25398.1189.241009",
			},
			{
				FullName: "AKSAzureLinux-V2-202410.09.0",
				OS:       "AKSAzureLinux",
				SKU:      "V2",
				Version:  "202410.09.0",
			},
			{
				FullName: "AKSAzureLinux-V3gen2fips-202409.23.0",
				OS:       "AKSAzureLinux",
				SKU:      "V3gen2fips",
				Version:  "202409.23.0",
			},
			{
				FullName: "AKSUbuntu-2404gen2containerd-202405.20.0",
				OS:       "AKSUbuntu",
				SKU:      "2404gen2containerd",
				Version:  "202405.20.0",
			},
			{
				FullName: "AKSUbuntu-2204gen2containerd-2022.10.03",
				OS:       "AKSUbuntu",
				SKU:      "2204gen2containerd",
				Version:  "2022.10.03",
			},
			{
				FullName: "AKSUbuntu-2404containerd-202405.20.0",
				OS:       "AKSUbuntu",
				SKU:      "2404containerd",
				Version:  "202405.20.0",
			},
			{
				FullName: "AKSUbuntu-1804gen2-2022.08.29",
				OS:       "AKSUbuntu",
				SKU:      "1804gen2",
				Version:  "2022.08.29",
			},
		},
	}, nil
}
