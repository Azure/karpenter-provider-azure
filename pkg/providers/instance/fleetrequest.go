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
	"errors"
	"fmt"
	"maps"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet/v2"
	"github.com/Azure/skewer"
	"github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/allocationstrategy"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/fleet"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/localdns"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/networksecuritygroup"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

const (
	fleetMaxCandidateSKUs = 10
	fleetTargetCapacity   = int32(1)
	// Matches cloudprovider.NodeClassReadinessUnknownReason, which cannot be imported here without a cycle.
	fleetNodeClassReadinessUnknownReason = "NodeClassReadinessUnknown"
)

// fleetTemplateProfile identifies the template settings that Fleet requires all candidate SKUs to share.
type fleetTemplateProfile struct {
	architecture    string
	imageID         string
	diskType        string
	diskPlacement   string
	diskSizeGB      int32
	localDNSEnabled bool
}

// FleetCandidateRanker filters and ranks instance offerings for a Fleet request.
type FleetCandidateRanker interface {
	FilterInstanceOfferings(ctx context.Context, instanceOfferings []allocationstrategy.InstanceOffering, requirements scheduling.Requirements) []allocationstrategy.InstanceOffering
}

var _ FleetCandidateRanker = (*allocationstrategy.DefaultProvider)(nil)

// FleetRequestBuilder adapts a NodeClaim into a Fleet provisioning request and Fleet body.
type FleetRequestBuilder struct {
	candidateRanker              FleetCandidateRanker
	instanceTypeProvider         instancetype.Provider
	launchTemplateProvider       *launchtemplate.Provider
	loadBalancerProvider         *loadbalancer.Provider
	networkSecurityGroupProvider *networksecuritygroup.Provider
	location                     string
	diskEncryptionSetID          string
}

// NewFleetRequestBuilder creates a builder with the providers needed to resolve Fleet candidates,
// build a shared launch template, and populate networking details.
func NewFleetRequestBuilder(
	candidateRanker FleetCandidateRanker,
	instanceTypeProvider instancetype.Provider,
	launchTemplateProvider *launchtemplate.Provider,
	loadBalancerProvider *loadbalancer.Provider,
	networkSecurityGroupProvider *networksecuritygroup.Provider,
	location string,
	diskEncryptionSetID string,
) *FleetRequestBuilder {
	return &FleetRequestBuilder{
		candidateRanker:              candidateRanker,
		instanceTypeProvider:         instanceTypeProvider,
		launchTemplateProvider:       launchTemplateProvider,
		loadBalancerProvider:         loadBalancerProvider,
		networkSecurityGroupProvider: networkSecurityGroupProvider,
		location:                     location,
		diskEncryptionSetID:          diskEncryptionSetID,
	}
}

// Build ranks the NodeClaim's instance offerings, selects the capacity type and placement
// scope from the top-ranked offering, and returns the Fleet request with compatible ranked
// SKUs and their available zones together with the Fleet body built from it. The request
// retains the same body for batching.
func (b *FleetRequestBuilder) Build(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*corecloudprovider.InstanceType,
) (*fleet.FleetVMProvisionRequest, *armcomputefleet.Fleet, error) {
	if err := validateFleetNodeClass(ctx, nodeClass); err != nil {
		return nil, nil, err
	}
	representativeSelection, candidates, err := b.selectCandidates(ctx, nodeClaim, instanceTypes)
	if err != nil {
		return nil, nil, err
	}
	skuNames := lo.Map(candidates, func(candidate allocationstrategy.InstanceOffering, _ int) string {
		return candidate.InstanceType.Name
	})
	resolvedSKUs, err := b.resolveSKUs(ctx, skuNames)
	if err != nil {
		return nil, nil, err
	}
	candidates = filterTrustedLaunchCandidates(nodeClass, candidates, resolvedSKUs)
	if len(candidates) == 0 {
		return nil, nil, corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("no instance types support Trusted Launch"))
	}
	candidates, profile, err := selectTemplateProfile(nodeClass, candidates, resolvedSKUs)
	if err != nil {
		return nil, nil, err
	}
	candidates = candidates[:min(fleetMaxCandidateSKUs, len(candidates))]
	representativeSelection.InstanceType = candidates[0].InstanceType
	representativeSelection.Offering = candidates[0].Offerings[0]

	ultraSSD := resolveUltraSSDRequested(nodeClaim)
	launchTemplate, err := b.getLaunchTemplate(ctx, nodeClass, nodeClaim, representativeSelection.InstanceType, profile.architecture, profile.localDNSEnabled, representativeSelection.CapacityType(), representativeSelection.PlacementScope(), ultraSSD)
	if err != nil {
		return nil, nil, err
	}
	req, err := b.buildProvisionRequest(ctx, nodeClass, nodeClaim, representativeSelection, candidates, resolvedSKUs, launchTemplate, ultraSSD)
	if err != nil {
		return nil, nil, err
	}
	body, err := fleet.BuildFleetBody(req, fleetTargetCapacity, launchTemplate.Tags)
	if err != nil {
		return nil, nil, err
	}
	req.FleetBody = body
	return req, body, nil
}

