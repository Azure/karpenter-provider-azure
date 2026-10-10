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
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	DiskEncryptionSetRBACMissing      = "DiskEncryptionSetRBACMissing"
	FIPSRequired                      = "FIPSRequired"
	IncompatibleProvisionMode         = "IncompatibleProvisionMode"
	SIGRequiredForAzureContainerLinux = "SIGRequiredForAzureContainerLinux"
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
	// ManagedGPUUnsupportedProvisionMode is the condition reason set when a NodeClass requests
	// the managed GPU experience but the provision mode cannot express the NVIDIA GPU profile.
	ManagedGPUUnsupportedProvisionMode = "ManagedGPUUnsupportedProvisionMode"
	// WindowsUnsupportedNetworkDataplane is the condition reason set when a Windows NodeClass is
	// configured on a cluster that uses an unsupported network dataplane.
	WindowsUnsupportedNetworkDataplane = "WindowsUnsupportedNetworkDataplane"
	// WindowsUnsupportedProvisionMode is the condition reason set when a Windows NodeClass is
	// configured on a cluster that does not provision through the AKS Machine API.
	WindowsUnsupportedProvisionMode = "WindowsUnsupportedProvisionMode"
)

type StaticValidationReconciler struct {
	diskEncryptionSetsAPI     azapi.DiskEncryptionSetsAPI
	parsedDiskEncryptionSetID *arm.ResourceID // parsed by options.Validate(), will be nil if DiskEncryptionSetID is not set
}

type validator func(context.Context, *v1beta1.AKSNodeClass) (validationResult, error)

type validationResult struct {
	passed       bool
	reason       string
	message      string
	requeueAfter time.Duration
}

func pass() validationResult {
	return validationResult{passed: true}
}

func fail(reason string, message string) validationResult {
	return validationResult{
		passed:  false,
		reason:  reason,
		message: message,
	}
}

func retryableFail(reason string, message string, requeueAfter time.Duration) validationResult {
	return validationResult{
		passed:       false,
		reason:       reason,
		message:      message,
		requeueAfter: requeueAfter,
	}
}

func NewStaticValidationReconciler(
	diskEncryptionSetsAPI azapi.DiskEncryptionSetsAPI,
	parsedDiskEncryptionSetID *arm.ResourceID,
) *StaticValidationReconciler {
	return &StaticValidationReconciler{
		diskEncryptionSetsAPI:     diskEncryptionSetsAPI,
		parsedDiskEncryptionSetID: parsedDiskEncryptionSetID,
	}
}

// Reconcile performs static validation checks on the given AKSNodeClass and updates its status accordingly.
// Complex checks or checks that are dependent on external systems (e.g. calling Azure) should be performed in their own status controller.
func (r *StaticValidationReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	validators := []validator{
		r.validateFIPS,
		r.validateACLConfiguration,
		r.validateWindowsCompatibility,
		r.validateKataProvisionMode,
		r.validateManagedGPUProvisionMode,
		r.validateKataImageFamily,
		r.validateDiskEncryptionSetRBAC,
	}

	for _, validate := range validators {
		result, err := validate(ctx, nodeClass)
		if err != nil {
			return reconcile.Result{}, err
		}
		if !result.passed {
			nodeClass.StatusConditions().SetFalse(
				v1beta1.ConditionTypeValidationSucceeded,
				result.reason,
				result.message,
			)
			return reconcile.Result{RequeueAfter: result.requeueAfter}, nil
		}
	}

	// All validations passed - requeue to detect permission revocations
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeValidationSucceeded)
	return reconcile.Result{RequeueAfter: ValidationSuccessRequeueInterval}, nil
}

