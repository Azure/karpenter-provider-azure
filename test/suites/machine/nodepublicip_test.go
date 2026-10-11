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

package machine_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	cloudproviderevents "github.com/Azure/karpenter-provider-azure/pkg/cloudprovider/events"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

const (
	// Built-in Network Contributor; it includes Microsoft.Network/publicIPPrefixes/join/action.
	networkContributorRoleID = "4d97b98b-1d4f-4787-a291-c67834d212e7"

	nodePublicIPPrefixLength = 28

	nodePublicIPProvisionTimeout = 20 * time.Minute
	nodePublicIPReleaseTimeout   = 15 * time.Minute
	nodePublicIPOperationTimeout = 2 * time.Minute
)

type nodePublicIPClients struct {
	prefixes  *armnetwork.PublicIPPrefixesClient
	addresses *armnetwork.PublicIPAddressesClient
}

// nodeAddress is what the shared checks learn about one node's public IP.
type nodeAddress struct {
	nodeName string
	ipID     string
	address  string
	zones    []string
}

var _ = Describe("Node Public IP", func() {
	var clients *nodePublicIPClients

	BeforeEach(func() {
		clients = newNodePublicIPClients()
	})

	It("should give a node a public IP without a prefix, and release it when disabled", func() {
		nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true)}
		nodePool.Spec.Disruption.Budgets = []karpv1.Budget{{Nodes: "100%"}}
		dep := nodePublicIPDeployment(nodePool, 1, false)
		env.ExpectCreated(nodeClass, nodePool, dep)

		pods := env.EventuallyExpectHealthyDeploymentWithTimeout(nodePublicIPProvisionTimeout, dep)
		env.EventuallyExpectHealthyWithTimeout(nodePublicIPProvisionTimeout, pods...)
		nodeClaims := env.EventuallyExpectRegisteredNodeClaimsForNodePoolWithTimeout(nodePublicIPProvisionTimeout, nodePool, 1)
		addrs := expectNodePublicIPs(clients, nodeClaims, "", nil)

		By("disabling node public IP on the NodeClass")
		nodes := nodesOf(nodeClaims)
		nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(false)}
		env.ExpectUpdated(nodeClass)
		env.EventuallyExpectNotFoundWithTimeout(nodePublicIPProvisionTimeout, nodes...)
		env.EventuallyExpectNotFound(lo.Map(nodeClaims, func(nc *karpv1.NodeClaim, _ int) client.Object { return nc })...)
		pods = env.EventuallyExpectHealthyDeploymentWithTimeout(nodePublicIPProvisionTimeout, dep)
		env.EventuallyExpectHealthyWithTimeout(nodePublicIPProvisionTimeout, pods...)

		By("expecting the replacement nodes to have no public IP")
		for _, nc := range env.EventuallyExpectRegisteredNodeClaimsForNodePoolWithTimeout(nodePublicIPProvisionTimeout, nodePool, 1) {
			expectNoNodePublicIP(nc)
		}
		eventuallyExpectPublicIPsReleased(clients, addrs)
	})

	It("should allocate node public IPs from a prefix in every zone, and release them on delete", func() {
		karpenterID, clusterID := karpenterPrincipalID(), clusterPrincipalID()
		prefixID := createPrefix(clients)
		for _, principalID := range lo.Uniq([]string{karpenterID, clusterID}) {
			grantJoin(prefixID, principalID)
		}

		armZones := env.GetAvailableZones()
		aksZones := lo.Map(armZones, func(z string, _ int) string { return zones.MakeAKSLabelZoneFromARMZone(env.Region, z) })
		replicas := max(len(aksZones), 1)
		if len(aksZones) > 0 {
			// Keep the regional zone out of the spread, so each node lands in a different zonal zone.
			nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      corev1.LabelTopologyZone,
				Operator: corev1.NodeSelectorOpIn,
				Values:   aksZones,
			})
		}

		nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true), PrefixIDs: []string{prefixID}}
		dep := nodePublicIPDeployment(nodePool, int32(replicas), len(aksZones) > 0)
		env.ExpectCreated(nodeClass, nodePool, dep)

		// The join grant can take minutes to apply; launches fail and retry until it does.
		pods := env.EventuallyExpectHealthyDeploymentWithTimeout(nodePublicIPProvisionTimeout, dep)
		env.EventuallyExpectHealthyWithTimeout(nodePublicIPProvisionTimeout, pods...)
		nodeClaims := env.EventuallyExpectRegisteredNodeClaimsForNodePoolWithTimeout(nodePublicIPProvisionTimeout, nodePool, replicas)
		addrs := expectNodePublicIPs(clients, nodeClaims, prefixID, nil)
		if len(aksZones) > 0 {
			nodeZones := lo.Map(nodesOf(nodeClaims), func(n client.Object, _ int) string { return n.GetLabels()[corev1.LabelTopologyZone] })
			Expect(nodeZones).To(ConsistOf(aksZones), "expected one node in every zone")
		}

		By("deleting the workload and the NodeClaims")
		nodes := nodesOf(nodeClaims)
		env.ExpectDeleted(dep)
		env.ExpectDeleted(lo.Map(nodeClaims, func(nc *karpv1.NodeClaim, _ int) client.Object { return nc })...)
		env.EventuallyExpectNotFoundWithTimeout(nodePublicIPProvisionTimeout, nodes...)
		eventuallyExpectPublicIPsReleased(clients, addrs)

		By("expecting the prefix to remain")
		id := lo.Must(arm.ParseResourceID(prefixID))
		_, err := clients.prefixes.Get(env.Context, id.ResourceGroupName, id.Name, nil)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should surface an error for a missing public IP prefix", func() {
		missingPrefixID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/publicIPPrefixes/%s",
			env.SubscriptionID, env.ClusterResourceGroup, test.RandomName("e2e-missing-prefix"))
		nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true), PrefixIDs: []string{missingPrefixID}}
		env.ExpectCreated(nodeClass, nodePool, nodePublicIPDeployment(nodePool, 1, false))

		By("expecting provisioning to fail because the prefix does not exist")
		// A missing prefix fails ARM's join check unless the caller has join permission at a broader scope.
		eventuallyExpectLaunchError(nodePool, Or(
			ContainSubstring("LinkedAuthorizationFailed"),
			ContainSubstring("GetPublicIPPrefixByResourceIDError"),
		))
	})

	Context("regional nodes", func() {
		BeforeEach(func() {
			// Internet routing preference requires the zone-redundant public IPs AKS creates for regional nodes.
			nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1beta1.LabelPlacementScope,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{v1beta1.PlacementScopeRegional},
			})
		})

		It("should apply the RoutingPreference=Internet tag to node public IPs", func() {
			ipTags := []v1beta1.IPTag{{IPTagType: "RoutingPreference", Tag: "Internet"}}
			nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true), IPTags: ipTags}
			dep := nodePublicIPDeployment(nodePool, 1, false)
			env.ExpectCreated(nodeClass, nodePool, dep)

			pods := env.EventuallyExpectHealthyDeploymentWithTimeout(nodePublicIPProvisionTimeout, dep)
			env.EventuallyExpectHealthyWithTimeout(nodePublicIPProvisionTimeout, pods...)
			nodeClaims := env.EventuallyExpectRegisteredNodeClaimsForNodePoolWithTimeout(nodePublicIPProvisionTimeout, nodePool, 1)
			addrs := expectNodePublicIPs(clients, nodeClaims, "", ipTags)

			By("expecting regional nodes with zone-redundant public IPs")
			armZones := env.GetAvailableZones()
			for _, n := range nodesOf(nodeClaims) {
				Expect(n.GetLabels()).To(HaveKeyWithValue(corev1.LabelTopologyZone, zones.Regional), "node %s isn't regional", n.GetName())
			}
			for _, a := range addrs {
				if len(armZones) > 0 {
					Expect(a.zones).To(ContainElements(armZones), "public IP %s isn't zone-redundant", a.ipID)
				}
			}
		})

		It("should surface AKS's error for an unsupported IP tag type", func() {
			const unsupportedIPTagType = "E2EUnsupportedIPTagType"
			nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true), IPTags: []v1beta1.IPTag{{IPTagType: unsupportedIPTagType, Tag: "Internet"}}}
			env.ExpectCreated(nodeClass, nodePool, nodePublicIPDeployment(nodePool, 1, false))

			By("expecting AKS to reject the Machine with the unsupported IP tag type")
			eventuallyExpectLaunchError(nodePool, And(ContainSubstring("UnsupportedIPTagType"), ContainSubstring(unsupportedIPTagType)))
		})
	})

	// Pending until AKS gives zonal Machines a routing-compatible public IP; enable it then.
	PContext("zonal nodes", func() {
		It("should apply the RoutingPreference=Internet tag to node public IPs", func() {
			armZones := env.GetAvailableZones()
			if len(armZones) == 0 {
				Skip(fmt.Sprintf("region %s has no availability zones", env.Region))
			}
			aksZone := zones.MakeAKSLabelZoneFromARMZone(env.Region, armZones[0])
			nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      corev1.LabelTopologyZone,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{aksZone},
			})

			ipTags := []v1beta1.IPTag{{IPTagType: "RoutingPreference", Tag: "Internet"}}
			nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true), IPTags: ipTags}
			dep := nodePublicIPDeployment(nodePool, 1, false)
			env.ExpectCreated(nodeClass, nodePool, dep)

			pods := env.EventuallyExpectHealthyDeploymentWithTimeout(nodePublicIPProvisionTimeout, dep)
			env.EventuallyExpectHealthyWithTimeout(nodePublicIPProvisionTimeout, pods...)
			nodeClaims := env.EventuallyExpectRegisteredNodeClaimsForNodePoolWithTimeout(nodePublicIPProvisionTimeout, nodePool, 1)
			addrs := expectNodePublicIPs(clients, nodeClaims, "", ipTags)

			By("expecting zonal nodes with public IPs that aren't single-zone")
			for _, n := range nodesOf(nodeClaims) {
				Expect(n.GetLabels()).To(HaveKeyWithValue(corev1.LabelTopologyZone, aksZone), "node %s isn't in zone %s", n.GetName(), aksZone)
			}
			for _, a := range addrs {
				Expect(a.zones).ToNot(HaveLen(1), "public IP %s is single-zone", a.ipID)
			}
		})
	})
})