// validateFleetNodeClass mirrors the NodeClass readiness, FIPS, version, and image checks used before VM creation.
func validateFleetNodeClass(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) error {
	if options.FromContext(ctx).EnableFIPS && lo.FromPtr(nodeClass.Spec.FIPSMode) != v1beta1.FIPSModeFIPS {
		return corecloudprovider.NewNodeClassNotReadyError(errors.New("AKSNodeClass spec.fipsMode must be set to FIPS because FIPS is enabled at the cluster level"))
	}
	nodeClassReady := nodeClass.StatusConditions().Get(status.ConditionReady)
	if nodeClassReady.IsFalse() {
		return corecloudprovider.NewNodeClassNotReadyError(errors.New(nodeClassReady.Message))
	}
	if nodeClassReady.IsUnknown() {
		return corecloudprovider.NewCreateError(fmt.Errorf("resolving NodeClass readiness, NodeClass is in Ready=Unknown, %s", nodeClassReady.Message), fleetNodeClassReadinessUnknownReason, "NodeClass is in Ready=Unknown")
	}
	if _, err := nodeClass.GetKubernetesVersion(); err != nil {
		return err
	}
	if _, err := nodeClass.GetImages(); err != nil {
		return err
	}
	return nil
}

// filterTrustedLaunchCandidates drops SKUs that cannot run Trusted Launch, because the Fleet
// security profile is shared by every candidate SKU.
func filterTrustedLaunchCandidates(
	nodeClass *v1beta1.AKSNodeClass,
	candidates []allocationstrategy.InstanceOffering,
	resolvedSKUs map[string]*skewer.SKU,
) []allocationstrategy.InstanceOffering {
	if !nodeClass.IsTrustedLaunchEnabled() {
		return candidates
	}
	return lo.Filter(candidates, func(candidate allocationstrategy.InstanceOffering, _ int) bool {
		sku := resolvedSKUs[candidate.InstanceType.Name]
		if sku == nil {
			return false
		}
		supported, err := sku.IsTrustedLaunchEnabled()
		return err == nil && supported
	})
}

// selectCandidates ranks offerings, then keeps candidates matching the top offering's capacity type and placement scope.
func (b *FleetRequestBuilder) selectCandidates(
	ctx context.Context,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*corecloudprovider.InstanceType,
) (*allocationstrategy.Selection, []allocationstrategy.InstanceOffering, error) {
	requirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)
	candidates := b.candidateRanker.FilterInstanceOfferings(ctx, allocationstrategy.NewInstanceOfferings(instanceTypes), requirements)
	if len(candidates) == 0 || candidates[0].InstanceType == nil || len(candidates[0].Offerings) == 0 {
		return nil, nil, corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("no instance types available"))
	}
	// Keep the top offering's capacity type and placement scope while allowing other zones.
	representativeSelection := &allocationstrategy.Selection{InstanceType: candidates[0].InstanceType, Offering: candidates[0].Offerings[0]}
	matchingRequirements := maps.Clone(representativeSelection.Offering.Requirements)
	delete(matchingRequirements, corev1.LabelTopologyZone)
	candidates = lo.FilterMap(candidates, func(candidate allocationstrategy.InstanceOffering, _ int) (allocationstrategy.InstanceOffering, bool) {
		candidate.Offerings = candidate.Offerings.Available().Compatible(matchingRequirements)
		return candidate, candidate.InstanceType != nil && len(candidate.Offerings) > 0
	})
	if len(candidates) == 0 {
		return nil, nil, corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("no instance types available for selected capacity type and placement"))
	}
	return representativeSelection, candidates, nil
}

