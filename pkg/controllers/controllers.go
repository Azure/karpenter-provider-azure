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

	"github.com/awslabs/operatorpkg/controller"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"sigs.k8s.io/controller-runtime/pkg/client"

	nodeclaimgarbagecollection "github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/garbagecollection"
	nodeclassstatus "github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"

	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/inplaceupdate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
)

func NewControllers(ctx context.Context, kubeClient client.Client, cloudProvider cloudprovider.CloudProvider, instanceProvider instance.Provider) []controller.Controller {
	controllers := []controller.Controller{
		nodeclaimgarbagecollection.NewController(kubeClient, cloudProvider),
		inplaceupdate.NewController(kubeClient, instanceProvider),
		nodeclassstatus.NewController(kubeClient),
	}
	return controllers
}
