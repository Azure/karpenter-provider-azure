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

package machine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

func TestHandleProvisioningState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                  string
		provisioningState     string
		provisioningError     *armcontainerservice.ErrorDetail
		status                *armcontainerservice.MachineStatus
		expectedErrorDetails  *armcontainerservice.ErrorDetail
		expectedPollingErr    bool
		expectedPollingErrMsg string
		expectedDone          bool
	}{
		{
			name:                 "Creating state - non-terminal",
			provisioningState:    consts.ProvisioningStateCreating,
			expectedErrorDetails: nil,
			expectedPollingErr:   false,
			expectedDone:         false,
		},
		{
			name:                 "Updating state - non-terminal",
			provisioningState:    consts.ProvisioningStateUpdating,
			expectedErrorDetails: nil,
			expectedPollingErr:   false,
			expectedDone:         false,
		},
		{
			name:                 "Succeeded state - terminal success",
			provisioningState:    consts.ProvisioningStateSucceeded,
			expectedErrorDetails: nil,
			expectedPollingErr:   false,
			expectedDone:         true,
		},
		{
			name:                  "Deleting state - terminal canceled",
			provisioningState:     consts.ProvisioningStateDeleting,
			expectedErrorDetails:  nil,
			expectedPollingErr:    true,
			expectedPollingErrMsg: "canceled provisioning state",
			expectedDone:          true,
		},
		{
			name:              "Failed state with ProvisioningError",
			provisioningState: consts.ProvisioningStateFailed,
			status: &armcontainerservice.MachineStatus{
				ProvisioningError: &armcontainerservice.ErrorDetail{
					Code:    lo.ToPtr("QuotaExceeded"),
					Message: lo.ToPtr("Quota exceeded for VM family"),
				},
			},
			expectedErrorDetails: &armcontainerservice.ErrorDetail{
				Code:    lo.ToPtr("QuotaExceeded"),
				Message: lo.ToPtr("Quota exceeded for VM family"),
			},
			expectedPollingErr: false,
			expectedDone:       true,
		},
		{
			name:              "Failed state with nil ProvisioningError",
			provisioningState: consts.ProvisioningStateFailed,
			status: &armcontainerservice.MachineStatus{
				ProvisioningError: nil,
			},
			expectedErrorDetails:  nil,
			expectedPollingErr:    true,
			expectedPollingErrMsg: "ProvisioningError is nil",
			expectedDone:          true,
		},
		{
			name:                  "Failed state with nil Status",
			provisioningState:     consts.ProvisioningStateFailed,
			status:                nil,
			expectedErrorDetails:  nil,
			expectedPollingErr:    true,
			expectedPollingErrMsg: "ProvisioningError is nil",
			expectedDone:          true,
		},
		{
			name:                 "Unknown state",
			provisioningState:    "UnknownState",
			expectedErrorDetails: nil,
			expectedPollingErr:   false,
			expectedDone:         false,
		},
		{
			name:                 "Empty state",
			provisioningState:    "",
			expectedErrorDetails: nil,
			expectedPollingErr:   false,
			expectedDone:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			ctx := context.Background()

			aksMachine := &armcontainerservice.Machine{
				ID:   lo.ToPtr("test-machine-id"),
				Name: lo.ToPtr("test-machine-name"),
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: lo.ToPtr(tt.provisioningState),
					Status:            tt.status,
				},
			}

			errorDetails, pollingErr, done := HandleProvisioningState(ctx, aksMachine)

			g.Expect(errorDetails).To(Equal(tt.expectedErrorDetails))
			if tt.expectedPollingErr {
				g.Expect(pollingErr).To(HaveOccurred())
				g.Expect(pollingErr.Error()).To(ContainSubstring(tt.expectedPollingErrMsg))
			} else {
				g.Expect(pollingErr).ToNot(HaveOccurred())
			}
			g.Expect(done).To(Equal(tt.expectedDone))
		})
	}
}

func TestIsAKSMachineOrMachinesPoolNotFound(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "404 Not Found",
			err:      createAzureResponseError("NotFound", "Resource not found", http.StatusNotFound),
			expected: true,
		},
		{
			name:     "400 InvalidParameter with 'Cannot find any valid machines' message",
			err:      createAzureResponseError("InvalidParameter", "Cannot find any valid machines in the pool", http.StatusBadRequest),
			expected: true,
		},
		{
			name:     "400 InvalidParameter with different message",
			err:      createAzureResponseError("InvalidParameter", "Some other validation error", http.StatusBadRequest),
			expected: false,
		},
		{
			name:     "400 with different error code",
			err:      createAzureResponseError("ValidationError", "Cannot find any valid machines", http.StatusBadRequest),
			expected: false,
		},
		{
			name:     "500 Internal Server Error",
			err:      createAzureResponseError("InternalServerError", "Server error", http.StatusInternalServerError),
			expected: false,
		},
		{
			name:     "403 Forbidden",
			err:      createAzureResponseError("Forbidden", "Access denied", http.StatusForbidden),
			expected: false,
		},
		{
			name:     "401 Unauthorized",
			err:      createAzureResponseError("Unauthorized", "Authentication required", http.StatusUnauthorized),
			expected: false,
		},
		{
			name:     "429 Too Many Requests",
			err:      createAzureResponseError("TooManyRequests", "Rate limited", http.StatusTooManyRequests),
			expected: false,
		},
		{
			name:     "standard Go error",
			err:      fmt.Errorf("standard error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			result := IsAKSMachineOrMachinesPoolNotFound(tt.err)

			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

// createAzureResponseError creates an Azure ResponseError for testing purposes
func createAzureResponseError(errorCode, errorMessage string, statusCode int) error {
	errorBody := fmt.Sprintf(`{"error": {"code": "%s", "message": "%s"}}`, errorCode, errorMessage)
	return &azcore.ResponseError{
		ErrorCode:  errorCode,
		StatusCode: statusCode,
		RawResponse: &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(strings.NewReader(errorBody)),
		},
	}
}
