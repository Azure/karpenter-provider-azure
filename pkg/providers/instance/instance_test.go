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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

func TestGetPriorityCapacityAndInstanceType(t *testing.T) {
	cases := []struct {
		name                 string
		instanceTypes        []*cloudprovider.InstanceType
		nodeClaim            *karpv1.NodeClaim
		expectedInstanceType string
		expectedPriority     string
		expectedZone         string
	}{
		{
			name:                 "No instance types in the list",
			instanceTypes:        []*cloudprovider.InstanceType{},
			nodeClaim:            &karpv1.NodeClaim{},
			expectedInstanceType: "",
			expectedPriority:     "",
			expectedZone:         "",
		},
		{
			name: "Selects First, Cheapest SKU",
			instanceTypes: []*cloudprovider.InstanceType{
				{
					Name: "Standard_D2s_v3",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.1,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-2"),
							),
							Available: true,
						},
					},
				},
				{
					Name: "Standard_NV16as_v4",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.1,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-2"),
							),
							Available: true,
						},
					},
				},
			},
			nodeClaim:            &karpv1.NodeClaim{},
			expectedInstanceType: "Standard_D2s_v3",
			expectedZone:         "westus-2",
			expectedPriority:     karpv1.CapacityTypeOnDemand,
		},
	}
	provider := NewDefaultProvider(nil, nil, nil, nil, cache.NewUnavailableOfferings(),
		"westus-2",
		"MC_xxxxx_yyyy-region",
		"0000000-0000-0000-0000-0000000000",
		"",
	)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			instanceType, priority, zone := provider.pickSkuSizePriorityAndZone(context.TODO(), c.nodeClaim, c.instanceTypes)
			if instanceType != nil {
				assert.Equal(t, c.expectedInstanceType, instanceType.Name)
			}
			assert.Equal(t, c.expectedZone, zone)
			assert.Equal(t, c.expectedPriority, priority)
		})
	}
}

func TestCreateNICFromQueryResponseData(t *testing.T) {
	id := "nic_id"
	name := "nic_name"
	tag := "tag1"
	val := "val1"
	tags := map[string]*string{tag: &val}

	tc := []struct {
		testName      string
		data          map[string]interface{}
		expectedError string
		expectedNIC   *armnetwork.Interface
	}{
		{
			testName: "missing id",
			data: map[string]interface{}{
				"name": name,
			},
			expectedError: "network interface is missing id",
			expectedNIC:   nil,
		},
		{
			testName: "missing name",
			data: map[string]interface{}{
				"id": id,
			},
			expectedError: "network interface is missing name",
			expectedNIC:   nil,
		},
		{
			testName: "happy case",
			data: map[string]interface{}{
				"id":   id,
				"name": name,
				"tags": map[string]interface{}{tag: val},
			},
			expectedNIC: &armnetwork.Interface{
				ID:   &id,
				Name: &name,
				Tags: tags,
			},
		},
	}

	for _, c := range tc {
		nic, err := createNICFromQueryResponseData(c.data)
		if nic != nil {
			expected := *c.expectedNIC
			actual := *nic
			assert.Equal(t, *expected.ID, *actual.ID, c.testName)
			assert.Equal(t, *expected.Name, *actual.Name, c.testName)
			for key := range expected.Tags {
				assert.Equal(t, *(expected.Tags[key]), *(actual.Tags[key]), c.testName)
			}
		}
		if err != nil {
			assert.Equal(t, c.expectedError, err.Error(), c.testName)
		}
	}
}

