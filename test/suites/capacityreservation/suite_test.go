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

package capacityreservation_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/apis/v1alpha1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	nodeclassstatus "github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *azure.Environment

func TestCapacityReservation(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	AfterSuite(func() {
		env.Stop()
	})

	RunSpecs(t, "CapacityReservation Suite")
}

var _ = BeforeEach(func() { env.BeforeEach() })
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

// reservedVMSize must be in the D family so it satisfies the default NodePool's
// sku-family requirement, and small enough to keep the reserved capacity cheap.
const reservedVMSize = "Standard_D2s_v3"

// reservedCapacity is deliberately zero. Azure only accepts a non-zero quantity when it
// can genuinely set that capacity aside, and reservable capacity is far scarcer than
// on-demand capacity: at the time of writing no D-family size could be reserved in
// westus2 at all, in any zone, with the family quota completely unused. A zero-quantity
// reservation is a supported configuration -- a VM associates with it and shows up in the
// reservation's utilization, it is simply overallocated -- so the association path can be
// tested end to end without depending on scarce capacity, and for free.
const reservedCapacity = int32(0)

// expensiveReservedVMSize is a second reservable size in the same family, priced well
// above reservedVMSize so that an unaided scheduler would never pick it. The overlay
// spec reserves both and asserts the overlay flips the choice.
const expensiveReservedVMSize = "Standard_D8s_v3"