func newNodePublicIPClients() *nodePublicIPClients {
	GinkgoHelper()
	opts := &arm.ClientOptions{ClientOptions: policy.ClientOptions{Cloud: env.CloudConfig}}
	cred := env.GetDefaultCredential()
	return &nodePublicIPClients{
		prefixes:  lo.Must(armnetwork.NewPublicIPPrefixesClient(env.SubscriptionID, cred, opts)),
		addresses: lo.Must(armnetwork.NewPublicIPAddressesClient(env.SubscriptionID, cred, opts)),
	}
}

// clusterPrincipalID returns the AKS cluster identity. AKS uses it to create the node's VM, which joins the prefix.
func clusterPrincipalID() string {
	GinkgoHelper()
	identity := env.ExpectGetManagedCluster().Identity
	if identity == nil {
		Skip("the cluster uses a service principal; this test grants the prefix to a managed cluster identity")
	}
	if len(identity.UserAssignedIdentities) > 0 {
		Expect(identity.UserAssignedIdentities).To(HaveLen(1))
		for _, uai := range identity.UserAssignedIdentities {
			Expect(uai.PrincipalID).ToNot(BeNil())
			return *uai.PrincipalID
		}
	}
	Expect(identity.PrincipalID).ToNot(BeNil())
	return *identity.PrincipalID
}

