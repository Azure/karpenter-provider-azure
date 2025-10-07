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
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/skewer"
	"github.com/stretchr/testify/assert"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type cloudErrorTestCaseBuilder struct {
	tc cloudErrorTestCase
}

func newCloudErrorTestCase(name string) *cloudErrorTestCaseBuilder {
	return &cloudErrorTestCaseBuilder{
		tc: cloudErrorTestCase{
			testName:                                name,
			originalRequestSKU:                      createDefaultTestSKU(),
			expectedUnavailableOfferingsInformation: []offeringToCheck{},
			expectedAvailableOfferingsInformation:   []offeringToCheck{},
		},
	}
}

func (b *cloudErrorTestCaseBuilder) withInstanceType(offerings ...offering) *cloudErrorTestCaseBuilder {
	b.tc.instanceType = createInstanceType(testInstanceName, offerings...)
	return b
}

func (b *cloudErrorTestCaseBuilder) withEmptyInstanceType() *cloudErrorTestCaseBuilder {
	b.tc.instanceType = &corecloudprovider.InstanceType{}
	return b
}

func (b *cloudErrorTestCaseBuilder) withZoneAndCapacity(zone, capacityType string) *cloudErrorTestCaseBuilder {
	b.tc.zone = zone
	b.tc.capacityType = capacityType
	return b
}

func (b *cloudErrorTestCaseBuilder) withCloudError(errorCode, errorMessage string) *cloudErrorTestCaseBuilder {
	b.tc.cloudErr = createCloudError(errorCode, errorMessage)
	return b
}

func (b *cloudErrorTestCaseBuilder) expectError(err error) *cloudErrorTestCaseBuilder {
	b.tc.expectedErr = err
	return b
}

func (b *cloudErrorTestCaseBuilder) expectUnavailable(offerings ...offeringToCheck) *cloudErrorTestCaseBuilder {
	b.tc.expectedUnavailableOfferingsInformation = offerings
	return b
}

func (b *cloudErrorTestCaseBuilder) expectAvailable(offerings ...offeringToCheck) *cloudErrorTestCaseBuilder {
	b.tc.expectedAvailableOfferingsInformation = offerings
	return b
}

func (b *cloudErrorTestCaseBuilder) build() cloudErrorTestCase {
	return b.tc
}

type cloudErrorTestCase struct {
	testName                                string
	instanceType                            *corecloudprovider.InstanceType
	originalRequestSKU                      *skewer.SKU
	zone                                    string
	capacityType                            string
	cloudErr                                armcontainerservice.CloudErrorBody
	expectedErr                             error
	expectedUnavailableOfferingsInformation []offeringToCheck
	expectedAvailableOfferingsInformation   []offeringToCheck
}

func createCloudError(code, message string) armcontainerservice.CloudErrorBody {
	return armcontainerservice.CloudErrorBody{
		Code:    to.Ptr(code),
		Message: to.Ptr(message),
	}
}

// newTestCloudErrorHandling creates a test provider with default configuration
func newTestCloudErrorHandling() *CloudErrorHandler {
	return NewCloudErrorHandler(cache.NewUnavailableOfferings())
}

