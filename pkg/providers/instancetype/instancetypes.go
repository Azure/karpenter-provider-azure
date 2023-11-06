// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package instancetype

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/samber/lo"

	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	kcache "github.com/Azure/karpenter/pkg/cache"
	"github.com/Azure/karpenter/pkg/utils"
	"github.com/patrickmn/go-cache"
	"k8s.io/apimachinery/pkg/util/sets"
	"knative.dev/pkg/logging"

	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"github.com/aws/karpenter-core/pkg/cloudprovider"

	"github.com/Azure/karpenter/pkg/providers/instance/skuclient"
	"github.com/Azure/karpenter/pkg/providers/pricing"
	"github.com/Azure/skewer"
	"github.com/alecthomas/units"
)

const (
	InstanceTypesCacheKey = "types"
	InstanceTypesCacheTTL = 23 * time.Hour
)

type Provider struct {
	sync.Mutex
	region               string
	skuClient            skuclient.SkuClient
	pricingProvider      *pricing.Provider
	unavailableOfferings *kcache.UnavailableOfferings
	// Has one cache entry for all the instance types (key: InstanceTypesCacheKey)
	cache *cache.Cache
}

func NewProvider(region string, cache *cache.Cache, skuClient skuclient.SkuClient, pricingProvider *pricing.Provider, offeringsCache *kcache.UnavailableOfferings) *Provider {
	return &Provider{
		// TODO: skewer api, subnetprovider, pricing provider, unavailable offerings, ...
		region:               region,
		skuClient:            skuClient,
		pricingProvider:      pricingProvider,
		unavailableOfferings: offeringsCache,
		cache:                cache,
	}
}

// Get all instance type options
func (p *Provider) List(
	ctx context.Context, kc *corev1beta1.KubeletConfiguration, nodeClass *v1alpha2.AKSNodeClass) ([]*cloudprovider.InstanceType, error) {
	p.Lock()
	defer p.Unlock()
	// Get SKUs from Azure
	skus, err := p.getInstanceTypes(ctx)
	if err != nil {
		return nil, err
	}

	// Get Viable offerings
	/// Azure has zones availability directly from SKU info
	var result []*cloudprovider.InstanceType
	for _, sku := range skus {
		vmsize, err := sku.GetVMSize()
		if err != nil {
			logging.FromContext(ctx).Errorf("parsing VM size %s, %v", *sku.Size, err)
			continue
		}
		if !p.isSupportedSize(vmsize) {
			continue
		}
		architecture, err := sku.GetCPUArchitectureType()
		if err != nil {
			logging.FromContext(ctx).Errorf("parsing SKU architecture %s, %v", *sku.Size, err)
			continue
		}
		instanceTypeZones := instanceTypeZones(sku, p.region)
		instanceType := NewInstanceType(ctx, sku, vmsize, kc, p.region, p.createOfferings(sku, instanceTypeZones), nodeClass, architecture)
		if len(instanceType.Offerings) == 0 {
			continue
		}
		result = append(result, instanceType)
	}
	return result, nil
}

func (p *Provider) LivenessProbe(req *http.Request) error {
	p.Lock()
	//nolint: staticcheck
	p.Unlock()
	return p.pricingProvider.LivenessProbe(req)
}

// instanceTypeZones generates the set of all supported zones for a given SKU
// The strings have to match Zone labels that will be placed on Node
func instanceTypeZones(sku *skewer.SKU, region string) sets.Set[string] {
	// skewer returns numerical zones, like "1" (as keys in the map);
	// prefix each zone with "<region>-", to have them match the labels placed on Node (e.g. "westus2-1")
	// Note this data comes from LocationInfo, then skewer is used to get the SKU info
	return sets.New(lo.Map(lo.Keys(sku.AvailabilityZones(region)), func(zone string, _ int) string {
		return fmt.Sprintf("%s-%s", region, zone)
	})...)
}

