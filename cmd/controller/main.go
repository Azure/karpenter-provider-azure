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
	"github.com/Azure/karpenter-provider-azure/pkg/operator"
	"github.com/go-logr/zapr"
	"github.com/samber/lo"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"sigs.k8s.io/karpenter/pkg/cloudprovider/metrics"
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
	)

	lo.Must0(op.AddHealthzCheck("cloud-provider", aksCloudProvider.LivenessProbe))

	cloudProvider := metrics.Decorate(aksCloudProvider)
	clusterState := state.NewCluster(op.Clock, op.GetClient(), cloudProvider)

	op.
		WithControllers(ctx, corecontrollers.NewControllers(
			ctx,
			op.Manager,
			op.Clock,
			op.GetClient(),
			op.EventRecorder,
			cloudProvider,
			clusterState,
		)...).
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
