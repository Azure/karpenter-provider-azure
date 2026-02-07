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

package instance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

// buildAKSMachineTemplate creates an in-memory AKS machine template from the provided specs.
// May return error whenever required fields are not set (check carefully).
func (p *DefaultAKSMachineProvider) buildAKSMachineTemplate(ctx context.Context, instanceType *corecloudprovider.InstanceType, capacityType string, zone string, nodeClass *v1beta1.AKSNodeClass, nodeClaim *karpv1.NodeClaim, creationTimestamp time.Time) (*armcontainerservice.Machine, error) {
	if instanceType == nil {
		return nil, fmt.Errorf("InstanceType is not set")
	}
	if nodeClass == nil {
		return nil, fmt.Errorf("NodeClass is not set")
	}
	if nodeClaim == nil {
		return nil, fmt.Errorf("NodeClaim is not set")
	}

	// NodeImageVersion
	// E.g., "AKSUbuntu-2204gen2containerd-2023.11.15"
	vmImageID, err := p.imageResolver.ResolveNodeImageFromNodeClass(nodeClass, instanceType)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve VM image ID: %w", err)
	}
	nodeImageVersion, err := utils.GetAKSMachineNodeImageVersionFromImageID(vmImageID)
	if err != nil {
		return nil, fmt.Errorf("failed to convert VM image ID to NodeImageVersion: %w", err)
	}

	// GPUProfile
	gpuProfile := configureGPUProfile(instanceType)

	// OrchestratorVersion (i.e., Kubernetes version)
	orchestratorVersion, err := nodeClass.GetKubernetesVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes version from NodeClass %q: %w", nodeClass.Name, err)
	}

	// OSSKU, EnableFIPS
	osSku, enableFIPS, err := configureOSSKUAndFIPs(nodeClass, orchestratorVersion)
	if err != nil {
		return nil, err
	}

	// OSDiskType
	osDiskType, err := configureOSDiskType(ctx, p.instanceTypeProvider, nodeClass, instanceType)
	if err != nil {
		return nil, err
	}

	// NodeTaints, NodeInitializationTaints
	nodeInitializationTaints, nodeTaints := configureTaints(nodeClaim)

	// NodeLabels, Mode
	nodeLabels, modePtr := configureLabelsAndMode(nodeClaim, instanceType, capacityType)

	// Priority (e.g., regular, spot)
	priority := configurePriority(capacityType)

	// Tags (to be put on AKS machine and all affiliated resources)
	// Note: as of the time of writing, AKS machine API does not support tags on NICs. This could be fixed server-side.
	tags := ConfigureAKSMachineTags(options.FromContext(ctx), nodeClass, nodeClaim, creationTimestamp)

	return &armcontainerservice.Machine{
		Zones: utils.MakeARMZonesFromAKSLabelZone(zone),
		Properties: &armcontainerservice.MachineProperties{
			NodeImageVersion: lo.ToPtr(nodeImageVersion),
			Network: &armcontainerservice.MachineNetworkProperties{
				VnetSubnetID: nodeClass.Spec.VNETSubnetID, // AKS machine API take control, if nil
				// As of the time of writing, the current version of AKS machine API support just that with nil. That is unlikely to change.
				// PodSubnetID:          "",
				// EnableNodePublicIP:   nil,
				// NodePublicIPPrefixID: "",
				// IPTags:               nil,
			},
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: lo.ToPtr(instanceType.Name),
				// GPUInstanceProfile: nil,
				GpuProfile: gpuProfile,
			},
			OperatingSystem: &armcontainerservice.MachineOSProfile{
				OSType:       lo.ToPtr(armcontainerservice.OSTypeLinux),
				OSSKU:        osSku,
				OSDiskSizeGB: nodeClass.Spec.OSDiskSizeGB, // AKS machine API defaults it if nil
				OSDiskType:   osDiskType,
				EnableFIPS:   enableFIPS,
				// LinuxProfile:   nil,
				// WindowsProfile: nil,
			},

			Kubernetes: &armcontainerservice.MachineKubernetesProfile{
				NodeLabels:          nodeLabels,
				OrchestratorVersion: lo.ToPtr(orchestratorVersion),
				// KubeletDiskType:          "",
				KubeletConfig:            configureKubeletConfig(nodeClass),
				NodeInitializationTaints: nodeInitializationTaints,
				NodeTaints:               nodeTaints,
				MaxPods:                  nodeClass.Spec.MaxPods, // AKS machine API defaults it per network plugins if nil.
				// WorkloadRuntime:          nil,
				// ArtifactStreamingProfile: nil,
			},

			Mode: modePtr,
			Security: &armcontainerservice.MachineSecurityProfile{
				SSHAccess:              lo.ToPtr(armcontainerservice.AgentPoolSSHAccessLocalUser),
				EnableEncryptionAtHost: lo.ToPtr(nodeClass.GetEncryptionAtHost()),
				// EnableVTPM:             nil,
				// EnableSecureBoot:       nil,
			},
			Priority: priority,

			Tags: tags,
		},
	}, nil
}

