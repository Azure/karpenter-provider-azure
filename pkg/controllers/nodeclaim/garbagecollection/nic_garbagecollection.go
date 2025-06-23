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

	"github.com/awslabs/operatorpkg/singleton"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
)

const (
	NicReservationDuration = time.Second * 180
	// We set this interval at 5 minutes, as thats how often our NRP limits are reset.
	// See: https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/request-limits-and-throttling#network-throttling
	NicGarbageCollectionInterval = time.Minute * 5
)

type NetworkInterface struct {
	kubeClient       client.Client
	instanceProvider instance.Provider
}

func NewNetworkInterface(kubeClient client.Client, instanceProvider instance.Provider) *NetworkInterface {
	return &NetworkInterface{
		kubeClient:       kubeClient,
		instanceProvider: instanceProvider,
	}
}

func (c *NetworkInterface) populateUnremovableInterfaces(ctx context.Context) (sets.Set[string], error) {
	unremovableInterfaces := sets.New[string]()
	vms, err := c.instanceProvider.List(ctx)
	if err != nil {
		return unremovableInterfaces, fmt.Errorf("listing VMs: %w", err)
	}
	for _, vm := range vms {
		unremovableInterfaces.Insert(lo.FromPtr(vm.Name))
	}
	nodeClaimList := &karpv1.NodeClaimList{}
	if err := c.kubeClient.List(ctx, nodeClaimList); err != nil {
		return unremovableInterfaces, fmt.Errorf("listing NodeClaims for NIC GC: %w", err)
	}

	for _, nodeClaim := range nodeClaimList.Items {
		unremovableInterfaces.Insert(instance.GenerateResourceName(nodeClaim.Name))
	}
	return unremovableInterfaces, nil
}

func (c *NetworkInterface) Reconcile(ctx context.Context) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, "networkinterface.garbagecollection")
	nics, err := c.instanceProvider.ListNics(ctx)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("listing NICs: %w", err)
	}

	unremovableInterfaces, err := c.populateUnremovableInterfaces(ctx)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error listing resources needed to populate unremovable nics %w", err)
	}
	workqueue.ParallelizeUntil(ctx, 100, len(nics), func(i int) {
		nicName := lo.FromPtr(nics[i].Name)
		if !unremovableInterfaces.Has(nicName) {
			err := c.instanceProvider.DeleteNic(ctx, nicName)
			if err != nil {
				log.FromContext(ctx).Error(err, "")
				return
			}

			log.FromContext(ctx).Info("garbage collected NIC", "nicName", nicName)
		}
	})
	return reconcile.Result{
		RequeueAfter: NicGarbageCollectionInterval,
	}, nil
}

func (c *NetworkInterface) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("networkinterface.garbagecollection").
		WatchesRawSource(singleton.Source()).
		Complete(singleton.AsReconciler(c))
}
