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
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

const (
	// Built-in Network Contributor; it includes Microsoft.Network/publicIPPrefixes/join/action.
	networkContributorRoleID = "4d97b98b-1d4f-4787-a291-c67834d212e7"

	nodePublicIPPrefixLength = 28
	nodePublicIPImage        = "mcr.microsoft.com/azurelinux/busybox:1.36"
	// An NSG rule opens allowedPort; deniedPort is left to the default NSG rules, which deny Internet inbound.
	allowedPort = 18080
	deniedPort  = 18081

	nodePublicIPProvisionTimeout = 20 * time.Minute
	nodePublicIPReleaseTimeout   = 15 * time.Minute
	nodePublicIPOperationTimeout = 2 * time.Minute
	nsgRuleCleanupTimeout        = 5 * time.Minute
	nsgRuleNamePrefix            = "e2e-node-public-ip"
	nsgRulePriorityMin           = 3000
	nsgRulePriorityMax           = 3999
)

// nodePublicIPClients are built here because the Environment's network clients aren't exported.
type nodePublicIPClients struct {
	prefixes  *armnetwork.PublicIPPrefixesClient
	addresses *armnetwork.PublicIPAddressesClient
	rules     *armnetwork.SecurityRulesClient
	subnets   *armnetwork.SubnetsClient
}

// nodeAddress is what the shared checks learn about one node's public IP.
type nodeAddress struct {
	nodeName  string
	ipID      string
	address   string
	privateIP string
	nsgIDs    []string
	zones     []string
}

// nsgRules tracks the NSG rules a spec added, so they can be removed before the nodes' IPs are released.
// A released IP can be reassigned to another Azure customer, and a leftover rule would still let it in.
type nsgRules struct {
	clients *nodePublicIPClients
	added   []nsgRule
}

type nsgRule struct {
	nsg  *arm.ResourceID
	name string
}

