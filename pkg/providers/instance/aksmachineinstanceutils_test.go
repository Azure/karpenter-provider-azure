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

package instance

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// createAzureResponseError creates a proper Azure SDK error with the given error code and message
func createAzureResponseError(errorCode, errorMessage string, statusCode int) error {
	errorBody := fmt.Sprintf(`{"error": {"code": "%s", "message": "%s"}}`, errorCode, errorMessage)
	return &azcore.ResponseError{
		ErrorCode:  errorCode,
		StatusCode: statusCode,
		RawResponse: &http.Response{
			Body: io.NopCloser(strings.NewReader(errorBody)),
		},
	}
}

var _ = Describe("AKSMachineInstanceUtils Helper Functions", func() {

	Context("IsAKSMachineOrMachinesPoolNotFound", func() {
		It("should return false for nil error", func() {
			result := IsAKSMachineOrMachinesPoolNotFound(nil)
			Expect(result).To(BeFalse())
		})

		It("should return true for HTTP 404 status code", func() {
			azureError := &azcore.ResponseError{
				ErrorCode:   "lol",
				StatusCode:  404,
				RawResponse: nil,
			}

			result := IsAKSMachineOrMachinesPoolNotFound(azureError)
			Expect(result).To(BeTrue())
		})

		It("should return true for InvalidParameter error with 'Cannot find any valid machines' message", func() {
			// Create the exact error message from your example
			errorMessage := "Cannot find any valid machines to delete. Please check your input machine names. The valid machines to delete in agent pool 'testmpool' are: testmachine."
			azureError := createAzureResponseError("InvalidParameter", errorMessage, 400)

			result := IsAKSMachineOrMachinesPoolNotFound(azureError)
			Expect(result).To(BeTrue())
		})

		It("should return false for HTTP 400 with InvalidParameter but different message", func() {
			// Create an InvalidParameter error with a different message that shouldn't match
			differentMessage := "InvalidParameter: Some other validation error"
			azureError := createAzureResponseError("InvalidParameter", differentMessage, 400)

			result := IsAKSMachineOrMachinesPoolNotFound(azureError)
			Expect(result).To(BeFalse())
		})

		It("should return false for other HTTP status codes", func() {
			azureError := &azcore.ResponseError{
				ErrorCode:   "Unauthorized",
				StatusCode:  401,
				RawResponse: nil,
			}

			result := IsAKSMachineOrMachinesPoolNotFound(azureError)
			Expect(result).To(BeFalse())

			azureError = &azcore.ResponseError{
				ErrorCode:   "InternalOperationError",
				StatusCode:  500,
				RawResponse: nil,
			}

			result = IsAKSMachineOrMachinesPoolNotFound(azureError)
			Expect(result).To(BeFalse())
		})

		It("should return false for non-Azure SDK errors", func() {
			result := IsAKSMachineOrMachinesPoolNotFound(fmt.Errorf("some generic error"))
			Expect(result).To(BeFalse())
		})
	})
})
