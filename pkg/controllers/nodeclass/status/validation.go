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
	"strconv"
	"strings"
	"time"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	DiskEncryptionSetRBACMissing   = "DiskEncryptionSetRBACMissing"
	NetIPv4IPLocalPortRangeInvalid = "NetIPv4IPLocalPortRangeInvalid"
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
)

type ValidationReconciler struct {
	diskEncryptionSetsAPI     azclient.DiskEncryptionSetsAPI
	parsedDiskEncryptionSetID *arm.ResourceID // parsed by options.Validate(), will be nil if DiskEncryptionSetID is not set
}

func NewValidationReconciler(
	diskEncryptionSetsAPI azclient.DiskEncryptionSetsAPI,
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

	// Validate LinuxOSConfig spec fields that can't be expressed as CRD markers or CEL
	if msg := validateNetIPv4IPLocalPortRange(nodeClass); msg != "" {
		nodeClass.StatusConditions().SetFalse(
			v1beta1.ConditionTypeValidationSucceeded,
			NetIPv4IPLocalPortRangeInvalid,
			msg,
		)
		return reconcile.Result{}, nil
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

// validateNetIPv4IPLocalPortRange checks that the port range string has valid numeric bounds.
// CRD pattern validation ensures the format is "first last", but numeric range checks require parsing.
// AKS API enforces: first ∈ [1024, 60999], last ∈ [32768, 65535], first ≤ last.
func validateNetIPv4IPLocalPortRange(nodeClass *v1beta1.AKSNodeClass) string {
	portRange := getNetIPv4IPLocalPortRange(nodeClass)
	if portRange == "" {
		return ""
	}
	parts := strings.Split(portRange, " ")
	if len(parts) != 2 {
		return fmt.Sprintf("netIPv4IPLocalPortRange must be in format 'first last', got: %q", portRange)
	}
	first, err1 := strconv.Atoi(parts[0])
	last, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return fmt.Sprintf("netIPv4IPLocalPortRange contains non-numeric values: %q", portRange)
	}
	return validatePortBounds(first, last)
}

func getNetIPv4IPLocalPortRange(nodeClass *v1beta1.AKSNodeClass) string {
	if nodeClass.Spec.LinuxOSConfig == nil || nodeClass.Spec.LinuxOSConfig.Sysctls == nil ||
		nodeClass.Spec.LinuxOSConfig.Sysctls.NetIPv4IPLocalPortRange == nil {
		return ""
	}
	return *nodeClass.Spec.LinuxOSConfig.Sysctls.NetIPv4IPLocalPortRange
}

func validatePortBounds(first, last int) string {
	if first < 1024 || first > 60999 {
		return fmt.Sprintf("netIPv4IPLocalPortRange first port must be in [1024, 60999], got %d", first)
	}
	if last < 32768 || last > 65535 {
		return fmt.Sprintf("netIPv4IPLocalPortRange last port must be in [32768, 65535], got %d", last)
	}
	if first > last {
		return fmt.Sprintf("netIPv4IPLocalPortRange first port (%d) must be <= last port (%d)", first, last)
	}
	return ""
}
