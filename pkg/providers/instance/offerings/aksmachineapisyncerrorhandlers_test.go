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

	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	. "github.com/onsi/gomega"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func newTestMachineAPISyncErrorHandling() *ResponseErrorHandler {
	return NewMachineAPISyncErrorHandler(cache.NewUnavailableOfferings())
}

func setupMachineAPISyncErrorTestCases() []responseErrorTestCase {
	return []responseErrorTestCase{
		newTestCase("VMSizeNotSupported").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError("VMSizeNotSupported",
				fmt.Sprintf("Virtual Machine size: '%s' is not supported for subscription sub-123 in location 'westus'. Please refer to aka.ms/aks/vm-size-selector to find supported VM sizes in location 'westus'.", testInstanceName)).
			expectError(fmt.Errorf(errMsgSKUNotAvailableFmt, testInstanceName, testZone2, karpv1.CapacityTypeOnDemand)).
			expectUnavailable(defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand)).
			expectAvailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			build(),

		newTestCase("BadRequest - VM size not supported for subscription").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeSpot).
			withResponseError("BadRequest",
				fmt.Sprintf("Virtual Machine size: '%s' is not supported for subscription sub-123 in location 'westus'. Please refer to aka.ms/aks/vm-size-selector to find supported VM sizes in location 'westus'.", testInstanceName)).
			expectError(fmt.Errorf(errMsgSKUNotAvailableFmt, testInstanceName, testZone2, karpv1.CapacityTypeSpot)).
			expectUnavailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			expectAvailable(defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand)).
			build(),

		newTestCase("BadRequest - GPU VM SKU restricted by AKS").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError("BadRequest",
				"The GPU VM SKU(s) `Standard_NC6` chosen for agentpool(s) `pool1` are restricted by AKS. The supported GPU VM sizes are `Standard_NC6s_v3,Standard_NC12s_v3`.").
			expectError(fmt.Errorf(errMsgSKUNotAvailableFmt, testInstanceName, testZone2, karpv1.CapacityTypeOnDemand)).
			expectUnavailable(defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand)).
			expectAvailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			build(),

		newTestCase("BadRequest - Small VM SKU restricted by AKS").
			withInstanceType(zone1Spot, zone2OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError("BadRequest",
				"The VM SKUs chosen for agentpool(s) `pool1` are restricted by AKS. This is typically due to small CPU/Memory. Please see https://aka.ms/aks/restricted-skus for more details.").
			expectError(fmt.Errorf(errMsgSKUNotAvailableFmt, testInstanceName, testZone2, karpv1.CapacityTypeOnDemand)).
			expectUnavailable(defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand)).
			expectAvailable(defaultTestOfferingInfo(testZone1, karpv1.CapacityTypeSpot)).
			build(),

		newTestCase("ErrorCodeUnsupportedGPUDedicatedVHDVMSize").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError("ErrorCodeUnsupportedGPUDedicatedVHDVMSize",
				fmt.Sprintf("The VM Size of %s is not a SKU that supports GPU Driver Type Selection. The supported sizes are 'Standard_NC6s_v3,Standard_NC12s_v3'", testInstanceName)).
			expectError(fmt.Errorf(errMsgSKUNotAvailableFmt, testInstanceName, testZone2, karpv1.CapacityTypeOnDemand)).
			expectUnavailable(defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand)).
			expectAvailable(defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot)).
			build(),

		// === Negative cases: errors that should NOT be handled ===

		newTestCase("BadRequest without subscription message - not handled").
			withInstanceType(zone2OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError("BadRequest", "Some other bad request error").
			expectError(nil).
			build(),

		newTestCase("VMSizeDoesNotSupportEncryptionAtHost - not handled").
			withInstanceType(zone2OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError("VMSizeDoesNotSupportEncryptionAtHost",
				fmt.Sprintf("The Virtual Machine size %s does not support EncryptionAtHost.", testInstanceName)).
			expectError(nil).
			build(),

		newTestCase("Unknown error code - not handled").
			withInstanceType(zone2OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withResponseError("UnknownErrorCode", "Some unknown error").
			expectError(nil).
			build(),

		newTestCase("Internal server error - not handled").
			withInstanceType(zone1Spot, zone2OnDemand).
			withZoneAndCapacity(testZone1, karpv1.CapacityTypeSpot).
			withResponseError("InternalServerError", "Azure service temporarily unavailable").
			expectError(nil).
			build(),

		newTestCase("Nil response error - not handled").
			withEmptyInstanceType().
			withZoneAndCapacity("", karpv1.CapacityTypeOnDemand).
			expectError(nil).
			build(),
	}
}

func TestHandleMachineAPISyncErrors(t *testing.T) {
	testCases := setupMachineAPISyncErrorTestCases()

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			g := NewWithT(t)
			provider := newTestMachineAPISyncErrorHandling()

			err := provider.Handle(
				context.Background(),
				tc.originalRequestSKU,
				tc.instanceType,
				tc.zone,
				tc.capacityType,
				tc.responseErr,
			)

			if tc.expectedErr == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err).To(Equal(tc.expectedErr))
			}
			assertOfferingsState(t, provider.UnavailableOfferings, tc.expectedUnavailableOfferingsInformation, tc.expectedAvailableOfferingsInformation)
		})
	}
}

