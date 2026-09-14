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

package capacitybuffer_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	autoscalingv1beta1 "sigs.k8s.io/karpenter/pkg/apis/autoscaling/v1beta1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

const (
	bufferTemplateName = "capacity-buffer-template"
	testInstanceType   = "Standard_D2s_v5"
)

// This suite mirrors the relevant behavioral contract from
// sigs.k8s.io/karpenter/test/suites/regression/capacitybuffer_test.go so it can
// run independently in Azure provider CI. Correspondence comments identify
// direct replicas, Azure-specific adaptations, and additional provider coverage.
var _ = Describe("CapacityBuffer", func() {
	BeforeEach(func() {
		if !env.InClusterController {
			Skip("CapacityBuffer tests require the controller to be running in-cluster")
		}
		featureGates, found := lo.Find(env.ExpectSettings(), func(envVar corev1.EnvVar) bool {
			return envVar.Name == "FEATURE_GATES"
		})
		Expect(found).To(BeTrue(), "expected FEATURE_GATES in the controller environment")
		Expect(featureGates.Value).To(MatchRegexp(`(?i)(^|,)CapacityBuffer=true(,|$)`))

		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("Never")
	})

	// Upstream correspondence: "should provision capacity when a buffer with podTemplateRef is applied".
	// Azure adaptation: assert bounded scale from zero without assuming a KWOK-specific node count.
	It("should remain inert without a CapacityBuffer and provision bounded PodTemplate capacity from zero", func() {
		env.ExpectCreated(nodeClass, nodePool)

		Consistently(func(g Gomega) {
			nodeClaims := &karpv1.NodeClaimList{}
			g.Expect(env.Client.List(env, nodeClaims, client.HasLabels{test.DiscoveryLabel})).To(Succeed())
			g.Expect(nodeClaims.Items).To(BeEmpty())
		}).WithTimeout(15 * time.Second).Should(Succeed())

		podTemplate := newPodTemplate(bufferTemplateName, "1", "1Gi")
		buffer := newPodTemplateBuffer("scale-from-zero", podTemplate.Name, 1)
		buffer.Spec.Limits = autoscalingv1beta1.Limits{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("1Gi"),
		}
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferReady(env, env.Client, buffer)
		EventuallyExpectCapacityBufferReplicas(env, env.Client, buffer, 1)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
		env.EventuallyExpectRegisteredNodeClaimCount("==", 1)
		nodes := env.EventuallyExpectInitializedNodeCount("==", 1)
		Expect(nodes[0].Labels).To(HaveKeyWithValue(corev1.LabelOSStable, string(corev1.Linux)))
		Expect(nodes[0].Labels).To(HaveKeyWithValue(karpv1.CapacityTypeLabelKey, karpv1.CapacityTypeOnDemand))
	})

	// Upstream correspondence: "should update buffer status when PodTemplate is updated".
	It("should update buffer status when its PodTemplate changes", func() {
		env.ExpectCreated(nodeClass, nodePool)
		podTemplate := newPodTemplate("updated-template", "500m", "256Mi")
		buffer := newPodTemplateBuffer("updated-template", podTemplate.Name, 1)
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferReady(env, env.Client, buffer)
		current := &autoscalingv1beta1.CapacityBuffer{}
		Expect(env.Client.Get(env, client.ObjectKeyFromObject(buffer), current)).To(Succeed())
		Expect(current.Status.PodTemplateGeneration).ToNot(BeNil())
		originalTemplateGeneration := *current.Status.PodTemplateGeneration

		updatedTemplate := &corev1.PodTemplate{}
		Expect(env.Client.Get(env, client.ObjectKeyFromObject(podTemplate), updatedTemplate)).To(Succeed())
		updatedTemplate.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("750m")
		env.ExpectUpdated(updatedTemplate)

		EventuallyExpectCapacityBufferGenerationUpdated(env, env.Client, buffer, originalTemplateGeneration+1)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
	})

	// Upstream correspondence: both consumer/refill regressions and real-pod coexistence.
	// Azure adaptation: pin a known SKU and prove the consumer uses the original node before refill.
	It("should let a real consumer use buffered capacity and refill the buffer", func() {
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      corev1.LabelInstanceTypeStable,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{testInstanceType},
		})
		env.ExpectCreated(nodeClass, nodePool)

		podTemplate := newPodTemplate(bufferTemplateName, "1", "512Mi")
		buffer := newPodTemplateBuffer("refill", podTemplate.Name, 1)
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
		initialNode := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		env.EventuallyExpectRegisteredNodeClaimCount("==", 1)
		initialProvisioningTransition := provisioningTransitionTime(buffer)

		consumer := test.Deployment(test.DeploymentOptions{
			ObjectMeta: metav1.ObjectMeta{Name: "buffer-consumer"},
			Replicas:   1,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "buffer-consumer"}},
				NodeSelector: map[string]string{
					karpv1.NodePoolLabelKey: nodePool.Name,
				},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("512Mi"),
					},
				},
			},
		})
		env.ExpectCreated(consumer)

		pods := env.EventuallyExpectHealthyDeployment(consumer)
		Expect(pods[0].Spec.NodeName).To(Equal(initialNode.Name))
		EventuallyExpectCapacityBufferNotProvisioned(env, env.Client, buffer)
		env.EventuallyExpectRegisteredNodeClaimCount(">=", 2)
		Eventually(func(g Gomega) {
			current := &autoscalingv1beta1.CapacityBuffer{}
			g.Expect(env.Client.Get(env, client.ObjectKeyFromObject(buffer), current)).To(Succeed())
			condition := apimeta.FindStatusCondition(current.Status.Conditions, autoscalingv1beta1.ProvisioningCondition)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(condition.LastTransitionTime.After(initialProvisioningTransition.Time)).To(BeTrue())
		}).Should(Succeed())
	})

	// Upstream correspondence: scalableRef percentage, fixed replicas, and deployment resize.
	It("should recompute scalableRef percentage, fixed replicas, and limits in both directions", func() {
		env.ExpectCreated(nodeClass, nodePool)
		deployment := test.Deployment(test.DeploymentOptions{
			ObjectMeta: metav1.ObjectMeta{Name: "scalable-buffer-workload"},
			Replicas:   5,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "scalable-buffer-workload"}},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
			},
		})
		buffer := test.CapacityBuffer(autoscalingv1beta1.CapacityBuffer{
			ObjectMeta: metav1.ObjectMeta{Name: "scalable-buffer"},
			Spec: autoscalingv1beta1.CapacityBufferSpec{
				ScalableRef: &autoscalingv1beta1.ScalableRef{
					APIGroup: "apps",
					Kind:     "Deployment",
					Name:     deployment.Name,
				},
				Replicas:   lo.ToPtr(int32(1)),
				Percentage: lo.ToPtr(int32(40)),
				Limits: autoscalingv1beta1.Limits{
					corev1.ResourceCPU: resource.MustParse("1500m"),
				},
			},
		})
		env.ExpectCreated(deployment, buffer)

		EventuallyExpectCapacityBufferReplicas(env, env.Client, buffer, 2)

		updateDeploymentReplicas(deployment, 10)
		eventuallyExpectBufferReplicas(buffer, 3)

		updateDeploymentReplicas(deployment, 2)
		eventuallyExpectBufferReplicas(buffer, 1)
	})

	// Upstream correspondence: "should recover when scalable ref is created after buffer".
	It("should recover when a referenced Deployment is created after the buffer", func() {
		env.ExpectCreated(nodeClass, nodePool)
		buffer := test.CapacityBuffer(autoscalingv1beta1.CapacityBuffer{
			ObjectMeta: metav1.ObjectMeta{Name: "late-scalable-ref"},
			Spec: autoscalingv1beta1.CapacityBufferSpec{
				ScalableRef: &autoscalingv1beta1.ScalableRef{
					APIGroup: "apps",
					Kind:     "Deployment",
					Name:     "late-scalable-ref",
				},
				Replicas: lo.ToPtr(int32(2)),
			},
		})
		env.ExpectCreated(buffer)

		EventuallyExpectCapacityBufferNotReady(env, env.Client, buffer, "ScalableRefNotFound")

		deployment := test.Deployment(test.DeploymentOptions{
			ObjectMeta: metav1.ObjectMeta{Name: "late-scalable-ref"},
			Replicas:   0,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "late-scalable-ref"}},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
			},
		})
		env.ExpectCreated(deployment)

		eventuallyExpectBufferReplicas(buffer, 2)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
	})

	// Upstream correspondence: "should grow buffer replicas when limits are increased".
	It("should grow a limits-only buffer when its limits increase", func() {
		env.ExpectCreated(nodeClass, nodePool)
		podTemplate := newPodTemplate("limits-template", "1", "256Mi")
		buffer := test.CapacityBuffer(autoscalingv1beta1.CapacityBuffer{
			ObjectMeta: metav1.ObjectMeta{Name: "limits-growth"},
			Spec: autoscalingv1beta1.CapacityBufferSpec{
				PodTemplateRef: &autoscalingv1beta1.LocalObjectRef{Name: podTemplate.Name},
				Limits: autoscalingv1beta1.Limits{
					corev1.ResourceCPU: resource.MustParse("2"),
				},
			},
		})
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferReplicas(env, env.Client, buffer, 2)

		current := &autoscalingv1beta1.CapacityBuffer{}
		Expect(env.Client.Get(env, client.ObjectKeyFromObject(buffer), current)).To(Succeed())
		current.Spec.Limits = autoscalingv1beta1.Limits{
			corev1.ResourceCPU: resource.MustParse("3"),
		}
		env.ExpectUpdated(current)

		eventuallyExpectBufferReplicas(buffer, 3)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
	})

	// Azure-specific recovery coverage complementary to the upstream partial-limit regression.
	It("should recover provisioning after a NodePool limit is raised", func() {
		nodePool.Spec.Limits = karpv1.Limits{
			corev1.ResourceCPU: resource.MustParse("0"),
		}
		env.ExpectCreated(nodeClass, nodePool)

		podTemplate := newPodTemplate(bufferTemplateName, "1", "512Mi")
		buffer := newPodTemplateBuffer("nodepool-limit", podTemplate.Name, 1)
		env.ExpectCreated(podTemplate, buffer)
		EventuallyExpectCapacityBufferReplicas(env, env.Client, buffer, 1)

		Consistently(func(g Gomega) {
			nodeClaims := &karpv1.NodeClaimList{}
			g.Expect(env.Client.List(env, nodeClaims, client.HasLabels{test.DiscoveryLabel})).To(Succeed())
			g.Expect(nodeClaims.Items).To(BeEmpty())
		}).WithTimeout(30 * time.Second).Should(Succeed())

		nodePool.Spec.Limits = karpv1.Limits{
			corev1.ResourceCPU: resource.MustParse("4"),
		}
		env.ExpectUpdated(nodePool)

		env.EventuallyExpectRegisteredNodeClaimCount("==", 1)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
	})

	// Upstream correspondence: "should not provision buffer capacity beyond NodePool CPU limit".
	// Azure adaptation: assert aggregate NodeClaim CPU stays within the limit rather than assuming two 2-CPU nodes.
	It("should partially satisfy a buffer without exceeding a nonzero NodePool CPU limit", func() {
		nodePool.Spec.Limits = karpv1.Limits{
			corev1.ResourceCPU: resource.MustParse("4"),
		}
		env.ExpectCreated(nodeClass, nodePool)
		podTemplate := newPodTemplate("partial-limit-template", "1", "256Mi")
		buffer := newPodTemplateBuffer("partial-limit", podTemplate.Name, 10)
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferReplicas(env, env.Client, buffer, 10)
		EventuallyExpectCapacityBufferNotProvisioned(env, env.Client, buffer)
		env.EventuallyExpectRegisteredNodeClaimCount(">=", 1)
		eventuallyExpectNodeClaimCPUWithin(resource.MustParse("4"))

		Consistently(func(g Gomega) {
			totalCPU, count, allRegisteredWithCapacity := nodeClaimCPU()
			g.Expect(count).To(BeNumerically(">=", 1))
			g.Expect(allRegisteredWithCapacity).To(BeTrue())
			g.Expect(totalCPU.Cmp(resource.MustParse("4"))).To(BeNumerically("<=", 0))
		}).WithTimeout(30 * time.Second).Should(Succeed())
	})

	// Upstream correspondence: PodTemplate-backed label propagation and anti-affinity.
	// Azure extension: also verify zonal and on-demand offering selection.
	It("should preserve template labels for anti-affinity and honor zonal on-demand constraints", func() {
		availableZones := env.GetAvailableZones()
		if len(availableZones) == 0 {
			Skip("region does not support availability zones")
		}
		expectedZone := zones.MakeAKSLabelZoneFromARMZone(env.Region, availableZones[0])
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      corev1.LabelTopologyZone,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{expectedZone},
		})
		env.ExpectCreated(nodeClass, nodePool)

		podTemplate := newPodTemplate("anti-affinity-template", "500m", "256Mi")
		podTemplate.Template.Labels = map[string]string{"app": "spread-buffer"}
		podTemplate.Template.Spec.Affinity = &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
					LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "spread-buffer"}},
					TopologyKey:   corev1.LabelHostname,
				}},
			},
		}
		buffer := newPodTemplateBuffer("anti-affinity", podTemplate.Name, 2)
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferReplicas(env, env.Client, buffer, 2)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
		nodes := env.EventuallyExpectInitializedNodeCount("==", 2)
		for _, node := range nodes {
			Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelTopologyZone, expectedZone))
			Expect(node.Labels).To(HaveKeyWithValue(karpv1.CapacityTypeLabelKey, karpv1.CapacityTypeOnDemand))
		}
	})

	// Upstream correspondence: "should respect pod anti-affinity ... using scalableRef".
	It("should preserve Deployment labels and anti-affinity for scalableRef virtual pods", func() {
		env.ExpectCreated(nodeClass, nodePool)
		deployment := test.Deployment(test.DeploymentOptions{
			ObjectMeta: metav1.ObjectMeta{Name: "scalable-anti-affinity"},
			Replicas:   1,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "scalable-anti-affinity"}},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
				PodAntiRequirements: []corev1.PodAffinityTerm{{
					LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "scalable-anti-affinity"}},
					TopologyKey:   corev1.LabelHostname,
				}},
			},
		})
		env.ExpectCreated(deployment)
		env.EventuallyExpectHealthyDeployment(deployment)

		buffer := test.CapacityBuffer(autoscalingv1beta1.CapacityBuffer{
			ObjectMeta: metav1.ObjectMeta{Name: "scalable-anti-affinity"},
			Spec: autoscalingv1beta1.CapacityBufferSpec{
				ScalableRef: &autoscalingv1beta1.ScalableRef{
					APIGroup: "apps",
					Kind:     "Deployment",
					Name:     deployment.Name,
				},
				Replicas: lo.ToPtr(int32(2)),
			},
		})
		env.ExpectCreated(buffer)

		EventuallyExpectCapacityBufferReplicas(env, env.Client, buffer, 2)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
		env.EventuallyExpectInitializedNodeCount("==", 3)
	})

	// Azure-specific offering coverage.
	It("should provision spot capacity when the buffer explicitly requests it", func() {
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      karpv1.CapacityTypeLabelKey,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{karpv1.CapacityTypeSpot},
		})
		env.ExpectCreated(nodeClass, nodePool)

		podTemplate := newPodTemplate("spot-template", "500m", "256Mi")
		podTemplate.Template.Spec.NodeSelector[karpv1.CapacityTypeLabelKey] = karpv1.CapacityTypeSpot
		podTemplate.Template.Spec.Tolerations = append(podTemplate.Template.Spec.Tolerations, corev1.Toleration{
			Key:      v1beta1.AKSLabelScaleSetPriority,
			Operator: corev1.TolerationOpEqual,
			Value:    v1beta1.ScaleSetPrioritySpot,
			Effect:   corev1.TaintEffectNoSchedule,
		})
		buffer := newPodTemplateBuffer("spot", podTemplate.Name, 1)
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		Expect(node.Labels).To(HaveKeyWithValue(karpv1.CapacityTypeLabelKey, karpv1.CapacityTypeSpot))
		Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelScaleSetPriority, v1beta1.ScaleSetPrioritySpot))
	})

	// Azure-specific regional placement coverage.
	It("should provision regional capacity when the buffer targets a regional NodePool", func() {
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      v1beta1.LabelPlacementScope,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{v1beta1.PlacementScopeRegional},
		})
		env.ExpectCreated(nodeClass, nodePool)

		podTemplate := newPodTemplate("regional-template", "500m", "256Mi")
		podTemplate.Template.Spec.NodeSelector[v1beta1.LabelPlacementScope] = v1beta1.PlacementScopeRegional
		buffer := newPodTemplateBuffer("regional", podTemplate.Name, 1)
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		Expect(node.Labels).To(HaveKeyWithValue(v1beta1.LabelPlacementScope, v1beta1.PlacementScopeRegional))
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelTopologyZone, zones.Regional))
	})

	// Upstream correspondence: "should scale buffer down when replicas are reduced".
	// Azure adaptation: pin an instance type and use anti-affinity to make initial headroom deterministic.
	It("should reduce provisioned headroom when replicas decrease", func() {
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      corev1.LabelInstanceTypeStable,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{testInstanceType},
		})
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
		env.ExpectCreated(nodeClass, nodePool)

		podTemplate := newPodTemplate("reduction-template", "500m", "256Mi")
		podTemplate.Template.Labels = map[string]string{"app": "reduction-buffer"}
		podTemplate.Template.Spec.Affinity = requiredHostnameAntiAffinity("app", "reduction-buffer")
		buffer := newPodTemplateBuffer("reduction", podTemplate.Name, 2)
		env.ExpectCreated(podTemplate, buffer)

		env.EventuallyExpectRegisteredNodeClaimCount("==", 2)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)

		current := &autoscalingv1beta1.CapacityBuffer{}
		Expect(env.Client.Get(env, client.ObjectKeyFromObject(buffer), current)).To(Succeed())
		current.Spec.Replicas = lo.ToPtr(int32(1))
		env.ExpectUpdated(current)

		eventuallyExpectBufferReplicas(buffer, 1)
		Eventually(func(g Gomega) {
			remaining := &karpv1.NodeClaimList{}
			g.Expect(env.Client.List(env, remaining, client.HasLabels{test.DiscoveryLabel})).To(Succeed())
			g.Expect(remaining.Items).To(HaveLen(1))
		}).Should(Succeed())
	})

	// Upstream correspondence: "should not empty-consolidate nodes hosting buffer pods".
	// Azure adaptation: use one size-independent node and force an existing-node placement pass.
	It("should protect existing buffer capacity from empty consolidation", func() {
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
		env.ExpectCreated(nodeClass, nodePool)
		podTemplate := newPodTemplate("empty-protection-template", "100m", "128Mi")
		buffer := newPodTemplateBuffer("empty-protection", podTemplate.Name, 1)
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
		env.EventuallyExpectRegisteredNodeClaimCount("==", 1)
		env.EventuallyExpectInitializedNodeCount("==", 1)
		refreshExistingBufferPlacement(buffer)

		env.ConsistentlyExpectNoDisruptions(1, 60*time.Second)
	})

	// Azure-specific restart coverage for in-memory virtual-pod and empty-protection state.
	It("should reconstruct virtual-pod and empty-node protection after a controller restart", func() {
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
		env.ExpectCreated(nodeClass, nodePool)

		podTemplate := newPodTemplate(bufferTemplateName, "1", "512Mi")
		buffer := newPodTemplateBuffer("restart", podTemplate.Name, 1)
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
		env.EventuallyExpectRegisteredNodeClaimCount("==", 1)

		current := &autoscalingv1beta1.CapacityBuffer{}
		Expect(env.Client.Get(env, client.ObjectKeyFromObject(buffer), current)).To(Succeed())
		Expect(current.Status.PodTemplateGeneration).ToNot(BeNil())
		originalTemplateGeneration := *current.Status.PodTemplateGeneration
		oldControllerPods := env.ExpectKarpenterPods()
		env.EventuallyExpectRollout("karpenter", "kube-system")

		Eventually(func(g Gomega) {
			currentPods := env.ExpectKarpenterPods()
			for _, oldPod := range oldControllerPods {
				g.Expect(currentPods).ToNot(ContainElement(HaveField("UID", oldPod.UID)))
			}
		}).Should(Succeed())

		currentTemplate := &corev1.PodTemplate{}
		Expect(env.Client.Get(env, client.ObjectKeyFromObject(podTemplate), currentTemplate)).To(Succeed())
		currentTemplate.Template.Labels = lo.Assign(currentTemplate.Template.Labels, map[string]string{"restart": "verified"})
		env.ExpectUpdated(currentTemplate)
		EventuallyExpectCapacityBufferGenerationUpdated(env, env.Client, buffer, originalTemplateGeneration+1)
		env.ConsistentlyExpectNoDisruptions(1, 45*time.Second)
	})

	// Upstream correspondence: "should allow drift to replace buffer nodes".
	// Azure adaptation: assert replacement of captured capacity without assuming multiple KWOK nodes.
	It("should replace drifted capacity while preserving the buffer", func() {
		env.ExpectCreated(nodeClass, nodePool)
		podTemplate := newPodTemplate("drift-template", "1", "512Mi")
		buffer := newPodTemplateBuffer("drift", podTemplate.Name, 1)
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
		originalNodeClaim := env.EventuallyExpectRegisteredNodeClaimCount("==", 1)[0]

		nodePool.Spec.Template.Annotations = lo.Assign(nodePool.Spec.Template.Annotations, map[string]string{
			"capacity-buffer-test": "drift",
		})
		env.ExpectUpdated(nodePool)

		env.EventuallyExpectDrifted(originalNodeClaim)
		env.EventuallyExpectNotFound(originalNodeClaim)
		env.EventuallyExpectRegisteredNodeClaimCount("==", 1)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
	})

	// Upstream correspondence: "should refill buffer after node expiry".
	It("should replace expired capacity while preserving the buffer", func() {
		nodePool.Spec.Template.Spec.ExpireAfter = karpv1.MustParseNillableDuration("2m")
		env.ExpectCreated(nodeClass, nodePool)
		podTemplate := newPodTemplate("expiration-template", "1", "512Mi")
		buffer := newPodTemplateBuffer("expiration", podTemplate.Name, 1)
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
		originalNodeClaim := env.EventuallyExpectRegisteredNodeClaimCount("==", 1)[0]

		env.EventuallyExpectNotFound(originalNodeClaim)
		env.EventuallyExpectRegisteredNodeClaimCount("==", 1)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
	})

	// Azure-specific complement to the upstream late scalableRef recovery test.
	It("should report a missing PodTemplate reference and recover when it is created", func() {
		env.ExpectCreated(nodeClass, nodePool)
		buffer := newPodTemplateBuffer("missing-reference", "late-template", 1)
		env.ExpectCreated(buffer)

		EventuallyExpectCapacityBufferNotReady(env, env.Client, buffer, "PodTemplateNotFound")

		podTemplate := newPodTemplate("late-template", "500m", "256Mi")
		env.ExpectCreated(podTemplate)
		EventuallyExpectCapacityBufferReady(env, env.Client, buffer)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
	})

	// Upstream correspondence: "should provision capacity independently for multiple buffers".
	// Azure adaptation: assert independent heterogeneous buffer readiness without predicting SKU packing.
	It("should provision heterogeneous buffers independently", func() {
		largeNodePool := nodePool
		smallNodePool := env.DefaultNodePool(nodeClass)
		largeNodePool.Spec.Template.Labels = lo.Assign(largeNodePool.Spec.Template.Labels, map[string]string{
			"capacity-buffer-test/shape": "large",
		})
		smallNodePool.Spec.Template.Labels = lo.Assign(smallNodePool.Spec.Template.Labels, map[string]string{
			"capacity-buffer-test/shape": "small",
		})
		env.ExpectCreated(nodeClass, largeNodePool, smallNodePool)
		largeTemplate := newPodTemplate("heterogeneous-large-template", "1", "512Mi")
		smallTemplate := newPodTemplate("heterogeneous-small-template", "250m", "128Mi")
		largeTemplate.Template.Spec.NodeSelector["capacity-buffer-test/shape"] = "large"
		smallTemplate.Template.Spec.NodeSelector["capacity-buffer-test/shape"] = "small"
		largeBuffer := newPodTemplateBuffer("heterogeneous-large", largeTemplate.Name, 2)
		smallBuffer := newPodTemplateBuffer("heterogeneous-small", smallTemplate.Name, 3)
		env.ExpectCreated(largeTemplate, smallTemplate, largeBuffer, smallBuffer)

		EventuallyExpectCapacityBufferReplicas(env, env.Client, largeBuffer, 2)
		EventuallyExpectCapacityBufferReplicas(env, env.Client, smallBuffer, 3)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, largeBuffer)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, smallBuffer)
		env.EventuallyExpectRegisteredNodeClaimCountWithSelector(">=", 1, labels.SelectorFromSet(map[string]string{
			"capacity-buffer-test/shape": "large",
		}))
		env.EventuallyExpectRegisteredNodeClaimCountWithSelector(">=", 1, labels.SelectorFromSet(map[string]string{
			"capacity-buffer-test/shape": "small",
		}))
	})

	// Azure-specific bounded scale sanity coverage.
	It("should provision and reconcile ten bounded buffers", func() {
		env.ExpectCreated(nodeClass, nodePool)
		podTemplate := newPodTemplate(bufferTemplateName, "50m", "64Mi")
		env.ExpectCreated(podTemplate)

		buffers := make([]*autoscalingv1beta1.CapacityBuffer, 0, 10)
		started := time.Now()
		for i := 0; i < 10; i++ {
			buffer := newPodTemplateBuffer(fmt.Sprintf("scale-sanity-%d", i), podTemplate.Name, 1)
			buffer.Spec.Limits = autoscalingv1beta1.Limits{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			}
			buffers = append(buffers, buffer)
			env.ExpectCreated(buffer)
		}
		for _, buffer := range buffers {
			EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
		}
		AddReportEntry("ten-buffer-reconciliation-duration", time.Since(started))
		env.EventuallyExpectRegisteredNodeClaimCount(">=", 1)
	})

	// Upstream correspondence: "should not leak nodes on rapid buffer create and delete".
	// Pending with the other deletion-path regressions until kubernetes-sigs/karpenter#3258 is fixed.
	PIt("should not leak nodes after rapid buffer create and delete", func() {
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
		env.ExpectCreated(nodeClass, nodePool)
		podTemplate := newPodTemplate("rapid-delete-template", "250m", "128Mi")
		env.ExpectCreated(podTemplate)

		seedBuffer := newPodTemplateBuffer("rapid-delete-seed", podTemplate.Name, 1)
		env.ExpectCreated(seedBuffer)
		env.EventuallyExpectRegisteredNodeClaimCount(">=", 1)
		env.ExpectDeleted(seedBuffer)

		for i := 0; i < 5; i++ {
			buffer := newPodTemplateBuffer(fmt.Sprintf("rapid-delete-%d", i), podTemplate.Name, 2)
			env.ExpectCreated(buffer)
			time.Sleep(2 * time.Second)
			env.ExpectDeleted(buffer)
		}

		finalBuffer := newPodTemplateBuffer("rapid-delete-final", podTemplate.Name, 1)
		env.ExpectCreated(finalBuffer)
		EventuallyExpectCapacityBufferProvisioned(env, env.Client, finalBuffer)
		env.EventuallyExpectRegisteredNodeClaimCount(">=", 1)
		env.EventuallyExpectInitializedNodeCount(">=", 1)
		refreshExistingBufferPlacement(finalBuffer)
		env.ExpectDeleted(finalBuffer)

		Eventually(func(g Gomega) {
			buffers := &autoscalingv1beta1.CapacityBufferList{}
			g.Expect(env.Client.List(env, buffers, client.InNamespace("default"))).To(Succeed())
			g.Expect(buffers.Items).To(BeEmpty())
		}).WithTimeout(30 * time.Second).Should(Succeed())
		Eventually(func(g Gomega) {
			nodeClaims := &karpv1.NodeClaimList{}
			g.Expect(env.Client.List(env, nodeClaims, client.HasLabels{test.DiscoveryLabel})).To(Succeed())
			g.Expect(nodeClaims.Items).To(BeEmpty())
		}).WithTimeout(3 * time.Minute).Should(Succeed())
	})

	// Upstream correspondence: "should consolidate buffer nodes after buffer is deleted".
	PIt("should release empty-node protection after the CapacityBuffer is deleted without unrelated scheduling activity", func() {
		// Blocked by kubernetes-sigs/karpenter#3258. Change PIt to It once the
		// selected core revision includes the deletion trigger fix.
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
		env.ExpectCreated(nodeClass, nodePool)
		podTemplate := newPodTemplate("delete-buffer-template", "100m", "128Mi")
		buffer := newPodTemplateBuffer("delete-buffer", podTemplate.Name, 1)
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
		nodeClaim := env.EventuallyExpectRegisteredNodeClaimCount("==", 1)[0]
		env.EventuallyExpectInitializedNodeCount("==", 1)
		refreshExistingBufferPlacement(buffer)
		env.ConsistentlyExpectNoDisruptions(1, 45*time.Second)

		env.ExpectDeleted(buffer)
		eventuallyExpectNodeClaimDeleted(nodeClaim, 3*time.Minute)
	})

	PIt("should release empty-node protection after the backing reference is deleted without unrelated scheduling activity", func() {
		// Blocked by kubernetes-sigs/karpenter#3258. Change PIt to It once the
		// selected core revision includes the reference-loss trigger fix.
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
		env.ExpectCreated(nodeClass, nodePool)
		podTemplate := newPodTemplate("delete-reference-template", "100m", "128Mi")
		buffer := newPodTemplateBuffer("delete-reference", podTemplate.Name, 1)
		env.ExpectCreated(podTemplate, buffer)

		EventuallyExpectCapacityBufferProvisioned(env, env.Client, buffer)
		nodeClaim := env.EventuallyExpectRegisteredNodeClaimCount("==", 1)[0]
		env.EventuallyExpectInitializedNodeCount("==", 1)
		refreshExistingBufferPlacement(buffer)
		env.ConsistentlyExpectNoDisruptions(1, 45*time.Second)

		env.ExpectDeleted(podTemplate)
		EventuallyExpectCapacityBufferNotReady(env, env.Client, buffer, "PodTemplateNotFound")
		eventuallyExpectNodeClaimDeleted(nodeClaim, 3*time.Minute)
	})
})

