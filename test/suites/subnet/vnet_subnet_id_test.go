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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	opstatus "github.com/awslabs/operatorpkg/status"
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
	It("should disrupt nodes that drifted due to VNETSubnetID", func() {
		env.ExpectCreated(dep, nodeClass, nodePool)
		env.EventuallyExpectHealthyPodCount(selector, numPods)
		By("expect created node count to be 1")
		env.ExpectCreatedNodeCount("==", 1)
		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]

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
			vnetName = "test-byo-vnet-drift"
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
		env.ExpectCreatedOrUpdated(nodeClass)
		env.EventuallyExpectDrifted(nodeClaim)
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
			vnetName = "test-byo-vnet-alloc"
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

		env.EventuallyExpectHealthyPodCount(selector, numPods)
		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
		nodes := env.EventuallyExpectCreatedNodeCount("==", 1)

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
	It("should reject the AKSNodeClass if the subnet ID is invalid", func() {
		nodeClass.Spec.VNETSubnetID = lo.ToPtr("/subnets/fake-subnet")
		err := env.Client.Create(env.Context, nodeClass)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Invalid value"))
		Expect(err.Error()).To(ContainSubstring("spec.vnetSubnetID"))
		Expect(err.Error()).To(ContainSubstring("should match"))
	})

	It("should mark the AKSNodeClass as unready if custom subnet is in managed VNet", func() {
		clusterSubnet := env.GetClusterSubnet()
		isManaged, err := utils.IsAKSManagedVNET(env.NodeResourceGroup, lo.FromPtr(clusterSubnet.ID))
		Expect(err).ToNot(HaveOccurred())

		// E.g., runs when cluster is created with az-mkaks, az-mkaks-cilium, az-mkaks-overlay, etc.
		// Skips when cluster is created with az-mkaks-custom-vnet (BYO VNet).
		if !isManaged {
			Skip("Skipping test: cluster uses BYO VNet, cannot test managed VNet blocking")
		}

		// Try to create a custom subnet in the managed VNet
		vnet := env.GetClusterVNET()
		vnetResourceID, err := arm.ParseResourceID(lo.FromPtr(vnet.ID))
		Expect(err).ToNot(HaveOccurred())

		customSubnetID := utils.GetSubnetResourceID(
			vnetResourceID.SubscriptionID,
			vnetResourceID.ResourceGroupName,
			vnetResourceID.Name,
			"custom-test-subnet",
		)

		nodeClass.Spec.VNETSubnetID = lo.ToPtr(customSubnetID)
		env.ExpectCreated(nodeClass)

		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(condition.IsFalse()).To(BeTrue())
			g.Expect(condition.Message).To(ContainSubstring("custom subnet cannot be in the same VNet as cluster managed VNet"))
			g.Expect(condition.Message).To(ContainSubstring(customSubnetID))
		}).Should(Succeed())
	})

	DescribeTable("should mark the AKSNodeClass as unready if the subnetID doesn't belong to the cluster vnet",
		func(modifyComponents func(utils.VnetSubnetResource) utils.VnetSubnetResource) {
			vnet := env.GetClusterVNET()
			vnetID := lo.FromPtr(vnet.ID)

			vnetResourceID, err := arm.ParseResourceID(vnetID)
			Expect(err).ToNot(HaveOccurred())

			baseComponents := utils.VnetSubnetResource{
				SubscriptionID:    vnetResourceID.SubscriptionID,
				ResourceGroupName: vnetResourceID.ResourceGroupName,
				VNetName:          vnetResourceID.Name,
				SubnetName:        "test-subnet",
			}

			modifiedComponents := modifyComponents(baseComponents)

			subnetID := utils.GetSubnetResourceID(
				modifiedComponents.SubscriptionID,
				modifiedComponents.ResourceGroupName,
				modifiedComponents.VNetName,
				modifiedComponents.SubnetName,
			)

			nodeClass.Spec.VNETSubnetID = lo.ToPtr(subnetID)
			env.ExpectCreated(nodeClass)

			Eventually(func(g Gomega) {
				g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
				g.Expect(condition).ToNot(BeNil())
				g.Expect(condition.IsFalse()).To(BeTrue())
				// Generic assertion - just check that the message mentions the subnet doesn't match
				g.Expect(condition.Message).To(ContainSubstring("does not match"))
				g.Expect(condition.Message).To(ContainSubstring(subnetID))
			}).Should(Succeed())
		},
		Entry("different subscription",
			func(components utils.VnetSubnetResource) utils.VnetSubnetResource {
				components.SubscriptionID = "12345678-1234-1234-1234-123456789012"
				return components
			}),
		Entry("different resource group",
			func(components utils.VnetSubnetResource) utils.VnetSubnetResource {
				components.ResourceGroupName = "different-rg"
				return components
			}),
		Entry("different virtual network",
			func(components utils.VnetSubnetResource) utils.VnetSubnetResource {
				components.VNetName = "different-vnet"
				return components
			}),
	)
	It("should mark the AKSNodeClass as unready if the subnet is NotFound and fall back to a different nodeclass", func() {
		newNodeClass := env.DefaultAKSNodeClass()
		newNodepool := env.DefaultNodePool(newNodeClass)
		newNodepool.Spec.Weight = lo.ToPtr(int32(10))

		vnet := env.GetClusterVNET() // Use cluster vnet in fake subnet id
		vnetResourceID, err := arm.ParseResourceID(lo.FromPtr(vnet.ID))
		Expect(err).ToNot(HaveOccurred())

		// Create a subnet ID that doesn't exist but is in the same vnet
		newNodeClass.Spec.VNETSubnetID = lo.ToPtr(utils.GetSubnetResourceID(
			vnetResourceID.SubscriptionID,
			vnetResourceID.ResourceGroupName,
			vnetResourceID.Name,
			"nodeClassSubnet2", // This subnet doesn't exist
		))
		env.ExpectCreated(nodeClass, nodePool, newNodeClass, newNodepool, dep)

		By("falling back to original nodeclass due to misconfigured subnet")
		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthyPodCount(selector, numPods)
		Expect(node.Labels[karpv1.NodePoolLabelKey]).To(Equal(nodePool.Name)) // Validate we are are falling back to the original nodepool

		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(newNodeClass), newNodeClass)).To(Succeed())
			condition := newNodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
			g.Expect(condition.IsTrue()).To(BeFalse())
			Expect(condition.Reason).To(Equal("SubnetNotFound"))

			rootCondition := newNodeClass.StatusConditions().Get(opstatus.ConditionReady)
			Expect(rootCondition.IsTrue()).To(BeFalse())
		}).Should(Succeed())
	})
})
