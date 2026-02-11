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

package instancetype

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mitchellh/hashstructure/v2"
	"github.com/samber/lo"

	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/patrickmn/go-cache"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	kcache "github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing"

	"github.com/Azure/skewer"
	"github.com/alecthomas/units"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

const (
	InstanceTypesCacheKey = "types"
	InstanceTypesCacheTTL = 23 * time.Hour
)

type Provider interface {
	LivenessProbe(*http.Request) error
	List(context.Context, *v1beta1.AKSNodeClass) ([]*cloudprovider.InstanceType, error)

	// Return Azure Skewer Representation of the instance type
	Get(context.Context, *v1beta1.AKSNodeClass, string) (*skewer.SKU, error)
	//UpdateInstanceTypes(ctx context.Context) error
	//UpdateInstanceTypeOfferings(ctx context.Context) error
}

// assert that DefaultProvider implements Provider interface
var _ Provider = (*DefaultProvider)(nil)

type DefaultProvider struct {
	region               string
	skuClient            skewer.ResourceClient
	pricingProvider      *pricing.Provider
	unavailableOfferings *kcache.UnavailableOfferings

	// Has one cache entry for all the instance types (key: InstanceTypesCacheKey)
	// Values cached *before* considering insufficient capacity errors from the unavailableOfferings cache.
	// Fully initialized Instance Types are also cached based on the set of all instance types,
	// unavailableOfferings cache, AWSNodeClass, and kubelet configuration from the NodePool
	mu                 sync.Mutex
	instanceTypesCache *cache.Cache

	// rawSKUCache holds the last skewer cache from the Azure API so that
	// List() can look up additional SKUs (e.g. pinned GPU types) without
	// making a second API call.
	rawSKUCache *skewer.Cache

	cm *pretty.ChangeMonitor
	// instanceTypesSeqNum is a monotonically increasing change counter used to avoid the expensive hashing operation on instance types
	instanceTypesSeqNum uint64
}

func NewDefaultProvider(
	region string,
	cache *cache.Cache,
	skuClient skewer.ResourceClient,
	pricingProvider *pricing.Provider,
	offeringsCache *kcache.UnavailableOfferings,
) *DefaultProvider {
	return &DefaultProvider{
		// TODO: skewer api, subnetprovider, pricing provider, unavailable offerings, ...
		region:               region,
		skuClient:            skuClient,
		pricingProvider:      pricingProvider,
		unavailableOfferings: offeringsCache,
		instanceTypesCache:   cache,
		cm:                   pretty.NewChangeMonitor(),
		instanceTypesSeqNum:  0,
	}
}

