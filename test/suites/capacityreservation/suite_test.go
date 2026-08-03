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

	corev1 "k8s.io/api/core/v1"
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
			VMSize:             reservedVMSize,
			Capacity:           reservedCapacity,
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

		By("Verifying Azure reports the reservation as consumed")
		Eventually(func(g Gomega) {
			allocated := env.ExpectCapacityReservationUtilization(ctx, groupName)
			g.Expect(allocated).To(HaveLen(1))
			g.Expect(strings.EqualFold(allocated[0], lo.FromPtr(vm.ID))).To(BeTrue(),
				"expected %s to be allocated against the reservation, got %v", lo.FromPtr(vm.ID), allocated)
		}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
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
	}).WithTimeout(3 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}