func configureGPUProfile(instanceType *corecloudprovider.InstanceType) *armcontainerservice.GPUProfile {
	// If none is specified, then that's not GPU instance, so nil is fine. Current version of AKS machine API supports this.
	if utils.IsNvidiaEnabledSKU(instanceType.Name) {
		return &armcontainerservice.GPUProfile{
			Driver: lo.ToPtr(armcontainerservice.GPUDriverInstall),
			// DriverType: nil,
		}
	}
	return nil
}

func configureOSDiskType(ctx context.Context, instanceTypeProvider instancetype.Provider, nodeClass *v1beta1.AKSNodeClass, instanceType *corecloudprovider.InstanceType) (*armcontainerservice.OSDiskType, error) {
	// Karpenter defaults to Managed, but decides whether to use Ephemeral
	sku, err := instanceTypeProvider.Get(ctx, nodeClass, instanceType.Name)
	if err != nil {
		return nil, err
	}
	if instancetype.UseEphemeralDisk(sku, nodeClass) {
		return lo.ToPtr(armcontainerservice.OSDiskTypeEphemeral), nil
	}
	return lo.ToPtr(armcontainerservice.OSDiskTypeManaged), nil
}

func configurePriority(capacityType string) *armcontainerservice.ScaleSetPriority {
	switch capacityType {
	case karpv1.CapacityTypeSpot:
		return lo.ToPtr(armcontainerservice.ScaleSetPrioritySpot)
	case karpv1.CapacityTypeOnDemand:
		return lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular)
	default:
		// Karpenter defaults to Regular
		return lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular)
	}
}

func configureOSSKUAndFIPs(nodeClass *v1beta1.AKSNodeClass, orchestratorVersion string) (*armcontainerservice.OSSKU, *bool, error) {
	// Counterpart for ProvisionModeBootstrappingClient is in customscriptsbootstrap/provisionclientbootstrap.go

	if nodeClass.Spec.ImageFamily == nil {
		return nil, nil, fmt.Errorf("ImageFamily is not set in NodeClass %q", nodeClass.Name)
	}

	var ossku armcontainerservice.OSSKU
	enableFIPS := lo.FromPtr(nodeClass.Spec.FIPSMode) == v1beta1.FIPSModeFIPS

	switch *nodeClass.Spec.ImageFamily {
	case v1beta1.Ubuntu2204ImageFamily:
		ossku = armcontainerservice.OSSKUUbuntu2204
	case v1beta1.Ubuntu2404ImageFamily:
		ossku = armcontainerservice.OSSKUUbuntu2404
	case v1beta1.AzureLinuxImageFamily:
		ossku = armcontainerservice.OSSKUAzureLinux
	case v1beta1.UbuntuImageFamily:
		fallthrough
	default:
		if enableFIPS {
			ossku = armcontainerservice.OSSKUUbuntu
		} else if imagefamily.UseUbuntu2404(orchestratorVersion) {
			ossku = armcontainerservice.OSSKUUbuntu2404
		} else {
			ossku = armcontainerservice.OSSKUUbuntu2204
		}
	}

	return lo.ToPtr(ossku), lo.ToPtr(enableFIPS), nil
}