// Get all instance type options
func (p *DefaultProvider) List(
	ctx context.Context, nodeClass *v1beta1.AKSNodeClass) ([]*cloudprovider.InstanceType, error) {
	kc := nodeClass.Spec.Kubelet

	// Get SKUs from Azure
	skus, err := p.getInstanceTypes(ctx)
	if err != nil {
		return nil, err
	}

	// For explicitly requested instance types that weren't found in the
	// standard filtered set (e.g. pinned GPU capacity like H200 that may be
	// filtered out by IncludesFilter or isSupported checks), look them up
	// directly in the raw skewer cache which holds the full live API data.
	// This avoids the nil-pointer panics that occurred when injecting from
	// the static allAzureVMSkus list (which only has Name, no Size/capabilities).
	if len(nodeClass.Spec.InstanceTypes) > 0 && p.rawSKUCache != nil {
		augmented := make(map[string]*skewer.SKU, len(skus))
		for k, v := range skus {
			augmented[k] = v
		}
		for _, requested := range nodeClass.Spec.InstanceTypes {
			if _, exists := augmented[requested]; exists {
				continue
			}
			// Query the raw (unfiltered) skewer cache for this specific VM SKU name
			liveSKUs := p.rawSKUCache.List(ctx, skewer.ResourceTypeFilter(skewer.VirtualMachines), skewer.NameFilter(requested))
			if len(liveSKUs) > 0 {
				sku := liveSKUs[0]
				augmented[sku.GetName()] = &sku
				log.FromContext(ctx).Info("injected requested instance type from live Azure API (bypassed standard filters)", "instanceType", sku.GetName())
			} else {
				log.FromContext(ctx).Info("requested instance type not found in live Azure SKU API for region", "instanceType", requested, "region", p.region)
			}
		}
		skus = augmented
	}

	// Compute fully initialized instance types hash key
	kcHash, _ := hashstructure.Hash(kc, hashstructure.FormatV2, &hashstructure.HashOptions{SlicesAsSets: true})
	instanceTypesFilter := strings.Join(nodeClass.Spec.InstanceTypes, ",")
	key := fmt.Sprintf("%d-%d-%016x-%s-%d-%d-%t-%t-%s",
		p.instanceTypesSeqNum,
		p.unavailableOfferings.SeqNum,
		kcHash,
		lo.FromPtr(nodeClass.Spec.ImageFamily),
		lo.FromPtr(nodeClass.Spec.OSDiskSizeGB),
		utils.GetMaxPods(nodeClass, options.FromContext(ctx).NetworkPlugin, options.FromContext(ctx).NetworkPluginMode),
		nodeClass.GetEncryptionAtHost(),
		nodeClass.IsLocalDNSEnabled(),
		instanceTypesFilter,
	)
	if item, ok := p.instanceTypesCache.Get(key); ok {
		// Ensure what's returned from this function is a shallow-copy of the slice (not a deep-copy of the data itself)
		// so that modifications to the ordering of the data don't affect the original
		return append([]*cloudprovider.InstanceType{}, item.([]*cloudprovider.InstanceType)...), nil
	}

	// Get Viable offerings
	/// Azure has zones availability directly from SKU info
	var result []*cloudprovider.InstanceType
	isRequested := func(name string) bool {
		return len(nodeClass.Spec.InstanceTypes) > 0 && lo.Contains(nodeClass.Spec.InstanceTypes, name)
	}
	for _, sku := range skus {
		vmsize, err := sku.GetVMSize()
		if err != nil {
			if isRequested(sku.GetName()) {
				// For explicitly requested instance types (e.g. H200 GPU), the skewer
				// VM size parser may not support the naming convention. Construct a
				// minimal VMSizeType from the SKU capabilities instead of skipping.
				log.FromContext(ctx).Info("requested instance type failed GetVMSize, using fallback", "instanceType", sku.GetName(), "error", err)
				cpus, _ := sku.VCPU()
				subfamily := ""
				vmsize = &skewer.VMSizeType{
					Family:    string([]rune(sku.GetName())[0]),
					Cpus:      fmt.Sprintf("%d", cpus),
					Subfamily: &subfamily,
				}
			} else {
				log.FromContext(ctx).Error(err, "parsing VM size", "vmSize", sku.GetSize())
				continue
			}
		}
		architecture, err := sku.GetCPUArchitectureType()
		if err != nil {
			if isRequested(sku.GetName()) {
				log.FromContext(ctx).Info("requested instance type failed GetCPUArchitectureType", "instanceType", sku.GetName(), "error", err)
			}
			log.FromContext(ctx).Error(err, "parsing SKU architecture", "vmSize", sku.GetSize())
			continue
		}
		instanceTypeZones := p.instanceTypeZones(sku)
		// When instanceTypes override is set on the NodeClass, force-create offerings
		// for listed SKUs even without pricing data (pinned GPU capacity may not
		// have retail pricing).
		forceOfferings := isRequested(sku.GetName())
		instanceType := NewInstanceType(ctx, sku, vmsize, kc, p.region, p.createOfferings(sku, instanceTypeZones, forceOfferings), nodeClass, architecture)
		if len(instanceType.Offerings) == 0 {
			if forceOfferings {
				log.FromContext(ctx).Info("requested instance type has 0 offerings even with force", "instanceType", sku.GetName(), "zones", instanceTypeZones.UnsortedList())
			}
			continue
		}

		if !p.isInstanceTypeSupportedByImageFamily(sku.GetName(), lo.FromPtr(nodeClass.Spec.ImageFamily)) {
			continue
		}
		if !p.isInstanceTypeSupportedByEncryptionAtHost(sku, nodeClass) {
			continue
		}
		if !p.isInstanceTypeSupportedByLocalDNS(sku, nodeClass) {
			continue
		}

		result = append(result, instanceType)
	}

	// Filter to only requested instance types if the NodeClass specifies an explicit list
	if len(nodeClass.Spec.InstanceTypes) > 0 {
		allowedSet := sets.New(nodeClass.Spec.InstanceTypes...)
		result = lo.Filter(result, func(it *cloudprovider.InstanceType, _ int) bool {
			return allowedSet.Has(it.Name)
		})
		// Log warnings for requested types not found in the discovered set
		foundSet := sets.New(lo.Map(result, func(it *cloudprovider.InstanceType, _ int) string { return it.Name })...)
		for _, requested := range nodeClass.Spec.InstanceTypes {
			if !foundSet.Has(requested) {
				log.FromContext(ctx).Info("requested instance type not found in discovered SKUs, skipping", "instanceType", requested)
			}
		}
	}

	p.instanceTypesCache.SetDefault(key, result)
	return result, nil
}

