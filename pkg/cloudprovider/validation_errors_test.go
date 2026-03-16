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
	"fmt"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	. "github.com/onsi/gomega"
)

func TestExtractValidationErrorFromError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectNil    bool
		expectedCode string
	}{
		{
			name:      "nil error returns nil",
			err:       nil,
			expectNil: true,
		},
		{
			name:      "generic error returns nil",
			err:       errors.New("something went wrong"),
			expectNil: true,
		},
		{
			name: "ResponseError with non-validation code returns nil",
			err: &azcore.ResponseError{
				ErrorCode:  "SkuNotAvailable",
				StatusCode: http.StatusConflict,
			},
			expectNil: true,
		},
		{
			name: "ResponseError with InvalidParameter returns info",
			err: &azcore.ResponseError{
				ErrorCode:  "InvalidParameter",
				StatusCode: http.StatusBadRequest,
			},
			expectNil:    false,
			expectedCode: "InvalidParameter",
		},
		{
			name: "ResponseError with ValidationError returns info",
			err: &azcore.ResponseError{
				ErrorCode:  "ValidationError",
				StatusCode: http.StatusBadRequest,
			},
			expectNil:    false,
			expectedCode: "ValidationError",
		},
		{
			name: "ResponseError with PropertyChangeNotAllowed returns info",
			err: &azcore.ResponseError{
				ErrorCode:  "PropertyChangeNotAllowed",
				StatusCode: http.StatusBadRequest,
			},
			expectNil:    false,
			expectedCode: "PropertyChangeNotAllowed",
		},
		{
			name: "ResponseError with BadRequest returns info",
			err: &azcore.ResponseError{
				ErrorCode:  "BadRequest",
				StatusCode: http.StatusBadRequest,
			},
			expectNil:    false,
			expectedCode: "BadRequest",
		},
		{
			name: "ResponseError with InvalidTemplateDeployment returns info",
			err: &azcore.ResponseError{
				ErrorCode:  "InvalidTemplateDeployment",
				StatusCode: http.StatusBadRequest,
			},
			expectNil:    false,
			expectedCode: "InvalidTemplateDeployment",
		},
		{
			name: "wrapped ResponseError with InvalidParameter returns info",
			err: fmt.Errorf("failed to begin create AKS machine %q: %w", "test-machine", &azcore.ResponseError{
				ErrorCode:  "InvalidParameter",
				StatusCode: http.StatusBadRequest,
			}),
			expectNil:    false,
			expectedCode: "InvalidParameter",
		},
		{
			name:         "error message containing code= pattern from handleMachineProvisioningError",
			err:          fmt.Errorf("failed to create AKS machine \"test\" during LRO, unhandled provisioning error: code=InvalidParameter, message=The taint key is invalid"),
			expectNil:    false,
			expectedCode: "InvalidParameter",
		},
		{
			name:         "error message containing Code= pattern from AKS RP error format",
			err:          fmt.Errorf(`some wrapping: Code="ValidationError" Message="The request was invalid"`),
			expectNil:    false,
			expectedCode: "ValidationError",
		},
		{
			name: "ResponseError with quota error code does not match",
			err: &azcore.ResponseError{
				ErrorCode:  "OperationNotAllowed",
				StatusCode: http.StatusForbidden,
			},
			expectNil: true,
		},
		{
			name: "ResponseError with allocation error code does not match",
			err: &azcore.ResponseError{
				ErrorCode:  "AllocationFailed",
				StatusCode: http.StatusConflict,
			},
			expectNil: true,
		},
		{
			name:      "error message with quota code but not validation pattern does not match",
			err:       fmt.Errorf("code=OperationNotAllowed, message=quota exceeded"),
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result := extractValidationErrorFromError(tt.err)

			if tt.expectNil {
				g.Expect(result).To(BeNil())
			} else {
				g.Expect(result).ToNot(BeNil())
				g.Expect(result.Code).To(Equal(tt.expectedCode))
				g.Expect(result.Message).ToNot(BeEmpty())
			}
		})
	}
}
