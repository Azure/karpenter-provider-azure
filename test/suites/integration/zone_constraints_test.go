package integration_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

// Helper function to find a requirement by key from the list of requirements
func findNodeSelectorRequirementByKey(requirements []karpv1.NodeSelectorRequirementWithMinValues, key string) *corev1.NodeSelectorRequirement {
	for _, req := range requirements {
		if req.Key == key {
			return &req.NodeSelectorRequirement
		}
	}
	return nil
}

var _ = Describe("Zone Constraints", func() {
	BeforeEach(func() {
		// Create a node pool with zone constraints
		nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      "topology.kubernetes.io/zone",
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{env.Region + "2"},
			},
		})
		env.ExpectCreated(nodePool, nodeClass)
	})

	It("should be respected", func() {
		// Deploy spread pods
		podCount := 6
		spreadDep := test.Deployment(test.DeploymentOptions{
			Replicas: int32(podCount),
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "spread-app"},
				},
				NodeRequirements: []corev1.NodeSelectorRequirement{
					{
						Key:      "karpenter.sh/registered",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"true"},
					},
				},
				PodAntiRequirements: []corev1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app": "spread-app"},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		})
		env.ExpectCreated(spreadDep)

		// Verify that all pods are created
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(spreadDep.Spec.Selector.MatchLabels), podCount)

		// Verify nodes are created with the count equals to replicas, all in the correct zone
		nodes := env.ExpectCreatedNodeCount("==", podCount)
		for _, node := range nodes {
			// Find the zone requirement from nodePool requirements
			zoneConstraint := findNodeSelectorRequirementByKey(nodePool.Spec.Template.Spec.Requirements, "topology.kubernetes.io/zone")
			Expect(zoneConstraint).NotTo(BeNil(), "Zone requirement not found in nodePool")

			// Check that node zone is one of the allowed values
			nodeZone := node.Labels["topology.kubernetes.io/zone"]
			Expect(zoneConstraint.Values).To(ContainElement(nodeZone),
				"Node zone %s is not in the allowed zones %v", nodeZone, zoneConstraint.Values)
		}

		// Delete the deployment to clean up
		env.ExpectDeleted(spreadDep)
	})

	It("should create nodes that zone constraints can satisfy", func() {
		zoneConstraint := findNodeSelectorRequirementByKey(nodePool.Spec.Template.Spec.Requirements, "topology.kubernetes.io/zone")
		Expect(zoneConstraint).NotTo(BeNil(), "Zone requirement not found in nodePool")
		allowedZonesCount := len(zoneConstraint.Values)

		// Deploy zone spread pods, more than the allowed zones
		podCount := allowedZonesCount * 2
		zoneSpreadDep := test.Deployment(test.DeploymentOptions{
			Replicas: int32(podCount),
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "zone-spread-app"},
				},
				NodeRequirements: []corev1.NodeSelectorRequirement{
					{
						Key:      "karpenter.sh/registered",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"true"},
					},
				},
				TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       "topology.kubernetes.io/zone",
						WhenUnsatisfiable: corev1.DoNotSchedule,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app": "zone-spread-app"},
						},
					},
				},
			},
		})
		env.ExpectCreated(zoneSpreadDep)

		// Verify that pod is created as much as possible, == number of allowed zones
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(zoneSpreadDep.Spec.Selector.MatchLabels), allowedZonesCount)

		// Verify node count consistently stays, only counting nodes created by this test
		env.ConsistentlyExpectCreatedNodeCount("==", allowedZonesCount, 5*time.Minute)

		// Verify that pod count is still the same
		env.ExpectHealthyPodCount(labels.SelectorFromSet(zoneSpreadDep.Spec.Selector.MatchLabels), allowedZonesCount)

		// Delete the deployment to clean up
		env.ExpectDeleted(zoneSpreadDep)
	})

	It("should not create nodes if zone constraints cannot satisfy", func() {
		// Deploy pods that nodepool constaints cannot be satisfied
		unsatisfiableDep := test.Deployment(test.DeploymentOptions{
			Replicas: int32(3),
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "unsatisfiable-app"},
				},
				NodeRequirements: []corev1.NodeSelectorRequirement{
					{
						Key:      "karpenter.sh/registered",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"true"},
					},
					{
						Key:      "topology.kubernetes.io/zone",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{env.Region + "1"},
					},
				},
			},
		})
		env.ExpectCreated(unsatisfiableDep)

		// Verify that no nodes are created as a result of the unsatisfiable deployment
		env.ConsistentlyExpectNodeCount("==", 0, 5*time.Minute)

		// Delete the deployment to clean up
		env.ExpectDeleted(unsatisfiableDep)
	})
})