func (p *DefaultProvider) LivenessProbe(req *http.Request) error {
	return p.pricingProvider.LivenessProbe(req)
}

func (p *DefaultProvider) Get(ctx context.Context, nodeClass *v1beta1.AKSNodeClass, instanceType string) (*skewer.SKU, error) {
	skus, err := p.getInstanceTypes(ctx)
	if err != nil {
		return nil, err
	}
	if sku, ok := skus[instanceType]; ok {
		return sku, nil
	}
	// Fall back to the raw skewer cache for explicitly requested types
	// that may have been filtered out by the standard filters.
	if p.rawSKUCache != nil {
		liveSKUs := p.rawSKUCache.List(ctx, skewer.ResourceTypeFilter(skewer.VirtualMachines), skewer.NameFilter(instanceType))
		if len(liveSKUs) > 0 {
			return &liveSKUs[0], nil
		}
	}
	return nil, fmt.Errorf("instance type %s not found", instanceType)
}

// instanceTypeZones generates the set of all supported zones for a given SKU
// The strings have to match Zone labels that will be placed on Node
func (p *DefaultProvider) instanceTypeZones(sku *skewer.SKU) sets.Set[string] {
	// skewer returns numerical zones, like "1" (as keys in the map);
	// prefix each zone with "<region>-", to have them match the labels placed on Node (e.g. "westus2-1")
	// Note this data comes from LocationInfo, then skewer is used to get the SKU info
	// If an offering is non-zonal, the availability zones will be empty.
	skuZones := lo.Keys(sku.AvailabilityZones(p.region))
	if len(skuZones) > 0 {
		return sets.New(lo.Map(skuZones, func(zone string, _ int) string {
			return utils.MakeAKSLabelZoneFromARMZone(p.region, zone)
		})...)
	}
	return sets.New("") // empty string means non-zonal offering
}

