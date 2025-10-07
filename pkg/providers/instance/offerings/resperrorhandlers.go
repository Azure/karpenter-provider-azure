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
	"errors"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/skewer"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type responseErrorHandlerEntry struct {
	match  func(error) bool
	handle commonErrorHandle
}

type ResponseErrorHandler struct {
	UnavailableOfferings *cache.UnavailableOfferings
	HandlerEntries       []responseErrorHandlerEntry
}

func NewResponseErrorHandler(unavailableOfferings *cache.UnavailableOfferings) *ResponseErrorHandler {
	return &ResponseErrorHandler{
		UnavailableOfferings: unavailableOfferings,
		HandlerEntries: []responseErrorHandlerEntry{
			{
				match:  sdkerrors.LowPriorityQuotaHasBeenReached,
				handle: handleLowPriorityQuotaError,
			},
			{
				match:  sdkerrors.SKUFamilyQuotaHasBeenReached,
				handle: handleSKUFamilyQuotaError,
			},
			{
				match:  sdkerrors.IsSKUNotAvailable,
				handle: handleSKUNotAvailableError,
			},
			{
				match:  sdkerrors.ZonalAllocationFailureOccurred,
				handle: handleZonalAllocationFailureError,
			},
			{
				match:  sdkerrors.AllocationFailureOccurred,
				handle: handleAllocationFailureError,
			},
			{
				match:  sdkerrors.OverconstrainedZonalAllocationFailureOccurred,
				handle: handleOverconstrainedZonalAllocationFailureError,
			},
			{
				match:  sdkerrors.OverconstrainedAllocationFailureOccurred,
				handle: handleOverconstrainedAllocationFailureError,
			},
			{
				match:  sdkerrors.RegionalQuotaHasBeenReached,
				handle: handleRegionalQuotaError,
			},
		},
	}
}

func (h *ResponseErrorHandler) extractErrorCodeAndMessage(err error) (string, string) {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.ErrorCode, respErr.Error()
	}
	return "", err.Error()
}

func (h *ResponseErrorHandler) Handle(ctx context.Context, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, responseError error) error {
	for _, handler := range h.HandlerEntries {
		if handler.match(responseError) {
			errorCode, errorMessage := h.extractErrorCodeAndMessage(responseError)
			return handler.handle(ctx, h.UnavailableOfferings, sku, instanceType, zone, capacityType, errorCode, errorMessage)
		}
	}

	return nil
}
