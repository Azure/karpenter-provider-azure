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

	"github.com/aws/karpenter-core/pkg/apis/v1beta1"
	corecontroller "github.com/aws/karpenter-core/pkg/operator/controller"
	"github.com/samber/lo"
	"go.uber.org/zap/zapcore"
	"knative.dev/pkg/logging"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

type Controller struct {
	kubeClient       client.Client
	instanceProvider *instance.Provider
}

var _ corecontroller.TypedController[*v1beta1.NodeClaim] = &Controller{}

func NewController(
	kubeClient client.Client,
	instanceProvider *instance.Provider,
) corecontroller.Controller {
	controller := &Controller{
		kubeClient:       kubeClient,
		instanceProvider: instanceProvider,
	}

	return corecontroller.Typed[*v1beta1.NodeClaim](kubeClient, controller)
}

func (c *Controller) Name() string {
	return "nodeclaim.inplaceupdate"
}

func (c *Controller) Reconcile(ctx context.Context, nodeClaim *v1beta1.NodeClaim) (reconcile.Result, error) {
	if !nodeClaim.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	// Node doesn't have provider ID yet
	if nodeClaim.Status.ProviderID == "" {
		return reconcile.Result{}, nil
	}

	stored := nodeClaim.DeepCopy()

	// TODO: When we have sources of truth coming from NodePool we can do:
	// nodePool, err := nodeclaimutil.Owner(ctx, c.kubeClient, nodeClaim)
	// TODO: To look it up and use that as input to calculate the goal state as well

	// Compare the expected hash with the actual hash
	options := options.FromContext(ctx)
	goalHash, err := HashFromNodeClaim(options, nodeClaim)
	if err != nil {
		return reconcile.Result{}, err
	}
	actualHash := nodeClaim.Annotations[v1alpha2.AnnotationInPlaceUpdateHash]

	logging.FromContext(ctx).Debugf("goal hash is: %q, actual hash is: %q", goalHash, actualHash)

	// If there's no difference from goal state, no need to do anything else
	if goalHash == actualHash {
		return reconcile.Result{}, nil
	}

	vmName, err := utils.GetVMName(nodeClaim.Status.ProviderID)
	if err != nil {
		return reconcile.Result{}, err
	}

	vm, err := c.instanceProvider.Get(ctx, vmName)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting azure VM for machine, %w", err)
	}

	update := calculateVMPatch(options, vm)
	// This is safe only as long as we're not updating fields which we consider secret.
	// If we do/are, we need to redact them.
	logVMPatch(ctx, update)

	// Apply the update, if one is needed
	if update != nil {
		err = c.instanceProvider.Update(ctx, vmName, *update)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to apply update to VM, %w", err)
		}
	}

	if nodeClaim.Annotations == nil {
		nodeClaim.Annotations = make(map[string]string)
	}
	// Regardless of whether we actually changed anything in Azure, we have confirmed that
	// the goal shape is in alignment with our expected shape, so update the annotation to reflect that
	nodeClaim.Annotations[v1alpha2.AnnotationInPlaceUpdateHash] = goalHash
	err = c.kubeClient.Patch(ctx, nodeClaim, client.MergeFrom(stored))
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	return reconcile.Result{}, nil
}

func calculateVMPatch(
	options *options.Options,
	// TODO: Can pass and consider NodeClaim and/or NodePool here if we need to in the future
	currentVM *armcompute.VirtualMachine,
) *armcompute.VirtualMachineUpdate {
	// Determine the differences between the current state and the goal state
	expectedIdentities := options.NodeIdentities
	var currentIdentities []string
	if currentVM.Identity != nil {
		currentIdentities = lo.Keys(currentVM.Identity.UserAssignedIdentities)
	}

	toAdd, _ := lo.Difference(expectedIdentities, currentIdentities)
	// It's not possible to PATCH identities away, so for now we never remove them even if they've been removed from
	// the configmap. This matches the RPs behavior and also ensures that we don't remove identities which users have
	// manually added.

	if len(toAdd) == 0 {
		return nil // No update to perform
	}

	identity := instance.ConvertToVirtualMachineIdentity(toAdd)

	return &armcompute.VirtualMachineUpdate{
		Identity: identity,
	}
}

func (c *Controller) Builder(_ context.Context, m manager.Manager) corecontroller.Builder {
	return corecontroller.Adapt(controllerruntime.NewControllerManagedBy(m).For(
		&v1beta1.NodeClaim{},
		builder.WithPredicates(
			predicate.Or(
				predicate.GenerationChangedPredicate{}, // Note that this will trigger on pod restart for all Machines.
			),
		)).WithOptions(controller.Options{MaxConcurrentReconciles: 10}),
	// TODO: Can add .Watches(&v1beta1.NodePool{}, nodeclaimutil.NodePoolEventHandler(c.kubeClient))
	// TODO: similar to https://github.com/aws/karpenter-core/blob/main/pkg/controllers/nodeclaim/disruption/controller.go#L214C3-L217C5
	// TODO: if/when we need to monitor provisoner changes and flow updates on the NodePool down to the underlying VMs.
	)
}

func logVMPatch(ctx context.Context, update *armcompute.VirtualMachineUpdate) {
	if logging.FromContext(ctx).Level().Enabled(zapcore.DebugLevel) {
		rawStr := "<nil>"
		if update != nil {
			raw, _ := json.Marshal(update)
			rawStr = string(raw)
		}
		logging.FromContext(ctx).Debugf("applying patch to Azure VM: %s", rawStr)
	}
}