func configureTaints(nodeClaim *karpv1.NodeClaim) ([]*string, []*string) {
	generalTaints, startupTaints := utils.ExtractTaints(nodeClaim)
	allTaints := lo.Flatten([][]v1.Taint{generalTaints, startupTaints})
	allTaintsStr := lo.Map(allTaints, func(taint v1.Taint, _ int) string { return taint.ToString() })
	// Deduplicate (original behavior used sets.NewString for deduplication)
	allTaintsStr = lo.Uniq(allTaintsStr)

	// Currently, we will use "nodeInitializationTaints" field for all taints, as "taints" field are subjected to server-side reconciliation and extra validation
	// Server-side reconciliation is not necessarily a bad thing, but needs to resolve validation conflicts at least. E.g., system node cannot have hard taints other than CriticalAddonsOnly, per AKS Machine API.
	// If changing, don't forget to update unit + acceptance tests accordingly.
	nodeInitializationTaintPtrs := lo.Map(allTaintsStr, func(taint string, _ int) *string { return lo.ToPtr(taint) })
	nodeTaintPtrs := []*string{}
	return nodeInitializationTaintPtrs, nodeTaintPtrs
}

func configureLabelsAndMode(nodeClaim *karpv1.NodeClaim, instanceType *corecloudprovider.InstanceType, capacityType string) (map[string]*string, *armcontainerservice.AgentPoolMode) {
	// Counterpart for ProvisionModeBootstrappingClient is in customscriptsbootstrap/provisionclientbootstrap.go and instance/vminstance.go

	// We need to get all single-valued requirement labels from the instance type and the nodeClaim to pass down to kubelet.
	// We don't just include single-value labels from the instance type because in the case where the label is NOT single-value on the instance
	// (i.e. there are options), the nodeClaim may have selected one of those options via its requirements which we want to include.
	// These may contain restricted labels from the pod that we need to filter out. We don't bother filtering the instance type requirements below because
	// we know those can't be restricted since they're controlled by the provider and none use the kubernetes.io domain.
	claimLabels := labels.GetFilteredSingleValuedRequirementLabels(
		scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...),
		func(k string, req *scheduling.Requirement) bool {
			return labels.IsKubeletLabel(k)
		},
	)
	// Filter nodeClaim.Labels to only include kubelet-compatible labels.
	// In karpenter v1.9+, nodeClaim.Labels may contain custom labels resolved from requirements
	// (e.g. node-restriction.kubernetes.io/*) that should not be passed to kubelet.
	filteredNodeClaimLabels := lo.OmitBy(nodeClaim.Labels, func(k string, _ string) bool {
		return !labels.IsKubeletLabel(k)
	})
	nodeLabels := lo.Assign(filteredNodeClaimLabels, claimLabels, labels.GetAllSingleValuedRequirementLabels(instanceType.Requirements), map[string]string{karpv1.CapacityTypeLabelKey: capacityType})
	var modePtr *armcontainerservice.AgentPoolMode
	if modeFromLabel, ok := nodeLabels["kubernetes.azure.com/mode"]; ok && modeFromLabel == "system" {
		modePtr = lo.ToPtr(armcontainerservice.AgentPoolModeSystem)
	} else {
		modePtr = lo.ToPtr(armcontainerservice.AgentPoolModeUser)
	}

	// TEMPORARY
	// TODO(mattchr): verify/rework this, also do the same for taints (which don't have sanitization logic like this yet)
	labelsToRemove := []string{
		"beta.kubernetes.io/instance-type",
		"failure-domain.beta.kubernetes.io/region",
		"beta.kubernetes.io/os",
		"beta.kubernetes.io/arch",
		"failure-domain.beta.kubernetes.io/zone",
		"topology.kubernetes.io/zone",
		"topology.kubernetes.io/region",
		"node.kubernetes.io/instance-type",
		"kubernetes.io/arch",
		"kubernetes.io/os",
		"node.kubernetes.io/windows-build",
	}
	nodeLabels = lo.OmitByKeys(nodeLabels, labelsToRemove)
	// Remove all labels with kubernetes.azure.com prefix
	nodeLabels = lo.OmitBy(nodeLabels, func(key string, _ string) bool {
		return strings.HasPrefix(key, "kubernetes.azure.com/")
	})

	nodeLabelPtrs := make(map[string]*string, len(nodeLabels))
	for k, v := range nodeLabels {
		nodeLabelPtrs[k] = lo.ToPtr(v)
	}

	return nodeLabelPtrs, modePtr
}

