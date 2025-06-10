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
		pager := env.interfacesClient.NewListPager(env.NodeResourceGroup, nil)
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
	poller, err := env.interfacesClient.BeginCreateOrUpdate(env.Context, env.NodeResourceGroup, lo.FromPtr(networkInterface.Name), networkInterface, nil)
	Expect(err).ToNot(HaveOccurred())
	resp, err := poller.PollUntilDone(env.Context, nil)
	Expect(err).ToNot(HaveOccurred())
	env.tracker.Add(lo.FromPtr(resp.Interface.ID), func() error {
		deletePoller, err := env.interfacesClient.BeginDelete(env.Context, env.NodeResourceGroup, lo.FromPtr(networkInterface.Name), nil)
		if err != nil {
			return fmt.Errorf("failed to delete network interface %s: %w", lo.FromPtr(networkInterface.Name), err)
		}
		_, err = deletePoller.PollUntilDone(env.Context, nil)
		if err != nil {
			return fmt.Errorf("failed to delete network interface %s: %w", lo.FromPtr(networkInterface.Name), err)
		}
		return nil
	})
}

func (env *Environment) ExpectSuccessfulGetOfAvailableKubernetesVersionUpgradesForManagedCluster() []*containerservice.ManagedClusterPoolUpgradeProfileUpgradesItem {
	GinkgoHelper()
	upgradeProfile, err := env.managedClusterClient.GetUpgradeProfile(env.Context, env.ClusterResourceGroup, env.ClusterName, nil)
	Expect(err).ToNot(HaveOccurred())
	return upgradeProfile.ManagedClusterUpgradeProfile.Properties.ControlPlaneProfile.Upgrades
}

func (env *Environment) ExpectSuccessfulUpgradeOfManagedCluster(kubernetesUpgradeVersion string) containerservice.ManagedCluster {
	GinkgoHelper()
	managedClusterResponse, err := env.managedClusterClient.Get(env.Context, env.ClusterResourceGroup, env.ClusterName, nil)
	Expect(err).ToNot(HaveOccurred())
	managedCluster := managedClusterResponse.ManagedCluster

	// See documentation for KubernetesVersion (client specified) and CurrentKubernetesVersion (version under use):
	// https://learn.microsoft.com/en-us/rest/api/aks/managed-clusters/get?view=rest-aks-2025-01-01&tabs=HTTP
	By(fmt.Sprintf("upgrading from kubernetes version %s to kubernetes version %s", *managedCluster.Properties.CurrentKubernetesVersion, kubernetesUpgradeVersion))
	managedCluster.Properties.KubernetesVersion = &kubernetesUpgradeVersion
	// Note that this is an update not a create so we don't need to add it to the tracker
	poller, err := env.managedClusterClient.BeginCreateOrUpdate(env.Context, env.ClusterResourceGroup, env.ClusterName, managedCluster, nil)
	Expect(err).ToNot(HaveOccurred())
	res, err := poller.PollUntilDone(env.Context, nil)
	Expect(err).ToNot(HaveOccurred())
	return res.ManagedCluster
}

func (env *Environment) ExpectParsedProviderID(providerID string) string {
	GinkgoHelper()
	providerIDSplit := strings.Split(providerID, "/")
	Expect(len(providerIDSplit)).ToNot(Equal(0))
	return providerIDSplit[len(providerIDSplit)-1]
}

func (env *Environment) ExpectCreatedSubnet(vnetName string, subnet *armnetwork.Subnet) {
	GinkgoHelper()
	poller, err := env.subnetClient.BeginCreateOrUpdate(env.Context, env.NodeResourceGroup, vnetName, lo.FromPtr(subnet.Name), *subnet, nil)
	Expect(err).ToNot(HaveOccurred())
	resp, err := poller.PollUntilDone(env.Context, nil)
	Expect(err).ToNot(HaveOccurred())
	*subnet = resp.Subnet
	env.tracker.Add(lo.FromPtr(resp.ID), func() error {
		deletePoller, err := env.subnetClient.BeginDelete(env.Context, env.NodeResourceGroup, vnetName, lo.FromPtr(subnet.Name), nil)
		if err != nil {
			return fmt.Errorf("failed to delete subnet %s: %w", lo.FromPtr(subnet.Name), err)
		}
		_, err = deletePoller.PollUntilDone(env.Context, nil)
		if err != nil {
			return fmt.Errorf("failed to delete subnet %s: %w", lo.FromPtr(subnet.Name), err)
		}
		return nil
	})
}
