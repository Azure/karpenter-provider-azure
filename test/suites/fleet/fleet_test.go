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

package fleet_test

import (
	"context"
	"fmt"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/karpenter/pkg/test"
)

var _ = Describe("Fleet", func() {
	Describe("On-Demand Provisioning", func() {
		It("should provision a single on-demand node via Fleet", func() {
			nodePool = test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      karpv1.CapacityTypeLabelKey,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{karpv1.CapacityTypeOnDemand},
			})
			env.ExpectCreated(nodePool, nodeClass)

			podLabels := map[string]string{"app": "fleet-ondemand-test"}
			dep := test.Deployment(test.DeploymentOptions{
				Replicas: 1,
				PodOptions: test.PodOptions{
					ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
					TerminationGracePeriodSeconds: lo.ToPtr(int64(0)),
				},
			})
			env.ExpectCreated(dep)

			// Verify pod becomes healthy
			env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(dep.Spec.Selector.MatchLabels), 1)

			// Verify node is created with on-demand label
			nodes := env.ExpectCreatedNodeCount("==", 1)
			Expect(nodes[0].Labels).To(HaveKeyWithValue(karpv1.CapacityTypeLabelKey, karpv1.CapacityTypeOnDemand))

			// Verify a Fleet resource exists in the node RG (proves Fleet path was used)
			fleets := listFleets(env)
			Expect(len(fleets)).To(BeNumerically(">=", 1))
			found := lo.ContainsBy(fleets, func(f *armcomputefleet.Fleet) bool {
				return f.Name != nil && len(*f.Name) > 0
			})
			Expect(found).To(BeTrue(), "expected at least one Fleet resource")

			env.ExpectDeleted(dep)
		})
	})

	Describe("Spot Provisioning", func() {
		It("should provision a spot node via Fleet", func() {
			nodePool = test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      karpv1.CapacityTypeLabelKey,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{karpv1.CapacityTypeSpot},
			})
			env.ExpectCreated(nodePool, nodeClass)

			podLabels := map[string]string{"app": "fleet-spot-test"}
			dep := test.Deployment(test.DeploymentOptions{
				Replicas: 1,
				PodOptions: test.PodOptions{
					ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
					NodeSelector: map[string]string{
						karpv1.CapacityTypeLabelKey: karpv1.CapacityTypeSpot,
					},
					Tolerations: []corev1.Toleration{
						{
							Key:      "kubernetes.azure.com/scalesetpriority",
							Operator: corev1.TolerationOpEqual,
							Value:    "spot",
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
					TerminationGracePeriodSeconds: lo.ToPtr(int64(0)),
				},
			})
			env.ExpectCreated(dep)

			// Verify pod is healthy
			env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(dep.Spec.Selector.MatchLabels), 1)

			// Verify node labels
			nodes := env.ExpectCreatedNodeCount("==", 1)
			Expect(nodes[0].Labels).To(HaveKeyWithValue(karpv1.CapacityTypeLabelKey, karpv1.CapacityTypeSpot))
			Expect(nodes[0].Labels).To(HaveKeyWithValue(v1beta1.AKSLabelScaleSetPriority, v1beta1.ScaleSetPrioritySpot))

			env.ExpectDeleted(dep)
		})
	})

	Describe("Batch Multiple NodeClaims", func() {
		It("should batch 3 NodeClaims into a single Fleet PUT", func() {
			nodePool = test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      karpv1.CapacityTypeLabelKey,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{karpv1.CapacityTypeOnDemand},
			})
			env.ExpectCreated(nodePool, nodeClass)

			podLabels := map[string]string{"app": "fleet-batch-test"}
			dep := test.Deployment(test.DeploymentOptions{
				Replicas: 3,
				PodOptions: test.PodOptions{
					ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
					ResourceRequirements: corev1.ResourceRequirements{
						// Request enough resources to force 1 pod per node
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1800m"),
							corev1.ResourceMemory: resource.MustParse("3Gi"),
						},
					},
					TerminationGracePeriodSeconds: lo.ToPtr(int64(0)),
				},
			})
			env.ExpectCreated(dep)

			// Verify all 3 pods are healthy
			env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(dep.Spec.Selector.MatchLabels), 3)

			// Verify 3 nodes created
			nodes := env.EventuallyExpectCreatedNodeCount("==", 3)
			for _, node := range nodes {
				Expect(node.Labels).To(HaveKeyWithValue(karpv1.CapacityTypeLabelKey, karpv1.CapacityTypeOnDemand))
			}

			// Verify batching: only 1 Fleet resource should exist (all 3 in same batch)
			fleets := listFleets(env)
			Expect(len(fleets)).To(Equal(1), "expected exactly 1 Fleet resource for batched requests")

			env.ExpectDeleted(dep)
		})
	})

	Describe("Zone Constraints", func() {
		It("should respect zone requirements in Fleet provisioning", func() {
			if !env.SupportsZones() {
				Skip("region does not support availability zones")
			}

			zones := env.GetAvailableZones()
			if len(zones) == 0 {
				Skip("no available zones")
			}
			targetZone := zones[0] // Pick first available zone

			nodePool = test.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{karpv1.CapacityTypeOnDemand},
				},
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      corev1.LabelTopologyZone,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{fmt.Sprintf("%s-%s", env.Region, targetZone)},
				},
			)
			env.ExpectCreated(nodePool, nodeClass)

			podLabels := map[string]string{"app": "fleet-zone-test"}
			dep := test.Deployment(test.DeploymentOptions{
				Replicas: 1,
				PodOptions: test.PodOptions{
					ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
					TerminationGracePeriodSeconds: lo.ToPtr(int64(0)),
				},
			})
			env.ExpectCreated(dep)

			// Verify pod is healthy
			env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(dep.Spec.Selector.MatchLabels), 1)

			// Verify node is in the correct zone
			nodes := env.ExpectCreatedNodeCount("==", 1)
			expectedZoneLabel := fmt.Sprintf("%s-%s", env.Region, targetZone)
			Expect(nodes[0].Labels).To(HaveKeyWithValue(corev1.LabelTopologyZone, expectedZoneLabel))

			env.ExpectDeleted(dep)
		})
	})
})

// listFleets returns all Fleet resources in the node resource group.
func listFleets(env *azure.Environment) []*armcomputefleet.Fleet {
	cred := env.GetDefaultCredential()
	client, err := armcomputefleet.NewFleetsClient(env.SubscriptionID, cred, nil)
	Expect(err).ToNot(HaveOccurred())

	pager := client.NewListByResourceGroupPager(env.NodeResourceGroup, nil)
	var fleets []*armcomputefleet.Fleet
	for pager.More() {
		page, err := pager.NextPage(context.Background())
		Expect(err).ToNot(HaveOccurred())
		fleets = append(fleets, page.Value...)
	}
	return fleets
}
