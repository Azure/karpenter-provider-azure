//go:build !ccp

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

package main

import (
	"context"
	"time"

	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/proactivescaleup"
	"github.com/Azure/karpenter-provider-azure/pkg/operator"
	"github.com/go-logr/zapr"
	"github.com/samber/lo"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"sigs.k8s.io/karpenter/pkg/cloudprovider/metrics"
	"sigs.k8s.io/karpenter/pkg/cloudprovider/overlay"
	corecontrollers "sigs.k8s.io/karpenter/pkg/controllers"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	coreoperator "sigs.k8s.io/karpenter/pkg/operator"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
	"sigs.k8s.io/karpenter/pkg/operator/logging"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
)

func main() {
	ctx := injection.WithOptionsOrDie(context.Background(), coreoptions.Injectables...)
	logger := zapr.NewLogger(logging.NewLogger(ctx, "controller"))
	lo.Must0(operator.WaitForCRDs(ctx, 2*time.Minute, ctrl.GetConfigOrDie(), logger), "failed waiting for CRDs")

	ctx, op := operator.NewOperator(coreoperator.NewOperator())

	// TODO: Consider also dumping at least some core options
	logger.V(0).Info("Initial options", "options", options.FromContext(ctx).String())

	aksCloudProvider := cloudprovider.New(
		op.InstanceTypesProvider,
		op.VMInstanceProvider,
		op.EventRecorder,
		op.GetClient(),
		op.ImageProvider,
		op.InstanceTypeStore,
	)

	lo.Must0(op.AddHealthzCheck("cloud-provider", aksCloudProvider.LivenessProbe))

	overlayUndecoratedCloudProvider := metrics.Decorate(aksCloudProvider)
	cloudProvider := overlay.Decorate(overlayUndecoratedCloudProvider, op.GetClient(), op.InstanceTypeStore)
	clusterState := state.NewCluster(op.Clock, op.GetClient(), cloudProvider)

	// Create core controllers and get the provisioner
	coreControllerList, provisioner := corecontrollers.NewControllers(
		ctx,
		op.Manager,
		op.Clock,
		op.GetClient(),
		op.EventRecorder,
		cloudProvider,
		overlayUndecoratedCloudProvider,
		clusterState,
		op.InstanceTypeStore,
	)

	// Set up proactive scale-up injector if enabled
	if opts := options.FromContext(ctx); opts != nil && opts.ProactiveScaleupEnabled {
		injector := proactivescaleup.NewInjector(op.GetClient())
		provisioner.SetPodInjector(injector)
		logger.V(0).Info("Proactive scale-up enabled with pod injection")
	}

	op.
		WithControllers(ctx, coreControllerList...).
		WithControllers(ctx, controllers.NewControllers(
			ctx,
			op.Manager,
			op.GetClient(),
			op.EventRecorder,
			aksCloudProvider,
			op.VMInstanceProvider,
			// TODO: still need to refactor ImageProvider side of things.
			op.KubernetesVersionProvider,
			op.ImageProvider,
			op.InClusterKubernetesInterface,
			op.AZClient.SubnetsClient(),
		)...).
		Start(ctx)
}
