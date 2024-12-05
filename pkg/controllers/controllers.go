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
	"github.com/awslabs/operatorpkg/status"

	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/events"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	nodeclaimgarbagecollection "github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/garbagecollection"
	nodeclasshash "github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/hash"
	nodeclassstatus "github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	nodeclasstermination "github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/termination"

	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/inplaceupdate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
)

func NewControllers(ctx context.Context, mgr manager.Manager, kubeClient client.Client, recorder events.Recorder,
	cloudProvider cloudprovider.CloudProvider, instanceProvider instance.Provider) []controller.Controller {
	controllers := []controller.Controller{
		nodeclasshash.NewController(kubeClient),
		nodeclassstatus.NewController(kubeClient),
		nodeclasstermination.NewController(kubeClient, recorder),
		nodeclaimgarbagecollection.NewController(kubeClient, cloudProvider),
		// TODO: nodeclaim tagging
		inplaceupdate.NewController(kubeClient, instanceProvider),
		status.NewController[*v1alpha2.AKSNodeClass](kubeClient, mgr.GetEventRecorderFor("karpenter")),
	}
	return controllers
}