func newPodTemplate(name, cpu, memory string) *corev1.PodTemplate {
	return &corev1.PodTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "workload",
					Image: "mcr.microsoft.com/oss/kubernetes/pause:3.6",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(cpu),
							corev1.ResourceMemory: resource.MustParse(memory),
						},
					},
				}},
				NodeSelector: map[string]string{corev1.LabelOSStable: string(corev1.Linux)},
			},
		},
	}
}

func newPodTemplateBuffer(name, templateName string, replicas int32) *autoscalingv1beta1.CapacityBuffer {
	return test.CapacityBuffer(autoscalingv1beta1.CapacityBuffer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: autoscalingv1beta1.CapacityBufferSpec{
			ProvisioningStrategy: lo.ToPtr(autoscalingv1beta1.ActiveProvisioningStrategy),
			PodTemplateRef:       &autoscalingv1beta1.LocalObjectRef{Name: templateName},
			Replicas:             lo.ToPtr(replicas),
		},
	})
}

func requiredHostnameAntiAffinity(labelKey, labelValue string) *corev1.Affinity {
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
				LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{labelKey: labelValue}},
				TopologyKey:   corev1.LabelHostname,
			}},
		},
	}
}

func updateDeploymentReplicas(deployment *appsv1.Deployment, replicas int32) {
	GinkgoHelper()
	current := &appsv1.Deployment{}
	Expect(env.Client.Get(env, client.ObjectKeyFromObject(deployment), current)).To(Succeed())
	current.Spec.Replicas = lo.ToPtr(replicas)
	env.ExpectUpdated(current)
}

