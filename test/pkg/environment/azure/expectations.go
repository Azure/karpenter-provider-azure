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
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v4"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

func (env *Environment) GetVM(nodeName string) armcompute.VirtualMachine {
	GinkgoHelper()
	node := env.Environment.GetNode(nodeName)
	return env.GetVMByName(env.ExpectParsedProviderID(node.Spec.ProviderID))
}

func (env *Environment) GetVMSKU(nodeName string) string {
	GinkgoHelper()
	vm := env.GetVM(nodeName)
	Expect(vm.Properties).ToNot(BeNil())
	Expect(vm.Properties.HardwareProfile).ToNot(BeNil())
	Expect(vm.Properties.HardwareProfile.VMSize).ToNot(BeNil())
	return string(*vm.Properties.HardwareProfile.VMSize)
}

func (env *Environment) GetVMByName(vmName string) armcompute.VirtualMachine {
	GinkgoHelper()
	response, err := env.vmClient.Get(env.Context, env.NodeResourceGroup, vmName, nil)
	Expect(err).ToNot(HaveOccurred())
	return response.VirtualMachine
}

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
	GinkgoHelper()
	upgradeProfile, err := env.AKSManagedClusterClient.GetUpgradeProfile(env.Context, env.ClusterResourceGroup, env.ClusterName, nil)
	Expect(err).ToNot(HaveOccurred())
	return upgradeProfile.ManagedClusterUpgradeProfile.Properties.ControlPlaneProfile.Upgrades
}

func (env *Environment) ExpectSuccessfulUpgradeOfManagedCluster(kubernetesUpgradeVersion string) containerservice.ManagedCluster {
	GinkgoHelper()
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

func (env *Environment) ExpectParsedProviderID(providerID string) string {
	GinkgoHelper()
	providerIDSplit := strings.Split(providerID, "/")
	Expect(len(providerIDSplit)).ToNot(Equal(0))
	return providerIDSplit[len(providerIDSplit)-1]
}

func (env *Environment) K8sVersion() string {
	GinkgoHelper()
	return env.K8sVersionWithOffset(0)
}

func (env *Environment) K8sVersionWithOffset(offset int) string {
	GinkgoHelper()
	serverVersion, err := env.KubeClient.Discovery().ServerVersion()
	Expect(err).To(BeNil())
	minorVersion, err := strconv.Atoi(strings.TrimSuffix(serverVersion.Minor, "+"))
	Expect(err).To(BeNil())
	// Choose a minor version one lesser than the server's minor version. This ensures that we choose an AMI for
	// this test that wouldn't be selected as Karpenter's SSM default (therefore avoiding false positives), and also
	// ensures that we aren't violating version skew.
	return fmt.Sprintf("%s.%d", serverVersion.Major, minorVersion-offset)
}

func (env *Environment) K8sMinorVersion() int {
	GinkgoHelper()
	version, err := strconv.Atoi(strings.Split(env.K8sVersion(), ".")[1])
	Expect(err).ToNot(HaveOccurred())
	return version
}

func (env *Environment) GetVMExtensions(vmName string) ([]*armcompute.VirtualMachineExtension, error) {
	GinkgoHelper()
	response, err := env.vmExtensionsClient.List(env.Context, env.NodeResourceGroup, vmName, nil)
	Expect(err).ToNot(HaveOccurred())
	return response.VirtualMachineExtensionsListResult.Value, nil
}
