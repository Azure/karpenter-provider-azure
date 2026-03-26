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

package quota

import (
	"context"
	"time"

	"github.com/awslabs/operatorpkg/reconciler"
	"github.com/awslabs/operatorpkg/singleton"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/quota"
)

const (
	RefreshInterval = 10 * time.Minute
)

type Controller struct {
	quotaProvider quota.Provider
}

func NewController(quotaProvider quota.Provider) *Controller {
	return &Controller{
		quotaProvider: quotaProvider,
	}
}

func (c *Controller) Reconcile(ctx context.Context) (reconciler.Result, error) {
	ctx = injection.WithControllerName(ctx, "quota")

	if err := c.quotaProvider.Update(ctx); err != nil {
		log.FromContext(ctx).Error(err, "updating quota usages")
		return reconciler.Result{}, err
	}
	log.FromContext(ctx).V(1).Info("updated quota usages")
	return reconciler.Result{RequeueAfter: RefreshInterval}, nil
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("quota").
		WatchesRawSource(singleton.Source()).
		Complete(singleton.AsReconciler(c))
}
