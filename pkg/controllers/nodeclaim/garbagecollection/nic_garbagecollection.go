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

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
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

// populateUnremovableVMNICNames returns NICs that should not be deleted. Scoped to NICs associated with VM instances only (i.e., not AKS machine instances).
func (c *NetworkInterface) populateUnremovableVMNICNames(ctx context.Context, nodeClaimList *karpv1.NodeClaimList) (sets.Set[string], error) {
	unremovableInterfaces := sets.New[string]()
	vms, err := c.vmInstanceProvider.List(ctx)
	if err != nil {
		return unremovableInterfaces, fmt.Errorf("listing VMs: %w", err)
	}
	for _, vm := range vms {
		unremovableInterfaces.Insert(lo.FromPtr(vm.Name))
	}

	for _, nodeClaim := range nodeClaimList.Items {
		unremovableInterfaces.Insert(instance.GenerateResourceName(nodeClaim.Name))
	}
	return unremovableInterfaces, nil
}

func (c *NetworkInterface) Reconcile(ctx context.Context) (reconciler.Result, error) {
	ctx = injection.WithControllerName(ctx, "networkinterface.garbagecollection")
	nics, err := c.vmInstanceProvider.ListNics(ctx)
	if err != nil {
		return reconciler.Result{}, fmt.Errorf("listing NICs: %w", err)
	}
	nodeClaimList := &karpv1.NodeClaimList{}
	if err := c.kubeClient.List(ctx, nodeClaimList); err != nil {
		return reconciler.Result{}, fmt.Errorf("listing NodeClaims for NIC GC: %w", err)
	}

	unremovableInterfaces, err := c.populateUnremovableVMNICNames(ctx, nodeClaimList)
	if err != nil {
		return reconciler.Result{}, fmt.Errorf("error listing resources needed to populate unremovable nics %w", err)
	}
	nodeClaimNames := sets.New[string]()
	for _, nc := range nodeClaimList.Items {
		nodeClaimNames.Insert(nc.Name)
	}

	workqueue.ParallelizeUntil(ctx, 100, len(nics), func(i int) {
		nicName := lo.FromPtr(nics[i].Name)
		machineNodeClaimName, isMachineNIC := nics[i].Tags[launchtemplate.KarpenterAKSMachineNodeClaimTagKey]
		// Temporary. This is to help mitigate CCM bug where it incorrectly creates unused NICs by enabling garbage collection from Karpenter.
		// Should be safe to remove once https://github.com/kubernetes-sigs/cloud-provider-azure/pull/11267 is rolled out.
		// Machine NICs use their tag to name the owning NodeClaim.
		if isMachineNIC {
			// AKS machine instance NIC garbage collection here will delete NIC if no NodeClaim of the name tagged on NIC object exists.
			// That relies on the assumption that we tag AKS machine instance accordingly, and AKS Machine API correctly propagates it to underlying NIC.
			if !nodeClaimNames.Has(lo.FromPtr(machineNodeClaimName)) {
				err := c.vmInstanceProvider.DeleteNic(ctx, nicName)
				if err != nil {
					log.FromContext(ctx).Error(err, "")
					return
				}

				log.FromContext(ctx).Info("garbage collected NIC", "nicName", nicName)
			}

			return
		}

		// VM instance NIC garbage collection will delete NIC if no VM of the same name exists AND no NodeClaim of the matching name (i.e., once removing "aks-" prefix) exists.
		// That relies on naming assumptions between the three.
		if !unremovableInterfaces.Has(nicName) {
			err := c.vmInstanceProvider.DeleteNic(ctx, nicName)
			if err != nil {
				log.FromContext(ctx).Error(err, "")
				return
			}

			log.FromContext(ctx).Info("garbage collected NIC", "nicName", nicName)
		}
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
