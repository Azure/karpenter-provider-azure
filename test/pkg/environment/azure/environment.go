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

package azure

import (
	"os"
	"testing"

	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"
)

func init() {
	// TODO: should have core1beta1.NormalizedLabels too?
	karpv1.NormalizedLabels = lo.Assign(karpv1.NormalizedLabels, map[string]string{"topology.disk.csi.azure.com/zone": v1.LabelTopologyZone})
}

const (
	WindowsDefaultImage      = "mcr.microsoft.com/oss/kubernetes/pause:3.9"
	CiliumAgentNotReadyTaint = "node.cilium.io/agent-not-ready"
)

type Environment struct {
	*common.Environment
	AzureClients
	Vars
}

type Vars struct {
	NodeResourceGroup string
	Region            string
	SubscriptionID    string
	VNETResourceGroup string
	ACRName           string
	ClusterName       string
}
type AzureClients struct {
	VNETClient       *armnetwork.VirtualNetworksClient
	InterfacesClient *armnetwork.InterfacesClient
}

func NewEnvironment(t *testing.T) *Environment {
	env := common.NewEnvironment(t)
	azureEnv := &Environment{
		Environment: env,
	}
	azureEnv.NodeResourceGroup = os.Getenv("AZURE_RESOURCE_GROUP_MC")
	azureEnv.SubscriptionID = os.Getenv("AZURE_SUBSCRIPTION_ID")
	azureEnv.VNETResourceGroup = os.Getenv("VNET_RESOURCE_GROUP")
	if azureEnv.VNETResourceGroup == "" {
		azureEnv.VNETResourceGroup = azureEnv.NodeResourceGroup
	}
	azureEnv.ClusterName = os.Getenv("AZURE_CLUSTER_NAME")
	azureEnv.ACRName = os.Getenv("ACR_NAME")
	azureEnv.Region = os.Getenv("AZURE_LOCATION")

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic(err)
	}

	interfacesClient, err := armnetwork.NewInterfacesClient(azureEnv.SubscriptionID, cred, nil)
	if err != nil {
		panic(err)
	}
	vnetClient, err := armnetwork.NewVirtualNetworksClient(azureEnv.SubscriptionID, cred, nil)
	if err != nil {
		panic(err)
	}
	azureEnv.VNETClient = vnetClient
	azureEnv.InterfacesClient = interfacesClient
	return azureEnv
}

func (env *Environment) DefaultAKSNodeClass() *v1alpha2.AKSNodeClass {
	nodeClass := test.AKSNodeClass()
	return nodeClass
}

func (env *Environment) AZLinuxNodeClass() *v1alpha2.AKSNodeClass {
	nodeClass := env.DefaultAKSNodeClass()
	nodeClass.Spec.ImageFamily = lo.ToPtr(v1alpha2.AzureLinuxImageFamily)
	return nodeClass
}
