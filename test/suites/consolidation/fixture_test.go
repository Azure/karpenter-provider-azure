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

package consolidation_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	pscheduling "sigs.k8s.io/karpenter/pkg/controllers/provisioning/scheduling"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"
)

// These tests construct fixtures and use a fake Kubernetes client. Run the standard
// tests without the live suite using go test -skip '^TestConsolidation$' ./test/suites/consolidation.
// The separate SSA contract test requires the installed envtest API-server assets.
func TestReplacementBudgetFixture(t *testing.T) {
	for _, dataplane := range []string{common.NetworkDataplaneAzure, common.NetworkDataplaneCilium} {
		t.Run(dataplane, func(t *testing.T) {
			g := NewWithT(t)
			fixtureEnv := &common.Environment{NetworkDataplane: dataplane}
			nodePool := fixtureEnv.DefaultNodePool(&v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: "test-class"},
			})
			original := nodePool.DeepCopy()
			ds, deployments := replacementBudgetResources(nodePool)
			template := pscheduling.NewNodeClaimTemplate(nodePool)

			g.Expect(deployments).To(HaveLen(5))
			g.Expect(nodePool.Spec.Disruption.ConsolidateAfter.Duration).To(BeNil())
			allowed, err := nodePool.GetAllowedDisruptionsByReason(clock.RealClock{}, 5, karpv1.DisruptionReasonUnderutilized)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(allowed).To(Equal(3))
			g.Expect(nodePool.Spec.Template.Spec.StartupTaints).To(Equal(original.Spec.Template.Spec.StartupTaints))
			g.Expect(nodePool.Spec.Template.Spec.NodeClassRef).To(Equal(original.Spec.Template.Spec.NodeClassRef))
			g.Expect(nodePool.Spec.Template.Labels).To(Equal(original.Spec.Template.Labels))
			g.Expect(nodePool.Spec.Limits).To(Equal(original.Spec.Limits))
			g.Expect(nodePool.Spec.Template.Spec.Requirements).To(ContainElement(karpv1.NodeSelectorRequirementWithMinValues{
				Key: v1beta1.LabelSKUCPU, Operator: corev1.NodeSelectorOpIn, Values: []string{"4", "8"},
			}))
			g.Expect(nodePool.Spec.Template.Spec.Requirements).To(ContainElement(karpv1.NodeSelectorRequirementWithMinValues{
				Key: "test-partition", Operator: corev1.NodeSelectorOpExists,
			}))

			daemonPod := &corev1.Pod{Spec: ds.Spec.Template.Spec}
			g.Expect(scheduling.Taints(template.Spec.Taints).ToleratesPod(daemonPod)).To(Succeed())
			g.Expect(template.Requirements.Compatible(scheduling.NewPodRequirements(daemonPod), scheduling.AllowUndefinedWellKnownLabels)).To(Succeed())
			daemonRequests := resources.RequestsForPods(daemonPod)
			g.Expect(daemonRequests.Cpu().Cmp(resource.MustParse("3"))).To(BeZero())
			for i, deployment := range deployments {
				g.Expect(*deployment.Spec.Replicas).To(Equal(int32(1)))
				pod := &corev1.Pod{Spec: deployment.Spec.Template.Spec}
				g.Expect(pod.Spec.NodeSelector).To(HaveKeyWithValue("test-partition", fmt.Sprintf("%d", i)))
				g.Expect(scheduling.Taints(template.Spec.Taints).ToleratesPod(pod)).To(Succeed())
				g.Expect(template.Requirements.Compatible(scheduling.NewPodRequirements(pod), scheduling.AllowUndefinedWellKnownLabels)).To(Succeed())
				podRequests := resources.RequestsForPods(pod)
				combinedRequests := resources.RequestsForPods(pod, daemonPod)
				g.Expect(podRequests.Cpu().Cmp(resource.MustParse("3"))).To(BeZero())
				g.Expect(combinedRequests.Cpu().Cmp(resource.MustParse("6"))).To(BeZero())
				// The isolation toleration must not also tolerate the disruption taint.
				g.Expect(scheduling.Taints{karpv1.DisruptedNoScheduleTaint}.ToleratesPod(pod)).ToNot(Succeed())
			}

			addon := &corev1.Pod{Spec: corev1.PodSpec{Tolerations: []corev1.Toleration{
				{Key: "CriticalAddonsOnly", Operator: corev1.TolerationOpExists},
				{Key: corev1.TaintNodeNotReady, Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: corev1.TaintNodeUnreachable, Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
			}}}
			g.Expect(scheduling.Taints(template.Spec.Taints).ToleratesPod(addon)).ToNot(Succeed(), "unrelated addon must not match the counted budget pool")
			g.Expect(nodePool.Spec.Template.Spec.Taints).To(Equal([]corev1.Taint{{
				Key: "testing/consolidation-budget", Value: "true", Effect: corev1.TaintEffectNoSchedule,
			}}))
			// Other tests still get an untainted default pool.
			g.Expect(fixtureEnv.DefaultNodePool(&v1beta1.AKSNodeClass{}).Spec.Template.Spec.Taints).To(BeEmpty())
		})
	}
}