// selectTemplateProfile groups candidates by shared template settings and returns the largest compatible group.
func selectTemplateProfile(
	nodeClass *v1beta1.AKSNodeClass,
	candidates []allocationstrategy.InstanceOffering,
	resolvedSKUs map[string]*skewer.SKU,
) ([]allocationstrategy.InstanceOffering, fleetTemplateProfile, error) {
	images, err := nodeClass.GetImages()
	if err != nil {
		return nil, fleetTemplateProfile{}, fmt.Errorf("getting node images: %w", err)
	}

	// Fleet shares one template across its candidates; only candidates with the same template profile can be grouped.
	profiles := make(map[fleetTemplateProfile][]allocationstrategy.InstanceOffering)
	// Preserve rank order so equal-sized profiles prefer the highest-ranked candidate.
	profileOrder := make([]fleetTemplateProfile, 0)
	for _, candidate := range candidates {
		profile, compatible, err := resolveCandidateTemplateProfile(nodeClass, candidate.InstanceType, images, resolvedSKUs)
		if err != nil {
			return nil, fleetTemplateProfile{}, err
		}
		if !compatible {
			continue
		}
		if _, exists := profiles[profile]; !exists {
			profileOrder = append(profileOrder, profile)
		}
		profiles[profile] = append(profiles[profile], candidate)
	}

	return selectLargestTemplateProfile(profiles, profileOrder)
}

// resolveCandidateTemplateProfile derives the shared-template settings for one SKU.
// It returns compatible=false when the SKU lacks an architecture or compatible image, and errors if SKU metadata is missing.
func resolveCandidateTemplateProfile(
	nodeClass *v1beta1.AKSNodeClass,
	instanceType *corecloudprovider.InstanceType,
	images []v1beta1.NodeImage,
	resolvedSKUs map[string]*skewer.SKU,
) (fleetTemplateProfile, bool, error) {
	if instanceType == nil {
		return fleetTemplateProfile{}, false, nil
	}
	architecture := instanceType.Requirements.Get(corev1.LabelArchStable).Any()
	if architecture == "" {
		return fleetTemplateProfile{}, false, nil
	}
	imageID := findCompatibleImageID(instanceType, images)
	if imageID == "" {
		return fleetTemplateProfile{}, false, nil
	}

	sku := resolvedSKUs[instanceType.Name]
	if sku == nil {
		return fleetTemplateProfile{}, false, fmt.Errorf("resolved SKU %q is missing", instanceType.Name)
	}
	osDiskProfile := instancetype.ResolveOSDiskProfileFromSKU(
		sku,
		nodeClass.Spec.OSDiskSizeGB,
		lo.FromPtr(nodeClass.Spec.OSDiskType),
		nodeClass.IsTrustedLaunchEnabled(),
	)
	diskPlacement := ""
	if osDiskProfile.Placement != nil {
		diskPlacement = string(*osDiskProfile.Placement)
	}
	return fleetTemplateProfile{
		architecture:    architecture,
		imageID:         imageID,
		diskType:        string(osDiskProfile.Type),
		diskPlacement:   diskPlacement,
		diskSizeGB:      osDiskProfile.SizeGB,
		localDNSEnabled: localdns.IsSupportedForInstanceType(nodeClass, instanceType.Requirements),
	}, true, nil
}

// findCompatibleImageID returns the first NodeClass image compatible with the instance type, or an empty string.
func findCompatibleImageID(instanceType *corecloudprovider.InstanceType, images []v1beta1.NodeImage) string {
	for _, image := range images {
		imageRequirements := scheduling.NewNodeSelectorRequirements(image.Requirements...)
		err := instanceType.Requirements.Compatible(
			imageRequirements,
			v1beta1.AllowUndefinedWellKnownAndRestrictedLabels,
		)
		if err == nil {
			return image.ID
		}
	}
	return ""
}

