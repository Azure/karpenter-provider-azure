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
	"github.com/Azure/skewer"
	. "github.com/onsi/gomega"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	cloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type handlableErrorTestCaseBuilder struct {
	tc handlableErrorTestCase
}

func newHandlableErrorTestCase(name string) *handlableErrorTestCaseBuilder {
	return &handlableErrorTestCaseBuilder{
		tc: handlableErrorTestCase{
			testName:                                name,
			originalRequestSKU:                      createDefaultTestSKU(),
			expectedUnavailableOfferingsInformation: []offeringToCheck{},
			expectedAvailableOfferingsInformation:   []offeringToCheck{},
		},
	}
}

func (b *handlableErrorTestCaseBuilder) withInstanceType(offerings ...offering) *handlableErrorTestCaseBuilder {
	b.tc.instanceType = createInstanceType(testInstanceName, offerings...)
	return b
}

func (b *handlableErrorTestCaseBuilder) withZoneAndCapacity(zone, capacityType string) *handlableErrorTestCaseBuilder {
	b.tc.zone = zone
	b.tc.capacityType = capacityType
	return b
}

func (b *handlableErrorTestCaseBuilder) withHandlableError(code, message string) *handlableErrorTestCaseBuilder {
	b.tc.he = &HandlableError{Code: code, Message: message}
	return b
}

func (b *handlableErrorTestCaseBuilder) expectError(err error) *handlableErrorTestCaseBuilder {
	b.tc.expectedErr = err
	return b
}

func (b *handlableErrorTestCaseBuilder) expectUnavailable(offerings ...offeringToCheck) *handlableErrorTestCaseBuilder {
	b.tc.expectedUnavailableOfferingsInformation = offerings
	return b
}

func (b *handlableErrorTestCaseBuilder) expectAvailable(offerings ...offeringToCheck) *handlableErrorTestCaseBuilder {
	b.tc.expectedAvailableOfferingsInformation = offerings
	return b
}

func (b *handlableErrorTestCaseBuilder) build() handlableErrorTestCase {
	return b.tc
}

type handlableErrorTestCase struct {
	testName                                string
	instanceType                            *cloudprovider.InstanceType
	originalRequestSKU                      *skewer.SKU
	zone                                    string
	capacityType                            string
	he                                      *HandlableError
	expectedErr                             error
	expectedUnavailableOfferingsInformation []offeringToCheck
	expectedAvailableOfferingsInformation   []offeringToCheck
}

func newTestHandlableErrorHandler() *HandlableErrorHandler {
	return NewMachineBeginCreateErrorHandler(cache.NewUnavailableOfferings())
}

func setupHandlableErrorTestCases() []handlableErrorTestCase {
	return []handlableErrorTestCase{
		newHandlableErrorTestCase("VMSizeNotSupported").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withHandlableError("VMSizeNotSupported", "hello").
			expectError(fmt.Errorf(errMsgSKUNotAvailableForSubscriptionFmt, testInstanceName)).
			expectUnavailable(
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot),
			).
			build(),

		newHandlableErrorTestCase("BadRequest - VM size not supported for subscription").
			withInstanceType(zone2OnDemand, zone3Spot).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeSpot).
			withHandlableError("BadRequest",
				fmt.Sprintf("Virtual Machine size: '%s' is not supported for subscription sub-123 in location 'westus'. Please refer to aka.ms/aks/vm-size-selector to find supported VM sizes in location 'westus'.", testInstanceName)).
			expectError(fmt.Errorf(errMsgSKUNotAvailableForSubscriptionFmt, testInstanceName)).
			expectUnavailable(
				defaultTestOfferingInfo(testZone2, karpv1.CapacityTypeOnDemand),
				defaultTestOfferingInfo(testZone3, karpv1.CapacityTypeSpot),
			).
			build(),

		// === Negative cases: errors that should NOT be handled ===

		newHandlableErrorTestCase("BadRequest without subscription message - not handled").
			withInstanceType(zone2OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withHandlableError("BadRequest", "Some other bad request error").
			expectError(nil).
			build(),

		newHandlableErrorTestCase("VMSizeDoesNotSupportEncryptionAtHost - not handled").
			withInstanceType(zone2OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withHandlableError("VMSizeDoesNotSupportEncryptionAtHost",
				fmt.Sprintf("The Virtual Machine size %s does not support EncryptionAtHost.", testInstanceName)).
			expectError(nil).
			build(),

		newHandlableErrorTestCase("Unknown error code - not handled").
			withInstanceType(zone2OnDemand).
			withZoneAndCapacity(testZone2, karpv1.CapacityTypeOnDemand).
			withHandlableError("UnknownErrorCode", "Some unknown error").
			expectError(nil).
			build(),

		newHandlableErrorTestCase("Internal server error - not handled").
			withInstanceType(zone1Spot, zone2OnDemand).
			withZoneAndCapacity(testZone1, karpv1.CapacityTypeSpot).
			withHandlableError("InternalServerError", "Azure service temporarily unavailable").
			expectError(nil).
			build(),
	}
}

