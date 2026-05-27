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
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/allocationstrategy"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/fleet"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/networksecuritygroup"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

const defaultMaxCandidateSKUs = 10

// FleetProvider is the interface for fleet-based instance creation.
type FleetProvider interface {
	BeginCreate(ctx context.Context, nodeClass *v1beta1.AKSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType) (*FleetMemberPromise, error)
}

// Ensure DefaultFleetProvider implements FleetProvider.
var _ FleetProvider = (*DefaultFleetProvider)(nil)

// DefaultFleetProvider orchestrates fleet-based VM creation.
// It adapts a Karpenter (NodeClass, NodeClaim, InstanceTypes) tuple into a
// FleetVMProvisionRequest and submits it to the fleet batch client.
type DefaultFleetProvider struct {
	fleetBatchClient             *fleet.Client
	launchTemplateProvider       *launchtemplate.Provider
	allocationStrategyProvider   *allocationstrategy.DefaultProvider
	loadBalancerProvider         *loadbalancer.Provider
	networkSecurityGroupProvider *networksecuritygroup.Provider
	location                     string
	resourceGroup                string
	subscriptionID               string
	diskEncryptionSetID          string
	maxCandidateSKUs             int // max SKUs to pass to Fleet vmSizesProfile (default 10)
}

// NewFleetProvider creates a new DefaultFleetProvider.
// maxCandidateSKUs controls how many instance types are included in each Fleet request;
// values ≤ 0 fall back to the default of 10.
func NewFleetProvider(
	fleetBatchClient *fleet.Client,
	launchTemplateProvider *launchtemplate.Provider,
	allocationStrategyProvider *allocationstrategy.DefaultProvider,
	loadBalancerProvider *loadbalancer.Provider,
	networkSecurityGroupProvider *networksecuritygroup.Provider,
	location, resourceGroup, subscriptionID, diskEncryptionSetID string,
	maxCandidateSKUs int,
) *DefaultFleetProvider {
	if maxCandidateSKUs <= 0 {
		maxCandidateSKUs = defaultMaxCandidateSKUs
	}
	return &DefaultFleetProvider{
		fleetBatchClient:             fleetBatchClient,
		launchTemplateProvider:       launchTemplateProvider,
		allocationStrategyProvider:   allocationStrategyProvider,
		loadBalancerProvider:         loadBalancerProvider,
		networkSecurityGroupProvider: networkSecurityGroupProvider,
		location:                     location,
		resourceGroup:                resourceGroup,
		subscriptionID:               subscriptionID,
		diskEncryptionSetID:          diskEncryptionSetID,
		maxCandidateSKUs:             maxCandidateSKUs,
	}
}

