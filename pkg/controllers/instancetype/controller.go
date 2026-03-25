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

package instancetype

import (
	"context"
	"time"

	"github.com/awslabs/operatorpkg/reconciler"
	"github.com/awslabs/operatorpkg/singleton"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	instancetypeprovider "github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
)

const (
	InstanceTypesRefreshInterval = 12 * time.Hour
)

// Controller periodically updates the instance types cache by fetching
// instance type data from Azure. This removes the need to fetch instance
// types on-demand during List calls.
type Controller struct {
	instanceTypeProvider instancetypeprovider.Provider
}

func NewController(instanceTypeProvider instancetypeprovider.Provider) *Controller {
	return &Controller{
		instanceTypeProvider: instanceTypeProvider,
	}
}

func (c *Controller) Reconcile(ctx context.Context) (reconciler.Result, error) {
	ctx = injection.WithControllerName(ctx, "instancetype")

	if err := c.instanceTypeProvider.UpdateInstanceTypes(ctx); err != nil {
		log.FromContext(ctx).Error(err, "updating instance types")
		return reconciler.Result{}, err
	}
	log.FromContext(ctx).V(1).Info("updated instance types")
	return reconciler.Result{RequeueAfter: InstanceTypesRefreshInterval}, nil
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("instancetype").
		WatchesRawSource(singleton.Source()).
		Complete(singleton.AsReconciler(c))
}
