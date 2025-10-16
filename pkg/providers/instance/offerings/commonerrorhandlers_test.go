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

package offerings

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/skewer/v2"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

const (
	testInstanceName       = "Standard_D2s_v3"
	testInstanceVMSize     = "D2s_v3"
	testInstanceFamilyName = "standardDsv3Family"
	testZone1              = "westus-1"
	testZone2              = "westus-2"
	testZone3              = "westus-3"

	errMsgLowPriorityQuota             = "this subscription has reached the regional vCPU quota for spot (LowPriorityQuota). To scale beyond this limit, please review the quota increase process here: https://docs.microsoft.com/en-us/azure/azure-portal/supportability/low-priority-quota"
	errMsgSKUFamilyQuotaFmt            = "subscription level %s vCPU quota for %s has been reached (may try provision an alternative instance type)"
	errMsgSKUNotAvailableFmt           = "the requested SKU is unavailable for instance type %s in zone %s with capacity type %s, for more details please visit: https://aka.ms/azureskunotavailable"
	errMsgZonalAllocationFailureFmt    = "unable to allocate resources in the selected zone (%s). (will try a different zone to fulfill your request)"
	errMsgAllocationFailureFmt         = "unable to allocate resources with selected VM size (%s). (will try a different VM size to fulfill your request)"
	errMsgOverconstrainedZonalFmt      = "unable to allocate resources in the selected zone (%s) with %s capacity type and %s VM size. (will try a different zone, capacity type or VM size to fulfill your request)"
	errMsgOverconstrainedAllocationFmt = "unable to allocate resources in all zones with %s capacity type and %s VM size. (will try a different capacity type or VM size to fulfill your request)"
	errMsgRegionalQuotaExceeded        = "regional on-demand vCPU quota limit for subscription has been reached. To scale beyond this limit, please review the quota increase process here: https://learn.microsoft.com/en-us/azure/quotas/regional-quota-requests"
)

var (
	zone1OnDemand = offering{zone: testZone1, capacityType: karpv1.CapacityTypeOnDemand}
	zone1Spot     = offering{zone: testZone1, capacityType: karpv1.CapacityTypeSpot}
	zone2OnDemand = offering{zone: testZone2, capacityType: karpv1.CapacityTypeOnDemand}
	zone2Spot     = offering{zone: testZone2, capacityType: karpv1.CapacityTypeSpot}
	zone3OnDemand = offering{zone: testZone3, capacityType: karpv1.CapacityTypeOnDemand}
	zone3Spot     = offering{zone: testZone3, capacityType: karpv1.CapacityTypeSpot}
)

// offering represents a zone and capacity type combination for cleaner test setup
type offering struct {
	zone         string
	capacityType string
}

type offeringToCheck struct {
	skuToCheck   *skewer.SKU
	zone         string
	capacityType string
}

func createInstanceType(instanceName string, offerings ...offering) *cloudprovider.InstanceType {
	it := &cloudprovider.InstanceType{
		Name: instanceName,
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(v1beta1.LabelSKUCPU, corev1.NodeSelectorOpIn, "2"),
		),
		Offerings: []*cloudprovider.Offering{},
	}

	for _, o := range offerings {
		it.Offerings = append(it.Offerings, &cloudprovider.Offering{
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, o.capacityType),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, o.zone),
			),
		})
	}

	return it
}

func offeringInformation(zone, capacityType, instanceTypeName, instanceVMSize, familyName, cpuCount string) offeringToCheck {
	return offeringToCheck{
		skuToCheck:   createTestSKU(instanceTypeName, instanceVMSize, familyName, cpuCount),
		zone:         zone,
		capacityType: capacityType,
	}
}

// Helper to create default offering information for testing, for errors where we don't block specific families of VM SKUs
func defaultTestOfferingInfo(zone, capacityType string) offeringToCheck {
	return offeringInformation(zone, capacityType, testInstanceName, testInstanceVMSize, testInstanceFamilyName, "2")
}

func createTestSKU(name, size, family, cpuCount string) *skewer.SKU {
	return &skewer.SKU{
		Name:   &name,
		Size:   &size,
		Family: &family,
		Capabilities: []*armcompute.ResourceSKUCapabilities{
			{
				Name:  to.Ptr(skewer.VCPUs),
				Value: &cpuCount,
			},
		},
	}
}

