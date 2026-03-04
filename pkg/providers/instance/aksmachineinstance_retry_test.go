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
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("isThrottlingError", func() {
	It("should return true for a 429 ResponseError", func() {
		err := fmt.Errorf("failed to get AKS machine %q: %w", "test-machine", &azcore.ResponseError{
			StatusCode: http.StatusTooManyRequests,
			ErrorCode:  "SubscriptionRequestsThrottled",
		})
		Expect(isThrottlingError(err)).To(BeTrue())
	})

	It("should return false for a 404 ResponseError", func() {
		err := fmt.Errorf("failed to get AKS machine %q: %w", "test-machine", &azcore.ResponseError{
			StatusCode: http.StatusNotFound,
			ErrorCode:  "NotFound",
		})
		Expect(isThrottlingError(err)).To(BeFalse())
	})

	It("should return false for a 400 ResponseError", func() {
		err := fmt.Errorf("failed to get AKS machine %q: %w", "test-machine", &azcore.ResponseError{
			StatusCode: http.StatusBadRequest,
			ErrorCode:  "BadRequest",
		})
		Expect(isThrottlingError(err)).To(BeFalse())
	})

	It("should return false for a 500 ResponseError", func() {
		err := fmt.Errorf("failed to get AKS machine %q: %w", "test-machine", &azcore.ResponseError{
			StatusCode: http.StatusInternalServerError,
			ErrorCode:  "InternalServerError",
		})
		Expect(isThrottlingError(err)).To(BeFalse())
	})

	It("should return false for a non-Azure error", func() {
		err := fmt.Errorf("some random error")
		Expect(isThrottlingError(err)).To(BeFalse())
	})

	It("should return false for a nil error", func() {
		Expect(isThrottlingError(nil)).To(BeFalse())
	})
})
