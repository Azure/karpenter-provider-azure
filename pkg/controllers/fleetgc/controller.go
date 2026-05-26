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

package fleetgc

import (
	"context"
	"time"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/awslabs/operatorpkg/reconciler"
	"github.com/awslabs/operatorpkg/singleton"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/fleet"
)

// Controller is the Fleet garbage collection controller.
// It periodically lists Fleets and deletes terminal/stuck ones.
type Controller struct {
	fleetClient   fleet.FleetAPI
	kubeClient    client.Client
	clusterName   string
	resourceGroup string
}

// NewController creates a new Fleet GC controller.
func NewController(
	fleetClient fleet.FleetAPI,
	kubeClient client.Client,
	clusterName, resourceGroup string,
) *Controller {
	return &Controller{
		fleetClient:   fleetClient,
		kubeClient:    kubeClient,
		clusterName:   clusterName,
		resourceGroup: resourceGroup,
	}
}

// Reconcile runs one GC cycle:
//  1. List all Fleets in resource group via fleetClient.NewListByResourceGroupPager()
//  2. Filter to Fleets tagged with karpenter.azure.com_managed-by=karpenter
//  3. Delete terminal Fleets (Succeeded/Failed/Canceled) older than 10 min
//  4. Delete stuck Fleets (Creating/Updating) older than 30 min
func (c *Controller) Reconcile(ctx context.Context) (reconciler.Result, error) {
	ctx = injection.WithControllerName(ctx, "fleet.garbagecollection")

	// TODO: implement GC logic per rules above
	return reconciler.Result{RequeueAfter: 5 * time.Minute}, nil
}

// Register registers the controller with the manager using the singleton pattern.
func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("fleet.garbagecollection").
		WatchesRawSource(singleton.Source()).
		Complete(singleton.AsReconciler(c))
}
