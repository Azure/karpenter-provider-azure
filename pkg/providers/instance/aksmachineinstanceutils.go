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
	"fmt"
	"strings"
	"time"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	coreapis "sigs.k8s.io/karpenter/pkg/apis"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

var (
	KarpCapacityTypeToAKSScaleSetPriority = map[string]armcontainerservice.ScaleSetPriority{
		karpv1.CapacityTypeSpot:     armcontainerservice.ScaleSetPrioritySpot,
		karpv1.CapacityTypeOnDemand: armcontainerservice.ScaleSetPriorityRegular,
	}
	AKSScaleSetPriorityToKarpCapacityType = map[armcontainerservice.ScaleSetPriority]string{
		armcontainerservice.ScaleSetPrioritySpot:    karpv1.CapacityTypeSpot,
		armcontainerservice.ScaleSetPriorityRegular: karpv1.CapacityTypeOnDemand,
	}
)

// Convention(?) These can change if needed. The purpose is to make assumptions more visible.
// Find:
//   Input: structs or values
//   Output: a struct, representing a resource to look for
// Build:
//   Input: structs or values
//   Output: a new struct
// Get:
//   Input: structs or values
//   Output: a struct or value

// Note that the template is not guaranteed to have status fields, thus they are made explicit here.
// Other Karpenter-level fields are also included as they may be easily retrieved during templating phase.
// Not assuming that NodeClaim exists.
func BuildNodeClaimFromAKSMachineTemplate(
	ctx context.Context, aksMachineTemplate *armcontainerservice.Machine,
	aksMachineName string,
	instanceType *corecloudprovider.InstanceType, // optional; won't be populated for standalone nodeclaims
	capacityType string,
	zone *string, // <region>-<zone-id>, optional
	creationTimestamp *time.Time, // optional, required for the first time only
	aksMachineResourceID string,
	vmResourceID string,
	isDeleting bool,
	aksMachineNodeImageVersion string,
) (*karpv1.NodeClaim, error) {
	nodeClaim := &karpv1.NodeClaim{}
	labels := map[string]string{}
	annotations := map[string]string{}

	annotations[v1beta1.AnnotationAKSMachineResourceID] = aksMachineResourceID // XPMT: (topic) New annotation(s) on NodeClaim
	if instanceType != nil {
		labels = offerings.GetAllSingleValuedRequirementLabels(instanceType)
		nodeClaim.Status.Capacity = lo.PickBy(instanceType.Capacity, func(_ corev1.ResourceName, v resource.Quantity) bool { return !resources.IsZero(v) })
		nodeClaim.Status.Allocatable = lo.PickBy(instanceType.Allocatable(), func(_ corev1.ResourceName, v resource.Quantity) bool { return !resources.IsZero(v) })
	}
	if zone != nil {
		labels[corev1.LabelTopologyZone] = *zone
	}
	labels[karpv1.CapacityTypeLabelKey] = capacityType
	if tag, ok := aksMachineTemplate.Properties.Tags[NodePoolTagKey]; ok {
		labels[karpv1.NodePoolLabelKey] = *tag
	}
	if tag, ok := aksMachineTemplate.Properties.Tags[launchtemplate.KarpenterAKSMachineNodeClaimTagKey]; ok {
		nodeClaim.Name = *tag
	} else {
		return nil, fmt.Errorf("AKS machine template is missing required tag %s", launchtemplate.KarpenterAKSMachineNodeClaimTagKey)
	}
	nodeClaim.Labels = labels
	nodeClaim.Annotations = annotations
	if creationTimestamp != nil {
		nodeClaim.CreationTimestamp = metav1.Time{Time: *creationTimestamp}
	}

	// Set the deletionTimestamp to be the current time if the instance is currently terminating
	if isDeleting {
		nodeClaim.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	}
	nodeClaim.Status.ProviderID = utils.VMResourceIDToProviderID(ctx, vmResourceID)
	nodeClaim.Status.ImageID = aksMachineNodeImageVersion // ASSUMPTION: this doesn't need to be full image ID (should be fine on core, as the definition of ID is provider agnostic)
	// XPMT: (topic) Machine-NodeClaim conversion: retrieving VM image ID

	return nodeClaim, nil
}