func (p *Provider) createOfferings(sku *skewer.SKU, zones sets.Set[string]) []cloudprovider.Offering {
	offerings := []cloudprovider.Offering{}
	for zone := range zones {
		onDemandPrice, onDemandOk := p.pricingProvider.OnDemandPrice(*sku.Name)
		spotPrice, spotOk := p.pricingProvider.SpotPrice(*sku.Name)
		availableOnDemand := onDemandOk && !p.unavailableOfferings.IsUnavailable(*sku.Name, zone, corev1beta1.CapacityTypeOnDemand)
		availableSpot := spotOk && !p.unavailableOfferings.IsUnavailable(*sku.Name, zone, corev1beta1.CapacityTypeSpot)
		offerings = append(offerings, cloudprovider.Offering{Zone: zone, CapacityType: corev1beta1.CapacityTypeSpot, Price: spotPrice, Available: availableSpot})
		offerings = append(offerings, cloudprovider.Offering{Zone: zone, CapacityType: corev1beta1.CapacityTypeOnDemand, Price: onDemandPrice, Available: availableOnDemand})
	}
	return offerings
}

// getInstanceTypes retrieves all instance types from skewer using some opinionated filters
func (p *Provider) getInstanceTypes(ctx context.Context) (map[string]*skewer.SKU, error) {
	if cached, ok := p.cache.Get(InstanceTypesCacheKey); ok {
		return cached.(map[string]*skewer.SKU), nil
	}
	instanceTypes := map[string]*skewer.SKU{}

	cache, err := skewer.NewCache(ctx, skewer.WithLocation(p.region), skewer.WithResourceClient(p.skuClient.GetInstance()))
	if err != nil {
		return nil, fmt.Errorf("fetching SKUs using skewer, %w", err)
	}

	skus := cache.List(ctx, skewer.ResourceTypeFilter(skewer.VirtualMachines))
	logging.FromContext(ctx).Debugf("Discovered %d SKUs", len(skus))
	for i := range skus {
		if p.isSupported(&skus[i]) {
			instanceTypes[skus[i].GetName()] = &skus[i]
		}
	}

	logging.FromContext(ctx).Debugf("%d SKUs remaining after filtering", len(instanceTypes))
	p.cache.SetDefault(InstanceTypesCacheKey, instanceTypes)
	return instanceTypes, nil
}

// isSupported indicates SKU is supported by AKS, based on SKU properties
func (p *Provider) isSupported(sku *skewer.SKU) bool {
	name := lo.FromPtr(sku.Name)

	// less than 2 cpus
	if cpu, err := sku.VCPU(); err == nil && cpu < 2 {
		return false
	}

	// less then 3.5 GiB of memory
	if memGiB, err := sku.Memory(); err == nil && memGiB < 3.5 {
		return false
	}

	// filter out instances AKS does not support
	if RestrictedVMSizes.Has(sku.GetName()) {
		return false
	}

	// filter out GPU SKUs AKS does not support
	if gpu, err := sku.GPU(); err == nil && gpu > 0 && !(utils.IsNvidiaEnabledSKU(name) || utils.IsMarinerEnabledGPUSKU(name)) {
		return false
	}

	return true
}

// isSupportedSize indicates VM size is supported by AKS, based on additional SKU properties
func (p *Provider) isSupportedSize(size *skewer.VMSizeType) bool {
	// any SKU with constrained CPUs
	return size.CpusConstrained == nil
}

// MaxEphemeralOSDiskSizeGB returns the maximum ephemeral OS disk size for a given SKU.
// Ephemeral OS disk size is determined by the larger of the two values:
// 1. MaxResourceVolumeMB (Temp Disk Space)
// 2. MaxCachedDiskBytes (Cached Disk Space)
// For Ephemeral disk creation, CRP will use the larger of the two values to ensure we have enough space for the ephemeral disk.
// Note that generally only older SKUs use the Temp Disk space for ephemeral disks, and newer SKUs use the Cached Disk in most cases.
// The ephemeral OS disk is created with the free space of the larger of the two values in that place.
func MaxEphemeralOSDiskSizeGB(sku *skewer.SKU) float64 {
	if sku == nil {
		return 0
	}
	maxCachedDiskBytes, _ := sku.MaxCachedDiskBytes()
	maxResourceVolumeMB, _ := sku.MaxResourceVolumeMB() // NOTE: this is a misnomer, MB is actually MiB, hence the conversion below

	maxResourceVolumeBytes := maxResourceVolumeMB * int64(units.Mebibyte)
	maxDiskBytes := math.Max(float64(maxCachedDiskBytes), float64(maxResourceVolumeBytes))
	if maxDiskBytes == 0 {
		return 0
	}
	// convert bytes to GB
	return maxDiskBytes / float64(units.Gigabyte)
}
