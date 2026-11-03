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
	"strings"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
)

const invalidResourceReferenceCode = "InvalidResourceReference"

// isMissingSubmittedBackendPool returns true if err is an Azure InvalidResourceReference
// whose message identifies one of the backend-pool IDs that were submitted in the NIC request.
// This avoids retrying errors about missing subnets, NSGs, or other unrelated resources.
func isMissingSubmittedBackendPool(err error, pools *loadbalancer.BackendAddressPools) bool {
	azErr := sdkerrors.IsResponseError(err)
	if azErr == nil {
		return false
	}
	if !strings.EqualFold(azErr.ErrorCode, invalidResourceReferenceCode) {
		return false
	}

	// Check whether any submitted pool ID appears in the error message.
	errMsg := strings.ToLower(err.Error())
	for _, poolID := range pools.IPv4PoolIDs {
		if strings.Contains(errMsg, strings.ToLower(poolID)) {
			return true
		}
	}
	for _, poolID := range pools.IPv6PoolIDs {
		if strings.Contains(errMsg, strings.ToLower(poolID)) {
			return true
		}
	}
	return false
}