func TestReplacementBudgetIsolation(t *testing.T) {
	tests := []struct {
		name        string
		tolerations []corev1.Toleration
		daemonSet   bool
		wantError   bool
	}{
		{name: "no tolerations"},
		{name: "critical addons only", tolerations: []corev1.Toleration{{Key: "CriticalAddonsOnly", Operator: corev1.TolerationOpExists}}},
		{name: "other effect", tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute}}},
		{name: "different value", tolerations: []corev1.Toleration{{Key: "testing/consolidation-budget", Operator: corev1.TolerationOpEqual, Value: "other", Effect: corev1.TaintEffectNoSchedule}}},
		{name: "wildcard NoSchedule", tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}}, wantError: true},
		{name: "wildcard all effects", tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}}, wantError: true},
		{name: "matching key Exists", tolerations: []corev1.Toleration{{Key: "testing/consolidation-budget", Operator: corev1.TolerationOpExists}}, wantError: true},
		{name: "matching Equal", tolerations: []corev1.Toleration{{Key: "testing/consolidation-budget", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}}, wantError: true},
		{name: "daemonset overhead", tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}}, daemonSet: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			nodePool := coretest.NodePool()
			nodePool.Spec.Template.Spec.Taints = []corev1.Taint{{Key: "testing/consolidation-budget", Value: "true", Effect: corev1.TaintEffectNoSchedule}}
			// Check running addons too, before they can become pending during disruption.
			pod := corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "addon"},
				Spec:       corev1.PodSpec{NodeName: "system-node", Tolerations: tt.tolerations},
				Status:     corev1.PodStatus{Phase: corev1.PodRunning},
			}
			if tt.daemonSet {
				pod.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "DaemonSet", Name: "node-agent"}}
			}
			err := validateReplacementBudgetIsolation(nodePool, []corev1.Pod{pod})
			if tt.wantError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("kube-system/addon"))
				g.Expect(err.Error()).To(ContainSubstring("testing/consolidation-budget"))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestReplacementBudgetExplicitUserMode(t *testing.T) {
	for _, dataplane := range []string{common.NetworkDataplaneAzure, common.NetworkDataplaneCilium} {
		t.Run(dataplane, func(t *testing.T) {
			g := NewWithT(t)
			fixtureEnv := &common.Environment{NetworkDataplane: dataplane}
			nodeClass := &v1beta1.AKSNodeClass{ObjectMeta: metav1.ObjectMeta{Name: "test-class"}}
			nodePool := fixtureEnv.DefaultNodePool(nodeClass)
			originalRequirements := nodePool.DeepCopy().Spec.Template.Spec.Requirements

			replacementBudgetResources(nodePool)

			// Restrict only this fixture, not provider defaults or other suites. Both
			// system and user are supported provider modes; a default is not exclusion.
			g.Expect(fixtureEnv.DefaultNodePool(nodeClass).Spec.Template.Spec.Requirements).To(Equal(originalRequirements))
			var modeRequirements []karpv1.NodeSelectorRequirementWithMinValues
			for _, requirement := range nodePool.Spec.Template.Spec.Requirements {
				if requirement.Key == v1beta1.AKSLabelMode {
					modeRequirements = append(modeRequirements, requirement)
				}
			}
			g.Expect(modeRequirements).To(Equal([]karpv1.NodeSelectorRequirementWithMinValues{{
				Key: v1beta1.AKSLabelMode, Operator: corev1.NodeSelectorOpIn, Values: []string{v1beta1.ModeUser},
			}}), "the counted replacement budget pool must explicitly require user mode")
		})
	}
}

