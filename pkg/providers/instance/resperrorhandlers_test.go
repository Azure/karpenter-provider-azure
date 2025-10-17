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
	"io"
	"net/http"
	"strings"
	"testing"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/skewer"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

const (
	responseErrorTestInstanceName       = "Standard_D2s_v3"
	responseErrorTestInstanceVMSize     = "D2s_v3"
	responseErrorTestInstanceFamilyName = "standardDsv3Family"
	testZone1                           = "westus-1"
	testZone2                           = "westus-2"
	testZone3                           = "westus-3"

	errMsgLowPriorityQuota             = "this subscription has reached the regional vCPU quota for spot (LowPriorityQuota). To scale beyond this limit, please review the quota increase process here: https://docs.microsoft.com/en-us/azure/azure-portal/supportability/low-priority-quota"
	errMsgSKUFamilyQuotaFmt            = "subscription level %s vCPU quota for %s has been reached (may try provision an alternative instance type)"
	errMsgSKUNotAvailableFmt           = "the requested SKU is unavailable for instance type %s in zone %s with capacity type %s, for more details please visit: https://aka.ms/azureskunotavailable"
	errMsgZonalAllocationFailureFmt    = "unable to allocate resources in the selected zone (%s). (will try a different zone to fulfill your request)"
	errMsgAllocationFailureFmt         = "unable to allocate resources with selected VM size (%s). (will try a different VM size to fulfill your request)"
	errMsgOverconstrainedZonalFmt      = "unable to allocate resources in the selected zone (%s) with %s capacity type and %s VM size. (will try a different zone, capacity type or VM size to fulfill your request)"
	errMsgOverconstrainedAllocationFmt = "unable to allocate resources in all zones with %s capacity type and %s VM size. (will try a different capacity type or VM size to fulfill your request)"
	errMsgRegionalQuotaExceeded        = "regional on-demand vCPU quota limit for subscription has been reached. To scale beyond this limit, please review the quota increase process here: https://learn.microsoft.com/en-us/azure/quotas/regional-quota-requests"
)

// offering represents a zone and capacity type combination for cleaner test setup
type offering struct {
	zone         string
	capacityType string
}

// Common offering configurations
var (
	zone1OnDemand = offering{zone: testZone1, capacityType: karpv1.CapacityTypeOnDemand}
	zone1Spot     = offering{zone: testZone1, capacityType: karpv1.CapacityTypeSpot}
	zone2OnDemand = offering{zone: testZone2, capacityType: karpv1.CapacityTypeOnDemand}
	zone2Spot     = offering{zone: testZone2, capacityType: karpv1.CapacityTypeSpot}
	zone3OnDemand = offering{zone: testZone3, capacityType: karpv1.CapacityTypeOnDemand}
	zone3Spot     = offering{zone: testZone3, capacityType: karpv1.CapacityTypeSpot}
)

type testCaseBuilder struct {
	tc responseErrorTestCase
}

func newTestCase(name string) *testCaseBuilder {
	return &testCaseBuilder{
		tc: responseErrorTestCase{
			testName:                                name,
			originalRequestSKU:                      createDefaultTestSKU(),
			expectedUnavailableOfferingsInformation: []offeringToCheck{},
			expectedAvailableOfferingsInformation:   []offeringToCheck{},
		},
	}
}

func (b *testCaseBuilder) withInstanceType(offerings ...offering) *testCaseBuilder {
	b.tc.instanceType = createInstanceType(responseErrorTestInstanceName, offerings...)
	return b
}

func (b *testCaseBuilder) withEmptyInstanceType() *testCaseBuilder {
	b.tc.instanceType = &cloudprovider.InstanceType{}
	return b
}

func (b *testCaseBuilder) withZoneAndCapacity(zone, capacityType string) *testCaseBuilder {
	b.tc.zone = zone
	b.tc.capacityType = capacityType
	return b
}

func (b *testCaseBuilder) withResponseError(errorCode, errorMessage string) *testCaseBuilder {
	b.tc.responseErr = createResponseError(errorCode, errorMessage)
	return b
}

func (b *testCaseBuilder) expectError(err error) *testCaseBuilder {
	b.tc.expectedErr = err
	return b
}