// TODO: review; switch to controller-driven updates
// createOfferings creates a set of mutually exclusive offerings for a given instance type. This provider maintains an
// invariant that each offering is mutually exclusive. Specifically, there is an offering for each permutation of zone
// and capacity type. ZoneID is also injected into the offering requirements, when available, but there is a 1-1
// mapping between zone and zoneID so this does not change the number of offerings.
//
// Each requirement on the offering is guaranteed to have a single value. To get the value for a requirement on an
// offering, you can do the following thanks to this invariant:
//
//	offering.Requirements.Get(v1.TopologyLabelZone).Any()
func (p *DefaultProvider) createOfferings(sku *skewer.SKU, zones sets.Set[string], forceOfferings bool) cloudprovider.Offerings {
	offerings := []*cloudprovider.Offering{}
	for zone := range zones {
		onDemandPrice, onDemandOk := p.pricingProvider.OnDemandPrice(*sku.Name)
		spotPrice, spotOk := p.pricingProvider.SpotPrice(*sku.Name)
		// When forceOfferings is true (instanceTypes override), treat missing pricing
		// as available with zero price. Pinned GPU capacity may not have retail pricing.
		if forceOfferings && !onDemandOk {
			onDemandPrice = 0
			onDemandOk = true
		}
		availableOnDemand := onDemandOk && !p.unavailableOfferings.IsUnavailable(sku, zone, karpv1.CapacityTypeOnDemand)
		availableSpot := spotOk && !p.unavailableOfferings.IsUnavailable(sku, zone, karpv1.CapacityTypeSpot)

		onDemandOffering := &cloudprovider.Offering{
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
				scheduling.NewRequirement(v1beta1.AKSLabelScaleSetPriority, corev1.NodeSelectorOpIn, v1beta1.ScaleSetPriorityRegular),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zone),
			),
			Price:     onDemandPrice,
			Available: availableOnDemand,
		}

		spotOffering := &cloudprovider.Offering{
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeSpot),
				scheduling.NewRequirement(v1beta1.AKSLabelScaleSetPriority, corev1.NodeSelectorOpIn, v1beta1.ScaleSetPrioritySpot),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zone),
			),
			Price:     spotPrice,
			Available: availableSpot,
		}

		offerings = append(offerings, onDemandOffering, spotOffering)

		/*
			instanceTypeOfferingAvailable.With(prometheus.Labels{
				instanceTypeLabel: *instanceType.InstanceType,
				capacityTypeLabel: capacityType,
				zoneLabel:         zone,
			}).Set(float64(lo.Ternary(available, 1, 0)))
			instanceTypeOfferingPriceEstimate.With(prometheus.Labels{
				instanceTypeLabel: *instanceType.InstanceType,
				capacityTypeLabel: capacityType,
				zoneLabel:         zone,
			}).Set(price)
		*/
	}
	return offerings
}

func (p *DefaultProvider) isInstanceTypeSupportedByImageFamily(skuName, imageFamily string) bool {
	// Currently only GPU has conditional support by image family
	if !utils.IsNvidiaEnabledSKU(skuName) && !utils.IsMarinerEnabledGPUSKU(skuName) {
		return true
	}
	switch {
	case v1beta1.UbuntuFamilies.Has(imageFamily):
		return utils.IsNvidiaEnabledSKU(skuName)
	case imageFamily == v1beta1.AzureLinuxImageFamily:
		return utils.IsMarinerEnabledGPUSKU(skuName)
	default:
		return false
	}
}

func (p *DefaultProvider) isInstanceTypeSupportedByEncryptionAtHost(sku *skewer.SKU, nodeClass *v1beta1.AKSNodeClass) bool {
	// If EncryptionAtHost is not enabled in the nodeclass, all instance types are supported
	if !nodeClass.GetEncryptionAtHost() {
		return true
	}
	// If EncryptionAtHost is enabled, only include instance types that support it
	return p.supportsEncryptionAtHost(sku)
}

// supportsEncryptionAtHost checks if the SKU supports encryption at host
func (p *DefaultProvider) supportsEncryptionAtHost(sku *skewer.SKU) bool {
	value, err := sku.GetCapabilityString("EncryptionAtHostSupported")
	if err != nil {
		return false
	}
	return strings.EqualFold(value, "True")
}

func (p *DefaultProvider) isInstanceTypeSupportedByLocalDNS(sku *skewer.SKU, nodeClass *v1beta1.AKSNodeClass) bool {
	// If LocalDNS won't be enabled, all instance types are supported
	if !nodeClass.IsLocalDNSEnabled() {
		return true
	}

	// LocalDNS requires at least 4 vCPUs and 256 MB (244.140625 MiB) of memory
	cpu, err := sku.VCPU()
	if err != nil || cpu < 4 {
		return false
	}

	return memoryMiB(sku) >= 244 // 256 MB = 244.140625 MiB
}

