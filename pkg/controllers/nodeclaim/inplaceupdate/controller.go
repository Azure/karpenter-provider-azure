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
	"fmt"
	"maps"
	"time"

	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
	nodeclaimutils "sigs.k8s.io/karpenter/pkg/utils/nodeclaim"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

type Controller struct {
	kubeClient       client.Client
	instanceProvider instance.Provider
}

func NewController(
	kubeClient client.Client,
	instanceProvider instance.Provider,
) *Controller {
	return &Controller{
		kubeClient:       kubeClient,
		instanceProvider: instanceProvider,
	}
}

func (c *Controller) Reconcile(ctx context.Context, nodeClaim *karpv1.NodeClaim) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, "nodeclaim.inplaceupdate")
	// No need to add nodeClaim name to the context as it's already there

	if shouldProcess, result := c.shouldProcess(ctx, nodeClaim); !shouldProcess {
		return result, nil
	}

	stored := nodeClaim.DeepCopy()

	// Get the NodeClass
	nodeClass, err := c.resolveAKSNodeClass(ctx, nodeClaim)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("resolving AKSNodeClass, %w", err)
	}

	// TODO: When we have sources of truth coming from NodePool we can do:
	// nodePool, err := nodeclaimutil.Owner(ctx, c.kubeClient, nodeClaim)
	// TODO: To look it up and use that as input to calculate the goal state as well

	// Compare the expected hash with the actual hash
	options := options.FromContext(ctx)
	goalHash, err := HashFromNodeClaim(options, nodeClaim, nodeClass)
	if err != nil {
		return reconcile.Result{}, err
	}
	actualHash := nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]

	log.FromContext(ctx).V(1).Info("comparing hashes", "goalHash", goalHash, "actualHash", actualHash)

	// If there's no difference from goal state, no need to do anything else
	if goalHash == actualHash {
		return reconcile.Result{}, nil
	}

	vm, err := c.getVM(ctx, nodeClaim)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting VM for nodeClaim %s: %w", nodeClaim.Name, err)
	}

	err = c.applyPatch(ctx, options, nodeClaim, nodeClass, vm)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("applying patch to VM for nodeClaim %s: %w", nodeClaim.Name, err)
	}

	if nodeClaim.Annotations == nil {
		nodeClaim.Annotations = make(map[string]string)
	}
	// Regardless of whether we actually changed anything in Azure, we have confirmed that
	// the goal shape is in alignment with our expected shape, so update the annotation to reflect that
	nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash] = goalHash
	err = c.kubeClient.Patch(ctx, nodeClaim, client.MergeFrom(stored))
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	return reconcile.Result{}, nil
}

// TODO: duplicate from resolveNodeClassFromNodeClaim for CloudProvider
// resolveAKSNodeClass resolves the AKSNodeClass from the NodeClaim's NodeClassRef
func (c *Controller) resolveAKSNodeClass(ctx context.Context, nodeClaim *karpv1.NodeClaim) (*v1beta1.AKSNodeClass, error) {
	if nodeClaim.Spec.NodeClassRef == nil {
		return nil, fmt.Errorf("nodeClaim %s does not have a nodeClassRef", nodeClaim.Name)
	}

	nodeClass := &v1beta1.AKSNodeClass{}
	if err := c.kubeClient.Get(ctx, types.NamespacedName{Name: nodeClaim.Spec.NodeClassRef.Name}, nodeClass); err != nil {
		return nil, fmt.Errorf("getting AKSNodeClass %s: %w", nodeClaim.Spec.NodeClassRef.Name, err)
	}

	// For the purposes of in-place updates, we treat deleting NodeClasses as an error
	// This error should ideally be transient and the NodeClaim itself will be deleted
	if !nodeClass.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("AKSNodeClass %s is being deleted", nodeClass.Name)
	}

	return nodeClass, nil
}

func (c *Controller) shouldProcess(ctx context.Context, nodeClaim *karpv1.NodeClaim) (bool, reconcile.Result) {
	if !nodeClaim.DeletionTimestamp.IsZero() {
		return false, reconcile.Result{}
	}

	// If the node isn't registered yet, we need to wait until it is as otherwise all the resources we need to update may not exist yet
	if !nodeClaim.StatusConditions().Get(karpv1.ConditionTypeRegistered).IsTrue() {
		log.FromContext(ctx).V(1).Info("can't update yet as the claim is not registered")
		return false, reconcile.Result{RequeueAfter: 20 * time.Second}
	}

	if nodeClaim.Status.ProviderID == "" {
		log.FromContext(ctx).V(1).Info("can't update yet as there's no provider ID")
		return false, reconcile.Result{RequeueAfter: 20 * time.Second}
	}

	return true, reconcile.Result{}
}

func (c *Controller) getVM(ctx context.Context, nodeClaim *karpv1.NodeClaim) (*armcompute.VirtualMachine, error) {
	vmName, err := utils.GetVMName(nodeClaim.Status.ProviderID)
	if err != nil {
		return nil, err
	}

	vm, err := c.instanceProvider.Get(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("getting azure VM for machine, %w", err)
	}

	return vm, nil
}

func (c *Controller) applyPatch(
	ctx context.Context,
	options *options.Options,
	nodeClaim *karpv1.NodeClaim,
	nodeClass *v1beta1.AKSNodeClass,
	vm *armcompute.VirtualMachine,
) error {
	update := CalculateVMPatch(options, nodeClaim, nodeClass, vm)
	// This is safe only as long as we're not updating fields which we consider secret.
	// If we do/are, we need to redact them.
	logVMPatch(ctx, update)

	// Apply the update, if one is needed
	if update != nil {
		err := c.instanceProvider.Update(ctx, lo.FromPtr(vm.Name), *update)
		if err != nil {
			return fmt.Errorf("failed to apply update to VM, %w", err)
		}
	}

	return nil
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("nodeclaim.inplaceupdate").
		For(
			&karpv1.NodeClaim{},
			builder.WithPredicates(
				predicate.Or(
					predicate.GenerationChangedPredicate{}, // Note that this will trigger on pod restart for all Machines.
				),
			)).
		Watches(&v1beta1.AKSNodeClass{}, nodeclaimutils.NodeClassEventHandler(m.GetClient()), builder.WithPredicates(tagsChangedPredicate{})).
		// TODO: Can add .Watches(&karpv1.NodePool{}, nodeclaimutil.NodePoolEventHandler(c.kubeClient))
		// TODO: similar to https://github.com/kubernetes-sigs/karpenter/blob/main/pkg/controllers/nodeclaim/disruption/controller.go#L214C3-L217C5
		// TODO: if/when we need to monitor provisioner changes and flow updates on the NodePool down to the underlying VMs.
		WithOptions(controller.Options{
			RateLimiter: reasonable.RateLimiter(),
			// TODO: Document why this magic number used. If we want to consistently use it accoss reconcilers, refactor to a reused const.
			// Comments thread discussing this: https://github.com/Azure/karpenter-provider-azure/pull/729#discussion_r2006629809
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}

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
