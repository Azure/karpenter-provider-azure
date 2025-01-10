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

	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"knative.dev/pkg/logging"

	"github.com/awslabs/operatorpkg/singleton"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
)

const (
	NICGCControllerName    = "networkinterface.garbagecollection"
	NicReservationDuration = time.Second * 180
)

type NetworkInterfaceController struct {
	kubeClient       client.Client
	instanceProvider instance.Provider
	// A networkInterface is considered unremovable if it meets the following 3 criteria
	// 1: Reserved by NRP: When creating a nic and attempting to assign it to a vm, the nic will be reserved for that vm arm_resource_id for 180 seconds
	// 2: Belongs to a Nodeclaim: If a nodeclaim exists in the cluster we shouldn't attempt removing it
	// 3: Belongs to VM: If the VM Garbage Collection controller is removing a vm, we should not attempt removing it in this controller, and delegate that responsibility to the vm gc controller since deleting a successfully provisioned vm has delete options to also clean up the associated nic
	unremovableNics *cache.Cache
}

func NewNetworkInterfaceController(kubeClient client.Client, instanceProvider instance.Provider, unremovableNics *cache.Cache) *NetworkInterfaceController {
	return &NetworkInterfaceController{
		kubeClient:       kubeClient,
		instanceProvider: instanceProvider,
		unremovableNics:  unremovableNics,
	}
}

func (c *NetworkInterfaceController) Reconcile(ctx context.Context) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, NICGCControllerName)

	nodeClaimList := &karpv1.NodeClaimList{}
	if err := c.kubeClient.List(ctx, nodeClaimList); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing NodeClaims for NIC GC: %w", err)
	}

	// List all NICs from the instance provider, this List call will give us network interfaces that belong to karpenter
	nics, err := c.instanceProvider.ListNics(ctx)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("listing NICs: %w", err)
	}

	// resourceNames is the resource representation for each nodeclaim
	resourceNames := sets.New[string]()
	for _, nodeClaim := range nodeClaimList.Items {
		// Adjust the prefix as per the aks naming convention
		resourceNames.Insert(fmt.Sprintf("aks-%s", nodeClaim.Name))
	}

	errs := make([]error, len(nics))
	workqueue.ParallelizeUntil(ctx, 100, len(nics), func(i int) {
		nicName := lo.FromPtr(nics[i].Name)
		_, removableNic := c.unremovableNics.Get(nicName)
		noNodeclaimExistsForNIC := !resourceNames.Has(nicName)
		// The networkInterface is unremovable if its
		// A: Reserved by NRP
		// B: Belongs to a Nodeclaim
		// C: Belongs to VM
		if noNodeclaimExistsForNIC && removableNic {
			err := c.instanceProvider.DeleteNic(ctx, nicName)
			if sdkerrors.IsNicReservedForAnotherVM(err) {
				c.unremovableNics.Set(nicName, sdkerrors.NicReservedForAnotherVM, NicReservationDuration)
				return
			}
			if err != nil {
				errs[i] = err
				return
			}

			logging.FromContext(ctx).With("nic", nicName).Infof("garbage collected NIC")
		}
	})

	// requeue every 5 minutes, adjust for throttling?
	return reconcile.Result{
		Requeue:      true,
		RequeueAfter: time.Minute * 5,
	}, nil
}

func (c *NetworkInterfaceController) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named(NICGCControllerName).
		WatchesRawSource(singleton.Source()).
		Complete(singleton.AsReconciler(c))
}
