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
	"context"
	"strings"
	"time"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	azureclouds "sigs.k8s.io/cloud-provider-azure/pkg/azclient"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	labelspkg "github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

var (
	KarpCapacityTypeToVMPriority = map[string]armcompute.VirtualMachinePriorityTypes{
		karpv1.CapacityTypeSpot:     armcompute.VirtualMachinePriorityTypesSpot,
		karpv1.CapacityTypeOnDemand: armcompute.VirtualMachinePriorityTypesRegular,
	}
	VMPriorityToKarpCapacityType = map[armcompute.VirtualMachinePriorityTypes]string{
		armcompute.VirtualMachinePriorityTypesSpot:    karpv1.CapacityTypeSpot,
		armcompute.VirtualMachinePriorityTypesRegular: karpv1.CapacityTypeOnDemand,
	}
	// Note that there is no ScaleSetPriorityToKarpCapacityType because the karpenter.sh/capacity-type
	// label is the "official" label that we actually key priority off of. Selection still works though
	// because when we list instance types on-demand offerings always have v1beta1.ScaleSetPriorityRegular
	// and spot instances always have v1beta1.ScaleSetPrioritySpot, so the correct karpenter.sh/capacity-type
	// label is still selected even if the user is using kubernetes.azure.com/scalesetpriority only on the NodePool.
	VMPriorityToScaleSetPriority = map[armcompute.VirtualMachinePriorityTypes]string{
		armcompute.VirtualMachinePriorityTypesSpot:    v1beta1.ScaleSetPrioritySpot,
		armcompute.VirtualMachinePriorityTypesRegular: v1beta1.ScaleSetPriorityRegular,
	}

	aksIdentifyingExtensionEnvs = sets.New(
		azureclouds.PublicCloud.Name,
		azureclouds.ChinaCloud.Name,
		azureclouds.USGovernmentCloud.Name,
	)
)

const (
	aksIdentifyingExtensionName = "computeAksLinuxBilling"
	// TODO: Why bother with a different CSE name for Windows?
	cseNameWindows = "windows-cse-agent-karpenter"
	cseNameLinux   = "cse-agent-karpenter"
)

// ErrorCodeForMetrics extracts a stable Azure error code for metric labeling when possible.
func ErrorCodeForMetrics(err error) string {
	if err == nil {
		return "UnknownError"
	}
	if azErr := sdkerrors.IsResponseError(err); azErr != nil {
		if azErr.ErrorCode != "" {
			return azErr.ErrorCode
		}
		return "UnknownError"
	}
	return "UnknownError"
}

// GetManagedExtensionNames gets the names of the VM extensions managed by Karpenter.
// This is a set of 1 or 2 extensions (depending on provisionMode): aksIdentifyingExtension and (sometimes) cse.
// In AzureVM mode, no extensions are managed.
func GetManagedExtensionNames(provisionMode string, env *auth.Environment) []string {
	if provisionMode == consts.ProvisionModeAzureVM {
		return nil
	}
	var result []string
	// Only including AKS identifying extension in the clouds it is supported in
	if isAKSIdentifyingExtensionEnabled(env) {
		result = append(result, aksIdentifyingExtensionName)
	}
	if provisionMode == consts.ProvisionModeBootstrappingClient {
		result = append(result, cseNameLinux) // TODO: Windows
	}
	return result
}

func isAKSIdentifyingExtensionEnabled(env *auth.Environment) bool {
	return aksIdentifyingExtensionEnvs.Has(env.Environment.Name)
}

func ConvertToVirtualMachineIdentity(nodeIdentities []string) *armcompute.VirtualMachineIdentity {
	var identity *armcompute.VirtualMachineIdentity
	if len(nodeIdentities) > 0 {
		identityMap := make(map[string]*armcompute.UserAssignedIdentitiesValue)
		for _, identityID := range nodeIdentities {
			identityMap[identityID] = &armcompute.UserAssignedIdentitiesValue{}
		}

		if len(identityMap) > 0 {
			identity = &armcompute.VirtualMachineIdentity{
				Type:                   lo.ToPtr(armcompute.ResourceIdentityTypeUserAssigned),
				UserAssignedIdentities: identityMap,
			}
		}
	}

	return identity
}

