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
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/samber/lo"
)

func TestAKSMachineProvisioningErrorCodeForMetrics(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		provisioningError *armcontainerservice.ErrorDetail
		want              string
	}{
		{
			name: "detail code takes precedence",
			provisioningError: &armcontainerservice.ErrorDetail{
				Code: lo.ToPtr("QuotaExceeded"),
				Details: []*armcontainerservice.ErrorDetail{
					{Code: lo.ToPtr("OperationNotAllowed")},
				},
			},
			want: "OperationNotAllowed",
		},
		{
			name:              "top-level code is the fallback",
			provisioningError: &armcontainerservice.ErrorDetail{Code: lo.ToPtr("ZonalAllocationFailed")},
			want:              "ZonalAllocationFailed",
		},
		{
			name: "empty detail code falls back to top-level code",
			provisioningError: &armcontainerservice.ErrorDetail{
				Code: lo.ToPtr("OperationPreempted"),
				Details: []*armcontainerservice.ErrorDetail{
					{Code: lo.ToPtr("")},
				},
			},
			want: "OperationPreempted",
		},
		{
			name:              "missing codes use the bounded fallback",
			provisioningError: &armcontainerservice.ErrorDetail{},
			want:              "UnknownError",
		},
		{
			name:              "nil error uses the bounded fallback",
			provisioningError: nil,
			want:              "UnknownError",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := aksMachineProvisioningErrorCodeForMetrics(testCase.provisioningError); got != testCase.want {
				t.Fatalf("aksMachineProvisioningErrorCodeForMetrics() = %q, want %q", got, testCase.want)
			}
		})
	}
}
