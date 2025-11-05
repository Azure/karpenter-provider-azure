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
	"net/http"
	"strings"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	coreapis "sigs.k8s.io/karpenter/pkg/apis"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
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
	instanceType *corecloudprovider.InstanceType, // optional; won't be populated for standalone nodeclaims
	capacityType string,
	zone *string, // <region>-<zone-id>, optional
	aksMachineResourceID string,
	vmResourceID string,
	isDeleting bool,
	aksMachineNodeImageVersion string,
) (*karpv1.NodeClaim, error) {
	nodeClaim := &karpv1.NodeClaim{}
	labels := map[string]string{}
	annotations := map[string]string{}

	annotations[v1beta1.AnnotationAKSMachineResourceID] = aksMachineResourceID
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
		// Missing tag (by design, only possible if user intervenes) will eventually be repaired by in-place update controller.
		// By the time of writing, this is being used for logging purposes within provider only.
		// That is unlikely to change for core. But be mindful of provider is to rely on this in that situation. Still, rare.
		// This was less of a concern for VM instance as NodeClaim name is always inferrable from instance name.
		nodeClaim.Name = *tag
	}
	nodeClaim.Labels = labels
	nodeClaim.Annotations = annotations

	if tag, ok := aksMachineTemplate.Properties.Tags[launchtemplate.KarpenterAKSMachineCreationTimestampTagKey]; ok {
		if parsedTime, err := AKSMachineTimestampFromTag(*tag); err == nil {
			// Note: this assignment to NodeClaim is not effective to the actual object in the cluster, which still represents NodeClaim's (not instance's) creation time.
			// By the time of writing, this "borrowed struct field" is being used by provider for instance garbage collection. AWS does the same.
			// Suggestion: this "borrowing" pattern and its inconsistency is not intuitive..., should reconsider this implementation?
			nodeClaim.CreationTimestamp = AKSMachineTimestampToMeta(parsedTime)
			// Note: AWS and (legacy) VM instance provider relies on server-side creation timestamp. Instead, this tag value is client-side creation timestamp, generated before the request.
			// For garbage collection:
			// - The 5m grace period will have to cover (server-side create - client-side create) period, in addition to existing (Create() returns to core - server-side create) period.
			//   - Which means it will be more aggressive, although, not significant statistically.
			// - Suggestion: suggest API change to introduce server-side creation timestamp, if we really want to exclude that period.
			// - Note that it is incorrect to use actual NodeClaim's creation time, as retries can occur on the same NodeClaim, hurting grace period with each.
		}
		// If tag value is irretrievable, then it is epoch.
		// - By design, that is only possible if user intervenes and messes with the tag.
		// - See inplaceupdate module for how this is being handled.
		// For garbage collection:
		// - Grace period will be effectively disabled, but no issue if that happens after it (5m) ended.
		// - More details/updates in that module.
	}

	// Set the deletionTimestamp to be the current time if the instance is currently terminating
	if isDeleting {
		nodeClaim.DeletionTimestamp = lo.ToPtr(AKSMachineTimestampToMeta(NewAKSMachineTimestamp()))
	}
	nodeClaim.Status.ProviderID = utils.VMResourceIDToProviderID(ctx, vmResourceID)
	nodeClaim.Status.ImageID = aksMachineNodeImageVersion // ASSUMPTION: this doesn't need to be full image ID (should be fine on core, as the definition of ID is provider agnostic)

	return nodeClaim, nil
}