// karpenterPrincipalID returns Karpenter's identity. ARM checks that it can join every prefix a Machine references.
// On NAP, Karpenter calls the Machines API as the cluster identity.
func karpenterPrincipalID() string {
	GinkgoHelper()
	if !env.InClusterController {
		return clusterPrincipalID()
	}
	return env.GetKarpenterWorkloadIdentity(env.Context)
}

// createPrefix creates a zone-redundant (no zones set) IPv4 prefix in the cluster resource group.
// The DeferCleanup runs after the suite's AfterEach has removed the nodes; the delete is retried
// because the nodes' IPs may still be releasing.
func createPrefix(c *nodePublicIPClients) string {
	GinkgoHelper()
	name := test.RandomName("e2e-node-public-ip")
	DeferCleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(env.Context), nodePublicIPReleaseTimeout)
		defer cancel()
		Eventually(ctx, func(g Gomega) {
			opCtx, opCancel := context.WithTimeout(ctx, nodePublicIPOperationTimeout)
			defer opCancel()
			deletePoller, deleteErr := c.prefixes.BeginDelete(opCtx, env.ClusterResourceGroup, name, nil)
			if sdkerrors.IsNotFoundErr(deleteErr) {
				return
			}
			g.Expect(deleteErr).ToNot(HaveOccurred())
			_, deleteErr = deletePoller.PollUntilDone(opCtx, nil)
			if !sdkerrors.IsNotFoundErr(deleteErr) {
				g.Expect(deleteErr).ToNot(HaveOccurred())
			}
		}).WithPolling(30 * time.Second).Should(Succeed())
	})
	ctx, cancel := context.WithTimeout(env.Context, 5*time.Minute)
	defer cancel()
	poller, err := c.prefixes.BeginCreateOrUpdate(ctx, env.ClusterResourceGroup, name, armnetwork.PublicIPPrefix{
		Location: lo.ToPtr(env.Region),
		SKU: &armnetwork.PublicIPPrefixSKU{
			Name: lo.ToPtr(armnetwork.PublicIPPrefixSKUNameStandard),
			Tier: lo.ToPtr(armnetwork.PublicIPPrefixSKUTierRegional),
		},
		Properties: &armnetwork.PublicIPPrefixPropertiesFormat{
			PrefixLength:           lo.ToPtr[int32](nodePublicIPPrefixLength),
			PublicIPAddressVersion: lo.ToPtr(armnetwork.IPVersionIPv4),
		},
	}, nil)
	Expect(err).ToNot(HaveOccurred(), "failed to create public IP prefix %s", name)
	resp, err := poller.PollUntilDone(ctx, nil)
	Expect(err).ToNot(HaveOccurred(), "failed to create public IP prefix %s", name)
	Expect(resp.ID).To(HaveValue(Not(BeEmpty())))
	return lo.FromPtr(resp.ID)
}

