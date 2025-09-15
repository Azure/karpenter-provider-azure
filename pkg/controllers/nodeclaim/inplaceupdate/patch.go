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

package inplaceupdate

import (
	"context"
	"encoding/json"
	"maps"
	"time"

	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
)

func logVMPatch(ctx context.Context, update *armcompute.VirtualMachineUpdate) {
	if log.FromContext(ctx).V(1).Enabled() {
		rawStr := "<nil>"
		if update != nil {
			raw, _ := json.Marshal(update)
			rawStr = string(raw)
		}
		log.FromContext(ctx).V(1).Info("patching Azure VM", "vmPatch", rawStr)
	} else {
		log.FromContext(ctx).V(0).Info("patching Azure VM")
	}
}

func logAKSMachinePatch(ctx context.Context, update *armcontainerservice.Machine) {
	if log.FromContext(ctx).V(1).Enabled() {
		rawStr := "<nil>"
		if update != nil {
			raw, _ := json.Marshal(update)
			rawStr = string(raw)
		}
		log.FromContext(ctx).V(1).Info("patching AKS machine", "aksMachinePatch", rawStr)
	} else {
		log.FromContext(ctx).V(0).Info("patching AKS machine")
	}
}

type patchParameters struct {
	opts      *options.Options
	nodeClaim *karpv1.NodeClaim
	nodeClass *v1beta1.AKSNodeClass
}

var vmPatchers = []func(*armcompute.VirtualMachineUpdate, *patchParameters, *armcompute.VirtualMachine) bool{
	patchVMIdentities,
	patchVMTags,
}

var aksMachinePatchers = []func(*armcontainerservice.Machine, *patchParameters, *armcontainerservice.Machine) bool{
	// VM identities are handled server-side for AKS machines. No need here.
	patchAKSMachineTags,
}

func CalculateVMPatch(
	options *options.Options,
	nodeClaim *karpv1.NodeClaim,
	nodeClass *v1beta1.AKSNodeClass,
	currentVM *armcompute.VirtualMachine,
) *armcompute.VirtualMachineUpdate {
	update := &armcompute.VirtualMachineUpdate{}
	hasPatches := false
	params := &patchParameters{
		opts:      options,
		nodeClass: nodeClass,
		nodeClaim: nodeClaim,
	}

	for _, patcher := range vmPatchers {
		patched := patcher(update, params, currentVM)
		hasPatches = hasPatches || patched
	}

	if !hasPatches {
		return nil // No update to perform
	}

	return update
}

// Given AKS machine support PUT, but not PATCH, the AKS machine object will be patched directly, while the returning Machine object is just a tracker for logging purposes.
func CalculateAKSMachinePatch(
	options *options.Options,
	nodeClaim *karpv1.NodeClaim,
	nodeClass *v1beta1.AKSNodeClass,
	patchingAKSMachine *armcontainerservice.Machine,
) *armcontainerservice.Machine {
	update := &armcontainerservice.Machine{}
	hasPatches := false
	params := &patchParameters{
		opts:      options,
		nodeClass: nodeClass,
		nodeClaim: nodeClaim,
	}

	for _, patcher := range aksMachinePatchers {
		patched := patcher(update, params, patchingAKSMachine)
		hasPatches = hasPatches || patched
	}

	if !hasPatches {
		return nil // No update to perform
	}

	return update
}

func patchVMIdentities(
	update *armcompute.VirtualMachineUpdate,
	params *patchParameters,
	currentVM *armcompute.VirtualMachine,
) bool {
	expectedIdentities := params.opts.NodeIdentities
	var currentIdentities []string
	if currentVM.Identity != nil {
		currentIdentities = lo.Keys(currentVM.Identity.UserAssignedIdentities)
	}

	// It's not possible to PATCH identities away, so for now we never remove them even if they've been removed from
	// the configmap. This matches the RPs behavior and also ensures that we don't remove identities which users have
	// manually added.
	toAdd, _ := lo.Difference(expectedIdentities, currentIdentities)
	if len(toAdd) == 0 {
		return false // No update to perform
	}

	update.Identity = instance.ConvertToVirtualMachineIdentity(toAdd)
	return true
}

