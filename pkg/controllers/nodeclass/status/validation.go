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
	"time"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	DiskEncryptionSetRBACMissing = "DiskEncryptionSetRBACMissing"
	// ImageFamilyKubernetesVersionIncompatible is the stable reason used when
	// spec.imageFamily explicitly pins an OS version that the Kubernetes version does not support.
	ImageFamilyKubernetesVersionIncompatible = "ImageFamilyKubernetesVersionIncompatible"
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
	// KataPodSandboxingUnsupportedProvisionMode is the condition reason set when a NodeClass requests a
	// Kata workloadRuntime but the provision mode cannot provision the Kata host stack.
	KataPodSandboxingUnsupportedProvisionMode = "KataPodSandboxingUnsupportedProvisionMode"
	// KataRequiresAzureLinux3 is the condition reason set when the Kubernetes version resolves
	// imageFamily AzureLinux to Azure Linux 2, which does not publish a Kata image.
	KataRequiresAzureLinux3 = "KataRequiresAzureLinux3"
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

	rules := []struct {
		rule string
		fn   validationRule
	}{
		{
			rule: "Kata eligibility",
			fn:   validateKata,
		},
		{
			rule: "Node image family compatibility",
			fn:   validateImageFamilyCompatibility,
		},
		{
			rule: "Disk Encryption Set RBAC",
			fn:   r.validateDiskEncryptionSetRBAC,
		},
	}

	for _, rule := range rules {
		logger.V(1).Info("validating rule", "rule", rule.rule)

		validationFailed, err := rule.fn(ctx, nodeClass)
		if err != nil {
			if requiresRequeue, reason := errorRequiresRequeue(err); requiresRequeue {
				message := errorRequeueMessages[reason]
				logger.V(1).Info(
					"validation failed and requires requeue",
					"reason", reason,
					"message", message,
					"error", err)

				nodeClass.StatusConditions().SetFalse(
					v1beta1.ConditionTypeValidationSucceeded,
					reason,
					err.Error(),
				)

				return reconcile.Result{
					RequeueAfter: ValidationFailureRequeueInterval,
				}, nil
			}

			// Unexpected error (network, parsing, etc.) - don't change condition, return error for retry
			logger.Error(err, "validation encountered unexpected error", "rule", rule.rule)
			return reconcile.Result{}, err
		}

		if validationFailed {
			return reconcile.Result{}, nil
		}
	}

	// All validations passed - requeue to detect permission revocations
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeValidationSucceeded)
	return reconcile.Result{RequeueAfter: ValidationSuccessRequeueInterval}, nil
}

// errorRequiresRequeue determines if an error should trigger a requeue.
// Returns a boolean indicating if the error should trigger a requeue and a reason code why
func errorRequiresRequeue(err error) (bool, string) {
	if sdkerrors.IsAuthorizationErr(err) {
		return true, DiskEncryptionSetRBACMissing
	}

	return false, ""
}

var errorRequeueMessages = map[string]string{
	DiskEncryptionSetRBACMissing: "Disk Encryption Set RBAC validation failed - missing permissions",
}

// validationRule signifies a function that performs a validation check on a NodeClass.
// Returns a boolean indicating if the validation failed and an error if one occurred.
type validationRule func(context.Context, *v1beta1.AKSNodeClass) (bool, error)

func validateKata(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (bool, error) {
	// A NodeClass requesting a Kata (Pod Sandboxing) workloadRuntime can only provision on a provision
	// mode that can express the workload runtime. Surface the gap as a validation failure so the user
	// gets fast feedback on the NodeClass (and Karpenter core won't create doomed NodeClaims) instead of
	// silently-pending pods and churning launch failures. The provisioning paths keep their own guards
	// as defense-in-depth.
	if nodeClass.IsKataEnabled() && !options.FromContext(ctx).SupportsWorkloadRuntime() {
		nodeClass.StatusConditions().SetFalse(
			v1beta1.ConditionTypeValidationSucceeded,
			KataPodSandboxingUnsupportedProvisionMode,
			fmt.Sprintf("workloadRuntime %q is not supported with provision-mode %q", nodeClass.GetWorkloadRuntime(), options.FromContext(ctx).ProvisionMode),
		)
		return true, nil
	}
	if nodeClass.IsKataEnabled() && lo.FromPtr(nodeClass.Spec.ImageFamily) == v1beta1.AzureLinuxImageFamily {
		kubernetesVersion, err := nodeClass.GetKubernetesVersion()
		if err != nil {
			return false, fmt.Errorf("getting kubernetes version, %w", err)
		}
		if !imagefamily.UseAzureLinux3(kubernetesVersion) {
			nodeClass.StatusConditions().SetFalse(
				v1beta1.ConditionTypeValidationSucceeded,
				KataRequiresAzureLinux3,
				fmt.Sprintf("workloadRuntime KataVmIsolation requires Azure Linux 3 and Kubernetes 1.32 or newer; Kubernetes version %s resolves imageFamily AzureLinux to Azure Linux 2", kubernetesVersion),
			)
			return true, nil
		}
	}
	return false, nil
}

func validateImageFamilyCompatibility(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (bool, error) {
	if err := imagefamily.ValidateImageFamilyCompatibility(nodeClass); err != nil {
		var incompatibleErr *imagefamily.ImageFamilyKubernetesVersionIncompatibleError
		if errors.As(err, &incompatibleErr) {
			// This incompatibility is static: it can only change when the NodeClass spec or
			// the discovered Kubernetes version changes, and both of those already trigger a
			// reconcile. Polling would burn reconciles without ever observing a difference.
			log.FromContext(ctx).V(1).Info("image family compatibility validation failed", "error", err)
			nodeClass.StatusConditions().SetFalse(
				v1beta1.ConditionTypeValidationSucceeded,
				ImageFamilyKubernetesVersionIncompatible,
				err.Error(),
			)
			return true, nil
		}

		// Any other error is unexpected; leave the condition alone and let controller-runtime retry.
		log.FromContext(ctx).Error(err, "image family compatibility validation encountered unexpected error")
		return false, fmt.Errorf("validating image family compatibility: %w", err)
	}

	return false, nil
}

func (r *ValidationReconciler) validateDiskEncryptionSetRBAC(ctx context.Context, _ *v1beta1.AKSNodeClass) (bool, error) {
	// Attempt to read the DiskEncryptionSet
	// This uses the controller's current credentials (DefaultAzureCredential)
	if r.parsedDiskEncryptionSetID != nil {
		_, err := r.diskEncryptionSetsAPI.Get(ctx, r.parsedDiskEncryptionSetID.ResourceGroupName, r.parsedDiskEncryptionSetID.Name, nil)
		if err != nil {
			if sdkerrors.IsAuthorizationErr(err) {
				// Wrap the original error to preserve the error chain for isAuthorizationErr checks
				return true, fmt.Errorf(
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

			return false, fmt.Errorf("failed to validate DiskEncryptionSet '%s': %w", r.parsedDiskEncryptionSetID, err)
		}

		log.FromContext(ctx).V(1).Info("Disk Encryption Set RBAC validation passed", "desID", r.parsedDiskEncryptionSetID)
	}

	return false, nil
}
