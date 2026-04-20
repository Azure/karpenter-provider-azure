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

package cloudprovider

import (
	"errors"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// validationErrorCodes are AKS RP error codes that indicate user-facing validation failures.
// When Machine API rejects a creation request due to invalid user configuration
// (e.g., invalid taints, labels, kubelet config), the error surfaces with one of these codes.
var validationErrorCodes = map[string]bool{
	"InvalidParameter":          true,
	"PropertyChangeNotAllowed":  true,
	"ValidationError":           true,
	"BadRequest":                true,
	"InvalidTemplateDeployment": true,
}

// ValidationErrorInfo holds extracted information from a validation error.
type ValidationErrorInfo struct {
	Code    string
	Message string
}

// extractValidationErrorFromError checks if a Go error wraps an Azure ResponseError
// with a validation error code. Returns the error info if found, nil otherwise.
// This covers the sync path where BeginCreateOrUpdate returns a ResponseError directly,
// as well as wrapped errors from the async path that contain validation error codes.
func extractValidationErrorFromError(err error) *ValidationErrorInfo {
	if err == nil {
		return nil
	}

	// First, try to extract from a direct azcore.ResponseError
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		if validationErrorCodes[respErr.ErrorCode] {
			return &ValidationErrorInfo{
				Code:    respErr.ErrorCode,
				Message: respErr.Error(),
			}
		}
	}

	// Also check for validation error codes in the error message string.
	// This handles cases where the error is wrapped through handleMachineProvisioningError
	// and the original ErrorDetail code appears in the formatted message.
	errMsg := err.Error()
	for code := range validationErrorCodes {
		if strings.Contains(errMsg, "code="+code) || strings.Contains(errMsg, "Code=\""+code+"\"") {
			return &ValidationErrorInfo{
				Code:    code,
				Message: errMsg,
			}
		}
	}

	return nil
}