// Currently tested: ID, Name, Tags, Zones
// TODO: Add the below attributes for Properties if needed:
// Priority, InstanceView.HyperVGeneration, TimeCreated
func TestCreateVMFromQueryResponseData(t *testing.T) {
	id := "vm_id"
	name := "vm_name"
	tag := "tag1"
	val := "val1"
	zone := "us-west"
	tags := map[string]*string{tag: &val}
	zones := []*string{&zone}

	tc := []struct {
		testName      string
		data          map[string]interface{}
		expectedError string
		expectedVM    *armcompute.VirtualMachine
	}{
		{
			testName: "missing id",
			data: map[string]interface{}{
				"name": name,
			},
			expectedError: "virtual machine is missing id",
			expectedVM:    nil,
		},
		{
			testName: "missing name",
			data: map[string]interface{}{
				"id": id,
			},
			expectedError: "virtual machine is missing name",
			expectedVM:    nil,
		},
		{
			testName: "happy case",
			data: map[string]interface{}{
				"id":    id,
				"name":  name,
				"tags":  map[string]interface{}{tag: val},
				"zones": []interface{}{zone},
			},
			expectedVM: &armcompute.VirtualMachine{
				ID:    &id,
				Name:  &name,
				Tags:  tags,
				Zones: zones,
			},
		},
	}

	for _, c := range tc {
		vm, err := createVMFromQueryResponseData(c.data)
		if vm != nil {
			expected := *c.expectedVM
			actual := *vm
			assert.Equal(t, *expected.ID, *actual.ID, c.testName)
			assert.Equal(t, *expected.Name, *actual.Name, c.testName)
			for key := range expected.Tags {
				assert.Equal(t, *(expected.Tags[key]), *(actual.Tags[key]), c.testName)
			}
			for i := range expected.Zones {
				assert.Equal(t, *(expected.Zones[i]), *(actual.Zones[i]), c.testName)
			}
		}
		if err != nil {
			assert.Equal(t, c.expectedError, err.Error(), c.testName)
		}
	}
}

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

