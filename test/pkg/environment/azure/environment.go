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
	"fmt"
	"os"
	"testing"

	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v4"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/test/azure"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"
)

func init() {
	karpv1.NormalizedLabels = lo.Assign(karpv1.NormalizedLabels, map[string]string{"topology.disk.csi.azure.com/zone": v1.LabelTopologyZone})
	coretest.DefaultImage = "mcr.microsoft.com/oss/kubernetes/pause:3.6"
}

const (
	CiliumAgentNotReadyTaint    = "node.cilium.io/agent-not-ready"
	EphemeralInitContainerImage = "alpine"
)

type Environment struct {
	*common.Environment

	NodeResourceGroup    string
	Region               string
	SubscriptionID       string
	VNETResourceGroup    string
	ACRName              string
	ClusterName          string
	ClusterResourceGroup string

	tracker *azure.Tracker

	// These should be unexported and access should be through the Environment methods
	// Any create calls should make sure they also register the created resources with the Environment's tracker
	// to ensure they are cleaned up after the test.
	vmClient             *armcompute.VirtualMachinesClient
	vnetClient           *armnetwork.VirtualNetworksClient
	subnetClient         *armnetwork.SubnetsClient
	interfacesClient     *armnetwork.InterfacesClient
	managedClusterClient *containerservice.ManagedClustersClient
}

func readEnv(name string) string {
	value, exists := os.LookupEnv(name)
	if !exists {
		panic(fmt.Sprintf("Environment variable %s is not set", name))
	}
	if value == "" {
		panic(fmt.Sprintf("Environment variable %s is set to an empty string", name))
	}
	return value
}

func NewEnvironment(t *testing.T) *Environment {
	azureEnv := &Environment{
		Environment:          common.NewEnvironment(t),
		SubscriptionID:       readEnv("AZURE_SUBSCRIPTION_ID"),
		ClusterName:          readEnv("AZURE_CLUSTER_NAME"),
		ClusterResourceGroup: readEnv("AZURE_RESOURCE_GROUP"),
		ACRName:              readEnv("AZURE_ACR_NAME"),
		Region:               lo.Ternary(os.Getenv("AZURE_LOCATION") == "", "westus2", os.Getenv("AZURE_LOCATION")),
		tracker:              azure.NewTracker(),
	}

	defaultNodeRG := fmt.Sprintf("MC_%s_%s_%s", azureEnv.ClusterResourceGroup, azureEnv.ClusterName, azureEnv.Region)
	azureEnv.VNETResourceGroup = lo.Ternary(os.Getenv("VNET_RESOURCE_GROUP") == "", defaultNodeRG, os.Getenv("VNET_RESOURCE_GROUP"))
	azureEnv.NodeResourceGroup = defaultNodeRG

	cred := lo.Must(azidentity.NewDefaultAzureCredential(nil))
	azureEnv.vmClient = lo.Must(armcompute.NewVirtualMachinesClient(azureEnv.SubscriptionID, cred, nil))
	azureEnv.vnetClient = lo.Must(armnetwork.NewVirtualNetworksClient(azureEnv.SubscriptionID, cred, nil))
	azureEnv.subnetClient = lo.Must(armnetwork.NewSubnetsClient(azureEnv.SubscriptionID, cred, nil))
	azureEnv.interfacesClient = lo.Must(armnetwork.NewInterfacesClient(azureEnv.SubscriptionID, cred, nil))
	azureEnv.managedClusterClient = lo.Must(containerservice.NewManagedClustersClient(azureEnv.SubscriptionID, cred, nil))
	return azureEnv
}

func (env *Environment) DefaultAKSNodeClass() *v1beta1.AKSNodeClass {
	nodeClass := test.AKSNodeClass()
	return nodeClass
}

func (env *Environment) AZLinuxNodeClass() *v1beta1.AKSNodeClass {
	nodeClass := env.DefaultAKSNodeClass()
	nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
	return nodeClass
}
