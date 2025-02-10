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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
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
		vnet, err := getVNET(env.Context, env.VNETClient, env.VNETResourceGroup)
		Expect(err).ToNot(HaveOccurred())
		nicName := "orphan-nic"

		err = createOrphanNIC(env.Context, env.InterfacesClient, env.NodeResourceGroup, env.Region, nicName, vnet.Properties.Subnets[0])
		Expect(err).ToNot(HaveOccurred())
		EventuallyExpectOrphanNicsToBeDeleted(env, env.NodeResourceGroup, nicName, env.InterfacesClient)
	})
})

func getVNET(ctx context.Context, client *armnetwork.VirtualNetworksClient, vnetRG string) (*armnetwork.VirtualNetwork, error) {
	pager := client.NewListPager(vnetRG, nil)
	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list virtual networks: %w", err)
		}
		if len(resp.VirtualNetworkListResult.Value) > 0 {
			return resp.VirtualNetworkListResult.Value[0], nil
		}
	}
	return nil, fmt.Errorf("no virtual networks found in resource group: %s", vnetRG)
}

func createOrphanNIC(ctx context.Context, client *armnetwork.InterfacesClient, resourceGroup, region, nicName string, subnet *armnetwork.Subnet) error {
	nodepoolKey := strings.ReplaceAll(karpv1.NodePoolLabelKey, "/", "_")
	poller, err := client.BeginCreateOrUpdate(ctx, resourceGroup, nicName, armnetwork.Interface{
		Location: to.Ptr(region),
		Tags:     map[string]*string{nodepoolKey: lo.ToPtr("default")},
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
				{
					Name: lo.ToPtr(nicName),
					Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
						Primary:                   lo.ToPtr(true),
						Subnet:                    subnet,
						PrivateIPAllocationMethod: lo.ToPtr(armnetwork.IPAllocationMethodDynamic),
					},
				},
			},
		},
	}, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

func EventuallyExpectOrphanNicsToBeDeleted(env *azure.Environment, resourceGroup, nicName string, nicClient *armnetwork.InterfacesClient) {
	GinkgoHelper()
	Eventually(func() bool {
		_, err := nicClient.Get(env.Context, resourceGroup, nicName, nil)
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == 404 {
			return true
		}
		return false
	}, 15*time.Minute, 15*time.Second).Should(BeTrue())
}
