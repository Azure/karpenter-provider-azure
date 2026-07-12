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
	"k8s.io/utils/clock"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/quota"
)

const (
	RefreshInterval = 10 * time.Minute
	// MaxStaleness is how long we keep using cached quota data after the last successful refresh.
	// If the quota API fails for longer than this, we clear the cache so HasQuotaFor fails open
	// for all sizes rather than filtering based on outdated information.
	MaxStaleness = RefreshInterval + 5*time.Minute
)

type Controller struct {
	quotaProvider        quota.Provider
	clock                clock.PassiveClock
	lastSuccessfulUpdate time.Time
}

func NewController(quotaProvider quota.Provider, clk clock.PassiveClock) *Controller {
	return &Controller{
		quotaProvider: quotaProvider,
		clock:         clk,
	}
}

func (c *Controller) Reconcile(ctx context.Context) (reconciler.Result, error) {
	ctx = injection.WithControllerName(ctx, "quota")

	if err := c.quotaProvider.Update(ctx); err != nil {
		log.FromContext(ctx).Error(err, "updating quota usages")
		// If data is too stale, clear it so HasQuotaFor fails open for all sizes
		if !c.lastSuccessfulUpdate.IsZero() && c.clock.Since(c.lastSuccessfulUpdate) > MaxStaleness {
			log.FromContext(ctx).Info("quota data is stale beyond max staleness, resetting to fail open",
				"lastSuccessfulUpdate", c.lastSuccessfulUpdate,
				"maxStaleness", MaxStaleness)
			c.quotaProvider.Reset()
		}
		return reconciler.Result{}, err
	}
	c.lastSuccessfulUpdate = c.clock.Now()
	return reconciler.Result{RequeueAfter: RefreshInterval}, nil
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("quota").
		WatchesRawSource(singleton.Source()).
		Complete(singleton.AsReconciler(c))
}
