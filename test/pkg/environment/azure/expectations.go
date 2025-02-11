package azure

import (
	"fmt" 
	"context"
	"strings"
	"time"
	
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

func (env *Environment) EventuallyExpectKarpenterNicsToBeDeleted() {
	GinkgoHelper()
	Eventually(func() bool {
		pager := env.InterfacesClient.NewListPager(env.NodeResourceGroup, nil)
		for pager.More() {
			resp, err := pager.NextPage(env.Context)
			if err != nil {
				return false
			}

			for _, nic := range resp.Value {
				if nic.Tags != nil {
					if _, exists := nic.Tags[strings.ReplaceAll(karpv1.NodePoolLabelKey, "/", "_")]; exists {
						return false
					}
				}
			}
		}
		return true
	}).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(BeTrue(), "Expected all orphan NICs to be deleted")
}

func (env *Environment) ExpectCreatedInterface(networkInterface armnetwork.Interface) {
	GinkgoHelper()
	poller, err := env.InterfacesClient.BeginCreateOrUpdate(env.Context, env.NodeResourceGroup, lo.FromPtr(networkInterface.Name), networkInterface, nil) 
	Expect(err).ToNot(HaveOccurred())	
	_, err = poller.PollUntilDone(env.Context, nil)
	Expect(err).ToNot(HaveOccurred())
}

func (env *Environment) GetClusterSubnet() *armnetwork.Subnet {
	GinkgoHelper()
		vnet, err := getVNET(env.Context, env.VNETClient, env.VNETResourceGroup)
		Expect(err).ToNot(HaveOccurred())
		return vnet.Properties.Subnets[0]
}

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
