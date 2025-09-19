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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

// buildAKSMachineTemplate creates an in-memory AKS machine template from the provided specs.
// May return error whenever required fields are not set (check carefully).
func (p *DefaultAKSMachineProvider) buildAKSMachineTemplate(ctx context.Context, instanceType *corecloudprovider.InstanceType, capacityType string, zone string, nodeClass *v1beta1.AKSNodeClass, nodeClaim *karpv1.NodeClaim, creationTimestamp time.Time) (*armcontainerservice.Machine, error) { // XPMT: ✅
	if instanceType == nil {
		return nil, fmt.Errorf("InstanceType is not set")
	}
	if nodeClass == nil {
		return nil, fmt.Errorf("NodeClass is not set")
	}
	if nodeClaim == nil {
		return nil, fmt.Errorf("NodeClaim is not set")
	}

	// GPUProfile
	var gpuProfilePtr *armcontainerservice.GPUProfile
	// If none is specified, then that's not GPU instance, so nil is fine. Current version of AKS machine API supports this.
	if utils.IsNvidiaEnabledSKU(instanceType.Name) {
		gpuProfilePtr = &armcontainerservice.GPUProfile{
			Driver: lo.ToPtr(armcontainerservice.GPUDriverInstall), // XPMT: ✅ (from CSE)
			// DriverType: nil,                                            // XPMT: REFORMATTED, 🚫 (Windows)
		}
	}

	// OrchestratorVersion (i.e., Kubernetes version)
	orchestratorVersion, err := nodeClass.GetKubernetesVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes version from NodeClass %q: %w", nodeClass.Name, err)
	}

	// OSSKU, ArtifactStreamingProfile
	osskuPtr, enabledArtifactStreamingPtr, enableFIPsPtr, err := configureOSSKUArtifactStreamingAndFIPs(nodeClass, instanceType, orchestratorVersion)
	if err != nil {
		return nil, err
	}

	// OSDiskType
	osDiskTypePtr := lo.ToPtr(armcontainerservice.OSDiskTypeManaged) // Karpenter defaults to Managed, but decides whether to use Ephemeral
	sku, err := p.instanceTypeProvider.Get(ctx, nodeClass, instanceType.Name)
	if err != nil {
		return nil, err
	}
	if instancetype.UseEphemeralDisk(sku, nodeClass) {
		osDiskTypePtr = lo.ToPtr(armcontainerservice.OSDiskTypeEphemeral)
	}

	// NodeTaints, NodeInitializationTaints
	nodeInitializationTaintPtrs, nodeTaintPtrs := configureTaints(nodeClaim)

	// NodeLabels, Mode
	nodeLabelPtrs, modePtr := configureLabelsAndMode(nodeClaim, instanceType, capacityType)

	// Priority (e.g., regular, spot)
	var priorityPtr *armcontainerservice.ScaleSetPriority
	switch capacityType {
	case karpv1.CapacityTypeSpot:
		priorityPtr = lo.ToPtr(armcontainerservice.ScaleSetPrioritySpot)
	case karpv1.CapacityTypeOnDemand:
		priorityPtr = lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular)
	default:
		// Karpenter defaults to Regular
		priorityPtr = lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular)
	}

	// Tags (to be put on AKS machine and all affiliated resources)
	// Note: as of the time of writing, AKS machine API does not support tags on NICs. This could be fixed server-side.
	tags := ConfigureAKSMachineTags(options.FromContext(ctx), nodeClass, nodeClaim, creationTimestamp)

	return &armcontainerservice.Machine{ // XPMT: ✅
		Zones: utils.GetARMZonesFromAKSZone(zone), // XPMT: ✅ (from CRP VM)
		Properties: &armcontainerservice.MachineProperties{ // XPMT: ✅
			Network: &armcontainerservice.MachineNetworkProperties{ // XPMT: ✅
				VnetSubnetID: nodeClass.Spec.VNETSubnetID, // AKS machine API take control, if nil    // XPMT: ✅ (from launch template > static parameters > CSE)
				// As of the time of writing, the current version of AKS machine API support just that with nil. That is unlikely to change.
				// PodSubnetID:          "",                                  // XPMT: 🆕🚫
				// EnableNodePublicIP:   nil,                                 // XPMT: 🆕🚫
				// NodePublicIPPrefixID: "",                                  // XPMT: 🆕🚫
				// IPTags:               nil,                                 // XPMT: 🆕🚫
			},
			Hardware: &armcontainerservice.MachineHardwareProfile{ // XPMT: ✅
				VMSize: lo.ToPtr(instanceType.Name), // XPMT: ✅ (from CRP VM; although our type was armcompute enum, while is is string, but exactly the same)
				// GPUInstanceProfile: nil,                           // XPMT: 🚫
				GpuProfile: gpuProfilePtr, // XPMT: ✅ (from CSE)
			},
			OperatingSystem: &armcontainerservice.MachineOSProfile{ // XPMT: ✅
				OSType:       lo.ToPtr(armcontainerservice.OSTypeLinux), // XPMT: ✅ (obvious)
				OSSKU:        osskuPtr,                                  // XPMT: ✅ (CSE)
				OSDiskSizeGB: nodeClass.Spec.OSDiskSizeGB,               // AKS machine API defaults it if nil   // XPMT ✅ (VM)
				OSDiskType:   osDiskTypePtr,                             // XPMT: ✅ (VM and CSE)
				EnableFIPS:   enableFIPsPtr,                             // XPMT: ✅ (CSE)
				// LinuxProfile:   nil,                  // XPMT: 🚫
				// WindowsProfile: nil,                  // XPMT: 🚫
			},

			Kubernetes: &armcontainerservice.MachineKubernetesProfile{ // XPMT: ✅
				NodeLabels:          nodeLabelPtrs,                 // XPMT: ✅ (CSE, various, mostly from launchtemplate)
				OrchestratorVersion: lo.ToPtr(orchestratorVersion), // XPMT: ✅ (CSE)
				// KubeletDiskType:          "",                                                 // XPMT: 🚫
				KubeletConfig:            configureKubeletConfig(nodeClass), // XPMT: ✅
				NodeInitializationTaints: nodeInitializationTaintPtrs,       // XPMT: ✅ (from CSE > defaultResolver)
				NodeTaints:               nodeTaintPtrs,                     // XPMT: ✅ (from CSE > defaultResolver)
				MaxPods:                  nodeClass.Spec.MaxPods,            // AKS machine API defaults it per network plugins if nil.                             // XPMT: ✅ (from CSE > defaultResolver)
				// WorkloadRuntime:          nil,                                                // XPMT: 🚫
				ArtifactStreamingProfile: &armcontainerservice.AgentPoolArtifactStreamingProfile{
					Enabled: enabledArtifactStreamingPtr, // XPMT: ✅ (from CSE)
				},
			},

			Mode: modePtr, // XPMT: ✅ (from CSE)
			Security: &armcontainerservice.AgentPoolSecurityProfile{ // XPMT: ✅
				SSHAccess: lo.ToPtr(armcontainerservice.AgentPoolSSHAccessLocalUser), // XPMT: ✅ (from CSE)
				// EnableVTPM:       nil,                // XPMT: 🚫
				// EnableSecureBoot: nil,                // XPMT: 🚫
			},
			Priority: priorityPtr, // XPMT: ✅ (obvious)

			Tags: tags, // XPMT: ✅ (from VM) ⚠️ tag is not copied to NIC yet
		},
	}, nil
}