func patchVMTags(
	update *armcompute.VirtualMachineUpdate,
	params *patchParameters,
	currentVM *armcompute.VirtualMachine,
) bool {
	expectedTags := launchtemplate.Tags(
		params.opts,
		params.nodeClass,
		params.nodeClaim,
	)

	eq := func(v1, v2 *string) bool {
		if v1 == nil && v2 == nil {
			return true
		}
		if v1 == nil || v2 == nil {
			return false
		}
		return *v1 == *v2
	}

	if maps.EqualFunc(expectedTags, currentVM.Tags, eq) {
		return false // No update to perform
	}

	update.Tags = expectedTags
	return true
}

func patchAKSMachineTags(
	update *armcontainerservice.Machine,
	params *patchParameters,
	patchingAKSMachine *armcontainerservice.Machine,
) bool {
	// For CreationTimestamp tag:
	// - If existing machine tag exists/valid, leave it unchanged (preserve existing)
	//   - Still prone to user modification:
	//     - If it is valid but incorrect, then there is no current way to detect it
	//     - If it is corrupted, then the logic below will repair it
	//     - Although, this is significant only for instance garbage collection in the first 5 minutes of the instance, so, not critical now
	// - Otherwise, fallback/default to epoch
	//   - Still, logic elsewhere should not assume that this is the case, as reconciliation may naturally come later
	//   - But still good to repair it
	// Also, we don't update it to actual NodeClaim.CreationTimestamp because that is NodeClaim creation time, not instance creation time.
	// See notes in aksmachineinstanceutils.go for context and suggestions.
	var creationTimestamp time.Time
	if patchingAKSMachine.Properties != nil && patchingAKSMachine.Properties.Tags != nil {
		if timestampTag, ok := patchingAKSMachine.Properties.Tags[launchtemplate.KarpenterAKSMachineCreationTimestampTagKey]; ok && timestampTag != nil {
			if parsed, err := instance.AKSMachineTimestampFromTag(*timestampTag); err == nil {
				// Preserve existing valid timestamp
				creationTimestamp = parsed
			} else {
				// Invalid timestamp, fallback to minimum time
				creationTimestamp = instance.ZeroAKSMachineTimestamp()
			}
		} else {
			// No existing timestamp tag, use minimum time
			creationTimestamp = instance.ZeroAKSMachineTimestamp()
		}
	} else {
		// No machine properties or tags, use minimum time
		creationTimestamp = instance.ZeroAKSMachineTimestamp()
	}

	// For NodeClaim name tag, given this controller is based on actual NodeClaim like during Create(), the patch will repair the tag if needed.

	expectedTags := instance.ConfigureAKSMachineTags(
		params.opts,
		params.nodeClass,
		params.nodeClaim,
		creationTimestamp,
	)

	eq := func(v1, v2 *string) bool {
		if v1 == nil && v2 == nil {
			return true
		}
		if v1 == nil || v2 == nil {
			return false
		}
		return *v1 == *v2
	}

	if patchingAKSMachine.Properties == nil {
		// Should not be possible, but handle it gracefully
		if len(expectedTags) == 0 {
			return false // No update to perform
		}
		patchingAKSMachine.Properties = &armcontainerservice.MachineProperties{
			Tags: expectedTags,
		}
		update.Properties = &armcontainerservice.MachineProperties{
			Tags: expectedTags,
		}
		return true
	}

	if maps.EqualFunc(expectedTags, patchingAKSMachine.Properties.Tags, eq) {
		return false // No update to perform
	}

	patchingAKSMachine.Properties.Tags = expectedTags
	if update.Properties == nil {
		update.Properties = &armcontainerservice.MachineProperties{}
	}
	update.Properties.Tags = expectedTags
	return true
}