func createInstanceType(offerings ...struct{ zone, capacityType string }) *cloudprovider.InstanceType {
	it := &cloudprovider.InstanceType{
		Name:      responseErrorTestInstanceName,
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
	instanceTypeName string
	zone             string
	capacityType     string
}

func offeringInformation(zone, capacityType string) offeringToCheck {
	return offeringToCheck{
		instanceTypeName: responseErrorTestInstanceName,
		zone:             zone,
		capacityType:     capacityType,
	}
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
				offeringInformation("", karpv1.CapacityTypeSpot),
			},
		},
		{
			testName: "SKU family quota has been reached",
			instanceType: createInstanceType(
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeOnDemand),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.OperationNotAllowed, sdkerrors.SKUFamilyQuotaExceededTerm),
			expectedErr:  fmt.Errorf("subscription level %s vCPU quota for %s has been reached (may try provision an alternative instance type)", karpv1.CapacityTypeOnDemand, responseErrorTestInstanceName),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				offeringInformation(testZone2, karpv1.CapacityTypeOnDemand),
				offeringInformation(testZone3, karpv1.CapacityTypeOnDemand),
			},
		},
		{
			testName: "SKU family quota 0 CPUs",
			instanceType: createInstanceType(
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeOnDemand),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.OperationNotAllowed, "Family Cores quota Current Limit: 0"),
			expectedErr:  fmt.Errorf("subscription level %s vCPU quota for %s has been reached (may try provision an alternative instance type)", karpv1.CapacityTypeOnDemand, responseErrorTestInstanceName),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				offeringInformation(testZone2, karpv1.CapacityTypeOnDemand),
				offeringInformation(testZone3, karpv1.CapacityTypeOnDemand),
			},
		},
		{
			testName: "SKU not available for spot",
			instanceType: createInstanceType(
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeSpot),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeSpot,
			responseErr:  createResponseError(sdkerrors.SKUNotAvailableErrorCode, ""),
			expectedErr:  fmt.Errorf("the requested SKU is unavailable for instance type %s in zone %s with capacity type %s, for more details please visit: https://aka.ms/azureskunotavailable", responseErrorTestInstanceName, testZone2, karpv1.CapacityTypeSpot),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				offeringInformation(testZone3, karpv1.CapacityTypeSpot),
			},
			expectedAvailableOfferingsInformation: []offeringToCheck{
				offeringInformation(testZone2, karpv1.CapacityTypeOnDemand),
			},
		},
		{
			testName: "SKU not available for on-demand",
			instanceType: createInstanceType(
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeSpot),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.SKUNotAvailableErrorCode, ""),
			expectedErr:  fmt.Errorf("the requested SKU is unavailable for instance type %s in zone %s with capacity type %s, for more details please visit: https://aka.ms/azureskunotavailable", responseErrorTestInstanceName, testZone2, karpv1.CapacityTypeOnDemand),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				offeringInformation(testZone2, karpv1.CapacityTypeOnDemand),
			},
			expectedAvailableOfferingsInformation: []offeringToCheck{
				offeringInformation(testZone3, karpv1.CapacityTypeSpot),
			},
		},
		{
			testName: "Zonal allocation failure",
			instanceType: createInstanceType(
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone2, karpv1.CapacityTypeSpot),
				createOfferingType(testZone3, karpv1.CapacityTypeSpot),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.ZoneAllocationFailed, ""),
			expectedErr:  fmt.Errorf("unable to allocate resources in the selected zone (%s). (will try a different zone to fulfill your request)", testZone2),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				offeringInformation(testZone2, karpv1.CapacityTypeOnDemand),
				offeringInformation(testZone2, karpv1.CapacityTypeSpot),
			},
			expectedAvailableOfferingsInformation: []offeringToCheck{
				offeringInformation(testZone3, karpv1.CapacityTypeSpot),
			},
		},
		{
			testName: "Allocation failure",
			instanceType: createInstanceType(
				createOfferingType(testZone1, karpv1.CapacityTypeSpot),
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeSpot),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.AllocationFailed, ""),
			expectedErr:  fmt.Errorf("unable to allocate resources with selected VM size (%s). (will try a different VM size to fulfill your request)", responseErrorTestInstanceName),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				offeringInformation(testZone1, karpv1.CapacityTypeOnDemand),
				offeringInformation(testZone1, karpv1.CapacityTypeSpot),
				offeringInformation(testZone2, karpv1.CapacityTypeOnDemand),
				offeringInformation(testZone2, karpv1.CapacityTypeSpot),
				offeringInformation(testZone3, karpv1.CapacityTypeOnDemand),
				offeringInformation(testZone3, karpv1.CapacityTypeSpot),
			},
		},
		{
			testName: "Overconstrained zonal allocation failure",
			instanceType: createInstanceType(
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeSpot),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.OverconstrainedZonalAllocationRequest, ""),
			expectedErr:  fmt.Errorf("unable to allocate resources in the selected zone (%s) with %s capacity type and %s VM size. (will try a different zone, capacity type or VM size to fulfill your request)", testZone2, karpv1.CapacityTypeOnDemand, responseErrorTestInstanceName),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				offeringInformation(testZone2, karpv1.CapacityTypeOnDemand),
			},
			expectedAvailableOfferingsInformation: []offeringToCheck{
				offeringInformation(testZone3, karpv1.CapacityTypeSpot),
			},
		},
		{
			testName: "Overconstrained allocation failure",
			instanceType: createInstanceType(
				createOfferingType(testZone1, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone2, karpv1.CapacityTypeOnDemand),
				createOfferingType(testZone3, karpv1.CapacityTypeSpot),
			),
			zone:         testZone2,
			capacityType: karpv1.CapacityTypeOnDemand,
			responseErr:  createResponseError(sdkerrors.OverconstrainedAllocationRequest, ""),
			expectedErr:  fmt.Errorf("unable to allocate resources in all zones with %s capacity type and %s VM size. (will try a different capacity type or VM size to fulfill your request)", karpv1.CapacityTypeOnDemand, responseErrorTestInstanceName),
			expectedUnavailableOfferingsInformation: []offeringToCheck{
				offeringInformation(testZone2, karpv1.CapacityTypeOnDemand),
				offeringInformation(testZone1, karpv1.CapacityTypeOnDemand),
			},
			expectedAvailableOfferingsInformation: []offeringToCheck{
				offeringInformation(testZone3, karpv1.CapacityTypeSpot),
			},
		},
		{
			testName: "Regional quota exceeded",
			instanceType: createInstanceType(
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
				unavailableOfferings: cache.NewUnavailableOfferings(),
			}

			err := testProvider.handleResponseErrors(context.Background(), tc.instanceType, tc.zone, tc.capacityType, tc.responseErr)
			assert.Equal(t, tc.expectedErr, err)
			for _, info := range tc.expectedUnavailableOfferingsInformation {
				if !testProvider.unavailableOfferings.IsUnavailable(info.instanceTypeName, info.zone, info.capacityType) {
					t.Errorf("Expected offering %s in zone %s with capacity type %s to be marked as unavailable", info.instanceTypeName, info.zone, info.capacityType)
				}
			}
			for _, info := range tc.expectedAvailableOfferingsInformation {
				if testProvider.unavailableOfferings.IsUnavailable(info.instanceTypeName, info.zone, info.capacityType) {
					t.Errorf("Expected offering %s in zone %s with capacity type %s to not be marked as unavailable", info.instanceTypeName, info.zone, info.capacityType)
				}
			}
		})
	}
}