// grantJoin grants Network Contributor on the prefix only. An assignment that already existed is left alone.
func grantJoin(prefixID, principalID string) {
	GinkgoHelper()
	roleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", env.SubscriptionID, networkContributorRoleID)
	assignmentID, err := env.RBACManager.EnsureRoleReportingCreate(env.Context, prefixID, roleDefinitionID, principalID, "ServicePrincipal")
	Expect(err).ToNot(HaveOccurred(), "failed to grant join on %s", prefixID)
	if assignmentID != "" {
		DeferCleanup(func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(env.Context), 5*time.Minute)
			defer cancel()
			Eventually(ctx, func() error {
				return env.RBACManager.DeleteRoleAssignment(ctx, assignmentID)
			}).WithPolling(5 * time.Second).Should(Succeed())
		})
	}
}

func nodePublicIPDeployment(pool *karpv1.NodePool, replicas int32, onePerZone bool) *appsv1.Deployment {
	podLabels := map[string]string{"app": test.RandomName("node-public-ip")}
	opts := coretest.PodOptions{
		ObjectMeta:   metav1.ObjectMeta{Labels: podLabels},
		NodeSelector: map[string]string{karpv1.NodePoolLabelKey: pool.Name},
	}
	if onePerZone {
		opts.PodAntiRequirements = []corev1.PodAffinityTerm{{
			LabelSelector: &metav1.LabelSelector{MatchLabels: podLabels},
			TopologyKey:   corev1.LabelTopologyZone,
		}}
	}
	return coretest.Deployment(coretest.DeploymentOptions{Replicas: replicas, PodOptions: opts})
}

func nodesOf(nodeClaims []*karpv1.NodeClaim) []client.Object {
	return lo.Map(nodeClaims, func(nc *karpv1.NodeClaim, _ int) client.Object { return env.GetNode(nc.Status.NodeName) })
}