var _ = Describe("CapacityReservation", func() {
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		nodeClass = env.DefaultAKSNodeClass()
	})

	It("should launch a node into the reserved capacity", func(ctx SpecContext) {
		if !env.InClusterController {
			Skip("requires granting the Karpenter workload identity a role on the capacity reservation group")
		}
		if env.IsAKSMachineAPIMode() {
			Skip("the AKS Machine API cannot pass a capacity reservation group through yet; the NodeClass fails closed instead")
		}

		By("Reserving capacity in Azure")
		armZone, expectedNodeZone := reservationPlacement()
		groupName := fmt.Sprintf("karpenter-e2e-crg-%d", time.Now().UnixNano())
		groupID := env.ExpectCreatedCapacityReservationGroup(ctx, azure.CapacityReservationGroupOptions{
			Name:               groupName,
			Members:            []azure.CapacityReservationMember{{VMSize: reservedVMSize, Capacity: reservedCapacity}},
			ARMZones:           lo.Ternary(armZone == "", nil, []string{armZone}),
			GrantToPrincipalID: env.GetKarpenterWorkloadIdentity(ctx),
		})

		By("Pointing a NodeClass at the group")
		nodeClass.Spec.CapacityReservationGroupID = lo.ToPtr(groupID)
		nodePool := env.DefaultNodePool(nodeClass)
		deployment := test.Deployment(test.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)

		By("Waiting for the group to resolve and the NodeClass to become Ready")
		expectCapacityReservationGroupCondition(ctx, nodeClass, func(g Gomega, condition *status.Condition) {
			g.Expect(condition.IsTrue()).To(BeTrue(), "expected the group to resolve, got %s: %s", condition.Reason, condition.Message)
		})

		By("Verifying ARM reports the member's provisioning state in the list response")
		// Eligibility fails open on an absent state, so if ARM omitted this field the gate
		// would silently never gate. Only a live list call can show that it does not.
		resolved := &v1beta1.AKSNodeClass{}
		Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), resolved)).To(Succeed())
		Expect(resolved.Status.CapacityReservationGroup.CapacityReservations).To(HaveLen(1))
		Expect(lo.FromPtr(resolved.Status.CapacityReservationGroup.CapacityReservations[0].ProvisioningState)).
			To(Equal(v1beta1.CapacityReservationProvisioningStateSucceeded))

		By("Waiting for the node to join and the workload to become healthy")
		nodes := env.EventuallyExpectCreatedNodeCount("==", 1)
		env.EventuallyExpectHealthyDeployment(deployment)

		By("Verifying the VM was allocated against the group")
		vm := env.GetVM(nodes[0].Name)
		Expect(vm.Properties.CapacityReservation).ToNot(BeNil(), "VM was launched without a capacity reservation profile")
		Expect(vm.Properties.CapacityReservation.CapacityReservationGroup).ToNot(BeNil())
		actualGroupID := lo.FromPtr(vm.Properties.CapacityReservation.CapacityReservationGroup.ID)
		// ARM echoes resource IDs back with different casing than it was given.
		Expect(strings.EqualFold(actualGroupID, groupID)).To(BeTrue(), "expected the VM to be associated with %s, got %s", groupID, actualGroupID)

		By("Verifying offering projection picked the reserved SKU and placement")
		Expect(string(lo.FromPtr(vm.Properties.HardwareProfile.VMSize))).To(Equal(reservedVMSize))
		if expectedNodeZone == zones.Regional {
			Expect(vm.Zones).To(BeEmpty(), "a regional group requires the VM to carry no zone")
		} else {
			Expect(lo.Map(vm.Zones, func(z *string, _ int) string { return lo.FromPtr(z) })).To(ConsistOf(armZone))
		}
		Expect(nodes[0].Labels[corev1.LabelTopologyZone]).To(Equal(expectedNodeZone))

		By("Verifying the configured group is recorded on the NodeClaim and the Node")
		// Core copies NodeClaim annotations onto the Node at registration, so the Node picks
		// this up rather than the launch path setting it twice.
		Eventually(func(g Gomega) {
			claims := &karpv1.NodeClaimList{}
			g.Expect(env.Client.List(ctx, claims, client.MatchingLabels{karpv1.NodePoolLabelKey: nodePool.Name})).To(Succeed())
			g.Expect(claims.Items).To(HaveLen(1))
			g.Expect(claims.Items[0].Annotations).To(HaveKeyWithValue(v1beta1.AnnotationCapacityReservationGroupID, groupID))

			node := &corev1.Node{}
			g.Expect(env.Client.Get(ctx, client.ObjectKey{Name: nodes[0].Name}, node)).To(Succeed())
			g.Expect(node.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationCapacityReservationGroupID, groupID))
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("Verifying Azure reports the reservation as consumed")
		Eventually(func(g Gomega) {
			allocated := env.ExpectCapacityReservationUtilization(ctx, groupName, reservedVMSize)
			g.Expect(allocated).To(HaveLen(1))
			g.Expect(strings.EqualFold(allocated[0], lo.FromPtr(vm.ID))).To(BeTrue(),
				"expected %s to be allocated against the reservation, got %v", lo.FromPtr(vm.ID), allocated)
		}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	})

	// A regional group is a distinct launch shape rather than a variation on the zonal one:
	// the VM must carry no zone at all. It is also the case the default offering rank works
	// against, since it prefers zonal, so the NodePool below deliberately leaves the zone
	// unconstrained -- a zonal offering winning here would produce a request Azure rejects.
	It("should launch a node into a regional reserved capacity group", func(ctx SpecContext) {
		if !env.InClusterController {
			Skip("requires granting the Karpenter workload identity a role on the capacity reservation group")
		}
		if env.IsAKSMachineAPIMode() {
			Skip("the AKS Machine API cannot pass a capacity reservation group through yet; the NodeClass fails closed instead")
		}

		By("Reserving capacity in Azure without a zone")
		groupName := fmt.Sprintf("karpenter-e2e-crg-regional-%d", time.Now().UnixNano())
		groupID := env.ExpectCreatedCapacityReservationGroup(ctx, azure.CapacityReservationGroupOptions{
			Name:               groupName,
			Members:            []azure.CapacityReservationMember{{VMSize: reservedVMSize, Capacity: reservedCapacity}},
			GrantToPrincipalID: env.GetKarpenterWorkloadIdentity(ctx),
		})

		By("Pointing a NodeClass at the group")
		nodeClass.Spec.CapacityReservationGroupID = lo.ToPtr(groupID)
		nodePool := env.DefaultNodePool(nodeClass)
		test.ReplaceRequirements(nodePool,
			karpv1.NodeSelectorRequirementWithMinValues{Key: corev1.LabelInstanceTypeStable, Operator: corev1.NodeSelectorOpIn, Values: []string{reservedVMSize}},
			karpv1.NodeSelectorRequirementWithMinValues{Key: karpv1.CapacityTypeLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{karpv1.CapacityTypeOnDemand}},
		)
		deployment := test.Deployment(test.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)

		expectCapacityReservationGroupCondition(ctx, nodeClass, func(g Gomega, condition *status.Condition) {
			g.Expect(condition.IsTrue()).To(BeTrue(), "expected the group to resolve, got %s: %s", condition.Reason, condition.Message)
		})

		By("Waiting for the node to join and the workload to become healthy")
		nodes := env.EventuallyExpectCreatedNodeCount("==", 1)
		env.EventuallyExpectHealthyDeployment(deployment)

		By("Verifying the launch carried no zone and targeted the group")
		vm := env.GetVM(nodes[0].Name)
		expectVMOnReservedCapacity(vm, groupID)
		Expect(string(lo.FromPtr(vm.Properties.HardwareProfile.VMSize))).To(Equal(reservedVMSize))
		Expect(vm.Zones).To(BeEmpty(), "a regional group requires the VM to carry no zone")
		Expect(nodes[0].Labels[corev1.LabelTopologyZone]).To(Equal(zones.Regional))
		Expect(nodes[0].Labels[v1beta1.LabelPlacementScope]).To(Equal(v1beta1.PlacementScopeRegional))
	})

	It("should keep the NodeClass NotReady when the group cannot be resolved", func(ctx SpecContext) {
		By("Pointing a NodeClass at a group that was never created")
		nodeClass.Spec.CapacityReservationGroupID = lo.ToPtr(fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/capacityReservationGroups/karpenter-e2e-crg-does-not-exist",
			env.SubscriptionID, env.NodeResourceGroup))
		nodePool := env.DefaultNodePool(nodeClass)
		deployment := test.Deployment(test.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)

		By("Verifying the group condition reports why")
		// ARM will not tell a caller that cannot read a resource whether it exists, so a
		// name that was never created comes back as 403 rather than 404 unless the identity
		// happens to hold read rights over the enclosing scope. Both answers mean the same
		// thing here: the group did not resolve, so nothing may launch.
		expectedReasons := []string{
			nodeclassstatus.CapacityReservationGroupUnreadyReasonNotFound,
			nodeclassstatus.CapacityReservationGroupUnreadyReasonAccessDenied,
		}
		if env.IsAKSMachineAPIMode() {
			expectedReasons = []string{nodeclassstatus.CapacityReservationGroupUnreadyReasonUnsupportedProvisionMode}
		}
		expectCapacityReservationGroupCondition(ctx, nodeClass, func(g Gomega, condition *status.Condition) {
			g.Expect(condition.IsFalse()).To(BeTrue(), "expected the group to fail to resolve")
			g.Expect(condition.Reason).To(BeElementOf(expectedReasons))
		})

		By("Verifying nothing is launched without reserved capacity")
		// Consistently rather than Eventually: the point is that a NodeClass naming an
		// unusable group never falls back to unreserved capacity.
		Consistently(func(g Gomega) {
			g.Expect(env.Monitor.CreatedNodeCount()).To(Equal(0))
		}).WithTimeout(90 * time.Second).WithPolling(15 * time.Second).Should(Succeed())
	})
})