var _ = Describe("Node Public IP", func() {
	var clients *nodePublicIPClients
	var rules *nsgRules

	BeforeEach(func() {
		rules = nil
		if !crdHasNodePublicIP() {
			Skip("the installed AKSNodeClass CRD has no spec.nodePublicIP field")
		}
		clients = newNodePublicIPClients()
		rules = &nsgRules{clients: clients}
	})

	// Runs before the suite's AfterEach deletes the nodes, so a failed spec doesn't leave rules behind either.
	AfterEach(func() {
		if rules != nil {
			rules.removeAll()
		}
	})

	It("should give each node a public IP without a prefix, and release it when disabled", func() {
		nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true)}
		nodePool.Spec.Disruption.Budgets = []karpv1.Budget{{Nodes: "100%"}}
		dep, selector := listenerDeployment(nodePool, 2, false)
		env.ExpectCreated(nodeClass, nodePool, dep)

		listeners := env.EventuallyExpectHealthyPodCountWithTimeout(nodePublicIPProvisionTimeout, selector, 2)
		env.EventuallyExpectHealthyWithTimeout(nodePublicIPProvisionTimeout, listeners...)
		nodeClaims := eventuallyExpectRegisteredNodeClaims(nodePool, 2)
		addrs := expectNodePublicIPs(clients, nodeClaims, "", nil)
		expectIngressAndEgress(rules, addrs)
		env.EventuallyExpectHealthyWithTimeout(5*time.Minute, listeners...)
		rules.removeAll()

		By("disabling node public IP on the NodeClass")
		nodes := nodesOf(nodeClaims)
		nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(false)}
		env.ExpectUpdated(nodeClass)
		env.EventuallyExpectNotFoundWithTimeout(nodePublicIPProvisionTimeout, nodes...)
		env.EventuallyExpectNotFound(lo.Map(nodeClaims, func(nc *karpv1.NodeClaim, _ int) client.Object { return nc })...)
		env.EventuallyExpectHealthyPodCountWithTimeout(nodePublicIPProvisionTimeout, selector, 2)

		By("expecting the replacement nodes to have no public IP")
		for _, nc := range eventuallyExpectRegisteredNodeClaims(nodePool, 2) {
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
		replicas := max(len(aksZones), 2)
		if len(aksZones) > 0 {
			// Keep the regional zone out of the spread, so each node lands in a different zonal zone.
			nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      corev1.LabelTopologyZone,
				Operator: corev1.NodeSelectorOpIn,
				Values:   aksZones,
			})
		}

		nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true), PrefixIDs: []string{prefixID}}
		dep, selector := listenerDeployment(nodePool, int32(replicas), len(aksZones) > 0)
		env.ExpectCreated(nodeClass, nodePool, dep)

		// The join grant can take minutes to apply; launches fail and retry until it does.
		listeners := env.EventuallyExpectHealthyPodCountWithTimeout(nodePublicIPProvisionTimeout, selector, replicas)
		env.EventuallyExpectHealthyWithTimeout(nodePublicIPProvisionTimeout, listeners...)
		nodeClaims := eventuallyExpectRegisteredNodeClaims(nodePool, replicas)
		addrs := expectNodePublicIPs(clients, nodeClaims, prefixID, nil)
		if len(aksZones) > 0 {
			nodeZones := lo.Map(nodesOf(nodeClaims), func(n client.Object, _ int) string { return n.GetLabels()[corev1.LabelTopologyZone] })
			Expect(nodeZones).To(ConsistOf(aksZones), "expected one node in every zone")
		}
		expectIngressAndEgress(rules, addrs)
		env.EventuallyExpectHealthyWithTimeout(5*time.Minute, listeners...)
		rules.removeAll()

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

	It("should surface the join errors for a missing prefix and for a prefix the cluster identity can't join", func() {
		karpenterID, clusterID := karpenterPrincipalID(), clusterPrincipalID()
		missingPrefixID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/publicIPPrefixes/%s",
			env.SubscriptionID, env.ClusterResourceGroup, test.RandomName("e2e-missing-prefix"))

		missingClass := nodeClass
		missingClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true), PrefixIDs: []string{missingPrefixID}}
		missingPool := nodePool
		objects := []client.Object{missingClass, missingPool, pendingDeployment(missingPool)}

		// Only Karpenter's identity can join this prefix, so the Machine is accepted and AKS fails it when it
		// creates the VM. This needs the cluster identity to be a different identity from Karpenter's.
		var karpenterOnlyPool *karpv1.NodePool
		if karpenterID != clusterID {
			karpenterOnlyPrefixID := createPrefix(clients)
			grantJoin(karpenterOnlyPrefixID, karpenterID)
			karpenterOnlyClass := env.DefaultAKSNodeClass()
			karpenterOnlyClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true), PrefixIDs: []string{karpenterOnlyPrefixID}}
			karpenterOnlyPool = env.DefaultNodePool(karpenterOnlyClass)
			objects = append(objects, karpenterOnlyClass, karpenterOnlyPool, pendingDeployment(karpenterOnlyPool))
		}
		env.ExpectCreated(objects...)

		// ARM checks join on every prefix the Machine references before AKS sees the request. A prefix that
		// doesn't exist fails that check too, unless Karpenter's identity can join at a broader scope.
		By("expecting ARM to reject the Machine because Karpenter's identity can't join the missing prefix")
		eventuallyExpectLaunchError(missingPool, And(ContainSubstring("LinkedAuthorizationFailed"), ContainSubstring(karpenterID)))

		if karpenterOnlyPool == nil {
			reason := "Karpenter and the cluster use the same identity; skipping the prefix only Karpenter's identity can join"
			By(reason)
			AddReportEntry("node public IP cluster identity join error skipped", reason)
			return
		}
		By("expecting provisioning to fail because the cluster identity can't join the prefix")
		eventuallyExpectLaunchError(karpenterOnlyPool, And(ContainSubstring("LinkedAuthorizationFailed"), ContainSubstring(clusterID)))
	})

	It("should set IP tags on regional node public IPs, and surface AKS's error for an unsupported IP tag type", func() {
		// Azure supports Internet routing preference only on zone-redundant public IPs, which AKS creates for
		// regional nodes. A zonal node gets a zonal public IP, and its provisioning fails.
		regional := karpv1.NodeSelectorRequirementWithMinValues{
			Key:      v1beta1.LabelPlacementScope,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{v1beta1.PlacementScopeRegional},
		}
		ipTags := []v1beta1.IPTag{{IPTagType: "RoutingPreference", Tag: "Internet"}}
		nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true), IPTags: ipTags}
		nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, regional)
		dep := pendingDeployment(nodePool)
		selector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)

		const unsupportedIPTagType = "E2EUnsupportedIPTagType"
		unsupportedClass := env.DefaultAKSNodeClass()
		unsupportedClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true), IPTags: []v1beta1.IPTag{{IPTagType: unsupportedIPTagType, Tag: "Internet"}}}
		unsupportedPool := env.DefaultNodePool(unsupportedClass)
		unsupportedPool.Spec.Template.Spec.Requirements = append(unsupportedPool.Spec.Template.Spec.Requirements, regional)
		env.ExpectCreated(nodeClass, nodePool, dep, unsupportedClass, unsupportedPool, pendingDeployment(unsupportedPool))

		By("expecting AKS to reject the Machine with the unsupported IP tag type")
		eventuallyExpectLaunchError(unsupportedPool, And(ContainSubstring("UnsupportedIPTagType"), ContainSubstring(unsupportedIPTagType)))

		env.EventuallyExpectHealthyPodCountWithTimeout(nodePublicIPProvisionTimeout, selector, 1)
		nodeClaims := eventuallyExpectRegisteredNodeClaims(nodePool, 1)
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
})

