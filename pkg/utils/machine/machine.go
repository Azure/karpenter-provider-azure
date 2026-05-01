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
	"net/http"
	"strings"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// HandleProvisioningState inspects the provisioning state of an AKS machine.
// It returns the provisioning error if the machine failed, a polling error if the provisioning
// state indicates a canceled or otherwise fatal state, and a boolean indicating whether the
// provisioning is complete (either succeeded, deleting or failed).
func HandleProvisioningState(ctx context.Context, aksMachine *armcontainerservice.Machine) (*armcontainerservice.ErrorDetail, error, bool) {
	provisioningState := lo.FromPtr(aksMachine.Properties.ProvisioningState)
	switch provisioningState {
	case consts.ProvisioningStateCreating, consts.ProvisioningStateUpdating:
		log.FromContext(ctx).V(2).Info("Provisioning ongoing",
			"aksMachineID", aksMachine.ID,
			"provisioningState", provisioningState,
		)
		return nil, nil, false

	case consts.ProvisioningStateDeleting:
		return nil, fmt.Errorf("AKS machine %q sees canceled provisioning state %s", lo.FromPtr(aksMachine.Name), provisioningState), true

	case consts.ProvisioningStateSucceeded:
		return nil, nil, true

	case consts.ProvisioningStateFailed:
		if aksMachine.Properties.Status != nil && aksMachine.Properties.Status.ProvisioningError != nil {
			return aksMachine.Properties.Status.ProvisioningError, nil, true
		}
		return nil, fmt.Errorf("AKS machine %q sees fatal provisioning state %s, but ProvisioningError is nil", lo.FromPtr(aksMachine.Name), provisioningState), true

	default:
		log.FromContext(ctx).V(1).Info("AKS machine is in unrecognized provisioning state",
			"aksMachineID", aksMachine.ID,
			"provisioningState", provisioningState,
		)
		return nil, nil, false
	}
}

// IsAKSMachineOrMachinesPoolNotFound returns true if the error
// returned from the Azure API indicates that the AKS machine or the AKS machines pool
// could not be found.
func IsAKSMachineOrMachinesPoolNotFound(err error) bool {
	if err == nil {
		return false
	}

	azErr := sdkerrors.IsResponseError(err)
	if azErr != nil && (azErr.StatusCode == http.StatusNotFound || // Covers AKS machines pool not found on PUT machine, GET machine, GET (list) machines, POST agent pool (DELETE machines), and AKS machine not found on GET machine
		(azErr.StatusCode == http.StatusBadRequest && azErr.ErrorCode == "InvalidParameter" && strings.Contains(azErr.Error(), "Cannot find any valid machines"))) { // Covers AKS machine not found on POST agent pool (DELETE machines)
		return true
	}
	return false
}
