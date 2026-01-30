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
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ValidationFailedReasonDESRBACMissing     = "DESRBACMissing"
	ValidationFailedReasonDESRBACCheckFailed = "DESRBACCheckFailed"
	// DESValidationSuccessCacheTTL defines how long to cache successful DES RBAC validations
	// After this duration, validation will be re-run to detect permission revocations
	// Set to 1 hour since RBAC changes are infrequent in production
	DESValidationSuccessCacheTTL = 1 * time.Hour
	// DESRBACErrorMessage is the error message shown when the controlling identity lacks Reader permissions
	DESRBACErrorMessage = "controlling identity does not have Reader role on Disk Encryption Set"
)

type ValidationReconciler struct {
	kubeClient            client.Client
	diskEncryptionSetsAPI instance.DiskEncryptionSetsAPI
	diskEncryptionSetID   string

	// validatedSuccessfully tracks whether DES validation has passed
	// Once set to true, we use the cache TTL to avoid redundant checks
	validatedSuccessfully bool
	lastValidationTime    time.Time
	validationMu          sync.RWMutex
}

func NewValidationReconciler(
	kubeClient client.Client,
	diskEncryptionSetsAPI instance.DiskEncryptionSetsAPI,
	opts *options.Options,
) *ValidationReconciler {
	return &ValidationReconciler{
		kubeClient:            kubeClient,
		diskEncryptionSetsAPI: diskEncryptionSetsAPI,
		diskEncryptionSetID:   opts.DiskEncryptionSetID,
	}
}

// ClearValidationCache clears the DES validation cache.
// This is primarily intended for testing scenarios where permissions may be revoked
// and you need to force immediate re-validation.
func (r *ValidationReconciler) ClearValidationCache() {
	r.validationMu.Lock()
	r.validatedSuccessfully = false
	r.lastValidationTime = time.Time{}
	r.validationMu.Unlock()
}

func (r *ValidationReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	// Check BYOK RBAC if DES ID is configured
	if r.diskEncryptionSetID != "" {
		logger.V(1).Info("validating DES RBAC", "diskEncryptionSetID", r.diskEncryptionSetID)
		if err := r.validateDiskEncryptionSetRBAC(ctx); err != nil {
			// Validation failed - set condition to False and return success so status is persisted
			logger.V(1).Info("DES RBAC validation failed", "error", err)
			nodeClass.StatusConditions().SetFalse(
				v1beta1.ConditionTypeValidationSucceeded,
				ValidationFailedReasonDESRBACMissing,
				err.Error(),
			)
			// Return success (not error) to allow status update to persist
			return reconcile.Result{}, nil
		}
	}

	// All validations passed
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeValidationSucceeded)
	return reconcile.Result{}, nil
}

func (r *ValidationReconciler) validateDiskEncryptionSetRBAC(ctx context.Context) error {
	// Check if we've already validated successfully and cache is still valid
	// If validation has succeeded before and cache hasn't expired, skip revalidation
	r.validationMu.RLock()
	if r.validatedSuccessfully && time.Since(r.lastValidationTime) < DESValidationSuccessCacheTTL {
		r.validationMu.RUnlock()
		return nil
	}
	r.validationMu.RUnlock()

	// Parse the DES resource ID
	parsedID, err := arm.ParseResourceID(r.diskEncryptionSetID)
	if err != nil {
		return fmt.Errorf("invalid DiskEncryptionSet ID: %w", err)
	}

	// Attempt to read the DiskEncryptionSet
	// This uses the controller's current credentials (DefaultAzureCredential)
	_, err = r.diskEncryptionSetsAPI.Get(ctx, parsedID.ResourceGroupName, parsedID.Name, nil)
	if err != nil {
		// Check if it's an authorization error
		if isAuthorizationError(err) {
			// Mark validation as failed - will retry on every reconcile
			r.validationMu.Lock()
			r.validatedSuccessfully = false
			r.validationMu.Unlock()
			return fmt.Errorf(
				"%s '%s'. "+
					"Grant the Reader role on the DiskEncryptionSet to the controlling identity. "+
					"For self-hosted installations, this is the Karpenter workload identity. "+
					"For managed/AKS-hosted installations, this is the AKS cluster control plane identity. "+
					"See https://learn.microsoft.com/en-us/azure/aks/azure-disk-customer-managed-keys for details",
				DESRBACErrorMessage,
				r.diskEncryptionSetID,
			)
		}
		// Other errors (not found, network error, etc.)
		// Don't cache failures - retry on next reconcile
		return fmt.Errorf("failed to validate DiskEncryptionSet '%s': %w", r.diskEncryptionSetID, err)
	}

	// Success - cache the result with current timestamp
	// This avoids redundant API calls for subsequent reconciles
	r.validationMu.Lock()
	r.validatedSuccessfully = true
	r.lastValidationTime = time.Now()
	r.validationMu.Unlock()

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