// expectNodePublicIPs runs the per-node checks: Machine readback, the NIC's public IP with its prefix and IP tags,
// and ExternalIP. IP tags are compared as a set.
func expectNodePublicIPs(c *nodePublicIPClients, nodeClaims []*karpv1.NodeClaim, prefixID string, ipTags []v1beta1.IPTag) []nodeAddress {
	GinkgoHelper()
	addrs := make([]nodeAddress, 0, len(nodeClaims))
	for _, nc := range nodeClaims {
		By(fmt.Sprintf("checking node public IP on %s", nc.Status.NodeName))
		expectMachineNetwork(nc, func(network *containerservice.MachineNetworkProperties) {
			Expect(network.EnableNodePublicIP).To(HaveValue(BeTrue()))
			if prefixID == "" {
				Expect(lo.FromPtr(network.NodePublicIPPrefixID)).To(BeEmpty())
			} else {
				Expect(network.NodePublicIPPrefixID).To(HaveValue(Equal(prefixID)))
			}
			expectIPTags(lo.Map(network.NodePublicIPTags, func(t *containerservice.IPTag, _ int) v1beta1.IPTag {
				return v1beta1.IPTag{IPTagType: lo.FromPtr(t.IPTagType), Tag: lo.FromPtr(t.Tag)}
			}), ipTags)
		})
		addr := expectNICPublicIP(c, nc.Status.NodeName, prefixID, ipTags)
		Eventually(env.Context, func(g Gomega) {
			node := &corev1.Node{}
			g.Expect(env.Client.Get(env.Context, client.ObjectKey{Name: addr.nodeName}, node)).To(Succeed())
			g.Expect(node.Status.Conditions).To(ContainElement(And(
				HaveField("Type", corev1.NodeReady),
				HaveField("Status", corev1.ConditionTrue),
			)))
			g.Expect(node.Status.Addresses).To(ContainElement(corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: addr.address}))
		}).WithTimeout(5 * time.Minute).Should(Succeed())
		addrs = append(addrs, addr)
	}
	return addrs
}

func expectIPTags(actual, expected []v1beta1.IPTag) {
	GinkgoHelper()
	if len(expected) == 0 {
		Expect(actual).To(BeEmpty())
	} else {
		Expect(actual).To(ConsistOf(expected))
	}
}

func expectNoNodePublicIP(nc *karpv1.NodeClaim) {
	GinkgoHelper()
	By(fmt.Sprintf("checking %s has no node public IP", nc.Status.NodeName))
	expectMachineNetwork(nc, func(network *containerservice.MachineNetworkProperties) {
		Expect(lo.FromPtr(network.EnableNodePublicIP)).To(BeFalse())
	})
	Expect(primaryIPConfig(env.GetNodeNetworkInterface(nc.Status.NodeName)).Properties.PublicIPAddress).To(BeNil())
	node := env.GetNode(nc.Status.NodeName)
	Expect(node.Status.Conditions).To(ContainElement(And(
		HaveField("Type", corev1.NodeReady),
		HaveField("Status", corev1.ConditionTrue),
	)))
	Expect(node.Status.Addresses).ToNot(ContainElement(HaveField("Type", corev1.NodeExternalIP)))
}

// expectMachineNetwork applies check to the Machine as returned by both GET and LIST, neither with expand.
func expectMachineNetwork(nc *karpv1.NodeClaim, check func(*containerservice.MachineNetworkProperties)) {
	GinkgoHelper()
	got := env.ExpectMachineByID(nc.Annotations[v1beta1.AnnotationAKSMachineResourceID])
	Expect(got.Properties).ToNot(BeNil())
	Expect(got.Properties.Network).ToNot(BeNil())
	check(got.Properties.Network)

	listed, ok := lo.Find(env.ExpectListMachines(), func(m *containerservice.Machine) bool {
		return strings.EqualFold(lo.FromPtr(m.Name), lo.FromPtr(got.Name))
	})
	Expect(ok).To(BeTrue(), "machine %s not found by LIST", lo.FromPtr(got.Name))
	Expect(listed.Properties).ToNot(BeNil())
	Expect(listed.Properties.Network).ToNot(BeNil())
	check(listed.Properties.Network)
}

func primaryIPConfig(nic armnetwork.Interface) *armnetwork.InterfaceIPConfiguration {
	GinkgoHelper()
	Expect(nic.Properties).ToNot(BeNil())
	Expect(nic.Properties.IPConfigurations).ToNot(BeEmpty())
	ipConfig, ok := lo.Find(nic.Properties.IPConfigurations, func(c *armnetwork.InterfaceIPConfiguration) bool {
		return c.Properties != nil && lo.FromPtr(c.Properties.Primary)
	})
	if !ok {
		ipConfig = nic.Properties.IPConfigurations[0]
	}
	Expect(ipConfig.Properties).ToNot(BeNil())
	return ipConfig
}