func (r *StaticValidationReconciler) validateACLConfiguration(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (validationResult, error) {
	if reason := incompatibleACLConfiguration(ctx, nodeClass); reason != "" {
		return fail(
			reason,
			"AzureContainerLinux requires an AKS Machine API provision mode and shared image gallery access (UseSIG=true)",
		), nil
	}
	return pass(), nil
}

func incompatibleACLConfiguration(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) string {
	if lo.FromPtr(nodeClass.Spec.ImageFamily) != v1beta1.AzureContainerLinuxImageFamily {
		return ""
	}
	opts := options.FromContext(ctx)
	if !opts.IsAKSMachineAPIMode() {
		return IncompatibleProvisionMode
	}
	if !opts.UseSIG {
		return SIGRequiredForAzureContainerLinux
	}
	return ""
}

func (r *StaticValidationReconciler) validateFIPS(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (validationResult, error) {
	if options.FromContext(ctx).EnableFIPS && lo.FromPtr(nodeClass.Spec.FIPSMode) != v1beta1.FIPSModeFIPS {
		return fail(
			FIPSRequired,
			"AKSNodeClass spec.fipsMode must be set to FIPS because FIPS is enabled at the cluster level",
		), nil
	}
	return pass(), nil
}

func (r *StaticValidationReconciler) validateWindowsCompatibility(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (validationResult, error) {
	imageFamily := lo.FromPtr(nodeClass.Spec.ImageFamily)
	if !v1beta1.IsWindowsImageFamily(imageFamily) {
		return pass(), nil
	}
	providerOptions := options.FromContext(ctx)
	if providerOptions.NetworkDataplane == consts.NetworkDataplaneCilium {
		return fail(
			WindowsUnsupportedNetworkDataplane,
			fmt.Sprintf("imageFamily %q is not supported with network-dataplane %q", imageFamily, providerOptions.NetworkDataplane),
		), nil
	}
	if !providerOptions.IsAKSMachineAPIMode() {
		return fail(
			WindowsUnsupportedProvisionMode,
			fmt.Sprintf("imageFamily %q is not supported with provision-mode %q; Windows requires an AKS Machine API provision mode", imageFamily, providerOptions.ProvisionMode),
		), nil
	}
	return pass(), nil
}

func (r *StaticValidationReconciler) validateKataProvisionMode(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (validationResult, error) {
	// A NodeClass requesting a Kata (Pod Sandboxing) workloadRuntime can only provision on a provision
	// mode that can express the workload runtime. Surface the gap as a validation failure so the user
	// gets fast feedback on the NodeClass (and Karpenter core won't create doomed NodeClaims) instead of
	// silently-pending pods and churning launch failures. The provisioning paths keep their own guards
	// as defense-in-depth.
	if nodeClass.IsKataEnabled() && !options.FromContext(ctx).SupportsWorkloadRuntime() {
		return fail(
			KataPodSandboxingUnsupportedProvisionMode,
			fmt.Sprintf("workloadRuntime %q is not supported with provision-mode %q", nodeClass.GetWorkloadRuntime(), options.FromContext(ctx).ProvisionMode),
		), nil
	}
	return pass(), nil
}

func (r *StaticValidationReconciler) validateManagedGPUProvisionMode(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (validationResult, error) {
	if nodeClass.IsManagedGPUEnabled() && !options.FromContext(ctx).IsAKSMachineAPIMode() {
		return fail(
			ManagedGPUUnsupportedProvisionMode,
			fmt.Sprintf("gpu.nvidia.managementMode %q requires an AKS Machine API provision mode; provision-mode %q is not supported", nodeClass.GetManagementMode(), options.FromContext(ctx).ProvisionMode),
		), nil
	}
	return pass(), nil
}

func (r *StaticValidationReconciler) validateKataImageFamily(_ context.Context, nodeClass *v1beta1.AKSNodeClass) (validationResult, error) {
	if nodeClass.IsKataEnabled() && lo.FromPtr(nodeClass.Spec.ImageFamily) == v1beta1.AzureLinuxImageFamily {
		kubernetesVersion, err := nodeClass.GetKubernetesVersion()
		if err != nil {
			return validationResult{}, fmt.Errorf("getting kubernetes version, %w", err)
		}
		if !imagefamily.UseAzureLinux3(kubernetesVersion) {
			return fail(
				KataRequiresAzureLinux3,
				fmt.Sprintf("workloadRuntime KataVmIsolation requires Azure Linux 3 and Kubernetes 1.32 or newer; Kubernetes version %s resolves imageFamily AzureLinux to Azure Linux 2", kubernetesVersion),
			), nil
		}
	}
	return pass(), nil
}

// TODO: Possibly this should move out because it is not really "static" validation
func (r *StaticValidationReconciler) validateDiskEncryptionSetRBAC(ctx context.Context, _ *v1beta1.AKSNodeClass) (validationResult, error) {
	if r.parsedDiskEncryptionSetID == nil {
		return pass(), nil
	}

	logger := log.FromContext(ctx)
	logger.V(1).Info("validating Disk Encryption Set RBAC")
	// Attempt to read the DiskEncryptionSet
	// This uses the controller's current credentials (DefaultAzureCredential)
	_, err := r.diskEncryptionSetsAPI.Get(ctx, r.parsedDiskEncryptionSetID.ResourceGroupName, r.parsedDiskEncryptionSetID.Name, nil)
	if err != nil {
		if sdkerrors.IsAuthorizationErr(err) {
			// Auth failure (403/401) - set condition to False, requeue soon to detect permission grants
			err = fmt.Errorf(
				"%s '%s'. "+
					"Grant the Reader role on the DiskEncryptionSet to the controlling identity. "+
					"For self-hosted installations, this is the Karpenter workload identity. "+
					"For NAP, this is the AKS cluster identity. "+
					"See https://learn.microsoft.com/azure/aks/azure-disk-customer-managed-keys for details: %w",
				DiskEncryptionSetRBACErrorMessage,
				r.parsedDiskEncryptionSetID,
				err,
			)
			logger.V(1).Info("Disk Encryption Set RBAC validation failed - missing permissions", "error", err)
			return retryableFail(
				DiskEncryptionSetRBACMissing,
				err.Error(),
				ValidationFailureRequeueInterval,
			), nil
		}
		// Unexpected error (network, parsing, etc.) - don't change condition, return error for retry
		err = fmt.Errorf("failed to validate DiskEncryptionSet '%s': %w", r.parsedDiskEncryptionSetID, err)
		logger.Error(err, "Disk Encryption Set RBAC validation encountered unexpected error")
		return validationResult{}, err
	}

	logger.V(1).Info("Disk Encryption Set RBAC validation passed", "desID", r.parsedDiskEncryptionSetID)
	return pass(), nil
}