// TestMachineAPISyncErrorMatcherFunctions tests the individual matcher functions directly
// to verify they correctly identify the AKS RP error patterns.
func TestMachineAPISyncErrorMatcherFunctions(t *testing.T) {
	tests := []struct {
		name    string
		matcher func(error) bool
		err     error
		expect  bool
	}{
		// IsSKUNotAvailableForSubscription
		{
			name:    "IsSKUNotAvailableForSubscription - matches VMSizeNotSupported with subscription message",
			matcher: IsSKUNotAvailableForSubscription,
			err:     createResponseError("VMSizeNotSupported", "Virtual Machine size: 'Standard_D2s_v3' is not supported for subscription sub-123 in location 'westus'."),
			expect:  true,
		},
		{
			name:    "IsSKUNotAvailableForSubscription - does not match VMSizeNotSupported without subscription message",
			matcher: IsSKUNotAvailableForSubscription,
			err:     createResponseError("VMSizeNotSupported", "Some other error"),
			expect:  false,
		},
		{
			name:    "IsSKUNotAvailableForSubscription - does not match BadRequest code",
			matcher: IsSKUNotAvailableForSubscription,
			err:     createResponseError("BadRequest", "Virtual Machine size is not supported for subscription"),
			expect:  false,
		},

		// IsSKUNotAvailableForSubscriptionBadRequest
		{
			name:    "IsSKUNotAvailableForSubscriptionBadRequest - matches BadRequest with subscription message",
			matcher: IsSKUNotAvailableForSubscriptionBadRequest,
			err:     createResponseError("BadRequest", "Virtual Machine size: 'Standard_D2s_v3' is not supported for subscription sub-123 in location 'westus'."),
			expect:  true,
		},
		{
			name:    "IsSKUNotAvailableForSubscriptionBadRequest - does not match BadRequest without subscription message",
			matcher: IsSKUNotAvailableForSubscriptionBadRequest,
			err:     createResponseError("BadRequest", "Some other bad request error"),
			expect:  false,
		},
		{
			name:    "IsSKUNotAvailableForSubscriptionBadRequest - does not match VMSizeNotSupported code",
			matcher: IsSKUNotAvailableForSubscriptionBadRequest,
			err:     createResponseError("VMSizeNotSupported", "is not supported for subscription"),
			expect:  false,
		},

		// IsSKURestrictedByAKS
		{
			name:    "IsSKURestrictedByAKS - matches GPU restriction message",
			matcher: IsSKURestrictedByAKS,
			err:     createResponseError("BadRequest", "The GPU VM SKU(s) `Standard_NC6` chosen for agentpool(s) `pool1` are restricted by AKS. The supported GPU VM sizes are `Standard_NC6s_v3`."),
			expect:  true,
		},
		{
			name:    "IsSKURestrictedByAKS - matches small SKU restriction message",
			matcher: IsSKURestrictedByAKS,
			err:     createResponseError("BadRequest", "The VM SKUs chosen for agentpool(s) `pool1` are restricted by AKS. This is typically due to small CPU/Memory."),
			expect:  true,
		},
		{
			name:    "IsSKURestrictedByAKS - does not match BadRequest without restricted message",
			matcher: IsSKURestrictedByAKS,
			err:     createResponseError("BadRequest", "Some other bad request error"),
			expect:  false,
		},
		{
			name:    "IsSKURestrictedByAKS - does not match non-BadRequest code with restricted message",
			matcher: IsSKURestrictedByAKS,
			err:     createResponseError("VMSizeNotSupported", "restricted by AKS"),
			expect:  false,
		},

		// IsUnsupportedGPUDedicatedVHDVMSize
		{
			name:    "IsUnsupportedGPUDedicatedVHDVMSize - matches correct error code",
			matcher: IsUnsupportedGPUDedicatedVHDVMSize,
			err:     createResponseError("ErrorCodeUnsupportedGPUDedicatedVHDVMSize", "The VM Size of Standard_D2s_v3 is not a SKU that supports GPU Driver Type Selection."),
			expect:  true,
		},
		{
			name:    "IsUnsupportedGPUDedicatedVHDVMSize - does not match other error codes",
			matcher: IsUnsupportedGPUDedicatedVHDVMSize,
			err:     createResponseError("BadRequest", "The VM Size of Standard_D2s_v3 is not a SKU that supports GPU Driver Type Selection."),
			expect:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(tt.matcher(tt.err)).To(Equal(tt.expect))
		})
	}
}