// BeginCreate starts fleet-based instance creation for a NodeClaim.
// It filters/ranks instance types, resolves the launch template using a representative
// instance type (all candidates in the same batch share the same image/subnet/disk per
// batch key), builds a FleetVMProvisionRequest, and submits it to the batch client.
// The returned FleetMemberPromise carries the deterministic fleet name and a handle to
// the shared batch state for later assignment retrieval.
func (p *DefaultFleetProvider) BeginCreate(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*cloudprovider.InstanceType,
) (*FleetMemberPromise, error) {
	// 1. Filter and rank instance types via allocation strategy.
	requirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)
	candidates := p.allocationStrategyProvider.FilterInstanceOfferings(
		ctx, allocationstrategy.NewInstanceOfferings(instanceTypes), requirements,
	)
	if len(candidates) == 0 {
		return nil, cloudprovider.NewInsufficientCapacityError(fmt.Errorf("no instance types available"))
	}

	// 2. Take top N SKUs (configurable, default 10).
	topN := min(p.maxCandidateSKUs, len(candidates))
	selected := candidates[:topN]

	// 3. Extract SKU names, zones union, and instanceTypes map.
	skuNames, zones, itMap := extractCandidateInfo(selected)

	// 4. Pick a representative instance type for launch template resolution.
	//    All candidates in the same batch share the same image/subnet/OS-disk config
	//    (guaranteed by batch key), so we can use the highest-ranked candidate.
	representativeType := selected[0].InstanceType

	// 5. Resolve launch template using representative type.
	capacityType := nodeClaim.Labels[karpv1.CapacityTypeLabelKey]
	claimLabels := labels.GetAllSingleValuedRequirementLabels(
		scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...),
	)
	additionalLabels := lo.Assign(
		claimLabels,
		labels.GetAllSingleValuedRequirementLabels(representativeType.Requirements),
		map[string]string{
			karpv1.CapacityTypeLabelKey: capacityType,
		},
	)
	launchTemplate, err := p.launchTemplateProvider.GetTemplate(ctx, nodeClass, nodeClaim, representativeType, additionalLabels)
	if err != nil {
		return nil, fmt.Errorf("getting launch template: %w", err)
	}

	// 6. Get LB backend pools.
	backendPools, err := p.loadBalancerProvider.LoadBalancerBackendPools(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting backend pools: %w", err)
	}

	// 7. Resolve NSG (empty for AKS-managed VNETs).
	nsgID := p.resolveNSG(ctx, launchTemplate.SubnetID)

	// 8. Build FleetVMProvisionRequest.
	// 8b. Build extensions for fleet body (CSE + billing).
	extensions := buildFleetExtensions(launchTemplate, p.location, launchTemplate.Tags)

	req := fleet.FleetVMProvisionRequest{
		NodeClaimName:       nodeClaim.Name,
		CapacityType:        capacityType,
		AcceptableSKUs:      skuNames,
		AcceptableZones:     zones,
		Tags:                launchTemplate.Tags,
		NodeClaim:           nodeClaim,
		NodeClass:           nodeClass,
		LaunchTemplate:      launchTemplate,
		InstanceTypes:       itMap,
		SSHPublicKey:        options.FromContext(ctx).SSHPublicKey,
		AdminUsername:       options.FromContext(ctx).LinuxAdminUsername,
		NodeIdentities:      options.FromContext(ctx).NodeIdentities,
		DiskEncryptionSetID: p.diskEncryptionSetID,
		NSG:                 nsgID,
		LBBackendPools:      backendPools.IPv4PoolIDs,
		Location:            p.location,
		Extensions:          extensions,
	}

	// 9. Compute deterministic fleet name from batch key.
	//    Format: fleet-{clusterName}-{batchKeyHash8}
	//    Same batch key config → same Fleet resource name → CreateOrUpdate is idempotent.
	batchKey, err := fleet.DetermineBatchKey(&req)
	if err != nil {
		return nil, fmt.Errorf("computing batch key: %w", err)
	}
	// batchKey = "<nodepool>/<capacityType>/<hash16>" — extract last 8 hex chars of hash.
	hash8 := batchKey[strings.LastIndex(batchKey, "/")+1:][:8]
	fleetName := fmt.Sprintf("fleet-%s-%s", options.FromContext(ctx).ClusterName, hash8)

	// 10. Submit to batcher.
	resp := p.fleetBatchClient.BeginCreateWithFleet(ctx, req)

	// 11. Build and return FleetMemberPromise.
	return &FleetMemberPromise{
		sharedState:   resp.SharedState,
		nodeClaimName: nodeClaim.Name,
		capacityType:  capacityType,
		fleetName:     fleetName,
	}, resp.Error
}

// resolveNSG returns the managed NSG ID for BYO VNETs, or empty string for AKS-managed VNETs.
// Errors are treated as non-fatal (empty string returned) — the Fleet will create NICs without
// an explicit NSG reference, matching the VM path behavior for AKS-managed VNETs.
func (p *DefaultFleetProvider) resolveNSG(ctx context.Context, subnetID string) string {
	isAKSManaged, err := utils.IsAKSManagedVNET(options.FromContext(ctx).NodeResourceGroup, subnetID)
	if err != nil || isAKSManaged {
		return ""
	}
	nsg, err := p.networkSecurityGroupProvider.ManagedNetworkSecurityGroup(ctx)
	if err != nil {
		return ""
	}
	return lo.FromPtr(nsg.ID)
}