func expectNICPublicIP(c *nodePublicIPClients, nodeName, prefixID string, ipTags []v1beta1.IPTag) nodeAddress {
	GinkgoHelper()
	nic := env.GetNodeNetworkInterface(nodeName)
	ipConfig := primaryIPConfig(nic)
	Expect(ipConfig.Properties.PublicIPAddress).ToNot(BeNil(), "node %s NIC has no public IP", nodeName)
	ipID := lo.FromPtr(ipConfig.Properties.PublicIPAddress.ID)
	parsed := lo.Must(arm.ParseResourceID(ipID))
	ip, err := c.addresses.Get(env.Context, parsed.ResourceGroupName, parsed.Name, nil)
	Expect(err).ToNot(HaveOccurred(), "failed to get public IP %s", ipID)
	Expect(ip.Properties).ToNot(BeNil())
	Expect(ip.Properties.IPAddress).ToNot(BeNil())
	if prefixID != "" {
		Expect(ip.Properties.PublicIPPrefix).ToNot(BeNil(), "public IP %s isn't from a prefix", ipID)
		Expect(strings.EqualFold(lo.FromPtr(ip.Properties.PublicIPPrefix.ID), prefixID)).To(BeTrue(),
			"public IP %s is from %s, not %s", ipID, lo.FromPtr(ip.Properties.PublicIPPrefix.ID), prefixID)
	}
	expectIPTags(lo.Map(ip.Properties.IPTags, func(t *armnetwork.IPTag, _ int) v1beta1.IPTag {
		return v1beta1.IPTag{IPTagType: lo.FromPtr(t.IPTagType), Tag: lo.FromPtr(t.Tag)}
	}), ipTags)
	return nodeAddress{
		nodeName: nodeName,
		ipID:     ipID,
		address:  *ip.Properties.IPAddress,
		zones:    lo.FromSlicePtr(ip.Zones),
	}
}

func eventuallyExpectPublicIPsReleased(c *nodePublicIPClients, addrs []nodeAddress) {
	GinkgoHelper()
	By("expecting the nodes' public IPs to be released")
	Eventually(env.Context, func(g Gomega) {
		for _, a := range addrs {
			id := lo.Must(arm.ParseResourceID(a.ipID))
			_, err := c.addresses.Get(env.Context, id.ResourceGroupName, id.Name, nil)
			g.Expect(sdkerrors.IsNotFoundErr(err)).To(BeTrue(), "public IP %s still exists (err: %v)", a.ipID, err)
		}
	}).WithTimeout(nodePublicIPReleaseTimeout).WithPolling(15 * time.Second).Should(Succeed())
}

// eventuallyExpectLaunchError waits for an error matching matcher on one of the pool's NodeClaims: either the
// Launched condition (synchronous rejections, the NodeClaim is kept) or an AsyncProvisioningError Warning event
// (provisioning failures, emitted before the NodeClaim is deleted).
func eventuallyExpectLaunchError(pool *karpv1.NodePool, matcher types.GomegaMatcher) {
	GinkgoHelper()
	Eventually(env.Context, func(g Gomega) {
		messages := launchErrorMessages(g, pool)
		g.Expect(messages).To(ContainElement(matcher), "no launch error for NodePool %s matched", pool.Name)
	}).WithTimeout(nodePublicIPProvisionTimeout).WithPolling(10 * time.Second).Should(Succeed())
}

func launchErrorMessages(g Gomega, pool *karpv1.NodePool) []string {
	var messages []string
	nodeClaims := &karpv1.NodeClaimList{}
	g.Expect(env.Client.List(env.Context, nodeClaims, client.MatchingLabels{karpv1.NodePoolLabelKey: pool.Name})).To(Succeed())
	for _, nc := range nodeClaims.Items {
		if cond := nc.StatusConditions().Get(karpv1.ConditionTypeLaunched); cond != nil && !cond.IsTrue() && cond.Message != "" {
			messages = append(messages, cond.Message)
		}
	}
	events, err := env.KubeClient.CoreV1().Events("").List(env.Context, metav1.ListOptions{FieldSelector: "involvedObject.kind=NodeClaim"})
	g.Expect(err).ToNot(HaveOccurred())
	for _, e := range events.Items {
		if e.Type == corev1.EventTypeWarning && e.Reason == cloudproviderevents.AsyncProvisioningReason && strings.HasPrefix(e.InvolvedObject.Name, pool.Name+"-") {
			messages = append(messages, e.Message)
		}
	}
	return messages
}