// Expect AKS machine struct to be fully populated as if it comes from GET.
// Not assuming that NodeClaim exists.
func BuildNodeClaimFromAKSMachine(ctx context.Context, aksMachine *armcontainerservice.Machine, possibleInstanceTypes []*corecloudprovider.InstanceType, aksMachineLocation string, creationTimestamp *time.Time) (*karpv1.NodeClaim, error) {
	// ASSUMPTION: unless said otherwise, the fields below must exist on the AKS machine instance. Either set by Karpenter (see Create()) or visibly defaulted by the API.
	if err := validateRetrievedAKSMachineBasicProperties(aksMachine); err != nil {
		return nil, fmt.Errorf("failed to validate AKS machine instance %q: %w", lo.FromPtr(aksMachine.Name), err)
	}
	var zonePtr *string // This one is optional.
	if len(aksMachine.Zones) < 1 || aksMachine.Zones[0] == nil {
		log.FromContext(ctx).Info("AKS machine instance is missing zone", "aksMachineName", lo.FromPtr(aksMachine.Name))
	} else {
		zonePtr = lo.ToPtr(utils.GetAKSZoneFromARMZone(aksMachineLocation, lo.FromPtr(aksMachine.Zones[0])))
	}

	return BuildNodeClaimFromAKSMachineTemplate(
		ctx,
		aksMachine,
		lo.FromPtr(aksMachine.Name),
		offerings.GetInstanceTypeFromVMSize(lo.FromPtr(aksMachine.Properties.Hardware.VMSize), possibleInstanceTypes),
		GetCapacityTypeFromAKSScaleSetPriority(lo.FromPtr(aksMachine.Properties.Priority)),
		zonePtr,
		creationTimestamp,
		lo.FromPtr(aksMachine.ID),
		lo.FromPtr(aksMachine.Properties.ResourceID),
		IsAKSMachineDeleting(aksMachine),
		lo.FromPtr(aksMachine.Properties.NodeImageVersion), // Empty: not fatal, no need to check
	)
}

// May return apimachinery.NotFoundError if NodePool is not found.
func FindNodePoolFromAKSMachine(ctx context.Context, aksMachine *armcontainerservice.Machine, kubeClient client.Client) (*karpv1.NodePool, error) {
	// ASSUMPTION: NodePool name is stored in the all AKS machine tags.
	nodePoolName, ok := aksMachine.Properties.Tags[launchtemplate.NodePoolTagKey]
	if ok && *nodePoolName != "" {
		nodePool := &karpv1.NodePool{}
		if err := kubeClient.Get(ctx, types.NamespacedName{Name: *nodePoolName}, nodePool); err != nil {
			return nil, err
		}
		return nodePool, nil
	}

	return nil, errors.NewNotFound(schema.GroupResource{Group: coreapis.Group, Resource: "nodepools"}, "")
}

// XPMT: TODO(Bryce-Soghigian): rework this thing below? Also consider add acceptance tests on this in cloudprovider module, if applicable.
// Real node name would be aks-<machinesPoolName>-<aksMachineName>-########-vm#. E.g., aks-aksmanagedap-default-2jf98-11274290-vm2.
func GetAKSMachineNameFromNodeClaimName(nodeClaimName string) string {
	// ASSUMPTION: all AKS machines are named after the NodeClaim name.
	// Does not guarantee that the NodeClaim is already associated with an AKS machine.
	// This assumption is weaker than the one in GetAKSMachineNameFromNodeClaim(), but still, not breaking anytime soon.
	// return nodeClaimName

	// XPMT: TEMPORARY
	splitted := strings.Split(nodeClaimName, "-")
	return "x" + splitted[len(splitted)-1]
}

// GetAKSMachineNameFromNodeClaim extracts the AKS machine name from the NodeClaim annotations
// Returns false if the annotation is not present or the value is empty, which can indicate that the NodeClaim is not associated with an AKS machine.
func GetAKSMachineNameFromNodeClaim(nodeClaim *karpv1.NodeClaim) (string, bool) {
	// ASSUMPTION: A NodeClaim is associated with an AKS machine iff the annotation is present (e.g., annotated when creating NodeClaim from a AKS machine instance).
	// ASSUMPTION: If exists, the annotation is always set to the correct AKS machine resource ID.
	if aksMachineResourceID, ok := nodeClaim.Annotations[v1beta1.AnnotationAKSMachineResourceID]; ok {
		splitted := strings.Split(aksMachineResourceID, "/")
		aksMachineName := splitted[len(splitted)-1] // The last part of the resource ID is the AKS machine name
		return aksMachineName, true
	}
	return "", false
}

func GetCapacityTypeFromAKSScaleSetPriority(scaleSetPriority armcontainerservice.ScaleSetPriority) string {
	return AKSScaleSetPriorityToKarpCapacityType[scaleSetPriority]
}