// Expect AKS machine struct to be fully populated as if it comes from GET.
// Not assuming that NodeClaim exists.
func BuildNodeClaimFromAKSMachine(ctx context.Context, aksMachine *armcontainerservice.Machine, possibleInstanceTypes []*corecloudprovider.InstanceType, aksMachineLocation string) (*karpv1.NodeClaim, error) {
	// ASSUMPTION: unless said otherwise, the fields below must exist on the AKS machine instance. Either set by Karpenter (see Create()) or visibly defaulted by the API.
	if err := validateRetrievedAKSMachineBasicProperties(aksMachine); err != nil {
		return nil, fmt.Errorf("failed to validate AKS machine instance %q: %w", lo.FromPtr(aksMachine.Name), err)
	}
	var zonePtr *string // This one is optional.
	if len(aksMachine.Zones) < 1 || aksMachine.Zones[0] == nil {
		log.FromContext(ctx).Info("AKS machine instance is missing zone", "aksMachineName", lo.FromPtr(aksMachine.Name))
	} else {
		zonePtr = lo.ToPtr(utils.GetAKSLabelZoneFromARMZone(aksMachineLocation, lo.FromPtr(aksMachine.Zones[0])))
	}
	if aksMachine.Properties.Priority == nil {
		return nil, fmt.Errorf("AKS machine instance %q is missing priority", lo.FromPtr(aksMachine.Name))
	}

	return BuildNodeClaimFromAKSMachineTemplate(
		ctx,
		aksMachine,
		offerings.GetInstanceTypeFromVMSize(lo.FromPtr(aksMachine.Properties.Hardware.VMSize), possibleInstanceTypes),
		GetCapacityTypeFromAKSScaleSetPriority(lo.FromPtr(aksMachine.Properties.Priority)),
		zonePtr,
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

// ASSUMPTION: NodeClaim name is in the format of <NodePool name>-<hash suffix>
// If total length exceeds AKS machine name limit, the exceeded part will be replaced with another deterministic hash.
// E.g., "thisisalongnodepoolname-a1b2c" --> "thisisalongnoz9y8x7-a1b2c"
func GetAKSMachineNameFromNodeClaimName(nodeClaimName string) (string, error) {
	const maxAKSMachineNameLength = 35 // Defined by AKS machine API.
	const prefixHashLength = 6         // The length of the hashed part replacing the exceeded part of the prefix.
	// If 6, given alphanumeric hash, there will be a total of 36^6 = 2,176,782,336 combinations.

	if len(nodeClaimName) <= maxAKSMachineNameLength {
		// Safe to use the whole name
		return nodeClaimName, nil
	}
	splitted := strings.Split(nodeClaimName, "-")
	// Combine the parts except the last one (NodeClaim hash suffix)
	prefix := strings.Join(splitted[:len(splitted)-1], "-")
	suffix := "-" + splitted[len(splitted)-1]

	// Keep the legit part of the prefix intact, but hash the rest
	// ASSUMPTION: prefix length is at least 6 characters at this point (which means suffix length is not too large)
	// At the time of writing, suffix length is 6 (e.g., "-a1b2c"). This is unlikely to change.
	keepPrefixLength := maxAKSMachineNameLength - len(suffix) - prefixHashLength
	prefixToKeep := prefix[:keepPrefixLength]
	prefixToHash := prefix[keepPrefixLength:]
	hashedPrefixToHash, err := utils.GetAlphanumericHash(prefixToHash, 6)
	if err != nil {
		return "", fmt.Errorf("failed to hash exceeded AKS machine name prefix %q: %w", prefixToHash, err)
	}

	hashTrimmedPrefix := prefixToKeep + hashedPrefixToHash
	return hashTrimmedPrefix + suffix, nil
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

// vmName = aks-<machinesPoolName>-<aksMachineName>-########-vm
// This is distinguishable from VM instance name as its suffix will always be 5 alphanumerics rather than "vm"
func GetAKSMachineNameFromVMName(aksMachinesPoolName, vmName string) (string, error) {
	if !strings.HasPrefix(vmName, "aks-"+aksMachinesPoolName+"-") {
		return "", fmt.Errorf("vm name %s does not start with expected prefix aks-%s-", vmName, aksMachinesPoolName)
	}
	prefixTrimmed := strings.TrimPrefix(vmName, "aks-"+aksMachinesPoolName+"-")

	splitted := strings.Split(prefixTrimmed, "-")
	if len(splitted) < 3 {
		return "", fmt.Errorf("vm name %s does not have enough parts after prefix aks-%s-", vmName, aksMachinesPoolName)
	}
	// Check whether the last part is "vm"
	if splitted[len(splitted)-1] != "vm" {
		return "", fmt.Errorf("vm name %s does not end with expected suffix vm", vmName)
	}
	// Remove the last two parts (########-vm) and join the rest to get the AKS machine name
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

// GetAKSLabelZoneFromAKSMachine returns the zone for the given AKS machine, or an empty string if there is no zone specified
// This function is analogous to utils.GetAKSLabelZoneFromVM but for AKS machines
func GetAKSLabelZoneFromAKSMachine(aksMachine *armcontainerservice.Machine, location string) (string, error) {
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
		return utils.GetAKSLabelZoneFromARMZone(location, lo.FromPtr(aksMachine.Zones[0])), nil
	}
	if len(aksMachine.Zones) > 1 {
		return "", fmt.Errorf("AKS machine has multiple zones")
	}
	return "", nil
}

func IsAKSMachineOrMachinesPoolNotFound(err error) bool {
	if err == nil {
		return false
	}
	azErr := sdkerrors.IsResponseError(err)
	if azErr != nil && (azErr.StatusCode == http.StatusNotFound || // Covers AKS machines pool not found on PUT machine, GET machine, GET (list) machines, POST agent pool (DELETE machines), and AKS machine not found on GET machine
		(azErr.StatusCode == http.StatusBadRequest && azErr.ErrorCode == "InvalidParameter" && strings.Contains(azErr.Error(), "Cannot find any valid machines"))) { // Covers AKS machine not found on POST agent pool (DELETE machines)
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
	if aksMachine.Properties.Priority == nil {
		// This not being guaranteed is per the current behavior of both AKS machine API and AKS AgentPool API: priority will shows up only for spot.
		// Although, it is expected that it gets rehydrated client-side before this validation function is called.
		// Suggestion: rework/research more on this pattern RP-side?
		return fmt.Errorf("irretrievable priority")
	}
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

func shouldAKSMachinesBeVisible(ctx context.Context) bool {
	return options.FromContext(ctx).ProvisionMode == consts.ProvisionModeAKSMachineAPI || options.FromContext(ctx).ManageExistingAKSMachines
}