// These two cover the shapes the operator playbook recommends. The mechanics they rest on
// -- static replicas, drift, overlay pricing -- belong to core and are tested there; what
// is verified here is that the recommended shape still lands on the reserved capacity,
// which is the part core will never check.
var _ = Describe("CapacityReservation operational shapes", func() {
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		if !env.InClusterController {
			Skip("requires granting the Karpenter workload identity a role on the capacity reservation group")
		}
		if env.IsAKSMachineAPIMode() {
			Skip("the AKS Machine API cannot pass a capacity reservation group through yet")
		}
		nodeClass = env.DefaultAKSNodeClass()
	})

	// The playbook's stable-baseline recipe: one static NodePool per member reservation,
	// replicas for occupancy, limits at N+1 so a drift replacement has room to surge, and
	// expireAfter Never so the 720h default does not recycle the pool.
	It("should keep a static pool on the reserved capacity across a drift replacement", func(ctx SpecContext) {
		env.ExpectSettingsOverridden(corev1.EnvVar{Name: "FEATURE_GATES", Value: featureGatesWith("StaticCapacity=true")})

		By("Reserving capacity in Azure")
		armZone, expectedNodeZone := reservationPlacement()
		groupName := fmt.Sprintf("karpenter-e2e-crg-static-%d", time.Now().UnixNano())
		groupID := env.ExpectCreatedCapacityReservationGroup(ctx, azure.CapacityReservationGroupOptions{
			Name:               groupName,
			Members:            []azure.CapacityReservationMember{{VMSize: reservedVMSize, Capacity: reservedCapacity}},
			ARMZones:           lo.Ternary(armZone == "", nil, []string{armZone}),
			GrantToPrincipalID: env.GetKarpenterWorkloadIdentity(ctx),
		})

		By("Creating a static NodePool with headroom for a replacement")
		nodeClass.Spec.CapacityReservationGroupID = lo.ToPtr(groupID)
		// One replica, limit at N+1: enough to exercise the recipe and the surge that drift
		// needs, without paying for a second full replacement cycle in every run.
		nodePool := reservedNodePool(nodeClass, expectedNodeZone, reservedVMSize)
		nodePool.Spec.Replicas = lo.ToPtr[int64](1)
		nodePool.Spec.Limits = karpv1.Limits{"nodes": resource.MustParse("2")}
		env.ExpectCreated(nodeClass, nodePool)

		// A static pool provisions off the NodeClass rather than off pending pods, so an
		// unresolved group shows up only as nodes that never appear. Gating here reports the
		// group's own reason instead, and absorbs the wait for the role assignment to take
		// effect rather than spending the replica-count budget on it.
		By("Waiting for the group to resolve and the NodeClass to become Ready")
		expectCapacityReservationGroupCondition(ctx, nodeClass, func(g Gomega, condition *status.Condition) {
			g.Expect(condition.IsTrue()).To(BeTrue(), "expected the group to resolve, got %s: %s", condition.Reason, condition.Message)
		})

		By("Waiting for the pool to reach its replica count")
		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
		env.EventuallyExpectInitializedNodeCount("==", 1)
		originalNames := expectNodeClaimsOnReservedCapacity(ctx, nodePool, groupID, expectedNodeZone, 1)

		By("Forcing drift on the NodeClass")
		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
			nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](100)
			g.Expect(env.Client.Update(ctx, nodeClass)).To(Succeed())
		}).WithTimeout(time.Minute).Should(Succeed())

		By("Waiting for every original NodeClaim to be replaced")
		// The pool has limits at N+1, so replacement surges to 3 and settles back to 2.
		Eventually(func(g Gomega) {
			claims := currentNodeClaims(ctx, g, nodePool)
			names := sets.New(lo.Map(claims, func(nc *karpv1.NodeClaim, _ int) string { return nc.Name })...)
			g.Expect(names.Intersection(originalNames)).To(BeEmpty(), "original claims should all be replaced")
			g.Expect(names).To(HaveLen(1), "the pool should settle back to its replica count")
			for _, claim := range claims {
				g.Expect(claim.StatusConditions().Get(karpv1.ConditionTypeDrifted).IsTrue()).To(BeFalse(), "%s is still drifted", claim.Name)
				g.Expect(claim.StatusConditions().Get(karpv1.ConditionTypeInitialized).IsTrue()).To(BeTrue(), "%s is not initialized", claim.Name)
			}
		}).WithTimeout(20 * time.Minute).WithPolling(20 * time.Second).Should(Succeed())

		By("Verifying the replacements are still on the reserved capacity")
		expectNodeClaimsOnReservedCapacity(ctx, nodePool, groupID, expectedNodeZone, 1)
	})

	// The playbook's flexible-case recipe: a NodeOverlay priced at zero to pull scheduling
	// into an otherwise more expensive reserved bucket.
	It("should launch the overlaid SKU into the reserved capacity", func(ctx SpecContext) {
		env.ExpectSettingsOverridden(corev1.EnvVar{Name: "FEATURE_GATES", Value: featureGatesWith("NodeOverlay=true")})

		By("Reserving two sizes in one group")
		armZone, expectedNodeZone := reservationPlacement()
		groupName := fmt.Sprintf("karpenter-e2e-crg-overlay-%d", time.Now().UnixNano())
		groupID := env.ExpectCreatedCapacityReservationGroup(ctx, azure.CapacityReservationGroupOptions{
			Name: groupName,
			Members: []azure.CapacityReservationMember{
				{VMSize: reservedVMSize, Capacity: reservedCapacity},
				{VMSize: expensiveReservedVMSize, Capacity: reservedCapacity},
			},
			ARMZones:           lo.Ternary(armZone == "", nil, []string{armZone}),
			GrantToPrincipalID: env.GetKarpenterWorkloadIdentity(ctx),
		})

		By("Pricing the expensive size at zero for this NodePool")
		nodeClass.Spec.CapacityReservationGroupID = lo.ToPtr(groupID)
		nodePool := reservedNodePool(nodeClass, expectedNodeZone, reservedVMSize, expensiveReservedVMSize)
		overlay := &v1alpha1.NodeOverlay{
			ObjectMeta: metav1.ObjectMeta{Name: "crg-overlay-" + strings.ToLower(strings.ReplaceAll(expensiveReservedVMSize, "_", "-"))},
			Spec: v1alpha1.NodeOverlaySpec{
				Weight: lo.ToPtr[int32](100),
				Requirements: []v1alpha1.NodeSelectorRequirement{
					{Key: karpv1.NodePoolLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{nodePool.Name}},
					{Key: corev1.LabelInstanceTypeStable, Operator: corev1.NodeSelectorOpIn, Values: []string{expensiveReservedVMSize}},
				},
				Price: lo.ToPtr("0"),
			},
		}
		deployment := test.Deployment(test.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool)

		// The group has to resolve before the overlay is created. NodeOverlay keys its price
		// updates by offering requirements, and the capacity reservation projection rewrites
		// those requirements (UltraSSD pinned to "false", zones narrowed to the reservation).
		// An overlay computed against the unprojected offerings keys nothing that scheduling
		// later looks up, and silently has no effect.
		By("Waiting for the capacity reservation group to resolve")
		expectCapacityReservationGroupCondition(ctx, nodeClass, func(g Gomega, condition *status.Condition) {
			g.Expect(condition.IsTrue()).To(BeTrue(), "reason=%s message=%s", condition.Reason, condition.Message)
		})

		By("Pricing the expensive size at zero for this NodePool")
		env.ExpectCreated(overlay)
		Eventually(func(g Gomega) {
			retrieved := &v1alpha1.NodeOverlay{}
			g.Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(overlay), retrieved)).To(Succeed())
			g.Expect(retrieved.StatusConditions().Root().IsTrue()).To(BeTrue(), "overlay not ready: %+v", retrieved.Status.Conditions)
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("Waiting for the node and workload")
		env.ExpectCreated(deployment)
		nodes := env.EventuallyExpectCreatedNodeCount("==", 1)
		env.EventuallyExpectHealthyDeployment(deployment)

		By("Verifying the overlaid size won and is on the reserved capacity")
		vm := env.GetVM(nodes[0].Name)
		Expect(string(lo.FromPtr(vm.Properties.HardwareProfile.VMSize))).To(Equal(expensiveReservedVMSize),
			"the overlay should have pulled scheduling onto the more expensive reserved size")
		expectVMOnReservedCapacity(vm, groupID)
		Expect(nodes[0].Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
		Expect(nodes[0].Labels[corev1.LabelTopologyZone]).To(Equal(expectedNodeZone))
	})
})