// vmName = aks-<machinesPoolName>-<aksMachineName>-########-vm#
// Note: there is no accurate way to tell from the VM name that the VM is created by AKS machine API.
// E.g., a non-AKS machine VM from Karpenter nodepool named "aksmanagedap-aks-nodepool-abcde-12345678" with suffix "vms12" would result in VM name "aks-aksmanagedap-aks-nodepool-abcde-12345678-vm12", which can be interpreted as a AKS machine with pool name "aksmanagedap" and name "aks-nodepool-abcde".
// The validation below is, thus, best-effort.
func GetAKSMachineNameFromVMName(aksMachinesPoolName, vmName string) (string, error) {
	if !strings.HasPrefix(vmName, "aks-"+aksMachinesPoolName+"-") {
		return "", fmt.Errorf("vm name %s does not start with expected prefix aks-%s-", vmName, aksMachinesPoolName)
	}
	prefixTrimmed := strings.TrimPrefix(vmName, "aks-"+aksMachinesPoolName+"-")

	splitted := strings.Split(prefixTrimmed, "-")
	if len(splitted) < 3 {
		return "", fmt.Errorf("vm name %s does not have enough parts after prefix aks-%s-", vmName, aksMachinesPoolName)
	}
	// Check whether the last part starts with "vm"
	if !strings.HasPrefix(splitted[len(splitted)-1], "vm") {
		return "", fmt.Errorf("vm name %s does not end with expected suffix vm#", vmName)
	}
	// Remove the last two parts (########-vm#) and join the rest to get the AKS machine name
	aksMachineName := strings.Join(splitted[:len(splitted)-2], "-")

	return aksMachineName, nil
}

func IsAKSMachineDeleting(aksMachine *armcontainerservice.Machine) bool {
	if aksMachine != nil && aksMachine.Properties != nil && aksMachine.Properties.ProvisioningState != nil {
		// Suggestion: find a constant?
		return *aksMachine.Properties.ProvisioningState == "Deleting"
	}
	return false
}

// GetAKSZoneFromAKSMachine returns the zone for the given AKS machine, or an empty string if there is no zone specified
// This function is analogous to utils.GetAKSZoneFromVM but for AKS machines
func GetAKSZoneFromAKSMachine(aksMachine *armcontainerservice.Machine, location string) (string, error) {
	if aksMachine == nil {
		return "", fmt.Errorf("cannot pass in a nil AKS machine")
	}
	if aksMachine.Zones == nil {
		return "", nil
	}
	if len(aksMachine.Zones) == 1 {
		if location == "" {
			return "", fmt.Errorf("AKS machine is missing location")
		}
		return utils.GetAKSZoneFromARMZone(location, lo.FromPtr(aksMachine.Zones[0])), nil
	}
	if len(aksMachine.Zones) > 1 {
		return "", fmt.Errorf("AKS machine has multiple zones")
	}
	return "", nil
}

func IsARMNotFound(err error) bool {
	if err == nil {
		return false
	}
	azErr := sdkerrors.IsResponseError(err)
	if azErr != nil && (azErr.ErrorCode == "NotFound" || azErr.ErrorCode == "ResourceNotFound" || azErr.ErrorCode == "ResourceGroupNotFound") {
		// Annoyingly, AKS AgentPool (machine pool)/AKS Machine, ManagedCluster, ARM ResourceGroup have different error code when not found.
		return true
	}
	return false
}

func validateRetrievedAKSMachineBasicProperties(aksMachine *armcontainerservice.Machine) error {
	// Assumptions may be made after this function returns no error.
	// Thus, check every usage before removing each validation.
	if aksMachine.Properties == nil {
		return fmt.Errorf("irretrievable properties")
	}
	if aksMachine.Properties.Hardware == nil || aksMachine.Properties.Hardware.VMSize == nil {
		return fmt.Errorf("irretrievable VM size")
	}
	// This not being guaranteed is per the current behavior of both AKS machine API and AKS AgentPool API: priority will shows up only for spot.
	// Suggestion: rework/research more on this pattern RP-side?
	// if aksMachine.Properties.Priority == nil {
	// return fmt.Errorf("irretrievable priority")
	// }
	if aksMachine.Properties.ResourceID == nil {
		return fmt.Errorf("irretrievable VM resource ID")
	}
	if aksMachine.ID == nil {
		return fmt.Errorf("irretrievable ID")
	}
	if aksMachine.Properties.NodeImageVersion == nil {
		return fmt.Errorf("irretrievable node image version")
	}
	return nil
}