// selectLargestTemplateProfile returns the largest profile group, using profileOrder to break ties by candidate rank.
func selectLargestTemplateProfile(
	profiles map[fleetTemplateProfile][]allocationstrategy.InstanceOffering,
	profileOrder []fleetTemplateProfile,
) ([]allocationstrategy.InstanceOffering, fleetTemplateProfile, error) {
	// Keep the largest group to offer Fleet as many compatible SKU alternatives as possible.
	var bestProfile fleetTemplateProfile
	var bestCandidates []allocationstrategy.InstanceOffering
	for _, profile := range profileOrder {
		if len(profiles[profile]) > len(bestCandidates) {
			bestProfile = profile
			bestCandidates = profiles[profile]
		}
	}
	if len(bestCandidates) == 0 {
		return nil, fleetTemplateProfile{}, corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("no instance types match a common Fleet template profile"))
	}
	return bestCandidates, bestProfile, nil
}

// fleetCandidateZones returns the distinct non-empty zones offered by the selected Fleet candidates.
func fleetCandidateZones(candidates []allocationstrategy.InstanceOffering) []string {
	candidateZones := sets.New[string]()
	for _, candidate := range candidates {
		for _, offering := range candidate.Offerings {
			candidateZones.Insert(offering.Requirements.Get(corev1.LabelTopologyZone).Any())
		}
	}
	candidateZones.Delete("")
	return sets.List(candidateZones)
}

// resolveSKUs loads Azure SKU metadata for each candidate name, keyed by SKU name.
func (b *FleetRequestBuilder) resolveSKUs(ctx context.Context, skuNames []string) (map[string]*skewer.SKU, error) {
	resolvedSKUs := make(map[string]*skewer.SKU, len(skuNames))
	for _, name := range skuNames {
		sku, err := b.instanceTypeProvider.Get(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("getting instance type %q: %w", name, err)
		}
		resolvedSKUs[name] = sku
	}
	return resolvedSKUs, nil
}

// resolveNetworking finds the eligible load-balancer pools and managed NSG for the selected subnet.
func (b *FleetRequestBuilder) resolveNetworking(ctx context.Context, nodeClaim *karpv1.NodeClaim, subnetID string) ([]string, string, error) {
	backendPools, err := b.loadBalancerProvider.LoadBalancerBackendPools(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("getting backend pools: %w", err)
	}
	backendPools = filterBackendPools(backendPools, nodeClaim)

	nsgID, err := b.resolveNSG(ctx, subnetID)
	if err != nil {
		return nil, "", err
	}
	return backendPools.IPv4PoolIDs, nsgID, nil
}

// buildProvisionRequest combines the selected candidates and template with NodeClaim, network, and cluster settings.
func (b *FleetRequestBuilder) buildProvisionRequest(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	representativeSelection *allocationstrategy.Selection,
	candidates []allocationstrategy.InstanceOffering,
	resolvedSKUs map[string]*skewer.SKU,
	launchTemplate *launchtemplate.Template,
	ultraSSD bool,
) (*fleet.FleetVMProvisionRequest, error) {
	skuNames := lo.Map(candidates, func(candidate allocationstrategy.InstanceOffering, _ int) string {
		return candidate.InstanceType.Name
	})
	selectedSKUs := make(map[string]*skewer.SKU, len(skuNames))
	for _, skuName := range skuNames {
		selectedSKUs[skuName] = resolvedSKUs[skuName]
	}
	backendPoolIDs, nsgID, err := b.resolveNetworking(ctx, nodeClaim, launchTemplate.SubnetID)
	if err != nil {
		return nil, err
	}
	opts := options.FromContext(ctx)
	return &fleet.FleetVMProvisionRequest{
		NodeClaimName:              nodeClaim.Name,
		NodePoolName:               nodeClaim.Labels[karpv1.NodePoolLabelKey],
		CapacityType:               representativeSelection.CapacityType(),
		UltraSSDEnabled:            ultraSSD,
		AcceptableSKUs:             skuNames,
		ResolvedSKUs:               selectedSKUs,
		AcceptableZones:            fleetCandidateZones(candidates),
		Tags:                       launchTemplate.Tags,
		LaunchTemplate:             launchTemplate,
		TrustedLaunchEnabled:       nodeClass.IsTrustedLaunchEnabled(),
		SecureBootEnabled:          nodeClass.IsSecureBootEnabled(),
		VTPMEnabled:                nodeClass.IsVTPMEnabled(),
		CapacityReservationGroupID: nodeClass.GetCapacityReservationGroupID(),
		SSHPublicKey:               opts.SSHPublicKey,
		AdminUsername:              opts.LinuxAdminUsername,
		NodeIdentities:             opts.NodeIdentities,
		DiskEncryptionSetID:        b.diskEncryptionSetID,
		NSG:                        nsgID,
		LBBackendPools:             backendPoolIDs,
		NetworkPlugin:              opts.NetworkPlugin,
		NetworkPluginMode:          opts.NetworkPluginMode,
		MaxPods:                    utils.GetMaxPods(nodeClass, opts.NetworkPlugin, opts.NetworkPluginMode),
		Location:                   b.location,
		Extensions:                 b.buildExtensions(launchTemplate),
	}, nil
}