func configureOSSKUArtifactStreamingAndFIPs(nodeClass *v1beta1.AKSNodeClass, instanceType *corecloudprovider.InstanceType, orchestratorVersion string) (*armcontainerservice.OSSKU, *bool, *bool, error) {
	// Counterpart for ProvisionModeBootstrappingClient is in customscriptsbootstrap/provisionclientbootstrap.go

	if nodeClass.Spec.ImageFamily == nil {
		return nil, nil, nil, fmt.Errorf("ImageFamily is not set in NodeClass %q", nodeClass.Name)
	}

	var ossku armcontainerservice.OSSKU
	var enabledArtifactStreaming bool
	var enableFIPs *bool
	var isAmd64 = true
	if err := instanceType.Requirements.Compatible(scheduling.NewRequirements(scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureArm64))); err == nil {
		isAmd64 = false
	}

	switch *nodeClass.Spec.ImageFamily {
	case v1beta1.UbuntuImageFamily:
		if lo.FromPtr(nodeClass.Spec.FIPSMode) == v1beta1.FIPSModeFIPS {
			ossku = armcontainerservice.OSSKUUbuntu
			enabledArtifactStreaming = false
			enableFIPs = lo.ToPtr(true)
		} else {
			ossku = armcontainerservice.OSSKUUbuntu2204
			enabledArtifactStreaming = isAmd64
		}
	case v1beta1.Ubuntu2204ImageFamily:
		ossku = armcontainerservice.OSSKUUbuntu2204
		enabledArtifactStreaming = isAmd64
	case v1beta1.AzureLinuxImageFamily:
		ossku = armcontainerservice.OSSKUAzureLinux
		if imagefamily.UseAzureLinux3(orchestratorVersion) {
			enabledArtifactStreaming = false
		} else if orchestratorVersion == "" {
			// Not enable artifact streaming to be safe, as we cannot really control AzureLinux version, and we don't know what the API will default us to
			// Although, in practice, this is not really possible as version should always be populated.
			enabledArtifactStreaming = false
		} else {
			enabledArtifactStreaming = isAmd64
		}
	default:
		return nil, nil, nil, fmt.Errorf("unsupported image family %q in NodeClass %q", *nodeClass.Spec.ImageFamily, nodeClass.Name)
	}

	return lo.ToPtr(ossku), lo.ToPtr(enabledArtifactStreaming), enableFIPs, nil
}

