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
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/skewer"
	"github.com/stretchr/testify/assert"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
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
	b.tc.instanceType = createInstanceType(testInstanceName, offerings...)
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

func createDefaultTestSKU() *skewer.SKU {
	return createTestSKU(testInstanceName, testInstanceVMSize, testInstanceFamilyName, "2")
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

// newTestResponseErrorHandling creates a test provider with default configuration
func newTestResponseErrorHandling() *ResponseErrorHandler {
	return NewResponseErrorHandler(cache.NewUnavailableOfferings())
}

func assertOfferingsState(t *testing.T, unavailableOfferings *cache.UnavailableOfferings, unavailable, available []offeringToCheck) {
	t.Helper()

	for _, info := range unavailable {
		assert.True(t,
			unavailableOfferings.IsUnavailable(info.skuToCheck, info.zone, info.capacityType),
			"Expected offering %s in zone %s with capacity type %s to be unavailable",
			info.skuToCheck.GetName(), info.zone, info.capacityType,
		)
	}

	for _, info := range available {
		assert.False(t,
			unavailableOfferings.IsUnavailable(info.skuToCheck, info.zone, info.capacityType),
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
			expectError(fmt.Errorf(errMsgSKUFamilyQuotaFmt, karpv1.CapacityTypeOnDemand, testInstanceName)).
			expectUnavailable(
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeOnDemand),
			).
			build(),

		newTestCase("SKU family quota 0 CPUs").
			withInstanceType(zone2OnDemand, zone3OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError(sdkerrors.OperationNotAllowed, "Family Cores quota Current Limit: 0").
			expectError(fmt.Errorf(errMsgSKUFamilyQuotaFmt, karpv1.CapacityTypeOnDemand, testInstanceName)).
			expectUnavailable(
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeOnDemand),
			).
			build(),

		newTestCase("SKU not available for spot").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeSpot).
			withResponseError(sdkerrors.SKUNotAvailableErrorCode, "").
			expectError(fmt.Errorf(errMsgSKUNotAvailableFmt, testInstanceName, testZone2, karpv1.CapacityTypeSpot)).
			expectUnavailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			expectAvailable(defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand)).
			build(),

		newTestCase("SKU not available for on-demand").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError(sdkerrors.SKUNotAvailableErrorCode, "").
			expectError(fmt.Errorf(errMsgSKUNotAvailableFmt, testInstanceName, testZone2, karpv1.CapacityTypeOnDemand)).
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
				offeringInformation(testZone2, karpv1.CapacityTypeOnDemand, "Standard_D16s_v3", "D16s_v3", testInstanceFamilyName, "16"),
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
			expectError(fmt.Errorf(errMsgAllocationFailureFmt, testInstanceName)).
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
			expectError(fmt.Errorf(errMsgOverconstrainedZonalFmt, testZone2, karpv1.CapacityTypeOnDemand, testInstanceName)).
			expectUnavailable(defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand)).
			expectAvailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			build(),

		newTestCase("Overconstrained allocation failure").
			withInstanceType(zone1OnDemand, zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError(sdkerrors.OverconstrainedAllocationRequest, "").
			expectError(fmt.Errorf(errMsgOverconstrainedAllocationFmt, karpv1.CapacityTypeOnDemand, testInstanceName)).
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

		newTestCase("Unknown error code - no handler matches").
			withInstanceType(zone2OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError("UnknownErrorCode", "Some unknown error message").
			expectError(nil).
			build(),

		newTestCase("Generic Azure error - no handler matches").
			withInstanceType(zone1Spot, zone2OnDemand).
			withZoneAndCapacity(testZone1, karpv1.CapacityTypeSpot).
			withResponseError("InternalServerError", "Azure service temporarily unavailable").
			expectError(nil).
			build(),

		newTestCase("Non-Azure error - no handler matches").
			withInstanceType(zone1OnDemand).
			withZoneAndCapacity(testZone1, karpv1.CapacityTypeOnDemand).
			withResponseError("NetworkError", "Network connection timeout").
			expectError(nil).
			build(),
	}
}

func TestHandleResponseErrors(t *testing.T) {
	testCases := setupTestCases()

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			provider := newTestResponseErrorHandling()

			err := provider.Handle(
				context.Background(),
				tc.originalRequestSKU,
				tc.instanceType,
				tc.zone,
				tc.capacityType,
				tc.responseErr,
			)

			assert.Equal(t, tc.expectedErr, err)
			assertOfferingsState(t, provider.UnavailableOfferings, tc.expectedUnavailableOfferingsInformation, tc.expectedAvailableOfferingsInformation)
		})
	}
}