// ConfigureAKSMachineTags returns the tags to be applied to AKS machine instances and their affiliated resources.
// This includes all standard tags plus the AKS machine distinguishing tag.
func ConfigureAKSMachineTags(opts *options.Options, nodeClass *v1beta1.AKSNodeClass, nodeClaim *karpv1.NodeClaim, creationTimestamp time.Time) map[string]*string {
	// TODO: move that code here instead, as AKS machine instances will be the main path forward
	// Can move when other provision modes are removed too.
	// Right now we are willing to call this just to avoid unnecessary code duplication.
	tags := launchtemplate.Tags(opts, nodeClass, nodeClaim)

	// Add AKS machine distinguishing tags
	tags[launchtemplate.KarpenterAKSMachineNodeClaimTagKey] = lo.ToPtr(nodeClaim.Name)
	tags[launchtemplate.KarpenterAKSMachineCreationTimestampTagKey] = lo.ToPtr(AKSMachineTimestampToTag(creationTimestamp))

	return tags
}

func configureKubeletConfig(nodeClass *v1beta1.AKSNodeClass) *armcontainerservice.KubeletConfig {
	// Counterpart for ProvisionModeBootstrappingClient is in customscriptsbootstrap/provisionclientbootstrap.go and imagefamily/resolver.go

	if nodeClass == nil || nodeClass.Spec.Kubelet == nil {
		return nil
	}

	kubeletConfig := &armcontainerservice.KubeletConfig{}

	// Map from v1beta1.KubeletConfiguration to AKS machine KubeletConfig
	if nodeClass.Spec.Kubelet.CPUManagerPolicy != "" {
		kubeletConfig.CPUManagerPolicy = lo.ToPtr(nodeClass.Spec.Kubelet.CPUManagerPolicy)
	}

	kubeletConfig.CPUCfsQuota = nodeClass.Spec.Kubelet.CPUCFSQuota

	if nodeClass.Spec.Kubelet.CPUCFSQuotaPeriod.Duration.String() != "0s" {
		kubeletConfig.CPUCfsQuotaPeriod = lo.ToPtr(nodeClass.Spec.Kubelet.CPUCFSQuotaPeriod.Duration.String())
	}

	kubeletConfig.ImageGcHighThreshold = nodeClass.Spec.Kubelet.ImageGCHighThresholdPercent
	kubeletConfig.ImageGcLowThreshold = nodeClass.Spec.Kubelet.ImageGCLowThresholdPercent

	if nodeClass.Spec.Kubelet.TopologyManagerPolicy != "" {
		kubeletConfig.TopologyManagerPolicy = lo.ToPtr(nodeClass.Spec.Kubelet.TopologyManagerPolicy)
	}

	if len(nodeClass.Spec.Kubelet.AllowedUnsafeSysctls) > 0 {
		kubeletConfig.AllowedUnsafeSysctls = lo.Map(nodeClass.Spec.Kubelet.AllowedUnsafeSysctls, func(sysctl string, _ int) *string { return lo.ToPtr(sysctl) })
	}

	// Convert container log max size to MB
	if nodeClass.Spec.Kubelet.ContainerLogMaxSize != "" {
		kubeletConfig.ContainerLogMaxSizeMB = convertContainerLogMaxSizeToMB(nodeClass.Spec.Kubelet.ContainerLogMaxSize)
	}

	kubeletConfig.ContainerLogMaxFiles = nodeClass.Spec.Kubelet.ContainerLogMaxFiles

	// Convert PodPidsLimit to PodMaxPids
	if nodeClass.Spec.Kubelet.PodPidsLimit != nil {
		kubeletConfig.PodMaxPids = convertPodMaxPids(*nodeClass.Spec.Kubelet.PodPidsLimit)
	}

	return kubeletConfig
}

