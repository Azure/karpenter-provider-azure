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

package azuregarbagecollection

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

var env *azure.Environment
var nodeClass *v1alpha2.AKSNodeClass
var nodePool *karpv1.NodePool

func TestGC(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	RunSpecs(t, "Azure Garbage Collection")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("gc", func() {
	It("should garbage collect network interfaces created by karpenter", func() {
		resourceGroup := os.Getenv("AZURE_RESOURCE_GROUP")
		subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
		vnetResourceGroup := os.Getenv("VNET_RESOURCE_GROUP")
		if vnetResourceGroup == "" {
			vnetResourceGroup = resourceGroup 
		}
		

		nicName := "orphan-nic"
		nodePoolKey := "test-nodepool"
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			Expect(err).ToNot(HaveOccurred())
		}
		interfacesClient, err := armnetwork.NewInterfacesClient(subscriptionID, cred, nil)
		Expect(err).ToNot(HaveOccurred())
		vnetClient, err := armnetwork.NewVirtualNetworksClient(subscriptionID, cred, nil) 
		Expect(err).ToNot(HaveOccurred())
		vnet := getVNET(env.Context, vnetClient, vnetResourceGroup)

		nodepoolKey := 	strings.ReplaceAll(karpv1.NodePoolLabelKey, "/", "_")
		poller, err := interfacesClient.BeginCreateOrUpdate(env.Context, resourceGroup, "orphan-nic", armnetwork.Interface{
			Location: to.Ptr(env.Region),
			Tags: map[string]*string{
					nodepoolKey: lo.ToPtr(nodePoolKey),
				},
			Properties: &armnetwork.InterfacePropertiesFormat{
				IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
					{
						Name: lo.ToPtr(nicName), 
						Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
							Primary: lo.ToPtr(true),
							Subnet: vnet.Properties.Subnets[0],
							PrivateIPAllocationMethod: lo.ToPtr(armnetwork.IPAllocationMethodDynamic),
						},
					},			
				},
				
			},
		}, nil)
		Expect(err).ToNot(HaveOccurred())
		resp, err := poller.PollUntilDone(env.Context, nil)
		// Log NIC
		fmt.Println(resp.Interface)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp).ToNot(BeNil())
		EventuallyExpectOrphanNicsToBeDeleted(env, resourceGroup, interfacesClient)
	})
})

func getVNET(ctx context.Context, client *armnetwork.VirtualNetworksClient, vnetRG string) *armnetwork.VirtualNetwork {
	pager := client.NewListPager(vnetRG, nil)
	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			Fail("failed to list virtual networks: " + err.Error())
		}
		if len(resp.VirtualNetworkListResult.Value) > 0 {
			return resp.VirtualNetworkListResult.Value[0]
		}
	}
	Fail("no virtual networks found in resource group: " + vnetRG)
	return &armnetwork.VirtualNetwork{} 
}

func EventuallyExpectOrphanNicsToBeDeleted(env *azure.Environment, resourceGroup string, nicClient *armnetwork.InterfacesClient) {
	GinkgoHelper()
	Eventually(func() bool {
		_, err := nicClient.Get(env.Context, resourceGroup, "orphan-nic", nil)
		if err != nil { 
			if strings.Contains(err.Error(), "ResourceNotFound") {	
				return true
			}
		}
		return false
	}, time.Minute*15, time.Second*15).Should(BeTrue())
}
