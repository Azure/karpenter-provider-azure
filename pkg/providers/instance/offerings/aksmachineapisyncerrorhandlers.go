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
	"strings"

	"github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
)

// This handles possible errors from AKS Machine API sync phase.
// Currently, it does not return CRP format, so this extra logic is needed.
// TODO: consider moving this to azure-sdk-for-go-extensions once it is more mature?
// We may want to stop relying on CRP error format entirely, and have a more organized error handling based on AKS API, in which this is a beginning of.
func NewMachineAPISyncErrorHandler(unavailableOfferings *cache.UnavailableOfferings) *ResponseErrorHandler {
	return &ResponseErrorHandler{
		UnavailableOfferings: unavailableOfferings,
		HandlerEntries: []responseErrorHandlerEntry{
			{
				match:  IsSKUNotAvailableForSubscription,
				handle: handleSKUNotAvailableError,
			},
			{
				match:  IsSKUNotAvailableForSubscriptionBadRequest,
				handle: handleSKUNotAvailableError,
			},
		},
	}
}

// For "Virtual Machine size: '%s' is not supported for subscription %s in location '%[3]s'. %s. Please refer to aka.ms/aks/vm-size-selector to find supported VM sizes in location '%[3]s'."
// ASSUMPTION: this error occuring means the whole VM family is not available. handleSKUNotAvailableError may mark the whole family as unavailable (not at the time of writing, but will likely be).
func IsSKUNotAvailableForSubscription(err error) bool {
	azErr := errors.IsResponseError(err)
	return azErr != nil && azErr.ErrorCode == "VMSizeNotSupported"
}

// For "Virtual Machine size: '%s' is not supported for subscription %s in location '%[3]s'. %s. Please refer to aka.ms/aks/vm-size-selector to find supported VM sizes in location '%[3]s'."
// Similar to IsSKUNotAvailableForSubscription, but this different error code is another possible variant.
// ASSUMPTION: this error occuring means the whole VM family is not available. handleSKUNotAvailableError may mark the whole family as unavailable (not at the time of writing, but will likely be).
func IsSKUNotAvailableForSubscriptionBadRequest(err error) bool {
	azErr := errors.IsResponseError(err)
	return azErr != nil && azErr.ErrorCode == "BadRequest" && strings.Contains(azErr.Error(), "is not supported for subscription")
}
