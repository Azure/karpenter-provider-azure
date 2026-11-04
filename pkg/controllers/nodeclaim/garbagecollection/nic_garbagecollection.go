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

package garbagecollection

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/awslabs/operatorpkg/reconciler"
	"github.com/awslabs/operatorpkg/singleton"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

const (
	NicReservationDuration = time.Second * 180
	// We set this interval at 5 minutes, as thats how often our NRP limits are reset.
	// See: https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/request-limits-and-throttling#network-throttling
	NicGarbageCollectionInterval = time.Minute * 5
)

type NetworkInterface struct {
	kubeClient         client.Client
	vmInstanceProvider instance.VMProvider
}

func NewNetworkInterface(kubeClient client.Client, vmInstanceProvider instance.VMProvider) *NetworkInterface {
	return &NetworkInterface{
		kubeClient:         kubeClient,
		vmInstanceProvider: vmInstanceProvider,
	}
}

func (c *NetworkInterface) populateUnremovableInterfaces(ctx context.Context) (sets.Set[string], sets.Set[string], error) {
	unremovableInterfaces := sets.New[string]()
	vmProviderIDs := sets.New[string]()
	liveAKSMachineNodeClaimNames := sets.New[string]()
	vms, err := c.vmInstanceProvider.List(ctx)
	if err != nil {
		return unremovableInterfaces, liveAKSMachineNodeClaimNames, fmt.Errorf("listing VMs: %w", err)
	}
	for _, vm := range vms {
		unremovableInterfaces.Insert(lo.FromPtr(vm.Name))
		vmProviderIDs.Insert(strings.ToLower(utils.VMResourceIDToProviderID(ctx, lo.FromPtr(vm.ID))))
	}
	nodeClaimList := &karpv1.NodeClaimList{}
	if err := c.kubeClient.List(ctx, nodeClaimList); err != nil {
		return unremovableInterfaces, liveAKSMachineNodeClaimNames, fmt.Errorf("listing NodeClaims for NIC GC: %w", err)
	}

	machineMode := options.FromContext(ctx).IsAKSMachineAPIMode()
	for _, nodeClaim := range nodeClaimList.Items {
		vmResourceName := instance.GenerateResourceName(nodeClaim.Name)
		providerID := strings.ToLower(nodeClaim.Status.ProviderID)
		vmBacked := providerID != "" && vmProviderIDs.Has(providerID)
		if providerID == "" && !machineMode {
			vmBacked = unremovableInterfaces.Has(vmResourceName)
		}
		unremovableInterfaces.Insert(vmResourceName)
		if !vmBacked {
			liveAKSMachineNodeClaimNames.Insert(nodeClaim.Name)
		}
	}
	return unremovableInterfaces, liveAKSMachineNodeClaimNames, nil
}

func (c *NetworkInterface) Reconcile(ctx context.Context) (reconciler.Result, error) {
	ctx = injection.WithControllerName(ctx, "networkinterface.garbagecollection")
	nics, err := c.vmInstanceProvider.ListNics(ctx)
	if err != nil {
		return reconciler.Result{}, fmt.Errorf("listing NICs: %w", err)
	}

	unremovableInterfaces, liveAKSMachineNodeClaimNames, err := c.populateUnremovableInterfaces(ctx)
	if err != nil {
		return reconciler.Result{}, fmt.Errorf("error listing resources needed to populate unremovable nics %w", err)
	}
	workqueue.ParallelizeUntil(ctx, 100, len(nics), func(i int) {
		nicName := lo.FromPtr(nics[i].Name)
		if unremovableInterfaces.Has(nicName) {
			return
		}

		// Temporary Machine API cleanup: Machine NIC names do not derive from NodeClaim names.
		// Karpenter puts the raw NodeClaim name in KarpenterAKSMachineNodeClaimTagKey on the Machine,
		// and AKS propagates Machine tags to its NIC. Preserve the NIC unless the live NodeClaim is
		// positively matched to a VM-path instance by provider ID, or by generated VM name while an
		// unlaunched NodeClaim is failing back to VM mode. This fail-closed distinction handles either
		// mode retrying the same NodeClaim after the other mode's create response was lost. An absent
		// Machine owner falls through to the existing orphan deletion path. Remove this block with
		// GetAKSMachineNICListQueryBuilder after the linked cloud-provider fix is fully deployed.
		// See https://github.com/kubernetes-sigs/cloud-provider-azure/pull/11267.
		nodeClaimName, hasMachineOwnerTag := nics[i].Tags[launchtemplate.KarpenterAKSMachineNodeClaimTagKey]
		_, hasMachinePoolTag := nics[i].Tags[launchtemplate.AKSManagedPoolNameTagKey]
		if hasMachinePoolTag && !hasMachineOwnerTag {
			// Pool membership identifies this as AKS-managed, but ownership is incomplete. Fail closed
			// rather than applying the VM name strategy to a Machine NIC with a missing owner tag.
			return
		}
		if hasMachineOwnerTag && liveAKSMachineNodeClaimNames.Has(lo.FromPtr(nodeClaimName)) {
			return
		}

		err := c.vmInstanceProvider.DeleteNic(ctx, nicName)
		if err != nil {
			log.FromContext(ctx).Error(err, "")
			return
		}

		log.FromContext(ctx).Info("garbage collected NIC", "nicName", nicName)
	})
	return reconciler.Result{
		RequeueAfter: NicGarbageCollectionInterval,
	}, nil
}

func (c *NetworkInterface) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("networkinterface.garbagecollection").
		WatchesRawSource(singleton.Source()).
		Complete(singleton.AsReconciler(c))
}
