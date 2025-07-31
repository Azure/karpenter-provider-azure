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

package subnet_test

import (
	"fmt"
	
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
		vnet := env.GetClusterVNET()
		env.ExpectCreatedSubnet(lo.FromPtr(vnet.Name), subnet)
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
	It("should mark the AKSNodeClass as unready if the subnet ID is invalid", func() {
		newNodeClass := env.DefaultAKSNodeClass()
		newNodepool := env.DefaultNodePool(newNodeClass)
		// Set a high weight to ensure that we select this nodepool with priority if its ready
		newNodepool.Spec.Weight = lo.ToPtr(int32(10))
		newNodeClass.Spec.VNETSubnetID = lo.ToPtr("/subnets/fake-subnet")
		env.ExpectCreated(nodeClass, nodePool, newNodeClass, newNodepool, dep)

		By("falling back to original nodeclass")
		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthyPodCount(selector, numPods)

		// Get the AKSNodeClass and check the status
		// Expect provisioning to still work
		Expect(node.Labels[karpv1.NodePoolLabelKey]).To(Equal(nodePool.Name))
	})
	It("should mark the AKSNodeClass as unready if the subnet is full, fall back to old nodeclass", func() {
		subnetName := "full-subnet"
		// Create a subnet with small address space (/28 = 16 total IPs, 5 reserved by Azure = 11 available)
		// Then we'll simulate it being full by adding IP configurations
		ipConfigs := make([]*armnetwork.IPConfiguration, 11) // Use all available IPs
		for i := range ipConfigs {
			ipConfigs[i] = &armnetwork.IPConfiguration{
				Name: lo.ToPtr(fmt.Sprintf("ip-config-%d", i)),
			}
		}
		
		subnet := &armnetwork.Subnet{
			Name: lo.ToPtr(subnetName),
			Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix:    lo.ToPtr("10.225.1.0/28"), // 16 total IPs minus 5 reserved = 11 available
				IPConfigurations: ipConfigs,                   // All 11 available IPs are used
			},
		}
		vnet := env.GetClusterVNET()
		env.ExpectCreatedSubnet(lo.FromPtr(vnet.Name), subnet)
		
		newNodeClass := env.DefaultAKSNodeClass()
		newNodepool := env.DefaultNodePool(newNodeClass)
		// Set a high weight to ensure that we select this nodepool with priority if its ready
		newNodepool.Spec.Weight = lo.ToPtr(int32(10))
		newNodeClass.Spec.VNETSubnetID = subnet.ID // Should be populated by the Expect call above
		env.ExpectCreated(nodeClass, nodePool, newNodeClass, newNodepool, dep)

		By("falling back to original nodeclass due to subnet capacity issues")
		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthyPodCount(selector, numPods)

		// Verify that the original nodeclass was used (not the one with full subnet)
		Expect(node.Labels[karpv1.NodePoolLabelKey]).To(Equal(nodePool.Name))
		
		// Verify the new nodeclass with full subnet has SubnetReady condition set to false
		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(newNodeClass), newNodeClass)).To(Succeed())
			condition := newNodeClass.StatusConditions().Get("SubnetReady")
			g.Expect(condition.IsFalse()).To(BeTrue())
			g.Expect(condition.GetReason()).To(Equal("SubnetFull"))
		}).Should(Succeed())
	})
})