func eventuallyExpectBufferReplicas(buffer *autoscalingv1beta1.CapacityBuffer, replicas int32) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		current := &autoscalingv1beta1.CapacityBuffer{}
		g.Expect(env.Client.Get(env, client.ObjectKeyFromObject(buffer), current)).To(Succeed())
		g.Expect(current.Status.Replicas).ToNot(BeNil())
		g.Expect(*current.Status.Replicas).To(Equal(replicas))
	}).WithTimeout(90 * time.Second).Should(Succeed())
}

func provisioningTransitionTime(buffer *autoscalingv1beta1.CapacityBuffer) metav1.Time {
	GinkgoHelper()
	current := &autoscalingv1beta1.CapacityBuffer{}
	Expect(env.Client.Get(env, client.ObjectKeyFromObject(buffer), current)).To(Succeed())
	condition := apimeta.FindStatusCondition(current.Status.Conditions, autoscalingv1beta1.ProvisioningCondition)
	Expect(condition).ToNot(BeNil())
	return condition.LastTransitionTime
}

func eventuallyExpectNodeClaimDeleted(nodeClaim *karpv1.NodeClaim, timeout time.Duration) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		current := &karpv1.NodeClaim{}
		err := env.Client.Get(env, client.ObjectKeyFromObject(nodeClaim), current)
		g.Expect(client.IgnoreNotFound(err)).To(Succeed())
		g.Expect(err).To(HaveOccurred())
	}).WithTimeout(timeout).Should(Succeed())
}

