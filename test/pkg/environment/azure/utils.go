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

	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

func (env *Environment) GetVM(nodeName string) armcompute.VirtualMachine {
	GinkgoHelper()
	node := env.GetNode(nodeName)
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

func (env *Environment) SimulateVMEviction(nodeName string) {
	GinkgoHelper()
	vm := env.GetVM(nodeName)
	Expect(vm.Name).ToNot(BeNil())
	vmName := lo.FromPtr(vm.Name)

	_, err := env.vmClient.SimulateEviction(env.Context, env.NodeResourceGroup, vmName, &armcompute.VirtualMachinesClientSimulateEvictionOptions{})
	Expect(err).ToNot(HaveOccurred())
}

func (env *Environment) GetNetworkInterface(nicName string) armnetwork.Interface {
	GinkgoHelper()
	nic, err := env.interfacesClient.Get(env.Context, env.NodeResourceGroup, nicName, nil)
	Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Failed to get NIC %s in resource group %s", nicName, env.NodeResourceGroup))
	return nic.Interface
}

func (env *Environment) GetClusterVNET() *armnetwork.VirtualNetwork {
	GinkgoHelper()
	vnet, err := firstVNETInRG(env.Context, env.vnetClient, env.VNETResourceGroup)
	Expect(err).ToNot(HaveOccurred())
	return vnet
}

func (env *Environment) GetClusterSubnet() *armnetwork.Subnet {
	GinkgoHelper()
	vnet := env.GetClusterVNET()
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
		if len(resp.Value) > 0 {
			return resp.Value[0], nil
		}
	}
	return nil, fmt.Errorf("no virtual networks found in resource group: %s", vnetRG)
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