func TestHandleMachineAPISyncErrors(t *testing.T) {
	testCases := setupHandlableErrorTestCases()

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			g := NewWithT(t)
			handler := newTestHandlableErrorHandler()

			err := handler.Handle(
				context.Background(),
				tc.originalRequestSKU,
				tc.instanceType,
				tc.zone,
				tc.capacityType,
				tc.he,
			)

			if tc.expectedErr == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err).To(Equal(tc.expectedErr))
			}
			assertOfferingsState(t, handler.UnavailableOfferings, tc.expectedUnavailableOfferingsInformation, tc.expectedAvailableOfferingsInformation)
		})
	}
}

// TestMachineAPISyncErrorMatcherFunctions tests the individual matcher functions directly.
func TestMachineAPISyncErrorMatcherFunctions(t *testing.T) {
	tests := []struct {
		name    string
		matcher func(*HandlableError) bool
		he      *HandlableError
		expect  bool
	}{
		{
			name:    "isSKUNotAvailableForSubscription - matches VMSizeNotSupported",
			matcher: isSKUNotAvailableForSubscription,
			he:      &HandlableError{Code: "VMSizeNotSupported", Message: "Virtual Machine size is not supported"},
			expect:  true,
		},
		{
			name:    "isSKUNotAvailableForSubscription - matches VMSizeNotSupported without subscription message too",
			matcher: isSKUNotAvailableForSubscription,
			he:      &HandlableError{Code: "VMSizeNotSupported", Message: "Some other error"},
			expect:  true,
		},
		{
			name:    "isSKUNotAvailableForSubscription - does not match BadRequest code",
			matcher: isSKUNotAvailableForSubscription,
			he:      &HandlableError{Code: "BadRequest", Message: "is not supported for subscription"},
			expect:  false,
		},
		{
			name:    "isSKUNotAvailableForSubscriptionBadRequest - matches BadRequest with subscription message",
			matcher: isSKUNotAvailableForSubscriptionBadRequest,
			he:      &HandlableError{Code: "BadRequest", Message: "Virtual Machine size: 'Standard_D2s_v3' is not supported for subscription sub-123"},
			expect:  true,
		},
		{
			name:    "isSKUNotAvailableForSubscriptionBadRequest - does not match BadRequest without subscription message",
			matcher: isSKUNotAvailableForSubscriptionBadRequest,
			he:      &HandlableError{Code: "BadRequest", Message: "Some other bad request error"},
			expect:  false,
		},
		{
			name:    "isSKUNotAvailableForSubscriptionBadRequest - does not match VMSizeNotSupported code",
			matcher: isSKUNotAvailableForSubscriptionBadRequest,
			he:      &HandlableError{Code: "VMSizeNotSupported", Message: "is not supported for subscription"},
			expect:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(tt.matcher(tt.he)).To(Equal(tt.expect))
		})
	}
}
