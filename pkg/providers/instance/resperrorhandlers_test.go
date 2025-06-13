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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/skewer"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

const (
	responseErrorTestInstanceName = "Standard_D2s_v3"
	testZone1                     = "westus-1"
	testZone2                     = "westus-2"
	testZone3                     = "westus-3"
)

type responseErrorTestCase struct {
	testName                                string
	instanceType                            *cloudprovider.InstanceType
	zone                                    string
	capacityType                            string
	responseErr                             error
	expectedErr                             error
	expectedUnavailableOfferingsInformation []offeringToCheck
	expectedAvailableOfferingsInformation   []offeringToCheck
}

// createOfferingType creates a struct with zone and capacity type for defining instance type offerings
func createOfferingType(zone, capacityType string) struct{ zone, capacityType string } {
	return struct{ zone, capacityType string }{
		zone:         zone,
		capacityType: capacityType,
	}
}

func createInstanceType(instanceName string, offerings ...struct{ zone, capacityType string }) *cloudprovider.InstanceType {
	it := &cloudprovider.InstanceType{
		Name:      instanceName,
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

// Helper to create a default instance type for testing, for errors where we don't block specific families of VM SKUs
func defaultCreateInstanceType(offerings ...struct{ zone, capacityType string }) *cloudprovider.InstanceType {
	return createInstanceType(responseErrorTestInstanceName, offerings...)
}

type offeringToCheck struct {
	skuToCheck   *skewer.SKU
	zone         string
	capacityType string
}

func offeringInformation(zone, capacityType, instanceTypeName string) offeringToCheck {
	return offeringToCheck{
		skuToCheck: &skewer.SKU{
			Name: &instanceTypeName,
		},
		zone:         zone,
		capacityType: capacityType,
	}
}

// Helper to create default offering information for testing, for errors where we don't block specific families of VM SKUs
func defaultTestOfferingInfo(zone, capacityType string) offeringToCheck {
	return offeringInformation(zone, capacityType, responseErrorTestInstanceName)
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

func TestHandleResponseErrors(t *testing.T) {
	testCases := []responseErrorTestCase{
		{
			testName:                                "Response error is nil",
			instanceType:                            &cloudprovider.InstanceType{},
			zone:                                    "",
			capacityType:                            karpv1.CapacityTypeOnDemand,
			responseErr:                             nil,
			expectedErr:                             nil,
			expectedUnavailableOfferingsInformation: []offeringToCheck{},
		},
		{
			testName:     "Low priority quota has been reached",
			instanceType: &cloudprovider.InstanceType{},
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeSpot,
			responseErr:  createResponseError(sdkerrors.OperationNotAllowed, sdkerrors.LowPriorityQuotaExceededTerm),
			expectedErr:  fmt.Errorf("this subscription has reached the regional vCPU quota for spot (LowPriorityQuota). To scale beyond this limit, please review the quota increase process here: https://docs.microsoft.com/en-us/azure/azure-portal/supportability/low-priority-quota"),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo("", karpv1.CapacityTypeSpot),
			},
		},
		{
			testName: "SKU family quota has been reached",
			instanceType: defaultCreateInstanceType(
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeOnDemand),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.OperationNotAllowed, sdkerrors.SKUFamilyQuotaExceededTerm),
			expectedErr:  fmt.Errorf("subscription level %s vCPU quota for %s has been reached (may try provision an alternative instance type)", karpv1.CapacityTypeOnDemand, responseErrorTestInstanceName),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeOnDemand),
			},
		},
		{
			testName: "SKU family quota 0 CPUs",
			instanceType: defaultCreateInstanceType(
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeOnDemand),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.OperationNotAllowed, "Family Cores quota Current Limit: 0"),
			expectedErr:  fmt.Errorf("subscription level %s vCPU quota for %s has been reached (may try provision an alternative instance type)", karpv1.CapacityTypeOnDemand, responseErrorTestInstanceName),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeOnDemand),
			},
		},
		{
			testName: "SKU not available for spot",
			instanceType: defaultCreateInstanceType(
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeSpot),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeSpot,
			responseErr:  createResponseError(sdkerrors.SKUNotAvailableErrorCode, ""),
			expectedErr:  fmt.Errorf("the requested SKU is unavailable for instance type %s in zone %s with capacity type %s, for more details please visit: https://aka.ms/azureskunotavailable", responseErrorTestInstanceName, testZone2, karpv1.CapacityTypeSpot),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot),
			},
			expectedAvailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
			},
		},
		{
			testName: "SKU not available for on-demand",
			instanceType: defaultCreateInstanceType(
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeSpot),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.SKUNotAvailableErrorCode, ""),
			expectedErr:  fmt.Errorf("the requested SKU is unavailable for instance type %s in zone %s with capacity type %s, for more details please visit: https://aka.ms/azureskunotavailable", responseErrorTestInstanceName, testZone2, karpv1.CapacityTypeOnDemand),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
			},
			expectedAvailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot),
			},
		},
		{
			testName: "Zonal allocation failure",
			instanceType: defaultCreateInstanceType(
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone2, karpv1.CapacityTypeSpot),
				createOfferingType(testZone3, karpv1.CapacityTypeSpot),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.ZoneAllocationFailed, ""),
			expectedErr:  fmt.Errorf("unable to allocate resources in the selected zone (%s). (will try a different zone to fulfill your request)", testZone2),
			// For zonal allocation failure, we block specific instance type for both capacity types in the zone that failed to allocate
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeSpot),
			},
			expectedAvailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeOnDemand),
				// an example of a VM SKU from the same family but different version(!)
				offeringInformation(testZone2, karpv1.CapacityTypeOnDemand, "Standard_D2s_v4"),
			},
		},
		{
			testName: "Allocation failure",
			instanceType: defaultCreateInstanceType(
				createOfferingType(testZone1, karpv1.CapacityTypeSpot),
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeSpot),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.AllocationFailed, ""),
			expectedErr:  fmt.Errorf("unable to allocate resources with selected VM size (%s). (will try a different VM size to fulfill your request)", responseErrorTestInstanceName),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo(testZone1, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone1, karpv1.CapacityTypeSpot),
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeSpot),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot),
			},
		},
		{
			testName: "Overconstrained zonal allocation failure",
			instanceType: defaultCreateInstanceType(
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeSpot),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.OverconstrainedZonalAllocationRequest, ""),
			expectedErr:  fmt.Errorf("unable to allocate resources in the selected zone (%s) with %s capacity type and %s VM size. (will try a different zone, capacity type or VM size to fulfill your request)", testZone2, karpv1.CapacityTypeOnDemand, responseErrorTestInstanceName),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
			},
			expectedAvailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot),
			},
		},
		{
			testName: "Overconstrained allocation failure",
			instanceType: defaultCreateInstanceType(
				createOfferingType(testZone1, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeSpot),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.OverconstrainedAllocationRequest, ""),
			expectedErr:  fmt.Errorf("unable to allocate resources in all zones with %s capacity type and %s VM size. (will try a different capacity type or VM size to fulfill your request)", karpv1.CapacityTypeOnDemand, responseErrorTestInstanceName),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone1, karpv1.CapacityTypeOnDemand),
			},
			expectedAvailableOfferingsInformation: []offeringToCheck{
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot),
			},
		},
		{
			testName: "Regional quota exceeded",
			instanceType: defaultCreateInstanceType(
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeSpot),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.OperationNotAllowed, sdkerrors.RegionalQuotaExceededTerm),
			expectedErr:  cloudprovider.NewInsufficientCapacityError(fmt.Errorf("regional on-demand vCPU quota limit for subscription has been reached. To scale beyond this limit, please review the quota increase process here: https://learn.microsoft.com/en-us/azure/quotas/regional-quota-requests")),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			testProvider := &DefaultProvider{
				unavailableOfferings:  cache.NewUnavailableOfferings(),
				responseErrorHandlers: defaultResponseErrorHandlers(),
			}

			err := testProvider.handleResponseErrors(context.Background(), tc.instanceType, tc.zone, tc.capacityType, tc.responseErr)
			assert.Equal(t, tc.expectedErr, err)
			for _, info := range tc.expectedUnavailableOfferingsInformation {
				if !testProvider.unavailableOfferings.IsUnavailable(info.skuToCheck, info.zone, info.capacityType) {
					t.Errorf("Expected offering %s in zone %s with capacity type %s to be marked as unavailable", info.skuToCheck.GetName(), info.zone, info.capacityType)
				}
			}
			for _, info := range tc.expectedAvailableOfferingsInformation {
				if testProvider.unavailableOfferings.IsUnavailable(info.skuToCheck, info.zone, info.capacityType) {
					t.Errorf("Expected offering %s in zone %s with capacity type %s to not be marked as unavailable", info.skuToCheck.GetName(), info.zone, info.capacityType)
				}
			}
		})
	}
}