func TestReplacementBudgetHardIsolation(t *testing.T) {
	modeTerm := func(operator corev1.NodeSelectorOperator, values ...string) corev1.NodeSelectorTerm {
		return corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{{
			Key: v1beta1.AKSLabelMode, Operator: operator, Values: values,
		}}}
	}
	requiredAffinity := func(terms ...corev1.NodeSelectorTerm) *corev1.Affinity {
		return &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: terms},
		}}
	}
	systemSelector := map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}
	systemTerm := modeTerm(corev1.NodeSelectorOpIn, v1beta1.ModeSystem)
	userTerm := modeTerm(corev1.NodeSelectorOpIn, v1beta1.ModeUser)
	systemAndArchitecture := systemTerm.DeepCopy()
	systemAndArchitecture.MatchExpressions = append(systemAndArchitecture.MatchExpressions, corev1.NodeSelectorRequirement{
		Key: corev1.LabelArchStable, Operator: corev1.NodeSelectorOpIn, Values: []string{karpv1.ArchitectureAmd64},
	})
	tests := []struct {
		name         string
		nodeName     string
		selector     map[string]string
		affinity     *corev1.Affinity
		poolModes    []string
		omitPoolMode bool
		untolerated  bool
		wantError    bool
	}{
		{name: "wildcard NoSchedule and NoExecute are not isolated", wantError: true},
		{name: "hard system selector excludes explicit user pool", selector: systemSelector},
		{name: "hard user selector remains compatible", selector: map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeUser}, wantError: true},
		{name: "current system node binding is not a future constraint", nodeName: "system-node", wantError: true},
		{name: "preferred system placement is not exclusion", affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{Weight: 100, Preference: systemTerm}},
		}}, wantError: true},
		{name: "required system affinity excludes user pool", affinity: requiredAffinity(systemTerm)},
		{name: "all required affinity OR terms exclude user", affinity: requiredAffinity(systemTerm, modeTerm(corev1.NodeSelectorOpNotIn, v1beta1.ModeUser))},
		{name: "required affinity AND retains mode contradiction", affinity: requiredAffinity(*systemAndArchitecture)},
		{name: "later compatible OR term prevents exclusion", affinity: requiredAffinity(systemTerm, userTerm), wantError: true},
		{name: "earlier compatible OR term prevents exclusion", affinity: requiredAffinity(userTerm, systemTerm), wantError: true},
		{name: "multi-value affinity can still select user", affinity: requiredAffinity(modeTerm(corev1.NodeSelectorOpIn, v1beta1.ModeSystem, v1beta1.ModeUser)), wantError: true},
		{name: "NotIn user excludes explicit user pool", affinity: requiredAffinity(modeTerm(corev1.NodeSelectorOpNotIn, v1beta1.ModeUser))},
		{name: "NotIn system still permits user", affinity: requiredAffinity(modeTerm(corev1.NodeSelectorOpNotIn, v1beta1.ModeSystem)), wantError: true},
		{name: "DoesNotExist mode contradicts required user label", affinity: requiredAffinity(modeTerm(corev1.NodeSelectorOpDoesNotExist))},
		{name: "Exists mode still permits user", affinity: requiredAffinity(modeTerm(corev1.NodeSelectorOpExists)), wantError: true},
		{name: "selector is conjunctive with every affinity term", selector: systemSelector, affinity: requiredAffinity(userTerm, systemTerm)},
		{name: "provider mode default is not a pool constraint", selector: systemSelector, omitPoolMode: true, wantError: true},
		{name: "pool permitting both modes is not disjoint", selector: systemSelector, poolModes: []string{v1beta1.ModeSystem, v1beta1.ModeUser}, wantError: true},
		{name: "system pool is not disjoint", selector: systemSelector, poolModes: []string{v1beta1.ModeSystem}, wantError: true},
		{name: "unconstrained selector key is not proof", selector: map[string]string{"example.com/placement": "elsewhere"}, wantError: true},
		{name: "unknown affinity operator fails closed", affinity: requiredAffinity(modeTerm(corev1.NodeSelectorOperator("Unknown"), v1beta1.ModeSystem)), wantError: true},
		{name: "unknown OR term cannot be discarded", affinity: requiredAffinity(systemTerm, corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{{
			Key: "example.com/placement", Operator: corev1.NodeSelectorOpIn, Values: []string{"elsewhere"},
		}}}), wantError: true},
		{name: "node-name affinity cannot rule out future claims", affinity: requiredAffinity(corev1.NodeSelectorTerm{MatchFields: []corev1.NodeSelectorRequirement{{
			Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{"system-node"},
		}}}), wantError: true},
		{name: "taint exclusion remains independently sufficient", untolerated: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			nodePool := (&common.Environment{}).DefaultNodePool(&v1beta1.AKSNodeClass{})
			nodePool.Spec.Template.Spec.Taints = []corev1.Taint{{Key: "testing/consolidation-budget", Value: "true", Effect: corev1.TaintEffectNoSchedule}}
			if !tt.omitPoolMode {
				modes := tt.poolModes
				if modes == nil {
					modes = []string{v1beta1.ModeUser}
				}
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					Key: v1beta1.AKSLabelMode, Operator: corev1.NodeSelectorOpIn, Values: modes,
				})
			}
			pod := corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "background-workload"},
				Spec: corev1.PodSpec{
					NodeName: tt.nodeName, NodeSelector: tt.selector, Affinity: tt.affinity,
					Tolerations: []corev1.Toleration{
						{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
						{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
					},
				},
				Status: corev1.PodStatus{Phase: corev1.PodPending},
			}
			if tt.nodeName != "" {
				pod.Status.Phase = corev1.PodRunning
			}
			if tt.untolerated {
				pod.Spec.Tolerations = nil
			}
			originalPod, originalPool := pod.DeepCopy(), nodePool.DeepCopy()
			err := validateReplacementBudgetIsolation(nodePool, []corev1.Pod{pod})
			g.Expect(&pod).To(Equal(originalPod), "the guard must not mutate scheduling constraints")
			g.Expect(nodePool).To(Equal(originalPool))
			if tt.wantError {
				g.Expect(err).To(HaveOccurred(), "compatible or unknown hard placement must fail closed")
				g.Expect(err.Error()).To(ContainSubstring("kube-system/background-workload"))
			} else {
				g.Expect(err).ToNot(HaveOccurred(), "taint exclusion OR proven hard incompatibility must be accepted")
			}
		})
	}
}