// Helper functions for common error handler tests

func createCommonErrorTestSKU(name, size, family, cpuCount string) *skewer.SKU {
	return &skewer.SKU{
		Name:   &name,
		Size:   &size,
		Family: &family,
		Capabilities: []*armcompute.ResourceSKUCapabilities{
			{
				Name:  to.Ptr(skewer.VCPUs),
				Value: &cpuCount,
			},
		},
	}
}

func createDefaultCommonErrorTestSKU() *skewer.SKU {
	return createCommonErrorTestSKU(testInstanceName, testInstanceVMSize, testInstanceFamilyName, "2")
}

func createCommonErrorInstanceType(instanceName string, offerings ...offering) *cloudprovider.InstanceType {
	it := &cloudprovider.InstanceType{
		Name: instanceName,
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(v1beta1.LabelSKUCPU, corev1.NodeSelectorOpIn, "2"),
		),
		Offerings: []*cloudprovider.Offering{},
	}

	for _, o := range offerings {
		it.Offerings = append(it.Offerings, &cloudprovider.Offering{
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, o.capacityType),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, o.zone),
			),
		})
	}

	return it
}

func TestMarkOfferingsUnavailableForCapacityType(t *testing.T) {
	ctx := context.Background()
	unavailableOfferings := cache.NewUnavailableOfferings()
	instanceType := createCommonErrorInstanceType(testInstanceName,
		zone1OnDemand, zone1Spot, zone2OnDemand, zone2Spot)

	// Mark spot offerings unavailable
	markOfferingsUnavailableForCapacityType(ctx, unavailableOfferings, instanceType, karpv1.CapacityTypeSpot, SKUNotAvailableReason, SKUNotAvailableSpotTTL)

	// Check that spot offerings are unavailable
	assert.True(t, unavailableOfferings.IsUnavailable(createDefaultCommonErrorTestSKU(), testZone1, karpv1.CapacityTypeSpot))
	assert.True(t, unavailableOfferings.IsUnavailable(createDefaultCommonErrorTestSKU(), testZone2, karpv1.CapacityTypeSpot))

	// Check that on-demand offerings are still available
	assert.False(t, unavailableOfferings.IsUnavailable(createDefaultCommonErrorTestSKU(), testZone1, karpv1.CapacityTypeOnDemand))
	assert.False(t, unavailableOfferings.IsUnavailable(createDefaultCommonErrorTestSKU(), testZone2, karpv1.CapacityTypeOnDemand))
}

func TestMarkAllZonesUnavailableForBothCapacityTypes(t *testing.T) {
	ctx := context.Background()
	unavailableOfferings := cache.NewUnavailableOfferings()
	instanceType := createCommonErrorInstanceType(testInstanceName,
		zone1OnDemand, zone1Spot, zone2OnDemand, zone2Spot, zone3Spot)

	// Mark all zones unavailable for both capacity types
	markAllZonesUnavailableForBothCapacityTypes(ctx, unavailableOfferings, instanceType, AllocationFailureReason, AllocationFailureTTL)

	// Check that all zones and capacity types are unavailable
	assert.True(t, unavailableOfferings.IsUnavailable(createDefaultCommonErrorTestSKU(), testZone1, karpv1.CapacityTypeOnDemand))
	assert.True(t, unavailableOfferings.IsUnavailable(createDefaultCommonErrorTestSKU(), testZone1, karpv1.CapacityTypeSpot))
	assert.True(t, unavailableOfferings.IsUnavailable(createDefaultCommonErrorTestSKU(), testZone2, karpv1.CapacityTypeOnDemand))
	assert.True(t, unavailableOfferings.IsUnavailable(createDefaultCommonErrorTestSKU(), testZone2, karpv1.CapacityTypeSpot))
	assert.True(t, unavailableOfferings.IsUnavailable(createDefaultCommonErrorTestSKU(), testZone3, karpv1.CapacityTypeOnDemand))
	assert.True(t, unavailableOfferings.IsUnavailable(createDefaultCommonErrorTestSKU(), testZone3, karpv1.CapacityTypeSpot))
}
