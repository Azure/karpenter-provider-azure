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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/skewer"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type cloudErrorHandler struct {
	match  func(cloudError armcontainerservice.CloudErrorBody) bool
	handle commonErrorHandle
}

type CloudErrorHandling struct {
	UnavailableOfferings *cache.UnavailableOfferings
	CloudErrorHandlers   []cloudErrorHandler
}

func (h *CloudErrorHandling) extractErrorCodeAndMessage(cloudError armcontainerservice.CloudErrorBody) (string, string) {
	var code, message string
	if cloudError.Code != nil {
		code = *cloudError.Code
	}
	if cloudError.Message != nil {
		message = *cloudError.Message
	}
	return code, message
}

func (h *CloudErrorHandling) HandleCloudError(ctx context.Context, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, cloudError armcontainerservice.CloudErrorBody) error {
	for _, handler := range h.CloudErrorHandlers {
		if handler.match(cloudError) {
			errorCode, errorMessage := h.extractErrorCodeAndMessage(cloudError)
			return handler.handle(ctx, h.UnavailableOfferings, sku, instanceType, zone, capacityType, errorCode, errorMessage)
		}
	}

	return nil
}

func DefaultCloudErrorHandlers() []cloudErrorHandler {
	return []cloudErrorHandler{
		{
			match:  isLowPriorityQuotaReached,
			handle: handleLowPriorityQuotaError,
		},
		{
			match:  isSKUFamilyQuotaReached,
			handle: handleSKUFamilyQuotaError,
		},
		{
			match:  isSKUNotAvailable,
			handle: handleSKUNotAvailableError,
		},
		{
			match:  isZonalAllocationFailure,
			handle: handleZonalAllocationFailureError,
		},
		{
			match:  isAllocationFailure,
			handle: handleAllocationFailureError,
		},
		{
			match:  isOverconstrainedZonalAllocationFailure,
			handle: handleOverconstrainedZonalAllocationFailureError,
		},
		{
			match:  isOverconstrainedAllocationFailure,
			handle: handleOverconstrainedAllocationFailureError,
		},
		{
			match:  isRegionalQuotaReached,
			handle: handleRegionalQuotaError,
		},
	}
}

// XPMT: TODO: move the placeholder below to that repo

// CloudError-specific matcher functions
func isLowPriorityQuotaReached(cloudError armcontainerservice.CloudErrorBody) bool {
	var code, message string
	if cloudError.Code != nil {
		code = *cloudError.Code
	}
	if cloudError.Message != nil {
		message = *cloudError.Message
	}
	return code == "OperationNotAllowed" && strings.Contains(message, "LowPriorityCores quota")
}

func isSKUFamilyQuotaReached(cloudError armcontainerservice.CloudErrorBody) bool {
	var code, message string
	if cloudError.Code != nil {
		code = *cloudError.Code
	}
	if cloudError.Message != nil {
		message = *cloudError.Message
	}
	return code == "OperationNotAllowed" && strings.Contains(message, "Family Cores quota")
}

func isSKUNotAvailable(cloudError armcontainerservice.CloudErrorBody) bool {
	return cloudError.Code != nil && *cloudError.Code == "SkuNotAvailable"
}

func isZonalAllocationFailure(cloudError armcontainerservice.CloudErrorBody) bool {
	return cloudError.Code != nil && *cloudError.Code == "ZonalAllocationFailed"
}

func isAllocationFailure(cloudError armcontainerservice.CloudErrorBody) bool {
	return cloudError.Code != nil && *cloudError.Code == "AllocationFailed"
}

func isOverconstrainedZonalAllocationFailure(cloudError armcontainerservice.CloudErrorBody) bool {
	return cloudError.Code != nil && *cloudError.Code == "OverconstrainedZonalAllocationRequest"
}

func isOverconstrainedAllocationFailure(cloudError armcontainerservice.CloudErrorBody) bool {
	return cloudError.Code != nil && *cloudError.Code == "OverconstrainedAllocationRequest"
}

func isRegionalQuotaReached(cloudError armcontainerservice.CloudErrorBody) bool {
	message := ""
	if cloudError.Message != nil {
		message = *cloudError.Message
	}
	return message != "" && strings.Contains(message, "Total Regional Cores quota")
}