func GetCapacityTypeFromVM(vm *armcompute.VirtualMachine) string {
	if vm != nil && vm.Properties != nil && vm.Properties.Priority != nil {
		return VMPriorityToKarpCapacityType[*vm.Properties.Priority]
	}
	return ""
}

func GetScaleSetPriorityLabelFromVM(vm *armcompute.VirtualMachine) string {
	if vm != nil && vm.Properties != nil && vm.Properties.Priority != nil {
		return VMPriorityToScaleSetPriority[*vm.Properties.Priority]
	}
	return ""
}

// BuildNodeClaimFromVM converts an Azure VirtualMachine to a Karpenter NodeClaim.
// This parallels BuildNodeClaimFromAKSMachine in aksmachineinstanceutils.go.
func BuildNodeClaimFromVM(ctx context.Context, vm *armcompute.VirtualMachine, instanceType *corecloudprovider.InstanceType) (*karpv1.NodeClaim, error) {
	nodeClaim := &karpv1.NodeClaim{}
	labels := map[string]string{}
	annotations := map[string]string{}

	if instanceType != nil {
		labels = labelspkg.GetAllSingleValuedRequirementLabels(instanceType.Requirements)
		nodeClaim.Status.Capacity = lo.PickBy(instanceType.Capacity, func(_ corev1.ResourceName, v resource.Quantity) bool { return !resources.IsZero(v) })
		nodeClaim.Status.Allocatable = lo.PickBy(instanceType.Allocatable(), func(_ corev1.ResourceName, v resource.Quantity) bool { return !resources.IsZero(v) })
	}

	if zone, err := utils.MakeAKSLabelZoneFromVM(vm); err != nil {
		log.FromContext(ctx).Info("failed to get zone for VM, zone label will be empty", "vmName", *vm.Name, "error", err)
	} else {
		labels[corev1.LabelTopologyZone] = zone
	}

	labels[karpv1.CapacityTypeLabelKey] = GetCapacityTypeFromVM(vm)
	labels[v1beta1.AKSLabelScaleSetPriority] = GetScaleSetPriorityLabelFromVM(vm)

	if tag, ok := vm.Tags[launchtemplate.NodePoolTagKey]; ok {
		labels[karpv1.NodePoolLabelKey] = *tag
	}

	nodeClaim.Name = GetNodeClaimNameFromVMName(*vm.Name)
	nodeClaim.Labels = labels
	nodeClaim.Annotations = annotations
	if vm.Properties != nil && vm.Properties.TimeCreated != nil {
		nodeClaim.CreationTimestamp = metav1.Time{Time: *vm.Properties.TimeCreated}
	} else {
		// Fallback to current time to ensure garbage collection grace period is enforced
		// when TimeCreated is unavailable. Without this, CreationTimestamp would be epoch (zero value)
		// and the instance could be immediately garbage collected, bypassing the 5-minute grace period.
		// TODO: Investigate a more fail-safe approach. If vm.Properties.TimeCreated is NEVER populated,
		// this fallback means the VM will never be garbage collected since we call this helper every time
		// we create an in-memory NodeClaim. We currently assume this shouldn't happen because VMs that fail
		// to come up should eventually stop appearing in Azure API responses.
		nodeClaim.CreationTimestamp = metav1.Time{Time: time.Now()}
	}
	// Set the deletionTimestamp to be the current time if the instance is currently terminating
	if utils.IsVMDeleting(*vm) {
		nodeClaim.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	}
	nodeClaim.Status.ProviderID = utils.VMResourceIDToProviderID(ctx, *vm.ID)
	if vm.Properties != nil && vm.Properties.StorageProfile != nil && vm.Properties.StorageProfile.ImageReference != nil {
		nodeClaim.Status.ImageID = utils.ImageReferenceToString(vm.Properties.StorageProfile.ImageReference)
	}
	return nodeClaim, nil
}

// GetNodeClaimNameFromVMName derives the NodeClaim name from the VM name by stripping the "aks-" prefix.
func GetNodeClaimNameFromVMName(vmName string) string {
	return strings.TrimPrefix(vmName, "aks-")
}
