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
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/skewer"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// HandlableError is a code + message pair extracted from an API error response.
// It is intentionally minimal and API-agnostic. This module decides how to interpret it.
type HandlableError struct {
	Code    string
	Message string
}

func (e *HandlableError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func ErrorToHandlableError(err error) *HandlableError {
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return nil
	}
	return &HandlableError{
		Code:    respErr.ErrorCode,
		Message: respErr.Error(), // Note: this is not truly extracting the message, but rather dump the whole error in.
		// This is okay for now, as all handling logic just does substring match on the message, or care only about ErrorCode (ideal in long run).
		// TODO: rework this? Once we revisit this whole module to perhaps share the handling logic to azure-sdk-for-go-extensions.
	}
}

type handlableErrorHandlerEntry struct {
	match  func(*HandlableError) bool
	handle errorHandle
}

// HandlableErrorHandler classifies HandlableErrors and takes appropriate offerings cache actions.
// TODO: consider replacing other error handlers with this one, which is more generic and serves the purpose. Can consider this when moving towards handling AKS errors more.
type HandlableErrorHandler struct {
	UnavailableOfferings *cache.UnavailableOfferings
	HandlerEntries       []handlableErrorHandlerEntry
}

// NewMachineBeginCreateErrorHandler creates a handler for AKS Machine API sync-phase errors.
// TODO: consider sharing this on azure-sdk-for-go-extensions like other error handlers.
func NewMachineBeginCreateErrorHandler(unavailableOfferings *cache.UnavailableOfferings) *HandlableErrorHandler {
	return &HandlableErrorHandler{
		UnavailableOfferings: unavailableOfferings,
		HandlerEntries: []handlableErrorHandlerEntry{
			{
				match:  isSKUNotAvailableForSubscription,
				handle: handleSKUNotAvailableError,
			},
			{
				match:  isSKUNotAvailableForSubscriptionBadRequest,
				handle: handleSKUNotAvailableError,
			},
		},
	}
}

func (h *HandlableErrorHandler) Handle(ctx context.Context, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, he *HandlableError) error {
	for _, handler := range h.HandlerEntries {
		if handler.match(he) {
			return handler.handle(ctx, h.UnavailableOfferings, sku, instanceType, zone, capacityType, he.Code, he.Message)
		}
	}
	return nil
}

// For "Virtual Machine size: '%s' is not supported for subscription %s in location '%[3]s'. %s. Please refer to aka.ms/aks/vm-size-selector to find supported VM sizes in location '%[3]s'."
// ASSUMPTION: this error occurring means the whole VM family is not available. handleSKUNotAvailableError may mark the whole family as unavailable (not at the time of writing, but will likely be).
func isSKUNotAvailableForSubscription(he *HandlableError) bool {
	return he.Code == "VMSizeNotSupported"
}

// For "Virtual Machine size: '%s' is not supported for subscription %s in location '%[3]s'. %s. Please refer to aka.ms/aks/vm-size-selector to find supported VM sizes in location '%[3]s'."
// Similar to IsSKUNotAvailableForSubscription, but this different error code is another possible variant.
// ASSUMPTION: this error occurring means the whole VM family is not available. handleSKUNotAvailableError may mark the whole family as unavailable (not at the time of writing, but will likely be).
func isSKUNotAvailableForSubscriptionBadRequest(he *HandlableError) bool {
	return he.Code == "BadRequest" && strings.Contains(he.Message, "is not supported for subscription")
}