func newNodePublicIPClients() *nodePublicIPClients {
	GinkgoHelper()
	opts := &arm.ClientOptions{ClientOptions: policy.ClientOptions{Cloud: env.CloudConfig}}
	cred := env.GetDefaultCredential()
	return &nodePublicIPClients{
		prefixes:  lo.Must(armnetwork.NewPublicIPPrefixesClient(env.SubscriptionID, cred, opts)),
		addresses: lo.Must(armnetwork.NewPublicIPAddressesClient(env.SubscriptionID, cred, opts)),
		rules:     lo.Must(armnetwork.NewSecurityRulesClient(env.SubscriptionID, cred, opts)),
		subnets:   lo.Must(armnetwork.NewSubnetsClient(env.SubscriptionID, cred, opts)),
	}
}

// crdHasNodePublicIP reports whether the installed CRD has the field. On NAP the CRD comes from AKS,
// which may not have shipped it yet.
func crdHasNodePublicIP() bool {
	GinkgoHelper()
	crd := &unstructured.Unstructured{}
	crd.SetGroupVersionKind(schema.GroupVersionKind{Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"})
	Expect(env.Client.Get(env.Context, client.ObjectKey{Name: "aksnodeclasses.karpenter.azure.com"}, crd)).To(Succeed())
	versions, _, err := unstructured.NestedSlice(crd.Object, "spec", "versions")
	Expect(err).ToNot(HaveOccurred())
	for _, v := range versions {
		version, ok := v.(map[string]any)
		if !ok || version["name"] != "v1beta1" {
			continue
		}
		_, found, err := unstructured.NestedMap(version, "schema", "openAPIV3Schema", "properties", "spec", "properties", "nodePublicIP")
		Expect(err).ToNot(HaveOccurred())
		return found
	}
	return false
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
			if isNotFound(deleteErr) {
				return
			}
			g.Expect(deleteErr).ToNot(HaveOccurred())
			_, deleteErr = deletePoller.PollUntilDone(opCtx, nil)
			if !isNotFound(deleteErr) {
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
			ctx, cancel := context.WithTimeout(context.WithoutCancel(env.Context), nsgRuleCleanupTimeout)
			defer cancel()
			Eventually(ctx, func() error {
				return env.RBACManager.DeleteRoleAssignment(ctx, assignmentID)
			}).WithPolling(5 * time.Second).Should(Succeed())
		})
	}
}