func eventuallyExpectNodeClaimCPUWithin(limit resource.Quantity) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		totalCPU, count, allRegisteredWithCapacity := nodeClaimCPU()
		g.Expect(count).To(BeNumerically(">=", 1))
		g.Expect(allRegisteredWithCapacity).To(BeTrue())
		g.Expect(totalCPU.Sign()).To(BeNumerically(">", 0))
		g.Expect(totalCPU.Cmp(limit)).To(BeNumerically("<=", 0))
	}).Should(Succeed())
}

func nodeClaimCPU() (resource.Quantity, int, bool) {
	GinkgoHelper()
	nodeClaims := &karpv1.NodeClaimList{}
	Expect(env.Client.List(env, nodeClaims, client.HasLabels{test.DiscoveryLabel})).To(Succeed())
	totalCPU := resource.MustParse("0")
	allRegisteredWithCapacity := true
	for i := range nodeClaims.Items {
		cpu, ok := nodeClaims.Items[i].Status.Capacity[corev1.ResourceCPU]
		if !ok || cpu.Sign() <= 0 || !nodeClaims.Items[i].StatusConditions().IsTrue(karpv1.ConditionTypeRegistered) {
			allRegisteredWithCapacity = false
			continue
		}
		totalCPU.Add(cpu)
	}
	return totalCPU, len(nodeClaims.Items), allRegisteredWithCapacity
}

