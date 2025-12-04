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

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/skewer"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type errorDetailHandlerEntry struct {
	match  func(errorDetail armcontainerservice.ErrorDetail) bool
	handle errorHandle
}

type ErrorDetailHandler struct {
	UnavailableOfferings *cache.UnavailableOfferings
	HandlerEntries       []errorDetailHandlerEntry
}

// Comparing to ResponseErrorHandler, this is handling same errors, but for a different error data model.
// HandlerEntries should generally be kept in sync.
//
// This exists because AKS machine data model returns this armcontainerservice.ErrorDetail (AKS-specific) instead of azcore.ResponseError (SDK-wide). They have no common interface.
// VM instance is still using ResponseErrorHandler.
// Ideally, if AKS machine data model returns azcore.ResponseError, we don't need this split at all.
func NewErrorDetailHandler(unavailableOfferings *cache.UnavailableOfferings) *ErrorDetailHandler {
	return &ErrorDetailHandler{
		UnavailableOfferings: unavailableOfferings,
		HandlerEntries: []errorDetailHandlerEntry{
			{
				match:  sdkerrors.LowPriorityQuotaHasBeenReachedInErrorDetail,
				handle: handleLowPriorityQuotaError,
			},
			{
				match:  sdkerrors.SKUFamilyQuotaHasBeenReachedInErrorDetail,
				handle: handleSKUFamilyQuotaError,
			},
			{
				match:  sdkerrors.IsSKUNotAvailableInErrorDetail,
				handle: handleSKUNotAvailableError,
			},
			{
				match:  sdkerrors.ZonalAllocationFailureOccurredInErrorDetail,
				handle: handleZonalAllocationFailureError,
			},
			{
				match:  sdkerrors.AllocationFailureOccurredInErrorDetail,
				handle: handleAllocationFailureError,
			},
			{
				match:  sdkerrors.OverconstrainedZonalAllocationFailureOccurredInErrorDetail,
				handle: handleOverconstrainedZonalAllocationFailureError,
			},
			{
				match:  sdkerrors.OverconstrainedAllocationFailureOccurredInErrorDetail,
				handle: handleOverconstrainedAllocationFailureError,
			},
			{
				match:  sdkerrors.RegionalQuotaHasBeenReachedInErrorDetail,
				handle: handleRegionalQuotaError,
			},
		},
	}
}

func (h *ErrorDetailHandler) extractErrorCodeAndMessage(errorDetail armcontainerservice.ErrorDetail) (string, string) {
	var code, message string
	if errorDetail.Code != nil {
		code = *errorDetail.Code
	}
	if errorDetail.Message != nil {
		message = *errorDetail.Message
	}
	return code, message
}

func (h *ErrorDetailHandler) Handle(ctx context.Context, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, errorDetail armcontainerservice.ErrorDetail) error {
	for _, handler := range h.HandlerEntries {
		if handler.match(errorDetail) {
			errorCode, errorMessage := h.extractErrorCodeAndMessage(errorDetail)
			return handler.handle(ctx, h.UnavailableOfferings, sku, instanceType, zone, capacityType, errorCode, errorMessage)
		}
	}

	return nil
}