// getLaunchTemplate builds the Fleet launch template using a representative candidate and its shared profile.
func (b *FleetRequestBuilder) getLaunchTemplate(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceType *corecloudprovider.InstanceType,
	architecture string,
	localDNSEnabled bool,
	capacityType string,
	placementScope string,
	ultraSSD bool,
) (*launchtemplate.Template, error) {
	additionalLabels := lo.Assign(
		labels.GetAllSingleValuedRequirementLabels(scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)),
		map[string]string{
			karpv1.CapacityTypeLabelKey:     capacityType,
			v1beta1.LabelPlacementScope:     placementScope,
			v1beta1.LabelUltraSSD:           fmt.Sprint(ultraSSD),
			labels.AKSLocalDNSStateLabelKey: lo.Ternary(localDNSEnabled, "enabled", "disabled"),
		},
	)
	launchTemplate, err := b.launchTemplateProvider.GetFleetTemplate(ctx, nodeClass, nodeClaim, instanceType, architecture, additionalLabels)
	if err != nil {
		return nil, fmt.Errorf("getting launch template: %w", err)
	}
	return launchTemplate, nil
}

// resolveNSG returns the managed NSG ID for a BYO VNET, or an empty ID for an AKS-managed VNET.
func (b *FleetRequestBuilder) resolveNSG(ctx context.Context, subnetID string) (string, error) {
	isAKSManagedVNET, err := utils.IsAKSManagedVNET(options.FromContext(ctx).NodeResourceGroup, subnetID)
	if err != nil {
		return "", fmt.Errorf("checking if vnet is managed: %w", err)
	}
	if isAKSManagedVNET {
		return "", nil
	}
	nsg, err := b.networkSecurityGroupProvider.ManagedNetworkSecurityGroup(ctx)
	if err != nil {
		return "", fmt.Errorf("getting managed network security group: %w", err)
	}
	return lo.FromPtr(nsg.ID), nil
}

// buildExtensions returns a Linux Custom Script Extension when the launch template contains a CSE command.
// Fleet does not support Windows launch templates.
func (b *FleetRequestBuilder) buildExtensions(launchTemplate *launchtemplate.Template) []*armcompute.VirtualMachineExtension {
	if launchTemplate.CustomScriptsCSE == "" {
		return nil
	}
	const (
		cseTypeLinux      = "CustomScript"
		csePublisherLinux = "Microsoft.Azure.Extensions"
		cseVersionLinux   = "2.0"
	)
	return []*armcompute.VirtualMachineExtension{{
		Location: lo.ToPtr(b.location),
		Name:     lo.ToPtr(cseNameLinux),
		Properties: &armcompute.VirtualMachineExtensionProperties{
			AutoUpgradeMinorVersion: lo.ToPtr(true),
			Type:                    lo.ToPtr(cseTypeLinux),
			Publisher:               lo.ToPtr(csePublisherLinux),
			TypeHandlerVersion:      lo.ToPtr(cseVersionLinux),
			Settings:                &map[string]interface{}{},
			ProtectedSettings: &map[string]interface{}{
				"commandToExecute": launchTemplate.CustomScriptsCSE,
			},
		},
		Tags: launchTemplate.Tags,
	}}
}
