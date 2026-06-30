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
	"fmt"
	"time"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	DiskEncryptionSetRBACMissing = "DiskEncryptionSetRBACMissing"
	// TODO: May want to rethink how we handle successful validation + potential for RBAC removal.
	// See this PR comment for considerations:
	// https://github.com/Azure/karpenter-provider-azure/pull/1372#discussion_r2795367386
	// ValidationSuccessRequeueInterval defines how often to re-validate DES RBAC after success
	// Set to 1 hour since RBAC changes are infrequent in production
	ValidationSuccessRequeueInterval = 1 * time.Hour
	// ValidationFailureRequeueInterval defines how often to retry DES RBAC validation after auth failure
	// Set to 1 minute to detect when permissions are granted without creating a high system load
	ValidationFailureRequeueInterval = 1 * time.Minute
	// DiskEncryptionSetRBACErrorMessage is the error message shown when the controlling identity lacks Reader permissions
	DiskEncryptionSetRBACErrorMessage = "controlling identity does not have Reader role on Disk Encryption Set"
	// KataPodSandboxingDisabled is the condition reason set when a NodeClass requests a Kata
	// workloadRuntime but the Kata Pod Sandboxing feature is disabled on this controller.
	KataPodSandboxingDisabled = "KataPodSandboxingDisabled"
	// KataPodSandboxingUnsupportedProvisionMode is the condition reason set when a NodeClass requests a
	// Kata workloadRuntime but the provision mode cannot provision the Kata host stack (only AKS Machine
	// API modes can).
	KataPodSandboxingUnsupportedProvisionMode = "KataPodSandboxingUnsupportedProvisionMode"
)

type ValidationReconciler struct {
	diskEncryptionSetsAPI     azapi.DiskEncryptionSetsAPI
	parsedDiskEncryptionSetID *arm.ResourceID // parsed by options.Validate(), will be nil if DiskEncryptionSetID is not set
}

func NewValidationReconciler(
	diskEncryptionSetsAPI azapi.DiskEncryptionSetsAPI,
	parsedDiskEncryptionSetID *arm.ResourceID,
) *ValidationReconciler {
	return &ValidationReconciler{
		diskEncryptionSetsAPI:     diskEncryptionSetsAPI,
		parsedDiskEncryptionSetID: parsedDiskEncryptionSetID,
	}
}

func (r *ValidationReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	// A NodeClass requesting a Kata (Pod Sandboxing) workloadRuntime can only provision when the
	// feature is enabled AND the provision mode is an AKS Machine API mode (the only path wired for
	// Kata). Surface either gap as a validation failure so the user gets fast feedback on the NodeClass
	// (and Karpenter core won't create doomed NodeClaims) instead of silently-pending pods and churning
	// launch failures. The provisioning paths (buildAKSMachineTemplate, imagefamily resolver) keep
	// their own guards as defense-in-depth.
	if nodeClass.IsKataEnabled() {
		opts := options.FromContext(ctx)
		if !opts.KataPodSandboxingEnabled() {
			nodeClass.StatusConditions().SetFalse(
				v1beta1.ConditionTypeValidationSucceeded,
				KataPodSandboxingDisabled,
				fmt.Sprintf("workloadRuntime %q requires the Kata Pod Sandboxing feature to be enabled (ENABLE_KATA_POD_SANDBOXING=true)", nodeClass.GetWorkloadRuntime()),
			)
			return reconcile.Result{}, nil
		}
		if !opts.IsAKSMachineAPIMode() {
			nodeClass.StatusConditions().SetFalse(
				v1beta1.ConditionTypeValidationSucceeded,
				KataPodSandboxingUnsupportedProvisionMode,
				fmt.Sprintf("workloadRuntime %q requires an AKS Machine API provision mode", nodeClass.GetWorkloadRuntime()),
			)
			return reconcile.Result{}, nil
		}
	}

	// Check BYOK RBAC if DES ID is configured
	if r.parsedDiskEncryptionSetID != nil {
		logger.V(1).Info("validating Disk Encryption Set RBAC")
		err := r.validateDiskEncryptionSetRBAC(ctx)
		if err != nil {
			if sdkerrors.IsAuthorizationErr(err) {
				// Auth failure (403/401) - set condition to False, requeue soon to detect permission grants
				logger.V(1).Info("Disk Encryption Set RBAC validation failed - missing permissions", "error", err)
				nodeClass.StatusConditions().SetFalse(
					v1beta1.ConditionTypeValidationSucceeded,
					DiskEncryptionSetRBACMissing,
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
		if sdkerrors.IsAuthorizationErr(err) {
			// Wrap the original error to preserve the error chain for isAuthorizationErr checks
			return fmt.Errorf(
				"%s '%s'. "+
					"Grant the Reader role on the DiskEncryptionSet to the controlling identity. "+
					"For self-hosted installations, this is the Karpenter workload identity. "+
					"For NAP, this is the AKS cluster identity. "+
					"See https://learn.microsoft.com/azure/aks/azure-disk-customer-managed-keys for details: %w",
				DiskEncryptionSetRBACErrorMessage,
				r.parsedDiskEncryptionSetID,
				err,
			)
		}
		return fmt.Errorf("failed to validate DiskEncryptionSet '%s': %w", r.parsedDiskEncryptionSetID, err)
	}

	log.FromContext(ctx).V(1).Info("Disk Encryption Set RBAC validation passed", "desID", r.parsedDiskEncryptionSetID)
	return nil
}