// listenerDeployment runs two host-network echo listeners per node: one on allowedPort and one on deniedPort.
func listenerDeployment(pool *karpv1.NodePool, replicas int32, spreadZones bool) (*appsv1.Deployment, labels.Selector) {
	podLabels := map[string]string{"app": test.RandomName("node-public-ip")}
	opts := coretest.PodOptions{
		ObjectMeta:   metav1.ObjectMeta{Labels: podLabels},
		NodeSelector: map[string]string{karpv1.NodePoolLabelKey: pool.Name},
		PodAntiRequirements: []corev1.PodAffinityTerm{{
			LabelSelector: &metav1.LabelSelector{MatchLabels: podLabels},
			TopologyKey:   corev1.LabelHostname,
		}},
	}
	if spreadZones {
		opts.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
			MaxSkew:           1,
			TopologyKey:       corev1.LabelTopologyZone,
			WhenUnsatisfiable: corev1.DoNotSchedule,
			LabelSelector:     &metav1.LabelSelector{MatchLabels: podLabels},
		}}
	}
	dep := coretest.Deployment(coretest.DeploymentOptions{Replicas: replicas, PodOptions: opts})
	dep.Spec.Template.Spec.HostNetwork = true
	dep.Spec.Template.Spec.Containers = []corev1.Container{echoContainer("allowed", allowedPort), echoContainer("denied", deniedPort)}
	return dep, labels.SelectorFromSet(podLabels)
}

func echoContainer(name string, port int32) corev1.Container {
	return corev1.Container{
		Name:    name,
		Image:   nodePublicIPImage,
		Command: []string{"sh", "-c", `exec tcpsvd 0.0.0.0 "$PORT" sh -c 'printf "%s\n" "$TCPREMOTEADDR"'`},
		Env: []corev1.EnvVar{
			{Name: "PORT", Value: strconv.Itoa(int(port))},
		},
		Ports: []corev1.ContainerPort{{ContainerPort: port, HostPort: port, Protocol: corev1.ProtocolTCP}},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{
				Command: []string{"sh", "-c", `nc -z -w 1 127.0.0.1 "$PORT"`},
			}},
			PeriodSeconds:  2,
			TimeoutSeconds: 2,
		},
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("32Mi"),
		}},
	}
}

func pendingDeployment(pool *karpv1.NodePool) *appsv1.Deployment {
	return coretest.Deployment(coretest.DeploymentOptions{
		Replicas: 1,
		PodOptions: coretest.PodOptions{
			ObjectMeta:   metav1.ObjectMeta{Labels: map[string]string{"app": test.RandomName("node-public-ip-error")}},
			NodeSelector: map[string]string{karpv1.NodePoolLabelKey: pool.Name},
		},
	})
}

func nodesOf(nodeClaims []*karpv1.NodeClaim) []client.Object {
	return lo.Map(nodeClaims, func(nc *karpv1.NodeClaim, _ int) client.Object { return env.GetNode(nc.Status.NodeName) })
}

