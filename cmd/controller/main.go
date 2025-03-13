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
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers"
	"github.com/Azure/karpenter-provider-azure/pkg/operator"

	"sigs.k8s.io/karpenter/pkg/cloudprovider/metrics"
	corecontrollers "sigs.k8s.io/karpenter/pkg/controllers"
	coreoperator "sigs.k8s.io/karpenter/pkg/operator"
	corewebhooks "sigs.k8s.io/karpenter/pkg/webhooks"
)

func main() {
	ctx, op := operator.NewOperator(coreoperator.NewOperator())
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
		WithWebhooks(ctx, corewebhooks.NewWebhooks()...).
		WithControllers(ctx, controllers.NewControllers(
			ctx,
			op.Manager,
			op.GetClient(),
			op.EventRecorder,
			aksCloudProvider,
			op.InstanceProvider,
			op.ImageProvider,
		)...).
		//WithWebhooks(ctx, webhooks.NewWebhooks()...).
		Start(ctx, cloudProvider)
}
