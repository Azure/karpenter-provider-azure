//go:build ccp

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

	"github.com/samber/lo"
	"go.uber.org/zap"
	"knative.dev/pkg/logging"

	// Injection stuff
	kubeclientinjection "knative.dev/pkg/client/injection/kube/client"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	knativeinjection "knative.dev/pkg/injection"
	secretinformer "knative.dev/pkg/injection/clients/namespacedkube/informers/core/v1/secret"
	kubeinformerfactory "knative.dev/pkg/injection/clients/namespacedkube/informers/factory"
	"knative.dev/pkg/webhook/certificates"

	altOperator "github.com/Azure/karpenter-provider-azure/pkg/alt/karpenter-core/pkg/operator"
	altwebhooks "github.com/Azure/karpenter-provider-azure/pkg/alt/karpenter-core/pkg/webhooks"
	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	controllers "github.com/Azure/karpenter-provider-azure/pkg/controllers"
	"github.com/Azure/karpenter-provider-azure/pkg/operator"
	"sigs.k8s.io/karpenter/pkg/cloudprovider/metrics"
	corecontrollers "sigs.k8s.io/karpenter/pkg/controllers"
)

func newWebhooks(ctx context.Context) []knativeinjection.ControllerConstructor {
	client := altOperator.GetCCPClient(ctx)
	ccpInformerFactory := kubeinformerfactory.Get(ctx)

	secretInformer := ccpInformerFactory.Core().V1().Secrets()
	ctx = context.WithValue(ctx, secretinformer.Key{}, secretInformer)

	logging.FromContext(ctx).Info("Starting horrible CCP informer")
	if err := controller.StartInformers(ctx.Done(), secretInformer.Informer()); err != nil {
		logging.FromContext(ctx).Fatalw("Failed to start horrible CCP informer", zap.Error(err))
	}

	return []knativeinjection.ControllerConstructor{
		func(ctx context.Context, watcher configmap.Watcher) *controller.Impl {
			ctx = context.WithValue(ctx, secretinformer.Key{}, secretInformer)
			ctx = context.WithValue(ctx, kubeclientinjection.Key{}, client)
			return certificates.NewController(ctx, watcher)
		},
		func(ctx context.Context, watcher configmap.Watcher) *controller.Impl {
			ctx = context.WithValue(ctx, secretinformer.Key{}, secretInformer)
			return altwebhooks.NewCRDConversionWebhook(ctx, watcher)
		},
	}
}

func main() {
	//ctx, op := operator.NewOperator(coreoperator.NewOperator())
	ctx, op := operator.NewOperator(altOperator.NewOperator())
	aksCloudProvider := cloudprovider.New(
		op.InstanceTypesProvider,
		op.InstanceProvider,
		op.EventRecorder,
		op.GetClient(),
		op.ImageProvider,
	)

	lo.Must0(op.AddHealthzCheck("cloud-provider", aksCloudProvider.LivenessProbe))
	cloudProvider := metrics.Decorate(aksCloudProvider)

	op.
		WithControllers(ctx, corecontrollers.NewControllers(
			ctx,
			op.Manager,
			op.Clock,
			op.GetClient(),
			op.EventRecorder,
			cloudProvider,
		)...).
		WithWebhooks(ctx, newWebhooks(ctx)...).
		WithControllers(ctx, controllers.NewControllers(
			ctx,
			op.Manager,
			op.GetClient(),
			op.EventRecorder,
			aksCloudProvider,
			op.InstanceProvider,
		)...).
		Start(ctx, cloudProvider)
}