// featureGatesWith returns the deployed gate string with one gate forced on, so that
// enabling an alpha gate does not silently drop whatever else the cluster was running.
func featureGatesWith(gate string) string {
	GinkgoHelper()
	name := strings.SplitN(gate, "=", 2)[0]
	deployment := &appsv1.Deployment{}
	Expect(env.Client.Get(env.Context, types.NamespacedName{Namespace: "kube-system", Name: "karpenter"}, deployment)).To(Succeed())
	current, found := lo.Find(deployment.Spec.Template.Spec.Containers[0].Env, func(e corev1.EnvVar) bool { return e.Name == "FEATURE_GATES" })
	if !found {
		return gate
	}
	gates := lo.Map(strings.Split(current.Value, ","), func(g string, _ int) string {
		if strings.HasPrefix(g, name+"=") {
			return gate
		}
		return g
	})
	if !lo.Contains(gates, gate) {
		gates = append(gates, gate)
	}
	return strings.Join(gates, ",")
}

// reservedNodePool pins a pool to one placement and an explicit set of sizes, so that the
// spec under test is not at the mercy of whatever else the region offers.
func reservedNodePool(nodeClass *v1beta1.AKSNodeClass, nodeZone string, vmSizes ...string) *karpv1.NodePool {
	nodePool := env.DefaultNodePool(nodeClass)
	nodePool.Spec.Template.Spec.ExpireAfter = karpv1.MustParseNillableDuration("Never")
	test.ReplaceRequirements(nodePool,
		karpv1.NodeSelectorRequirementWithMinValues{Key: corev1.LabelInstanceTypeStable, Operator: corev1.NodeSelectorOpIn, Values: vmSizes},
		karpv1.NodeSelectorRequirementWithMinValues{Key: corev1.LabelTopologyZone, Operator: corev1.NodeSelectorOpIn, Values: []string{nodeZone}},
		karpv1.NodeSelectorRequirementWithMinValues{Key: karpv1.CapacityTypeLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{karpv1.CapacityTypeOnDemand}},
	)
	return nodePool
}

