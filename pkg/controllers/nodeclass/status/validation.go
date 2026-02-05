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
	ValidationFailedReasonDESRBACMissing     = "DESRBACMissing"
	ValidationFailedReasonDESRBACCheckFailed = "DESRBACCheckFailed"
	// DESValidationSuccessRequeueInterval defines how often to re-validate DES RBAC after success
	// Set to 1 hour since RBAC changes are infrequent in production
	DESValidationSuccessRequeueInterval = 1 * time.Hour
	// DESValidationFailureRequeueInterval defines how often to retry DES RBAC validation after auth failure
	// Set to 1 minute to detect when permissions are granted
	DESValidationFailureRequeueInterval = 1 * time.Minute
	// DESRBACErrorMessage is the error message shown when the controlling identity lacks Reader permissions
	DESRBACErrorMessage = "controlling identity does not have Reader role on Disk Encryption Set"
)

type ValidationReconciler struct {
	diskEncryptionSetsAPI instance.DiskEncryptionSetsAPI
	diskEncryptionSetID   *arm.ResourceID // parsed by the constructor for efficient reuse across reconciles
}

func NewValidationReconciler(
	diskEncryptionSetsAPI instance.DiskEncryptionSetsAPI,
	diskEncryptionSetID string,
) *ValidationReconciler {
	var parsedID *arm.ResourceID
	var err error
	if diskEncryptionSetID != "" {
		parsedID, err = arm.ParseResourceID(diskEncryptionSetID)
		if err != nil {
			log.Log.Error(fmt.Errorf("invalid DiskEncryptionSet ID: %w", err), "failed to parse DiskEncryptionSet ID from options", "diskEncryptionSetID", diskEncryptionSetID)
		}
	}
	return &ValidationReconciler{
		diskEncryptionSetsAPI: diskEncryptionSetsAPI,
		diskEncryptionSetID:   parsedID, // Will be nil if DES ID is not set
	}
}

func (r *ValidationReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	// Check BYOK RBAC if DES ID is configured
	if r.diskEncryptionSetID != nil {
		logger.V(1).Info("validating DES RBAC", "diskEncryptionSetID", r.diskEncryptionSetID)
		err := r.validateDiskEncryptionSetRBAC(ctx)
		if err != nil {
			if isAuthorizationError(err) {
				// Auth failure (403/401) - set condition to False, requeue soon to detect permission grants
				logger.V(1).Info("DES RBAC validation failed - missing permissions", "error", err)
				nodeClass.StatusConditions().SetFalse(
					v1beta1.ConditionTypeValidationSucceeded,
					ValidationFailedReasonDESRBACMissing,
					err.Error(),
				)
				return reconcile.Result{RequeueAfter: DESValidationFailureRequeueInterval}, nil
			}
			// Unexpected error (network, parsing, etc.) - don't change condition, return error for retry
			logger.Error(err, "DES RBAC validation encountered unexpected error")
			return reconcile.Result{}, err
		}
	}

	// All validations passed - requeue to detect permission revocations
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeValidationSucceeded)
	return reconcile.Result{RequeueAfter: DESValidationSuccessRequeueInterval}, nil
}

func (r *ValidationReconciler) validateDiskEncryptionSetRBAC(ctx context.Context) error {
	// Attempt to read the DiskEncryptionSet
	// This uses the controller's current credentials (DefaultAzureCredential)
	_, err := r.diskEncryptionSetsAPI.Get(ctx, r.diskEncryptionSetID.ResourceGroupName, r.diskEncryptionSetID.Name, nil)
	if err != nil {
		if isAuthorizationError(err) {
			// Wrap the original error to preserve the error chain for isAuthorizationError checks
			return fmt.Errorf(
				"%s '%s'. "+
					"Grant the Reader role on the DiskEncryptionSet to the controlling identity. "+
					"For self-hosted installations, this is the Karpenter workload identity. "+
					"For NAP, this is the AKS cluster identity. "+
					"See https://learn.microsoft.com/en-us/azure/aks/azure-disk-customer-managed-keys for details: %w",
				DESRBACErrorMessage,
				r.diskEncryptionSetID,
				err,
			)
		}
		return fmt.Errorf("failed to validate DiskEncryptionSet '%s': %w", r.diskEncryptionSetID, err)
	}

	log.FromContext(ctx).V(1).Info("DES RBAC validation passed", "desID", r.diskEncryptionSetID)
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
