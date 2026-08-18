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
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	types "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	"github.com/samber/lo"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

// Windows image definitions. These match the SKU names returned by the AKS node image
// versions API (e.g. "windows-2022-containerd-gen2") and live in the AKSWindows shared
// image gallery. Windows is amd64-only.
const (
	Windows2022ContainerdGen2ImageDefinition = "windows-2022-containerd-gen2"
	Windows2022ContainerdImageDefinition     = "windows-2022-containerd"

	Windows2025Gen2ImageDefinition   = "windows-2025-gen2"
	Windows2025ImageDefinition       = "windows-2025"
	Windows2025Gen2TLImageDefinition = "windows-2025-gen2-tl"
)

// Windows2022 defines AKS Windows Server 2022 node images.
// Windows nodes are only supported via the AKS Machine API provision mode, which always
// uses the AKS-managed shared image gallery (SIG). Accordingly, DefaultImages() returns images
// only when useSIG is true; the community image gallery (CIG) is not supported for Windows.
type Windows2022 struct {
	Options *parameters.StaticParameters
}

func (Windows2022) Name() string {
	return v1beta1.Windows2022ImageFamily
}

func (Windows2022) DefaultImages(useSIG bool, fipsMode *v1beta1.FIPSMode, trustedLaunch bool) []types.DefaultImageOutput {
	if lo.FromPtr(fipsMode) == v1beta1.FIPSModeFIPS || trustedLaunch {
		return []types.DefaultImageOutput{}
	}
	return windowsDefaultImages(useSIG,
		windowsImage(Windows2022ContainerdGen2ImageDefinition, v1beta1.HyperVGenerationV2, "aks-windows-2022-containerd-gen2"),
		windowsImage(Windows2022ContainerdImageDefinition, v1beta1.HyperVGenerationV1, "aks-windows-2022-containerd"),
	)
}

// Windows2025 defines AKS Windows Server 2025 node images.
type Windows2025 struct {
	Options *parameters.StaticParameters
}

func (Windows2025) Name() string {
	return v1beta1.Windows2025ImageFamily
}

func (Windows2025) DefaultImages(useSIG bool, _ *v1beta1.FIPSMode, trustedLaunch bool) []types.DefaultImageOutput {
	if trustedLaunch {
		return windowsDefaultImages(useSIG,
			windowsImage(Windows2025Gen2TLImageDefinition, v1beta1.HyperVGenerationV2, "aks-windows-2025-gen2-tl"),
		)
	}
	return windowsDefaultImages(useSIG,
		windowsImage(Windows2025Gen2ImageDefinition, v1beta1.HyperVGenerationV2, "aks-windows-2025-gen2"),
		windowsImage(Windows2025ImageDefinition, v1beta1.HyperVGenerationV1, "aks-windows-2025"),
	)
}

func windowsDefaultImages(useSIG bool, images ...types.DefaultImageOutput) []types.DefaultImageOutput {
	if !useSIG {
		return []types.DefaultImageOutput{}
	}
	return images
}

// windowsImage builds a Windows DefaultImageOutput for the AKSWindows shared image gallery.
// PublicGalleryURL is intentionally left empty: Windows is SIG-only.
func windowsImage(imageDefinition, hyperVGeneration, distro string) types.DefaultImageOutput {
	return types.DefaultImageOutput{
		GalleryResourceGroup: AKSWindowsResourceGroup,
		GalleryName:          AKSWindowsGalleryName,
		ImageDefinition:      imageDefinition,
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
			scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, hyperVGeneration),
		),
		Distro: distro,
	}
}

func (w Windows2022) ScriptlessCustomData(
	_ *bootstrap.KubeletConfiguration,
	_ []v1.Taint,
	_ map[string]string,
	_ *string,
	_ *cloudprovider.InstanceType,
) bootstrap.Bootstrapper {
	return windowsUnsupportedBootstrapper{family: w.Name()}
}

func (w Windows2022) CustomScriptsNodeBootstrapping(
	_ *bootstrap.KubeletConfiguration,
	_ []v1.Taint,
	_ []v1.Taint,
	_ map[string]string,
	_ *cloudprovider.InstanceType,
	_ string,
	_ string,
	_ types.NodeBootstrappingAPI,
	_ *v1beta1.FIPSMode,
	_ *v1beta1.LocalDNS,
	_ *v1beta1.ArtifactStreaming,
	_ *v1beta1.LinuxOSConfiguration,
	_ *bool,
	_ *bool,
) customscriptsbootstrap.Bootstrapper {
	return windowsUnsupportedBootstrapper{family: w.Name()}
}

func (w Windows2025) ScriptlessCustomData(
	_ *bootstrap.KubeletConfiguration,
	_ []v1.Taint,
	_ map[string]string,
	_ *string,
	_ *cloudprovider.InstanceType,
) bootstrap.Bootstrapper {
	return windowsUnsupportedBootstrapper{family: w.Name()}
}

func (w Windows2025) CustomScriptsNodeBootstrapping(
	_ *bootstrap.KubeletConfiguration,
	_ []v1.Taint,
	_ []v1.Taint,
	_ map[string]string,
	_ *cloudprovider.InstanceType,
	_ string,
	_ string,
	_ types.NodeBootstrappingAPI,
	_ *v1beta1.FIPSMode,
	_ *v1beta1.LocalDNS,
	_ *v1beta1.ArtifactStreaming,
	_ *v1beta1.LinuxOSConfiguration,
	_ *bool,
	_ *bool,
) customscriptsbootstrap.Bootstrapper {
	return windowsUnsupportedBootstrapper{family: w.Name()}
}

// windowsUnsupportedBootstrapper implements both bootstrap.Bootstrapper and
// customscriptsbootstrap.Bootstrapper, returning a clear error if a non-AKS-Machine-API
// provision mode ever attempts to bootstrap a Windows node.
type windowsUnsupportedBootstrapper struct {
	family string
}

func (b windowsUnsupportedBootstrapper) Script() (string, error) {
	return "", b.err()
}

func (b windowsUnsupportedBootstrapper) GetCustomDataAndCSE(_ context.Context) (string, string, error) {
	return "", "", b.err()
}

func (b windowsUnsupportedBootstrapper) err() error {
	return fmt.Errorf("windows image family %q is only supported with PROVISION_MODE=aksmachineapi", b.family)
}