// currentNodeClaims lists the pool's live NodeClaims. The environment monitor accumulates
// every node the test ever created, which would include the ones drift just replaced.
func currentNodeClaims(ctx SpecContext, g Gomega, nodePool *karpv1.NodePool) []*karpv1.NodeClaim {
	list := &karpv1.NodeClaimList{}
	g.Expect(env.Client.List(ctx, list, client.MatchingLabels{karpv1.NodePoolLabelKey: nodePool.Name})).To(Succeed())
	return lo.FilterMap(list.Items, func(nc karpv1.NodeClaim, _ int) (*karpv1.NodeClaim, bool) {
		return &nc, nc.DeletionTimestamp.IsZero()
	})
}

// expectNodeClaimsOnReservedCapacity asserts every live NodeClaim in the pool is backed by
// a VM in the group, and returns their names.
func expectNodeClaimsOnReservedCapacity(ctx SpecContext, nodePool *karpv1.NodePool, groupID, expectedNodeZone string, expected int) sets.Set[string] {
	GinkgoHelper()
	claims := currentNodeClaims(ctx, Default, nodePool)
	Expect(claims).To(HaveLen(expected))
	for _, claim := range claims {
		Expect(claim.Status.NodeName).ToNot(BeEmpty(), "%s has no node yet", claim.Name)
		// Assert against the VM itself, not just the claim's labels: the labels only say
		// what Karpenter intended, the VM says what Azure actually built.
		vm := env.GetVM(claim.Status.NodeName)
		expectVMOnReservedCapacity(vm, groupID)
		Expect(string(lo.FromPtr(vm.Properties.HardwareProfile.VMSize))).To(Equal(reservedVMSize), "%s is not the reserved size", claim.Name)
		Expect(claim.Labels[corev1.LabelInstanceTypeStable]).To(Equal(reservedVMSize))
		Expect(claim.Labels[corev1.LabelTopologyZone]).To(Equal(expectedNodeZone))
		Expect(claim.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
	}
	return sets.New(lo.Map(claims, func(nc *karpv1.NodeClaim, _ int) string { return nc.Name })...)
}

func expectVMOnReservedCapacity(vm armcompute.VirtualMachine, groupID string) {
	GinkgoHelper()
	Expect(vm.Properties.CapacityReservation).ToNot(BeNil(), "VM was launched without a capacity reservation profile")
	Expect(vm.Properties.CapacityReservation.CapacityReservationGroup).ToNot(BeNil())
	actual := lo.FromPtr(vm.Properties.CapacityReservation.CapacityReservationGroup.ID)
	Expect(strings.EqualFold(actual, groupID)).To(BeTrue(), "expected the VM to target %s, got %s", groupID, actual)
}

// reservationPlacement returns the ARM zone to reserve in and the zone label the
// resulting node should carry. An empty ARM zone means a regional reservation.
func reservationPlacement() (armZone string, nodeZone string) {
	GinkgoHelper()
	available := env.GetAvailableZones()
	if len(available) == 0 {
		return "", zones.Regional
	}
	return available[0], zones.MakeAKSLabelZoneFromARMZone(env.Region, available[0])
}

func expectCapacityReservationGroupCondition(ctx SpecContext, nodeClass *v1beta1.AKSNodeClass, assert func(Gomega, *status.Condition)) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		retrieved := &v1beta1.AKSNodeClass{}
		g.Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), retrieved)).To(Succeed())
		condition := retrieved.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady)
		g.Expect(condition).ToNot(BeNil(), "expected a %s condition", v1beta1.ConditionTypeCapacityReservationGroupReady)
		assert(g, condition)
		// The group condition gates Ready only when the field is set, so the two must agree.
		g.Expect(retrieved.StatusConditions().Get(status.ConditionReady).IsTrue()).To(Equal(condition.IsTrue()))
		// Generous because the role assignment granting read on the group is made moments
		// earlier, and Azure takes its time making one effective; the reconciler retries every
		// minute until it does.
	}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}
