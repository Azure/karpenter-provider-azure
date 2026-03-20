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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	MachineAPIDryRunValidationFailed = "MachineAPIDryRunValidationFailed"
	// DryRunValidationSuccessRequeueInterval defines how often to re-validate via dry-run after success.
	// Set to 1 hour since RP validation rules change infrequently (release-gated).
	DryRunValidationSuccessRequeueInterval = 1 * time.Hour
	// DryRunValidationFailureRequeueInterval defines how often to retry after a validation failure.
	// Set to 5 minutes — validation failures are deterministic (same input, same result) so
	// frequent retries waste API calls. Users need time to fix their CRD config anyway.
	DryRunValidationFailureRequeueInterval = 5 * time.Minute
)

// MachineAPIDryRunReconciler validates AKSNodeClass configuration against AKS RP's
// real Machine API validator by sending a dry-run PUT request. This catches validation
// drift between CRD-level rules and RP-level rules without maintaining parallel logic.
//
// When the dry-run endpoint is not available (RP hasn't deployed it yet), the reconciler
// gracefully degrades: non-4xx errors are treated as transient and retried normally.
//
// TODO: Wire into Controller by adding to the reconciler chain in controller.go.
// Requires threading AZClient.AKSMachinesDryRunClient(), clusterResourceGroup,
// clusterName, and machinePoolName from the operator startup through NewControllers
// and NewController.
type MachineAPIDryRunReconciler struct {
	dryRunClient      azclient.AKSMachinesAPI
	clusterResourceGroup string
	clusterName          string
	machinePoolName      string // the Karpenter-managed machine pool name
}

func NewMachineAPIDryRunReconciler(
	dryRunClient azclient.AKSMachinesAPI,
	clusterResourceGroup string,
	clusterName string,
	machinePoolName string,
) *MachineAPIDryRunReconciler {
	return &MachineAPIDryRunReconciler{
		dryRunClient:         dryRunClient,
		clusterResourceGroup: clusterResourceGroup,
		clusterName:          clusterName,
		machinePoolName:      machinePoolName,
	}
}

func (r *MachineAPIDryRunReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	// Skip if no dry-run client is available (non-Machine-API provision modes)
	if r.dryRunClient == nil {
		return reconcile.Result{RequeueAfter: DryRunValidationSuccessRequeueInterval}, nil
	}

	// Build a template Machine from the AKSNodeClass for validation.
	// This is a minimal template — it validates the fields that come from AKSNodeClass
	// (OSDiskSizeGB, kubelet config, FIPS, etc.) without requiring a real NodeClaim.
	templateMachine := r.buildTemplateMachine(nodeClass)

	logger.V(1).Info("running Machine API dry-run validation")

	// Send the dry-run PUT. The dryRunPolicy on this client appends ?dryRun=true,
	// causing RP to run full validation without creating any resources.
	_, err := r.dryRunClient.BeginCreateOrUpdate(
		ctx,
		r.clusterResourceGroup,
		r.clusterName,
		r.machinePoolName,
		"karpenter-dryrun-validation", // synthetic machine name — never persisted
		templateMachine,
		nil,
	)
	if err != nil {
		// Check if this is a validation error (4xx)
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode >= 400 && respErr.StatusCode < 500 {
			// Validation failure — deterministic, user needs to fix their config
			logger.V(1).Info("Machine API dry-run validation failed", "error", respErr.Error())
			nodeClass.StatusConditions().SetFalse(
				v1beta1.ConditionTypeValidationSucceeded,
				MachineAPIDryRunValidationFailed,
				fmt.Sprintf("Machine API validation failed: %s", respErr.Error()),
			)
			return reconcile.Result{RequeueAfter: DryRunValidationFailureRequeueInterval}, nil
		}

		// Non-validation error (5xx, network, RP not deployed, etc.) — transient, retry normally.
		// Don't change the condition — the previous result (from DES RBAC check or prior dry-run) stays.
		logger.V(1).Info("Machine API dry-run encountered transient error, will retry", "error", err)
		return reconcile.Result{}, err
	}

	// Dry-run validation passed
	logger.V(1).Info("Machine API dry-run validation passed")
	return reconcile.Result{RequeueAfter: DryRunValidationSuccessRequeueInterval}, nil
}

// buildTemplateMachine constructs a minimal Machine from AKSNodeClass fields
// for dry-run validation. Only fields controlled by AKSNodeClass are included —
// fields from NodeClaim (labels, taints, instance type) are left empty or defaulted,
// as those are validated per-creation in Layer 3 (error surfacing).
func (r *MachineAPIDryRunReconciler) buildTemplateMachine(nodeClass *v1beta1.AKSNodeClass) armcontainerservice.Machine {
	machine := armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: lo.ToPtr("Standard_DS2_v2"), // placeholder — dry-run validates structure, not availability
			},
			OperatingSystem: &armcontainerservice.MachineOSProfile{
				OSDiskSizeGB: nodeClass.Spec.OSDiskSizeGB,
				OSType:       lo.ToPtr(armcontainerservice.OSTypeLinux),
			},
			Network: &armcontainerservice.MachineNetworkProperties{
				VnetSubnetID: nodeClass.Spec.VNETSubnetID,
			},
			Kubernetes: &armcontainerservice.MachineKubernetesProfile{
				KubeletConfig: buildKubeletConfigForDryRun(nodeClass),
			},
		},
	}

	// Set FIPS if enabled
	if nodeClass.Spec.FIPSMode != nil && *nodeClass.Spec.FIPSMode == v1beta1.FIPSModeFIPS {
		machine.Properties.OperatingSystem.EnableFIPS = lo.ToPtr(true)
	}

	return machine
}

// buildKubeletConfigForDryRun converts AKSNodeClass KubeletConfig to the Machine API format.
// Only includes fields that are validated by RP — this ensures CRD-level kubelet config
// is checked against RP's real validator.
func buildKubeletConfigForDryRun(nodeClass *v1beta1.AKSNodeClass) *armcontainerservice.KubeletConfig {
	if nodeClass.Spec.Kubelet == nil {
		return nil
	}
	k := nodeClass.Spec.Kubelet
	config := &armcontainerservice.KubeletConfig{}

	if k.CPUCFSQuotaPeriod.Duration != 0 {
		periodStr := k.CPUCFSQuotaPeriod.Duration.String()
		config.CPUCfsQuotaPeriod = &periodStr
	}
	if k.ImageGCHighThresholdPercent != nil {
		config.ImageGcHighThreshold = k.ImageGCHighThresholdPercent
	}
	if k.ImageGCLowThresholdPercent != nil {
		config.ImageGcLowThreshold = k.ImageGCLowThresholdPercent
	}
	if k.AllowedUnsafeSysctls != nil {
		config.AllowedUnsafeSysctls = lo.ToSlicePtr(k.AllowedUnsafeSysctls)
	}

	return config
}
