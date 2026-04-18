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

package aksmachinesheaderbatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/samber/lo"
)

// HandlableError is a code + message pair extracted from an API error response.
// It is intentionally minimal and API-agnostic — the caller decides how to interpret it.
type HandlableError struct {
	Code    string
	Message string
}

func (e *HandlableError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// extractPerMachineErrors takes an input map that should be pre-populated with all machine names in the batch,
// then fills in the corresponding HandlableError for each machine based on the API error.
// If the API error code is not a recognized batch error code, the whole error will be applied to all machines.
func extractPerMachineErrors(apiError error, perMachineErrors map[string]*HandlableError) error {
	// Design notes:
	// - The logic/API assumptions are based on the contract noted in the design doc for batch.
	// - This function assumes that the contract is upheld strictly. Any deviation will result in an error, aborting the operation and failing the whole batch.
	// - For the case where the whole error will be applied to all machines, we made an assumption that the top-level code/message is enough to be used by handle logic.
	//   At the time of writing, there is no handle logic for whole error case.

	var respErr *azcore.ResponseError
	if !errors.As(apiError, &respErr) {
		return fmt.Errorf("API error is not an *azcore.ResponseError: %v", apiError)
	}

	switch respErr.ErrorCode {
	case "BatchMachineClientError", "BatchMachineInternalServerError":
		// Recognized batch error codes — parse per-machine details from the response body.
		details, err := parsePerMachineDetails(respErr)
		if err != nil {
			return fmt.Errorf("API error has batch error code %q but failed to parse per-machine details: %w", respErr.ErrorCode, err)
		}
		if len(details) == 0 {
			return fmt.Errorf("API error has batch error code %q but have no per-machine details", respErr.ErrorCode)
		}

		for _, d := range details {
			if d.Target == nil || *d.Target == "" {
				return fmt.Errorf("API error has batch error code but a detail is missing target: Code=%q Message=%q", lo.FromPtr(d.Code), lo.FromPtr(d.Message))
			}
			if _, exists := perMachineErrors[*d.Target]; !exists {
				return fmt.Errorf("API error detail references machine %q which is not in the batch: Code=%q Message=%q", *d.Target, lo.FromPtr(d.Code), lo.FromPtr(d.Message))
			}

			perMachineErrors[*d.Target] = &HandlableError{
				Code:    lo.FromPtr(d.Code),
				Message: lo.FromPtr(d.Message),
			}
		}

	default:
		// Not a recognized batch error code — parse top-level code + message and apply to all machines.
		topLevel, err := parseTopLevelError(respErr)
		if err != nil {
			return fmt.Errorf("failed to parse top-level API error: %w", err)
		}

		for machineName := range perMachineErrors {
			perMachineErrors[machineName] = topLevel
		}
	}

	return nil
}

// parsePerMachineDetails extracts per-machine error details from a batch API error response body.
// Handles both BatchMachineClientError (details at top level) and BatchMachineInternalServerError
// (details JSON-encoded in the message field).
//
// Both batch error codes are AKS RP responses (always flat format), so the response body
// unmarshals directly into armcontainerservice.ErrorDetail.
func parsePerMachineDetails(respErr *azcore.ResponseError) ([]*armcontainerservice.ErrorDetail, error) {
	body, err := readResponseBody(respErr)
	if err != nil {
		return nil, err
	}

	var errorDetail armcontainerservice.ErrorDetail
	if err := json.Unmarshal(body, &errorDetail); err != nil {
		return nil, fmt.Errorf("failed to parse ErrorDetail: %w", err)
	}

	// Use different parsing logic based on the error code, since the location of details[] is
	// different for BatchMachineInternalServerError vs BatchMachineClientError.
	if respErr.ErrorCode == "BatchMachineInternalServerError" {
		// Details are JSON-encoded inside the message field.
		var inner struct {
			Details []*armcontainerservice.ErrorDetail `json:"details"`
		}
		if err := json.Unmarshal([]byte(lo.FromPtr(errorDetail.Message)), &inner); err != nil {
			return nil, fmt.Errorf("failed to parse BatchMachineInternalServerError message JSON: %w", err)
		}
		return inner.Details, nil
	} else if respErr.ErrorCode == "BatchMachineClientError" {
		// Details are at the top level.
		return errorDetail.Details, nil
	}
	return nil, fmt.Errorf("unrecognized batch error code %q when parsing per-machine details", respErr.ErrorCode)
}

// parseTopLevelError extracts the top-level code + message from an API error response body.
func parseTopLevelError(respErr *azcore.ResponseError) (*HandlableError, error) {
	body, err := readResponseBody(respErr)
	if err != nil {
		return nil, err
	}

	// The top-level code/message may be in different formats based on whether the error is from
	// ARM infrastructure (wrapped) vs directly from AKS RP (flat). We will try known formats in
	// order, but ultimately if we cannot parse it, we will return an error instead of silently
	// losing the information.

	// Try wrapped first (ARM infrastructure errors).
	//   {"error": {"code": "ResourceGroupNotFound", "message": "Resource group 'rg' could not be found."}}
	var wrapped struct {
		Error *armcontainerservice.ErrorDetail `json:"error"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Error != nil {
		return &HandlableError{Code: lo.FromPtr(wrapped.Error.Code), Message: lo.FromPtr(wrapped.Error.Message)}, nil
	}

	// Try flat (AKS RP errors).
	//   {"code": "BadRequest", "message": "Agent pool 'np' is not in 'Machines' mode.", "details": [...]}
	var flat armcontainerservice.ErrorDetail
	if err := json.Unmarshal(body, &flat); err != nil {
		return nil, fmt.Errorf("failed to parse API error body: %w", err)
	}
	return &HandlableError{Code: lo.FromPtr(flat.Code), Message: lo.FromPtr(flat.Message)}, nil
}

// readResponseBody reads the body from a ResponseError's RawResponse.
func readResponseBody(respErr *azcore.ResponseError) ([]byte, error) {
	if respErr.RawResponse == nil || respErr.RawResponse.Body == nil {
		return nil, fmt.Errorf("API error has no response body: %s", respErr.ErrorCode)
	}
	body, err := io.ReadAll(respErr.RawResponse.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read API error response body: %w", err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("API error response body is empty: %s", respErr.ErrorCode)
	}
	return body, nil
}