// extractCandidateInfo extracts SKU names, the sorted union of zones across all offerings,
// and an instanceType map from the selected candidates.
func extractCandidateInfo(candidates []allocationstrategy.InstanceOffering) (
	skuNames []string,
	zones []string,
	itMap map[string]*cloudprovider.InstanceType,
) {
	itMap = make(map[string]*cloudprovider.InstanceType, len(candidates))
	zoneSet := make(map[string]struct{})

	for _, c := range candidates {
		name := c.InstanceType.Name
		skuNames = append(skuNames, name)
		itMap[name] = c.InstanceType
		for _, o := range c.Offerings {
			if o.Requirements.Has(corev1.LabelTopologyZone) {
				zone := o.Requirements.Get(corev1.LabelTopologyZone).Any()
				if zone != "" {
					zoneSet[zone] = struct{}{}
				}
			}
		}
	}

	for z := range zoneSet {
		zones = append(zones, z)
	}
	sort.Strings(zones)
	return
}

// buildFleetExtensions builds the CSE and billing extensions for a Fleet VM request.
// Mirrors the logic in DefaultVMProvider.getCSExtension / getAKSIdentifyingExtension
// but returns them as a slice for embedding in the Fleet body's extension profile.
func buildFleetExtensions(lt *launchtemplate.Template, location string, tags map[string]*string) []*armcompute.VirtualMachineExtension {
	var exts []*armcompute.VirtualMachineExtension

	// 1. CSE (Custom Script Extension) — only when provisioning via CSE (not scriptless).
	if lt.CustomScriptsCSE != "" {
		cse := buildCSExtensionForFleet(lt.CustomScriptsCSE, lt.IsWindows, location, tags)
		exts = append(exts, cse)
	}

	// 2. AKS Billing/Identifying extension — always added for Linux.
	// (In production, gated by isAKSIdentifyingExtensionEnabled; for POC, always add.)
	exts = append(exts, buildBillingExtensionForFleet(location, tags))

	return exts
}

func buildCSExtensionForFleet(cse string, isWindows bool, location string, tags map[string]*string) *armcompute.VirtualMachineExtension {
	const (
		cseTypeWindows      = "CustomScriptExtension"
		csePublisherWindows = "Microsoft.Compute"
		cseVersionWindows   = "1.10"
		cseTypeLinux        = "CustomScript"
		csePublisherLinux   = "Microsoft.Azure.Extensions"
		cseVersionLinux     = "2.0"
	)
	return &armcompute.VirtualMachineExtension{
		Location: lo.ToPtr(location),
		Name:     lo.ToPtr(lo.Ternary(isWindows, cseNameWindows, cseNameLinux)),
		Properties: &armcompute.VirtualMachineExtensionProperties{
			AutoUpgradeMinorVersion: lo.ToPtr(true),
			Type:                    lo.ToPtr(lo.Ternary(isWindows, cseTypeWindows, cseTypeLinux)),
			Publisher:               lo.ToPtr(lo.Ternary(isWindows, csePublisherWindows, csePublisherLinux)),
			TypeHandlerVersion:      lo.ToPtr(lo.Ternary(isWindows, cseVersionWindows, cseVersionLinux)),
			Settings:                &map[string]interface{}{},
			ProtectedSettings: &map[string]interface{}{
				"commandToExecute": cse,
			},
		},
		Tags: tags,
	}
}

func buildBillingExtensionForFleet(location string, tags map[string]*string) *armcompute.VirtualMachineExtension {
	return &armcompute.VirtualMachineExtension{
		Location: lo.ToPtr(location),
		Name:     lo.ToPtr(aksIdentifyingExtensionName),
		Properties: &armcompute.VirtualMachineExtensionProperties{
			Publisher:               lo.ToPtr("Microsoft.AKS"),
			TypeHandlerVersion:      lo.ToPtr("1.0"),
			AutoUpgradeMinorVersion: lo.ToPtr(true),
			Settings:                &map[string]interface{}{},
			Type:                    lo.ToPtr("Compute.AKS.Linux.Billing"),
		},
		Tags: tags,
	}
}