// convertContainerLogMaxSizeToMB converts string size to MB integer
// TODO: refactor this to generic "convertSizeToMB" than just "convertContainerLogMaxSizeToMB"
func convertContainerLogMaxSizeToMB(containerLogMaxSize string) *int32 {
	// TODO: move that code here instead, as AKS machine instances will be the main path forward
	// Can move when other provision modes are removed too.
	// Right now we are willing to call this just to avoid unnecessary code duplication.
	return customscriptsbootstrap.ConvertContainerLogMaxSizeToMB(containerLogMaxSize)
}

func convertPodMaxPids(podPidsLimit int64) *int32 {
	// TODO: move that code here instead, as AKS machine instances will be the main path forward
	// Can move when other provision modes are removed too.
	// Right now we are willing to call this just to avoid unnecessary code duplication.
	return customscriptsbootstrap.ConvertPodMaxPids(lo.ToPtr(podPidsLimit))
}

// parseVMImageID parses a VM image ID and extracts the required components for custom OS image headers.
// Expected format: /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Compute/galleries/{galleryName}/images/{imageName}/versions/{version}
func parseVMImageID(vmImageID string) (subscriptionID, resourceGroup, gallery, imageName, version string, err error) {
	if vmImageID == "" {
		return "", "", "", "", "", fmt.Errorf("vmImageID is empty")
	}

	res, err := arm.ParseResourceID(vmImageID)
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("invalid vmImageID format: %w", err)
	}

	// Validate it's a gallery image version resource type (case-insensitive, as Azure resource IDs are case-insensitive)
	if !strings.EqualFold(res.ResourceType.String(), "Microsoft.Compute/galleries/images/versions") {
		return "", "", "", "", "", fmt.Errorf("invalid vmImageID format: expected resource type Microsoft.Compute/galleries/images/versions, got %s", res.ResourceType.String())
	}

	// Validate we have the required parent chain (gallery -> image -> version)
	if res.Parent == nil || res.Parent.Parent == nil {
		return "", "", "", "", "", fmt.Errorf("invalid vmImageID format: missing parent resources")
	}

	version = res.Name
	imageName = res.Parent.Name
	gallery = res.Parent.Parent.Name
	subscriptionID = res.SubscriptionID
	resourceGroup = res.ResourceGroupName

	// Validate none of the extracted values are empty
	if version == "" {
		return "", "", "", "", "", fmt.Errorf("invalid vmImageID format: missing version")
	}
	if imageName == "" {
		return "", "", "", "", "", fmt.Errorf("invalid vmImageID format: missing image name")
	}
	if gallery == "" {
		return "", "", "", "", "", fmt.Errorf("invalid vmImageID format: missing gallery name")
	}
	if subscriptionID == "" {
		return "", "", "", "", "", fmt.Errorf("invalid vmImageID format: missing subscription ID")
	}
	if resourceGroup == "" {
		return "", "", "", "", "", fmt.Errorf("invalid vmImageID format: missing resource group")
	}

	return subscriptionID, resourceGroup, gallery, imageName, version, nil
}