func configureTaints(nodeClaim *karpv1.NodeClaim) ([]*string, []*string) {
	// Counterpart for ProvisionModeBootstrappingClient is in imagefamily/resolver.go

	nodeInitializationTaints := lo.Map(nodeClaim.Spec.StartupTaints, func(taint v1.Taint, _ int) string { return taint.ToString() })
	nodeTaints := lo.Map(nodeClaim.Spec.Taints, func(taint v1.Taint, _ int) string { return taint.ToString() })
	allTaints := sets.NewString(nodeInitializationTaints...).Union(sets.NewString(nodeTaints...))
	if !allTaints.Has(karpv1.UnregisteredNoExecuteTaint.ToString()) {
		nodeInitializationTaints = append(nodeInitializationTaints, karpv1.UnregisteredNoExecuteTaint.ToString())
	}
	nodeInitializationTaintPtrs := lo.Map(nodeInitializationTaints, func(taint string, _ int) *string { return lo.ToPtr(taint) })
	nodeTaintPtrs := lo.Map(nodeTaints, func(taint string, _ int) *string { return lo.ToPtr(taint) })
	return nodeInitializationTaintPtrs, nodeTaintPtrs
}

func configureLabelsAndMode(nodeClaim *karpv1.NodeClaim, instanceType *corecloudprovider.InstanceType, capacityType string) (map[string]*string, *armcontainerservice.AgentPoolMode) {
	// Counterpart for ProvisionModeBootstrappingClient is in customscriptsbootstrap/provisionclientbootstrap.go and instance/vminstance.go

	nodeLabels := lo.Assign(nodeClaim.Labels, offerings.GetAllSingleValuedRequirementLabels(instanceType), map[string]string{karpv1.CapacityTypeLabelKey: capacityType})
	var modePtr *armcontainerservice.AgentPoolMode
	if modeFromLabel, ok := nodeLabels["kubernetes.azure.com/mode"]; ok && modeFromLabel == "system" {
		modePtr = lo.ToPtr(armcontainerservice.AgentPoolModeSystem)
	} else {
		modePtr = lo.ToPtr(armcontainerservice.AgentPoolModeUser)
	}

	// XPMT: TEMPORARY
	// XPMT: TODO(charliedmcb): verify/rework this, also do the same for taints (which don't have sanitization logic like this yet)
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
	for _, label := range labelsToRemove {
		delete(nodeLabels, label)
	}
	// Remove all labels with kubernetes.azure.com prefix
	for label := range nodeLabels {
		if strings.HasPrefix(label, "kubernetes.azure.com/") {
			delete(nodeLabels, label)
		}
	}

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
		return &armcontainerservice.KubeletConfig{}
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

	parts := strings.Split(vmImageID, "/")
	if len(parts) < 12 {
		return "", "", "", "", "", fmt.Errorf("invalid vmImageID format: expected at least 12 parts, got %d", len(parts))
	}

	// Validate expected static parts
	if parts[1] != "subscriptions" || parts[3] != "resourceGroups" ||
		parts[5] != "providers" || parts[6] != "Microsoft.Compute" ||
		parts[7] != "galleries" || parts[9] != "images" || parts[11] != "versions" {
		return "", "", "", "", "", fmt.Errorf("invalid vmImageID format: unexpected path structure")
	}

	if len(parts) < 13 {
		return "", "", "", "", "", fmt.Errorf("invalid vmImageID format: missing version")
	}

	subscriptionID = parts[2]
	resourceGroup = parts[4]
	gallery = parts[8]
	imageName = parts[10]
	version = parts[12]

	return subscriptionID, resourceGroup, gallery, imageName, version, nil
}