// getInstanceTypes retrieves all instance types from skewer using some opinionated filters
func (p *DefaultProvider) getInstanceTypes(ctx context.Context) (map[string]*skewer.SKU, error) {
	// DO NOT REMOVE THIS LOCK ----------------------------------------------------------------------------
	// We lock here so that multiple callers to GetInstanceTypes do not result in cache misses and multiple
	// calls to Resource API when we could have just made one call. This lock is here because multiple callers result
	// in A LOT of extra memory generated from the response for simultaneous callers.
	// (This can be made more efficient by holding a Read lock and only obtaining the Write if not in cache)
	p.mu.Lock()
	defer p.mu.Unlock()

	if cached, ok := p.instanceTypesCache.Get(InstanceTypesCacheKey); ok {
		return cached.(map[string]*skewer.SKU), nil
	}
	instanceTypes := map[string]*skewer.SKU{}

	skewerCache, err := skewer.NewCache(ctx, skewer.WithLocation(p.region), skewer.WithResourceClient(p.skuClient))
	if err != nil {
		return nil, fmt.Errorf("fetching SKUs using skewer, %w", err)
	}
	p.rawSKUCache = skewerCache

	skus := skewerCache.List(ctx, skewer.IncludesFilter(GetKarpenterWorkingSKUs()))
	log.FromContext(ctx).V(1).Info("discovered SKUs", "skuCount", len(skus))
	for i := range skus {
		vmsize, err := skus[i].GetVMSize()
		if err != nil {
			log.FromContext(ctx).Error(err, "parsing VM size", "vmSize", skus[i].GetSize())
			continue
		}
		useSIG := options.FromContext(ctx).UseSIG
		isEKSHybrid := options.FromContext(ctx).ProvisionMode == consts.ProvisionModeEKSHybrid
		supported := p.isSupported(&skus[i], vmsize, useSIG, isEKSHybrid)
		locRestriction := skus[i].HasLocationRestriction(p.region)
		if strings.Contains(skus[i].GetName(), "ND96") {
			log.FromContext(ctx).Info("H200 SKU check", "sku", skus[i].GetName(), "supported", supported, "locRestriction", locRestriction, "isEKSHybrid", isEKSHybrid, "useSIG", useSIG)
		}
		if !locRestriction && supported {
			instanceTypes[skus[i].GetName()] = &skus[i]
		}
	}

	if p.cm.HasChanged("instance-types", instanceTypes) {
		// Only update instanceTypesSeqNun with the instance types have been changed
		// This is to not create new keys with duplicate instance types option
		atomic.AddUint64(&p.instanceTypesSeqNum, 1)
		log.FromContext(ctx).V(1).Info("discovered instance types", "instanceTypeCount", len(instanceTypes))
	}
	p.instanceTypesCache.SetDefault(InstanceTypesCacheKey, instanceTypes)
	return instanceTypes, nil
}

// isSupported indicates SKU is supported, based on SKU properties
func (p *DefaultProvider) isSupported(sku *skewer.SKU, vmsize *skewer.VMSizeType, useSIG bool, isEKSHybrid bool) bool {
	return p.hasMinimumCPU(sku) &&
		p.hasMinimumMemory(sku) &&
		!p.isUnsupportedByAKS(sku) &&
		!p.isUnsupportedGPU(sku) &&
		!p.hasConstrainedCPUs(vmsize) &&
		!p.isConfidential(sku) &&
		// In ekshybrid mode, custom images are used (not AKS CIG/SIG),
		// so skip the image compatibility check that filters NVMe-only SKUs.
		(isEKSHybrid || isCompatibleImageAvailable(sku, useSIG))
}

// at least 2 cpus
func (p *DefaultProvider) hasMinimumCPU(sku *skewer.SKU) bool {
	cpu, err := sku.VCPU()
	return err == nil && cpu >= 2
}

// at least 3.5 GiB of memory
func (p *DefaultProvider) hasMinimumMemory(sku *skewer.SKU) bool {
	memGiB, err := sku.Memory()
	return err == nil && memGiB >= 3.5
}

// instances AKS does not support
func (p *DefaultProvider) isUnsupportedByAKS(sku *skewer.SKU) bool {
	return AKSRestrictedVMSizes.Has(sku.GetName())
}