func setupCloudErrorTestCases() []cloudErrorTestCase {
	return []cloudErrorTestCase{
		newCloudErrorTestCase("Cloud error is empty").
			withEmptyInstanceType().
			withZoneAndCapacity("", karpv1.CapacityTypeOnDemand).
			withCloudError("", "").
			expectError(nil).
			build(),

		newCloudErrorTestCase("Low priority quota has been reached").
			withEmptyInstanceType().
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeSpot).
			withCloudError("OperationNotAllowed", fmt.Sprintf("Operation could not be completed as it results in exceeding approved LowPriorityCores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: 3, Current Usage: 0, Additional Required: 6", testZone2)).
			expectError(fmt.Errorf("%s", errMsgLowPriorityQuota)).
			expectUnavailable(defaultTestOfferingInfo("", karpv1.CapacityTypeSpot)).
			build(),

		newCloudErrorTestCase("SKU family quota has been reached").
			withInstanceType(zone2OnDemand, zone3OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withCloudError("OperationNotAllowed", fmt.Sprintf("Operation could not be completed as it results in exceeding approved %s Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: 24, Current Usage: 24, Additional Required: 8", testInstanceName, testZone2)).
			expectError(fmt.Errorf(errMsgSKUFamilyQuotaFmt, karpv1.CapacityTypeOnDemand, testInstanceName)).
			expectUnavailable(
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeOnDemand),
			).
			build(),

		newCloudErrorTestCase("SKU family quota 0 CPUs").
			withInstanceType(zone2OnDemand, zone3OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withCloudError("OperationNotAllowed", fmt.Sprintf("Operation could not be completed as it results in exceeding approved %s Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: 0, Current Usage: 0, Additional Required: 8", testInstanceName, testZone2)).
			expectError(fmt.Errorf(errMsgSKUFamilyQuotaFmt, karpv1.CapacityTypeOnDemand, testInstanceName)).
			expectUnavailable(
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeOnDemand),
			).
			build(),

		newCloudErrorTestCase("SKU not available for spot").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeSpot).
			withCloudError("SkuNotAvailable", fmt.Sprintf("The requested VM size for resource 'Following SKUs have failed for Capacity Restrictions: %s' is currently not available in location '%s'. Please try another size or deploy to a different location or different zone. See https://aka.ms/azureskunotavailable for details.", testInstanceName, testZone2)).
			expectError(fmt.Errorf(errMsgSKUNotAvailableFmt, testInstanceName, testZone2, karpv1.CapacityTypeSpot)).
			expectUnavailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			expectAvailable(defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand)).
			build(),

		newCloudErrorTestCase("SKU not available for on-demand").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withCloudError("SkuNotAvailable", fmt.Sprintf("The requested VM size for resource 'Following SKUs have failed for Capacity Restrictions: %s' is currently not available in location '%s'. Please try another size or deploy to a different location or different zone. See https://aka.ms/azureskunotavailable for details.", testInstanceName, testZone2)).
			expectError(fmt.Errorf(errMsgSKUNotAvailableFmt, testInstanceName, testZone2, karpv1.CapacityTypeOnDemand)).
			expectUnavailable(defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand)).
			expectAvailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			build(),

		newCloudErrorTestCase("Zonal allocation failure").
			withInstanceType(zone2OnDemand, zone2Spot, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withCloudError("ZonalAllocationFailed", fmt.Sprintf("Allocation failed. We do not have sufficient capacity for the requested VM size %s in zone %s. Read more about improving likelihood of allocation success at http://aka.ms/allocation-guidance", testInstanceName, testZone2)).
			expectError(fmt.Errorf(errMsgZonalAllocationFailureFmt, testZone2)).
			expectUnavailable(
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeSpot),
				offeringInformation(testZone2, karpv1.CapacityTypeOnDemand, "Standard_D16s_v3", "D16s_v3", testInstanceFamilyName, "16"),
			).
			expectAvailable(
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeOnDemand),
				offeringInformation(testZone3, karpv1.CapacityTypeOnDemand, "Standard_D2s_v4", "D2s_v4", "standardDsv4Family", "2"),
			).
			build(),

		newCloudErrorTestCase("Allocation failure").
			withInstanceType(zone1Spot, zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withCloudError("AllocationFailed", "Allocation failed. If you are trying to add a new VM to an Availability Set or update/resize an existing VM in an Availability Set, please note that such Availability Set allocation is scoped to a single cluster, and it is possible that the cluster is out of capacity. Please read more about improving likelihood of allocation success at http://aka.ms/allocation-guidance.").
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

		newCloudErrorTestCase("Overconstrained zonal allocation failure").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withCloudError("OverconstrainedZonalAllocationRequest", fmt.Sprintf("Allocation failed. VM(s) with the following constraints cannot be allocated in zone %s, because the condition is too restrictive. Please remove some constraints and try again. Constraints applied are:\n  - Availability Zone (%s)\n  - Capacity Type (%s)\n  - VM Size (%s)\n  - Networking Constraints (such as Accelerated Networking or IPv6)", testZone2, testZone2, karpv1.CapacityTypeOnDemand, testInstanceName)).
			expectError(fmt.Errorf(errMsgOverconstrainedZonalFmt, testZone2, karpv1.CapacityTypeOnDemand, testInstanceName)).
			expectUnavailable(defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand)).
			expectAvailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			build(),

		newCloudErrorTestCase("Overconstrained allocation failure").
			withInstanceType(zone1OnDemand, zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withCloudError("OverconstrainedAllocationRequest", fmt.Sprintf("Allocation failed. VM(s) with the following constraints cannot be allocated across all zones, because the condition is too restrictive. Please remove some constraints and try again. Constraints applied are:\n  - Capacity Type (%s)\n  - VM Size (%s)\n  - Networking Constraints (such as Accelerated Networking or IPv6)\n  - Subscription Pinning", karpv1.CapacityTypeOnDemand, testInstanceName)).
			expectError(fmt.Errorf(errMsgOverconstrainedAllocationFmt, karpv1.CapacityTypeOnDemand, testInstanceName)).
			expectUnavailable(
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone1, karpv1.CapacityTypeOnDemand),
			).
			expectAvailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			build(),

		newCloudErrorTestCase("Regional quota exceeded").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withCloudError("OperationNotAllowed", fmt.Sprintf("Operation could not be completed as it results in exceeding approved Total Regional Cores quota. Additional details - Deployment Model: Resource Manager, Location: %s, Current Limit: 10, Current Usage: 8, Additional Required: 8", testZone2)).
			expectError(corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("%s", errMsgRegionalQuotaExceeded))).
			build(),

		newCloudErrorTestCase("Unknown error code - no handler matches").
			withInstanceType(zone2OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withCloudError("UnknownErrorCode", "Some unknown error message").
			expectError(nil).
			build(),

		newCloudErrorTestCase("Generic Azure error - no handler matches").
			withInstanceType(zone1Spot, zone2OnDemand).
			withZoneAndCapacity(testZone1, karpv1.CapacityTypeSpot).
			withCloudError("InternalServerError", "Azure service temporarily unavailable").
			expectError(nil).
			build(),

		newCloudErrorTestCase("Non-Azure error - no handler matches").
			withInstanceType(zone1OnDemand).
			withZoneAndCapacity(testZone1, karpv1.CapacityTypeOnDemand).
			withCloudError("NetworkError", "Network connection timeout").
			expectError(nil).
			build(),
	}
}

func TestHandleCloudErrors(t *testing.T) {
	testCases := setupCloudErrorTestCases()

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			provider := newTestCloudErrorHandling()

			err := provider.Handle(
				context.Background(),
				tc.originalRequestSKU,
				tc.instanceType,
				tc.zone,
				tc.capacityType,
				tc.cloudErr,
			)

			assert.Equal(t, tc.expectedErr, err)
			assertOfferingsState(t, provider.UnavailableOfferings, tc.expectedUnavailableOfferingsInformation, tc.expectedAvailableOfferingsInformation)
		})
	}
}