func eventuallyExpectRegisteredNodeClaims(pool *karpv1.NodePool, count int) []*karpv1.NodeClaim {
	GinkgoHelper()
	var claims []*karpv1.NodeClaim
	Eventually(func(g Gomega) {
		claims = lo.Filter(env.ExpectLiveNodeClaimsForNodePool(env.Context, g, pool), func(nc *karpv1.NodeClaim, _ int) bool {
			return nc.StatusConditions().IsTrue(karpv1.ConditionTypeRegistered) && nc.Status.NodeName != ""
		})
		g.Expect(claims).To(HaveLen(count), "expected %d live registered NodeClaims for NodePool %s", count, pool.Name)
	}).WithTimeout(nodePublicIPProvisionTimeout).Should(Succeed())
	return claims
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
		Eventually(func(g Gomega) {
			node := &corev1.Node{}
			g.Expect(env.Client.Get(env.Context, client.ObjectKey{Name: addr.nodeName}, node)).To(Succeed())
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
		Expect(network.EnableNodePublicIP).To(HaveValue(BeFalse()))
	})
	Expect(primaryIPConfig(nicOf(nc.Status.NodeName)).Properties.PublicIPAddress).To(BeNil())
	node := env.GetNode(nc.Status.NodeName)
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

func nicOf(nodeName string) armnetwork.Interface {
	GinkgoHelper()
	vm := env.GetVM(nodeName)
	Expect(vm.Properties).ToNot(BeNil())
	Expect(vm.Properties.NetworkProfile).ToNot(BeNil())
	Expect(vm.Properties.NetworkProfile.NetworkInterfaces).ToNot(BeEmpty())
	nicID := lo.Must(arm.ParseResourceID(lo.FromPtr(vm.Properties.NetworkProfile.NetworkInterfaces[0].ID)))
	return env.GetNetworkInterface(nicID.Name)
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
	nic := nicOf(nodeName)
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
	Expect(ipConfig.Properties.PrivateIPAddress).ToNot(BeNil(), "node %s NIC has no private IP", nodeName)
	return nodeAddress{
		nodeName:  nodeName,
		ipID:      ipID,
		address:   *ip.Properties.IPAddress,
		privateIP: lo.FromPtr(ipConfig.Properties.PrivateIPAddress),
		nsgIDs:    nsgIDsOf(c, nic, ipConfig),
		zones:     lo.FromSlicePtr(ip.Zones),
	}
}

// nsgIDsOf returns the NSGs on the NIC and on its subnet.
func nsgIDsOf(c *nodePublicIPClients, nic armnetwork.Interface, ipConfig *armnetwork.InterfaceIPConfiguration) []string {
	GinkgoHelper()
	var ids []string
	if nic.Properties.NetworkSecurityGroup != nil {
		ids = append(ids, lo.FromPtr(nic.Properties.NetworkSecurityGroup.ID))
	}
	Expect(ipConfig.Properties.Subnet).ToNot(BeNil())
	subnetID := lo.Must(arm.ParseResourceID(lo.FromPtr(ipConfig.Properties.Subnet.ID)))
	subnet, err := c.subnets.Get(env.Context, subnetID.ResourceGroupName, subnetID.Parent.Name, subnetID.Name, nil)
	Expect(err).ToNot(HaveOccurred())
	if subnet.Properties != nil && subnet.Properties.NetworkSecurityGroup != nil {
		ids = append(ids, lo.FromPtr(subnet.Properties.NetworkSecurityGroup.ID))
	}
	return ids
}

// expectIngressAndEgress opens allowedPort to the nodes' public IPs, then runs a host-network client on each node
// that calls the next node's listeners through that node's public IP. The listener echoes the caller's address,
// which must be the calling node's public IP. deniedPort, which no rule opens, must never answer.
func expectIngressAndEgress(rules *nsgRules, addrs []nodeAddress) {
	GinkgoHelper()
	if isNodeResourceGroupLockedDown() {
		reason := "node resource group lockdown prevents adding NSG rules; skipping the ingress and egress checks"
		By(reason)
		AddReportEntry("node public IP ingress and egress skipped", reason)
		return
	}
	sources := lo.Map(addrs, func(a nodeAddress, _ int) string { return a.address })
	// NSGs evaluate inbound traffic after the public IP is translated to the node's private IP.
	destinations := lo.Map(addrs, func(a nodeAddress, _ int) string { return a.privateIP })
	nsgIDs := lo.UniqBy(lo.FlatMap(addrs, func(a nodeAddress, _ int) []string { return a.nsgIDs }), strings.ToLower)
	for _, nsgID := range nsgIDs {
		rules.allow(nsgID, sources, destinations)
	}

	clients := make([]client.Object, 0, len(addrs))
	for i, from := range addrs {
		to := addrs[(i+1)%len(addrs)]
		By(fmt.Sprintf("calling %s through its public IP %s from %s", to.nodeName, to.address, from.nodeName))
		pod := echoClientPod(from.nodeName, to.address)
		env.ExpectCreated(pod)
		clients = append(clients, pod)
	}
	for i, pod := range clients {
		from := addrs[i]
		Eventually(func(g Gomega) {
			expectEchoes(g, pod.(*corev1.Pod), from)
		}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	}
	env.ExpectDeleted(clients...)
}

// echoClientPod calls target's allowedPort and deniedPort from nodeName's host network every few seconds and logs
// each reply, or the error, as "allowed=..." and "denied=...".
func echoClientPod(nodeName, target string) *corev1.Pod {
	script := fmt.Sprintf(`while true; do
  nc -z -w 1 127.0.0.1 %[2]d && nc -z -w 1 127.0.0.1 %[3]d
  local_before=$?
  allowed=$(nc -w 5 %[1]s %[2]d 2>&1)
  allowed_status=$?
  nc -z -w 5 %[1]s %[3]d
  denied_status=$?
  nc -z -w 1 127.0.0.1 %[2]d && nc -z -w 1 127.0.0.1 %[3]d
  local_after=$?
  printf 'allowed=%%s\nallowed-status=%%d\ndenied-status=%%d\nlocal-before=%%d\nlocal-after=%%d\n' \
    "$allowed" "$allowed_status" "$denied_status" "$local_before" "$local_after"
  sleep 5
done`, target, allowedPort, deniedPort)
	pod := coretest.Pod(coretest.PodOptions{
		ObjectMeta:                    metav1.ObjectMeta{Labels: map[string]string{"app": test.RandomName("node-public-ip-client")}},
		NodeName:                      nodeName,
		Image:                         nodePublicIPImage,
		Command:                       []string{"sh", "-c", script},
		TerminationGracePeriodSeconds: lo.ToPtr(int64(0)),
	})
	pod.Spec.HostNetwork = true
	return pod
}

// expectEchoes checks a client's log: allowedPort answered with the caller's public IP, and deniedPort didn't
// answer at all, including after allowedPort did.
func expectEchoes(g Gomega, pod *corev1.Pod, from nodeAddress) {
	ctx, cancel := context.WithTimeout(env.Context, nodePublicIPOperationTimeout)
	defer cancel()
	raw, err := env.KubeClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{}).DoRaw(ctx)
	g.Expect(err).ToNot(HaveOccurred(), "failed to read logs of %s", pod.Name)
	g.Expect(checkEchoes(string(raw), from)).To(Succeed())
}

func checkEchoes(raw string, from nodeAddress) error {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	var echoed bool
	for i, line := range lines {
		if status, ok := strings.CutPrefix(line, "denied-status="); ok {
			if status != "1" {
				return fmt.Errorf("port %d probe from %s returned status %q, expected connection failure (1)", deniedPort, from.nodeName, status)
			}
		}
		reply, ok := strings.CutPrefix(line, "allowed=")
		if !ok || i+4 >= len(lines) {
			continue
		}
		host, _, err := net.SplitHostPort(reply)
		if err == nil && host == from.address &&
			slices.Equal(lines[i+1:i+5], []string{"allowed-status=0", "denied-status=1", "local-before=0", "local-after=0"}) {
			echoed = true
		}
	}
	if !echoed {
		return fmt.Errorf("%s never completed a controlled probe with public IP %s echoed on port %d; last lines: %v",
			from.nodeName, from.address, allowedPort, lo.Subset(lines, -10, 10))
	}
	return nil
}

func isNodeResourceGroupLockedDown() bool {
	GinkgoHelper()
	mc := env.ExpectGetManagedCluster()
	return mc.Properties != nil && mc.Properties.NodeResourceGroupProfile != nil &&
		lo.FromPtr(mc.Properties.NodeResourceGroupProfile.RestrictionLevel) == containerservice.RestrictionLevelReadOnly
}

// allow removes rules that earlier runs left in the NSG, then adds a TCP allowedPort rule from sources to
// destinations at a priority no other rule uses.
func (r *nsgRules) allow(nsgID string, sources, destinations []string) {
	GinkgoHelper()
	nsg := lo.Must(arm.ParseResourceID(nsgID))
	existing := r.list(nsg)
	stale := lo.Filter(existing, func(rule *armnetwork.SecurityRule, _ int) bool {
		return strings.HasPrefix(lo.FromPtr(rule.Name), nsgRuleNamePrefix+"-")
	})
	for _, rule := range stale {
		By(fmt.Sprintf("removing stale NSG rule %s from %s", lo.FromPtr(rule.Name), nsg.Name))
		Eventually(func() error {
			return r.delete(env.Context, nsgRule{nsg: nsg, name: lo.FromPtr(rule.Name)})
		}).WithTimeout(nsgRuleCleanupTimeout).WithPolling(5 * time.Second).Should(Succeed())
	}

	name := test.RandomName(nsgRuleNamePrefix)
	r.added = append(r.added, nsgRule{nsg: nsg, name: name})
	ctx, cancel := context.WithTimeout(env.Context, nsgRuleCleanupTimeout)
	defer cancel()
	poller, err := r.clients.rules.BeginCreateOrUpdate(ctx, nsg.ResourceGroupName, nsg.Name, name, armnetwork.SecurityRule{
		Properties: &armnetwork.SecurityRulePropertiesFormat{
			Access:                     lo.ToPtr(armnetwork.SecurityRuleAccessAllow),
			Direction:                  lo.ToPtr(armnetwork.SecurityRuleDirectionInbound),
			Protocol:                   lo.ToPtr(armnetwork.SecurityRuleProtocolTCP),
			Priority:                   lo.ToPtr(freeRulePriority(lo.Without(existing, stale...), nsg)),
			SourceAddressPrefixes:      lo.ToSlicePtr(sources),
			SourcePortRange:            lo.ToPtr("*"),
			DestinationAddressPrefixes: lo.ToSlicePtr(destinations),
			DestinationPortRange:       lo.ToPtr(strconv.Itoa(allowedPort)),
		},
	}, nil)
	Expect(err).ToNot(HaveOccurred(), "failed to add rule to %s", nsgID)
	_, err = poller.PollUntilDone(ctx, nil)
	Expect(err).ToNot(HaveOccurred(), "failed to add rule to %s", nsgID)
}

// removeAll removes every rule allow added. It is safe to call more than once; rules that fail to delete are kept
// for the next call.
func (r *nsgRules) removeAll() {
	GinkgoHelper()
	if len(r.added) == 0 {
		return
	}
	By("removing the NSG rules before the nodes' public IPs are released")
	ctx, cancel := context.WithTimeout(context.WithoutCancel(env.Context), nsgRuleCleanupTimeout)
	defer cancel()
	Eventually(ctx, func() error { return r.remove(ctx) }).WithPolling(5 * time.Second).Should(Succeed())
}

func (r *nsgRules) remove(ctx context.Context) error {
	var errs []error
	r.added = lo.Filter(r.added, func(rule nsgRule, _ int) bool {
		err := r.delete(ctx, rule)
		errs = append(errs, err)
		return err != nil
	})
	return errors.Join(errs...)
}

func (r *nsgRules) delete(ctx context.Context, rule nsgRule) error {
	ctx, cancel := context.WithTimeout(ctx, nodePublicIPOperationTimeout)
	defer cancel()
	poller, err := r.clients.rules.BeginDelete(ctx, rule.nsg.ResourceGroupName, rule.nsg.Name, rule.name, nil)
	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("deleting rule %s from %s: %w", rule.name, rule.nsg.Name, err)
	}
	return nil
}