func (b *testCaseBuilder) expectUnavailable(offerings ...offeringToCheck) *testCaseBuilder {
	b.tc.expectedUnavailableOfferingsInformation = offerings
	return b
}

func (b *testCaseBuilder) expectAvailable(offerings ...offeringToCheck) *testCaseBuilder {
	b.tc.expectedAvailableOfferingsInformation = offerings
	return b
}

func (b *testCaseBuilder) build() responseErrorTestCase {
	return b.tc
}

type responseErrorTestCase struct {
	testName                                string
	instanceType                            *cloudprovider.InstanceType
	originalRequestSKU                      *skewer.SKU
	zone                                    string
	capacityType                            string
	responseErr                             error
	expectedErr                             error
	expectedUnavailableOfferingsInformation []offeringToCheck
	expectedAvailableOfferingsInformation   []offeringToCheck
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

type offeringToCheck struct {
	skuToCheck   *skewer.SKU
	zone         string
	capacityType string
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
	return offeringInformation(zone, capacityType, responseErrorTestInstanceName, responseErrorTestInstanceVMSize, responseErrorTestInstanceFamilyName, "2")
}

func createTestSKU(name, size, family, cpuCount string) *skewer.SKU {
	return &skewer.SKU{
		Name:   &name,
		Size:   &size,
		Family: &family,
		Capabilities: &[]compute.ResourceSkuCapabilities{
			{
				Name:  to.Ptr(skewer.VCPUs),
				Value: &cpuCount,
			},
		},
	}
}

func createDefaultTestSKU() *skewer.SKU {
	return createTestSKU(responseErrorTestInstanceName, responseErrorTestInstanceVMSize, responseErrorTestInstanceFamilyName, "2")
}

func createResponseError(errorCode, errorMessage string) error {
	errorBody := fmt.Sprintf(`{"error": {"code": "%s", "message": "%s"}}`, errorCode, errorMessage)
	return &azcore.ResponseError{
		ErrorCode: errorCode,
		RawResponse: &http.Response{
			Body: io.NopCloser(strings.NewReader(errorBody)),
		},
	}
}

// newTestProvider creates a test provider with default configuration
func newTestProvider() *DefaultProvider {
	return &DefaultProvider{
		unavailableOfferings:  cache.NewUnavailableOfferings(),
		responseErrorHandlers: defaultResponseErrorHandlers(),
	}
}

func assertOfferingsState(t *testing.T, provider *DefaultProvider, unavailable, available []offeringToCheck) {
	t.Helper()

	for _, info := range unavailable {
		assert.True(t,
			provider.unavailableOfferings.IsUnavailable(info.skuToCheck, info.zone, info.capacityType),
			"Expected offering %s in zone %s with capacity type %s to be unavailable",
			info.skuToCheck.GetName(), info.zone, info.capacityType,
		)
	}

	for _, info := range available {
		assert.False(t,
			provider.unavailableOfferings.IsUnavailable(info.skuToCheck, info.zone, info.capacityType),
			"Expected offering %s in zone %s with capacity type %s to be available",
			info.skuToCheck.GetName(), info.zone, info.capacityType,
		)
	}
}

func setupTestCases() []responseErrorTestCase {
	return []responseErrorTestCase{
		newTestCase("Response error is nil").
			withEmptyInstanceType().
			withZoneAndCapacity("", karpv1.CapacityTypeOnDemand).
			expectError(nil).
			build(),

		newTestCase("Low priority quota has been reached").
			withEmptyInstanceType().
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeSpot).
			withResponseError(sdkerrors.OperationNotAllowed, sdkerrors.LowPriorityQuotaExceededTerm).
			expectError(fmt.Errorf("%s", errMsgLowPriorityQuota)).
			expectUnavailable(defaultTestOfferingInfo("", karpv1.CapacityTypeSpot)).
			build(),

		newTestCase("SKU family quota has been reached").
			withInstanceType(zone2OnDemand, zone3OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError(sdkerrors.OperationNotAllowed, sdkerrors.SKUFamilyQuotaExceededTerm).
			expectError(fmt.Errorf(errMsgSKUFamilyQuotaFmt, karpv1.CapacityTypeOnDemand, responseErrorTestInstanceName)).
			expectUnavailable(
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeOnDemand),
			).
			build(),

		newTestCase("SKU family quota 0 CPUs").
			withInstanceType(zone2OnDemand, zone3OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError(sdkerrors.OperationNotAllowed, "Family Cores quota Current Limit: 0").
			expectError(fmt.Errorf(errMsgSKUFamilyQuotaFmt, karpv1.CapacityTypeOnDemand, responseErrorTestInstanceName)).
			expectUnavailable(
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeOnDemand),
			).
			build(),

		newTestCase("SKU not available for spot").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeSpot).
			withResponseError(sdkerrors.SKUNotAvailableErrorCode, "").
			expectError(fmt.Errorf(errMsgSKUNotAvailableFmt, responseErrorTestInstanceName, testZone2, karpv1.CapacityTypeSpot)).
			expectUnavailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			expectAvailable(defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand)).
			build(),

		newTestCase("SKU not available for on-demand").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError(sdkerrors.SKUNotAvailableErrorCode, "").
			expectError(fmt.Errorf(errMsgSKUNotAvailableFmt, responseErrorTestInstanceName, testZone2, karpv1.CapacityTypeOnDemand)).
			expectUnavailable(defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand)).
			expectAvailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			build(),

		newTestCase("Zonal allocation failure").
			withInstanceType(zone2OnDemand, zone2Spot, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError(sdkerrors.ZoneAllocationFailed, "").
			expectError(fmt.Errorf(errMsgZonalAllocationFailureFmt, testZone2)).
			expectUnavailable(
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeSpot),
				offeringInformation(testZone2, karpv1.CapacityTypeOnDemand, "Standard_D16s_v3", "D16s_v3", responseErrorTestInstanceFamilyName, "16"),
			).
			expectAvailable(
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeOnDemand),
				offeringInformation(testZone2, karpv1.CapacityTypeOnDemand, "Standard_D2s_v4", "D2s_v4", "standardDsv4Family", "2"),
			).
			build(),

		newTestCase("Allocation failure").
			withInstanceType(zone1Spot, zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError(sdkerrors.AllocationFailed, "").
			expectError(fmt.Errorf(errMsgAllocationFailureFmt, responseErrorTestInstanceName)).
			expectUnavailable(
				defaultTestOfferingInfo(testZone1, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone1, karpv1.CapacityTypeSpot),
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeSpot),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot),
			).
			build(),

		newTestCase("Overconstrained zonal allocation failure").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError(sdkerrors.OverconstrainedZonalAllocationRequest, "").
			expectError(fmt.Errorf(errMsgOverconstrainedZonalFmt, testZone2, karpv1.CapacityTypeOnDemand, responseErrorTestInstanceName)).
			expectUnavailable(defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand)).
			expectAvailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			build(),

		newTestCase("Overconstrained allocation failure").
			withInstanceType(zone1OnDemand, zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError(sdkerrors.OverconstrainedAllocationRequest, "").
			expectError(fmt.Errorf(errMsgOverconstrainedAllocationFmt, karpv1.CapacityTypeOnDemand, responseErrorTestInstanceName)).
			expectUnavailable(
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone1, karpv1.CapacityTypeOnDemand),
			).
			expectAvailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			build(),

		newTestCase("Regional quota exceeded").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError(sdkerrors.OperationNotAllowed, sdkerrors.RegionalQuotaExceededTerm).
			expectError(cloudprovider.NewInsufficientCapacityError(fmt.Errorf("%s", errMsgRegionalQuotaExceeded))).
			build(),
	}
}

func TestHandleResponseErrors(t *testing.T) {
	testCases := setupTestCases()

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			provider := newTestProvider()

			err := provider.handleResponseErrors(
				context.Background(),
				tc.originalRequestSKU,
				tc.instanceType,
				tc.zone,
				tc.capacityType,
				tc.responseErr,
			)

			assert.Equal(t, tc.expectedErr, err)
			assertOfferingsState(t, provider, tc.expectedUnavailableOfferingsInformation, tc.expectedAvailableOfferingsInformation)
		})
	}
}
