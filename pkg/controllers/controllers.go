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

package controllers

import (
	"context"

	"knative.dev/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/karpenter/pkg/operator/controller"

	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	nodeclaimgarbagecollection "github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/garbagecollection"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/inplaceupdate"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/project"
)

func NewControllers(ctx context.Context, kubeClient client.Client, cloudProvider *cloudprovider.CloudProvider, instanceProvider *instance.Provider) []controller.Controller {
	logging.FromContext(ctx).With("version", project.Version).Debugf("discovered version")
	opts := options.FromContext(ctx)

	controllers := []controller.Controller{
		nodeclaimgarbagecollection.NewController(kubeClient, cloudProvider),
		inplaceupdate.NewController(kubeClient, instanceProvider, opts),
	}
	return controllers
}