func (r *nsgRules) list(nsg *arm.ResourceID) []*armnetwork.SecurityRule {
	GinkgoHelper()
	var rules []*armnetwork.SecurityRule
	pager := r.clients.rules.NewListPager(nsg.ResourceGroupName, nsg.Name, nil)
	for pager.More() {
		page, err := pager.NextPage(env.Context)
		Expect(err).ToNot(HaveOccurred(), "failed to list rules in %s", nsg.Name)
		rules = append(rules, page.Value...)
	}
	return rules
}

func freeRulePriority(rules []*armnetwork.SecurityRule, nsg *arm.ResourceID) int32 {
	GinkgoHelper()
	used := map[int32]bool{}
	for _, rule := range rules {
		if rule.Properties != nil && rule.Properties.Priority != nil {
			used[*rule.Properties.Priority] = true
		}
	}
	for p := int32(nsgRulePriorityMin); p <= nsgRulePriorityMax; p++ {
		if !used[p] {
			return p
		}
	}
	Fail(fmt.Sprintf("no free rule priority in NSG %s", nsg.Name))
	return 0
}

func eventuallyExpectPublicIPsReleased(c *nodePublicIPClients, addrs []nodeAddress) {
	GinkgoHelper()
	By("expecting the nodes' public IPs to be released")
	Eventually(func(g Gomega) {
		for _, a := range addrs {
			id := lo.Must(arm.ParseResourceID(a.ipID))
			_, err := c.addresses.Get(env.Context, id.ResourceGroupName, id.Name, nil)
			g.Expect(isNotFound(err)).To(BeTrue(), "public IP %s still exists (err: %v)", a.ipID, err)
		}
	}).WithTimeout(nodePublicIPReleaseTimeout).WithPolling(15 * time.Second).Should(Succeed())
}

