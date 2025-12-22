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

package integration_test

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Subnets", func() {
	var dep *appsv1.Deployment
	var selector labels.Selector
	var numPods int

	BeforeEach(func() {
		numPods = 1
		dep = test.Deployment(test.DeploymentOptions{
			Replicas: int32(numPods),
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "my-app"},
				},
			},
		})
		selector = labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
	})

	It("should allocate node in NodeClass subnet", func() {
		subnetName := "test-subnet"
		subnet := &armnetwork.Subnet{
			Name: lo.ToPtr(subnetName),
			Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix: lo.ToPtr("10.225.0.0/16"),
			},
		}
		
		// Check if the cluster uses a managed VNet
		clusterSubnet := env.GetClusterSubnet()
		isManaged, err := utils.IsAKSManagedVNET(env.NodeResourceGroup, lo.FromPtr(clusterSubnet.ID))
		Expect(err).ToNot(HaveOccurred())

		var vnetName string
		if isManaged {
			// Create a BYO VNet for testing
			vnetName = "test-byo-vnet"
			byoVNet := &armnetwork.VirtualNetwork{
				Name:     lo.ToPtr(vnetName),
				Location: lo.ToPtr(env.Region),
				Properties: &armnetwork.VirtualNetworkPropertiesFormat{
					AddressSpace: &armnetwork.AddressSpace{
						AddressPrefixes: []*string{lo.ToPtr("10.225.0.0/16")},
					},
				},
			}
			env.ExpectCreatedVNet(byoVNet)
		} else {
			// Use existing cluster VNet
			vnet := env.GetClusterVNET()
			vnetName = lo.FromPtr(vnet.Name)
		}
		
		env.ExpectCreatedSubnet(vnetName, subnet)
		nodeClass.Spec.VNETSubnetID = subnet.ID // Should be populated by the Expect call above

		env.ExpectCreated(nodeClass, nodePool, dep)

		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
		nodes := env.EventuallyExpectCreatedNodeCount("==", 1)
		env.EventuallyExpectHealthyPodCount(selector, numPods)

		vm := env.GetVM(nodes[0].Name)
		Expect(vm.Properties).ToNot(BeNil())
		Expect(vm.Properties.NetworkProfile).ToNot(BeNil())
		Expect(vm.Properties.NetworkProfile.NetworkInterfaces).To(HaveLen(1))
		Expect(vm.Properties.NetworkProfile.NetworkInterfaces[0].ID).ToNot(BeNil())
		nicID, err := arm.ParseResourceID(*vm.Properties.NetworkProfile.NetworkInterfaces[0].ID)
		Expect(err).ToNot(HaveOccurred())

		// The NIC should have the right subnet
		nic := env.GetNetworkInterface(nicID.Name)
		Expect(nic.Properties).ToNot(BeNil())
		Expect(nic.Properties.IPConfigurations).To(HaveLen(1))
		Expect(nic.Properties.IPConfigurations[0].Properties).ToNot(BeNil())
		Expect(nic.Properties.IPConfigurations[0].Properties.Subnet).ToNot(BeNil())
		Expect(nic.Properties.IPConfigurations[0].Properties.Subnet.ID).To(Equal(subnet.ID))

		// The NIC should have the right NSG
		Expect(nic.Properties.NetworkSecurityGroup).ToNot(BeNil())
		Expect(nic.Properties.NetworkSecurityGroup.ID).ToNot(BeNil())
		Expect(*nic.Properties.NetworkSecurityGroup.ID).To(MatchRegexp(`aks-agentpool-\d{8}-nsg`))
	})
})
