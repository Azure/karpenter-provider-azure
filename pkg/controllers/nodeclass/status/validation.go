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

package status

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	RBACMissing = "DiskEncryptionSetRBACMissing"
	CheckFailed = "DiskEncryptionSetRBACCheckFailed"
	// ValidationSuccessRequeueInterval defines how often to re-validate DES RBAC after success
	// Set to 1 hour since RBAC changes are infrequent in production
	ValidationSuccessRequeueInterval = 1 * time.Hour
	// ValidationFailureRequeueInterval defines how often to retry DES RBAC validation after auth failure
	// Set to 1 minute to detect when permissions are granted
	ValidationFailureRequeueInterval = 1 * time.Minute
	// RBACErrorMessage is the error message shown when the controlling identity lacks Reader permissions
	RBACErrorMessage = "controlling identity does not have Reader role on Disk Encryption Set"
)

type ValidationReconciler struct {
	diskEncryptionSetsAPI     instance.DiskEncryptionSetsAPI
	parsedDiskEncryptionSetID *arm.ResourceID // parsed by options.Validate(), will be nil if DES ID is not set
}

func NewValidationReconciler(
	diskEncryptionSetsAPI instance.DiskEncryptionSetsAPI,
	parsedDiskEncryptionSetID *arm.ResourceID,
) *ValidationReconciler {
	return &ValidationReconciler{
		diskEncryptionSetsAPI:     diskEncryptionSetsAPI,
		parsedDiskEncryptionSetID: parsedDiskEncryptionSetID,
	}
}

func (r *ValidationReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	// Check BYOK RBAC if DES ID is configured
	if r.parsedDiskEncryptionSetID != nil {
		logger.V(1).Info("validating Disk Encryption Set RBAC")
		err := r.validateDiskEncryptionSetRBAC(ctx)
		if err != nil {
			if isAuthorizationError(err) {
				// Auth failure (403/401) - set condition to False, requeue soon to detect permission grants
				logger.V(1).Info("Disk Encryption Set RBAC validation failed - missing permissions", "error", err)
				nodeClass.StatusConditions().SetFalse(
					v1beta1.ConditionTypeValidationSucceeded,
					RBACMissing,
					err.Error(),
				)
				return reconcile.Result{RequeueAfter: ValidationFailureRequeueInterval}, nil
			}
			// Unexpected error (network, parsing, etc.) - don't change condition, return error for retry
			logger.Error(err, "Disk Encryption Set RBAC validation encountered unexpected error")
			return reconcile.Result{}, err
		}
	}

	// All validations passed - requeue to detect permission revocations
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeValidationSucceeded)
	return reconcile.Result{RequeueAfter: ValidationSuccessRequeueInterval}, nil
}

func (r *ValidationReconciler) validateDiskEncryptionSetRBAC(ctx context.Context) error {
	// Attempt to read the DiskEncryptionSet
	// This uses the controller's current credentials (DefaultAzureCredential)
	_, err := r.diskEncryptionSetsAPI.Get(ctx, r.parsedDiskEncryptionSetID.ResourceGroupName, r.parsedDiskEncryptionSetID.Name, nil)
	if err != nil {
		if isAuthorizationError(err) {
			// Wrap the original error to preserve the error chain for isAuthorizationError checks
			return fmt.Errorf(
				"%s '%s'. "+
					"Grant the Reader role on the DiskEncryptionSet to the controlling identity. "+
					"For self-hosted installations, this is the Karpenter workload identity. "+
					"For NAP, this is the AKS cluster identity. "+
					"See https://learn.microsoft.com/azure/aks/azure-disk-customer-managed-keys for details: %w",
				RBACErrorMessage,
				r.parsedDiskEncryptionSetID,
				err,
			)
		}
		return fmt.Errorf("failed to validate DiskEncryptionSet '%s': %w", r.parsedDiskEncryptionSetID, err)
	}

	log.FromContext(ctx).V(1).Info("Disk Encryption Set RBAC validation passed", "desID", r.parsedDiskEncryptionSetID)
	return nil
}

// isAuthorizationError checks if an error is a 401 or 403 authorization error
func isAuthorizationError(err error) bool {
	if err == nil {
		return false
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == http.StatusForbidden ||
			respErr.StatusCode == http.StatusUnauthorized
	}
	return false
}