// eventuallyExpectLaunchError waits for an error matching matcher on one of the pool's NodeClaims: either the
// Launched condition (synchronous rejections, the NodeClaim is kept) or a Warning event (provisioning failures,
// emitted before the NodeClaim is deleted).
func eventuallyExpectLaunchError(pool *karpv1.NodePool, matcher types.GomegaMatcher) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
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
		if e.Type == corev1.EventTypeWarning && strings.HasPrefix(e.InvolvedObject.Name, pool.Name+"-") {
			messages = append(messages, e.Message)
		}
	}
	return messages
}

func isNotFound(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound
}

func TestNodePublicIPClientLogs(t *testing.T) {
	from := nodeAddress{nodeName: "source", address: "203.0.113.1"}
	success := "allowed=203.0.113.1:12345\nallowed-status=0\ndenied-status=1\nlocal-before=0\nlocal-after=0\n"
	for _, tc := range []struct {
		name    string
		logs    string
		wantErr bool
	}{
		{name: "successful controlled probe", logs: success},
		{name: "retry after NSG propagation", logs: "allowed=nc: timed out\nallowed-status=1\ndenied-status=1\nlocal-before=0\nlocal-after=0\n" + success},
		{name: "empty logs", wantErr: true},
		{name: "missing denied probe", logs: "allowed=203.0.113.1:12345\nallowed-status=0\n", wantErr: true},
		{name: "denied port connected", logs: strings.ReplaceAll(success, "denied-status=1", "denied-status=0"), wantErr: true},
		{name: "earlier denied connection", logs: "denied-status=0\n" + success, wantErr: true},
		{name: "probe command failed", logs: strings.ReplaceAll(success, "denied-status=1", "denied-status=127"), wantErr: true},
		{name: "wrong source IP", logs: strings.ReplaceAll(success, "203.0.113.1", "203.0.113.2"), wantErr: true},
		{name: "HTTP error instead of peer address", logs: strings.ReplaceAll(success, "203.0.113.1:12345", "HTTP/1.1 500 Internal Server Error"), wantErr: true},
		{name: "allowed command failed", logs: strings.ReplaceAll(success, "allowed-status=0", "allowed-status=1"), wantErr: true},
		{name: "listener not ready before probe", logs: strings.ReplaceAll(success, "local-before=0", "local-before=1"), wantErr: true},
		{name: "listener failed after probe", logs: strings.ReplaceAll(success, "local-after=0", "local-after=1"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			err := checkEchoes(tc.logs, from)
			if tc.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestNodePublicIPNSGCleanup(t *testing.T) {
	g := NewWithT(t)
	var attempts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodDelete {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if strings.HasSuffix(req.URL.Path, "/retry") && attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if strings.HasSuffix(req.URL.Path, "/missing") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	clientOptions := &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Cloud: cloud.Configuration{
			Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {Endpoint: server.URL, Audience: server.URL},
			},
		},
		Transport: server.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}
	ruleClient, err := armnetwork.NewSecurityRulesClient("subscription", &fake.TokenCredential{}, clientOptions)
	g.Expect(err).ToNot(HaveOccurred())
	nsg, err := arm.ParseResourceID("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg")
	g.Expect(err).ToNot(HaveOccurred())
	rules := &nsgRules{
		clients: &nodePublicIPClients{rules: ruleClient},
		added:   []nsgRule{{nsg: nsg, name: "removed"}, {nsg: nsg, name: "retry"}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	g.Expect(rules.remove(ctx)).To(HaveOccurred())
	g.Expect(rules.added).To(ConsistOf(nsgRule{nsg: nsg, name: "retry"}))
	g.Expect(rules.remove(ctx)).To(Succeed())
	g.Expect(rules.added).To(BeEmpty())
	g.Expect(attempts.Load()).To(Equal(int32(2)))
	g.Expect(rules.remove(ctx)).To(Succeed())
	rules.added = []nsgRule{{nsg: nsg, name: "missing"}}
	g.Expect(rules.remove(ctx)).To(Succeed())
	g.Expect(rules.added).To(BeEmpty())

	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	rules.added = []nsgRule{{nsg: nsg, name: "canceled"}}
	g.Expect(rules.remove(canceled)).To(MatchError(ContainSubstring("context canceled")))
	g.Expect(rules.added).To(HaveLen(1))
}