func refreshExistingBufferPlacement(buffer *autoscalingv1beta1.CapacityBuffer) {
	GinkgoHelper()
	// Allow the state controller to move the newly initialized node out of the
	// in-flight set before forcing scheduling passes that must target ExistingNodes.
	time.Sleep(10 * time.Second)
	for _, replicas := range []int32{2, 3} {
		currentBuffer := &autoscalingv1beta1.CapacityBuffer{}
		Expect(env.Client.Get(env, client.ObjectKeyFromObject(buffer), currentBuffer)).To(Succeed())
		currentBuffer.Spec.Replicas = lo.ToPtr(replicas)
		env.ExpectUpdated(currentBuffer)

		Eventually(func(g Gomega) {
			refreshed := &autoscalingv1beta1.CapacityBuffer{}
			g.Expect(env.Client.Get(env, client.ObjectKeyFromObject(buffer), refreshed)).To(Succeed())
			g.Expect(refreshed.Status.Replicas).ToNot(BeNil())
			g.Expect(*refreshed.Status.Replicas).To(Equal(replicas))
			condition := apimeta.FindStatusCondition(refreshed.Status.Conditions, autoscalingv1beta1.ProvisioningCondition)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(condition.Reason).To(Equal("FitsExistingCapacity"))
			g.Expect(condition.ObservedGeneration).To(Equal(refreshed.Generation))
		}).Should(Succeed())
		time.Sleep(5 * time.Second)
	}
}