func TestReplacementBudgetCountAssertions(t *testing.T) {
	// Environment assertions use package-level Gomega. Restore it before the live suite runs.
	originalGomega := Default
	Default = NewWithT(t)
	t.Cleanup(func() { Default = originalGomega })
	tests := []struct {
		name        string
		claims      int
		nodes       int
		disruptions int
		wantFailure string
	}{
		{name: "eight claims and nodes are allowed", claims: 8, nodes: 8},
		{name: "ninth ordinary claim fails immediately", claims: 9, nodes: 8, wantFailure: "Too many nodeclaims created. Expected no more than 8, got 9"},
		{name: "ninth node fails immediately", claims: 8, nodes: 9, wantFailure: "Too many nodes created. Expected no more than 8, got 9"},
		{name: "four disruptions fail immediately", claims: 8, nodes: 8, disruptions: 4, wantFailure: "Too many disruptions detected. Expected no more than 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			fixtureEnv, listCalls := replacementBudgetCountEnvironment(t, tt.claims, tt.nodes, tt.disruptions)
			failure := InterceptGomegaFailure(func() {
				fixtureEnv.ConsistentlyExpectDisruptionsUntilNoneLeft(5, 3, time.Second)
			})
			if tt.wantFailure == "" {
				g.Expect(failure).ToNot(HaveOccurred())
			} else {
				g.Expect(failure).To(MatchError(ContainSubstring(tt.wantFailure)))
			}
			// Two lists are a single sample. A budget breach cannot wait for a later
			// acceptable sample and disappear inside Eventually.
			g.Expect(*listCalls).To(Equal(2))
		})
	}

	for _, count := range []int{5, 6} {
		t.Run(fmt.Sprintf("final count %d", count), func(t *testing.T) {
			g := NewWithT(t)
			fixtureEnv, _ := replacementBudgetCountEnvironment(t, count, count, 0)
			claimFailure := InterceptGomegaFailure(func() { fixtureEnv.ExpectNodeClaimCount("==", 5) })
			nodeFailure := InterceptGomegaFailure(func() { fixtureEnv.ExpectNodeCount("==", 5) })
			if count == 5 {
				g.Expect(claimFailure).ToNot(HaveOccurred())
				g.Expect(nodeFailure).ToNot(HaveOccurred())
			} else {
				g.Expect(claimFailure).To(HaveOccurred())
				g.Expect(nodeFailure).To(HaveOccurred())
			}
		})
	}
}

func replacementBudgetCountEnvironment(t *testing.T, claims, nodes, disruptions int) (*common.Environment, *int) {
	t.Helper()
	objects := []client.Object{}
	for i := 0; i < claims; i++ {
		objects = append(objects, &karpv1.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("claim-%d", i), Labels: map[string]string{coretest.DiscoveryLabel: "unspecified"}},
			Status:     karpv1.NodeClaimStatus{ProviderID: fmt.Sprintf("provider-%d", i)},
		})
	}
	for i := 0; i < nodes; i++ {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("node-%d", i), Labels: map[string]string{coretest.DiscoveryLabel: "unspecified"}},
			Spec: corev1.NodeSpec{
				ProviderID: fmt.Sprintf("provider-%d", i),
				Taints:     []corev1.Taint{{Key: "testing/consolidation-budget", Value: "true", Effect: corev1.TaintEffectNoSchedule}},
			},
		}
		if i < disruptions {
			node.Spec.Taints = append(node.Spec.Taints, karpv1.DisruptedNoScheduleTaint)
		}
		objects = append(objects, node)
	}
	listCalls := 0
	kubeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			listCalls++
			return c.List(ctx, list, opts...)
		},
	}).Build()
	return &common.Environment{Context: context.Background(), Client: kubeClient}, &listCalls
}
