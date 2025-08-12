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

	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
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

type patchParameters struct {
	opts      *options.Options
	nodeClaim *karpv1.NodeClaim
	nodeClass *v1beta1.AKSNodeClass
}

var patchers = []func(*armcompute.VirtualMachineUpdate, *patchParameters, *armcompute.VirtualMachine) bool{
	patchIdentities,
	patchTags,
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

	for _, patcher := range patchers {
		patched := patcher(update, params, currentVM)
		hasPatches = hasPatches || patched
	}

	if !hasPatches {
		return nil // No update to perform
	}

	return update
}

func patchIdentities(
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

func patchTags(
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
