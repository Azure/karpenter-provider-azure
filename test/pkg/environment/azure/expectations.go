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
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v4"
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
	vnet, err := firstVNETInRG(env.Context, env.VNETClient, env.VNETResourceGroup)
	Expect(err).ToNot(HaveOccurred())
	return vnet.Properties.Subnets[0]
}

// This returns the first vnet we find in the resource group, works for managed vnet, it hasn't been tested on custom vnet.
func firstVNETInRG(ctx context.Context, client *armnetwork.VirtualNetworksClient, vnetRG string) (*armnetwork.VirtualNetwork, error) {
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

func (env *Environment) ExpectSuccessfulGetOfAvailableKubernetesVersionUpgradesForManagedCluster() []*containerservice.ManagedClusterPoolUpgradeProfileUpgradesItem {
	upgradeProfile, err := env.AKSManagedClusterClient.GetUpgradeProfile(env.Context, env.ClusterResourceGroup, env.ClusterName, nil)
	Expect(err).ToNot(HaveOccurred())
	return upgradeProfile.ManagedClusterUpgradeProfile.Properties.ControlPlaneProfile.Upgrades
}

func (env *Environment) ExpectSuccessfulUpgradeOfManagedCluster(kubernetesUpgradeVersion string) containerservice.ManagedCluster {
	managedClusterResponse, err := env.AKSManagedClusterClient.Get(env.Context, env.ClusterResourceGroup, env.ClusterName, nil)
	Expect(err).ToNot(HaveOccurred())
	managedCluster := managedClusterResponse.ManagedCluster

	// See documentation for KubernetesVersion (client specified) and CurrentKubernetesVersion (version under use):
	// https://learn.microsoft.com/en-us/rest/api/aks/managed-clusters/get?view=rest-aks-2025-01-01&tabs=HTTP
	By(fmt.Sprintf("upgrading from kubernetes version %s to kubernetes version %s", *managedCluster.Properties.CurrentKubernetesVersion, kubernetesUpgradeVersion))
	managedCluster.Properties.KubernetesVersion = &kubernetesUpgradeVersion
	poller, err := env.AKSManagedClusterClient.BeginCreateOrUpdate(env.Context, env.ClusterResourceGroup, env.ClusterName, managedCluster, nil)
	Expect(err).ToNot(HaveOccurred())
	res, err := poller.PollUntilDone(env.Context, nil)
	Expect(err).ToNot(HaveOccurred())
	return res.ManagedCluster
}
