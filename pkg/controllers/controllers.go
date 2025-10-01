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
	"k8s.io/client-go/kubernetes"

	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/events"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	nodeclaimgarbagecollection "github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/garbagecollection"
	nodeclasshash "github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/hash"
	nodeclassstatus "github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	nodeclasstermination "github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/termination"

	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/inplaceupdate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/kubernetesversion"
)

func NewControllers(
	ctx context.Context,
	mgr manager.Manager,
	kubeClient client.Client,
	recorder events.Recorder,
	cloudProvider cloudprovider.CloudProvider,
	vmInstanceProvider instance.VMProvider,
	aksMachineInstanceProvider instance.AKSMachineProvider,
	kubernetesVersionProvider kubernetesversion.KubernetesVersionProvider,
	nodeImageProvider imagefamily.NodeImageProvider,
	inClusterKubernetesInterface kubernetes.Interface,
	subnetsClient instance.SubnetsAPI,
) []controller.Controller {
	controllers := []controller.Controller{
		nodeclasshash.NewController(kubeClient),
		nodeclassstatus.NewController(kubeClient, kubernetesVersionProvider, nodeImageProvider, inClusterKubernetesInterface, subnetsClient),
		nodeclasstermination.NewController(kubeClient, recorder),

		nodeclaimgarbagecollection.NewCloudProviderInstances(kubeClient, cloudProvider),
		nodeclaimgarbagecollection.NewNetworkInterface(kubeClient, vmInstanceProvider),

		// TODO: nodeclaim tagging
		inplaceupdate.NewController(kubeClient, vmInstanceProvider, aksMachineInstanceProvider),
		status.NewController[*v1beta1.AKSNodeClass](kubeClient, mgr.GetEventRecorderFor("karpenter")),
	}
	return controllers
}