// GPU SKUs AKS does not support
func (p *DefaultProvider) isUnsupportedGPU(sku *skewer.SKU) bool {
	name := lo.FromPtr(sku.Name)
	gpu, err := sku.GPU()
	if err != nil || gpu <= 0 {
		return false
	}
	return !utils.IsMarinerEnabledGPUSKU(name) && !utils.IsNvidiaEnabledSKU(name)
}

// SKU with constrained CPUs
func (p *DefaultProvider) hasConstrainedCPUs(vmsize *skewer.VMSizeType) bool {
	return vmsize.CpusConstrained != nil
}

// confidential VMs (DC, EC) are not yet supported by this Karpenter provider
func (p *DefaultProvider) isConfidential(sku *skewer.SKU) bool {
	size := sku.GetSize()
	return strings.HasPrefix(size, "DC") || strings.HasPrefix(size, "EC")
}

func FindMaxEphemeralSizeGBAndPlacement(sku *skewer.SKU) (sizeGB int64, placement *armcompute.DiffDiskPlacement) {
	if sku == nil {
		return 0, nil
	}

	if !sku.IsEphemeralOSDiskSupported() {
		return 0, nil // ephemeral OS disk is not supported by this SKU
	}

	maxNVMeMiB, _ := nvmeDiskSizeInMiB(sku)

	// Check NVMe disk first (highest priority)
	if maxNVMeMiB > 0 && supportsNVMeEphemeralOSDisk(sku) {
		return maxNVMeMiB * int64(units.MiB) / int64(units.Gigabyte), lo.ToPtr(armcompute.DiffDiskPlacementNvmeDisk)
	}

	maxCacheDiskBytes, _ := sku.MaxCachedDiskBytes()
	if maxCacheDiskBytes > 0 {
		return maxCacheDiskBytes / int64(units.Gigabyte), lo.ToPtr(armcompute.DiffDiskPlacementCacheDisk)
	}

	maxResourceDiskMiB, _ := sku.MaxResourceVolumeMB() // NOTE: MaxResourceVolumeMB is actually in MiBs
	if maxResourceDiskMiB > 0 {
		return maxResourceDiskMiB * int64(units.MiB) / int64(units.Gigabyte), lo.ToPtr(armcompute.DiffDiskPlacementResourceDisk)
	}

	return 0, nil
}

func isCompatibleImageAvailable(sku *skewer.SKU, useSIG bool) bool {
	hasSCSISupport := func(sku *skewer.SKU) bool { // TODO: move capability determination to skewer
		const diskControllerTypeCapability = "DiskControllerTypes"
		declaresSCSI := sku.HasCapabilityWithSeparator(diskControllerTypeCapability, string(compute.SCSI))
		declaresNVMe := sku.HasCapabilityWithSeparator(diskControllerTypeCapability, string(compute.NVMe))
		declaresNothing := !declaresSCSI && !declaresNVMe
		return declaresSCSI || declaresNothing // if nothing is declared, assume SCSI is supported
	}

	return useSIG || hasSCSISupport(sku) // CIG images are not currently tagged for NVMe
}

func supportsNVMeEphemeralOSDisk(sku *skewer.SKU) bool {
	const ephemeralOSDiskPlacementCapability = "SupportedEphemeralOSDiskPlacements"
	const nvme = "NvmeDisk"
	return sku.HasCapabilityWithSeparator(ephemeralOSDiskPlacementCapability, nvme)
}

func UseEphemeralDisk(sku *skewer.SKU, nodeClass *v1beta1.AKSNodeClass) bool {
	sizeGB, _ := FindMaxEphemeralSizeGBAndPlacement(sku)
	return int64(*nodeClass.Spec.OSDiskSizeGB) <= sizeGB // use ephemeral disk if it is large enough
}

func nvmeDiskSizeInMiB(s *skewer.SKU) (int64, error) {
	const selector = "NvmeDiskSizeInMiB"
	return s.GetCapabilityIntegerQuantity(selector)
}
