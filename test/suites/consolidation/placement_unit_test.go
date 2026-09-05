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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"
)

func TestReplacementBudgetHardProofEdges(t *testing.T) {
	g := NewWithT(t)
	pool := placementTestPool()
	pod := &corev1.Pod{}
	pool.Spec.Template.Spec.Taints = []corev1.Taint{{Key: "soft", Effect: corev1.TaintEffectPreferNoSchedule}}
	g.Expect(replacementBudgetPodExcluded(pool, pod)).To(BeFalse(), "a soft taint cannot prove exclusion")
	pool.Spec.Template.Labels = map[string]string{"example.com/placement": "budget"}
	pod.Spec.NodeSelector = map[string]string{"example.com/placement": "other"}
	g.Expect(replacementBudgetPodExcluded(pool, pod)).To(BeTrue(), "an explicit template label is a hard constraint")
	pool.Spec.Template.Labels = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}
	pod.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}
	g.Expect(replacementBudgetHardExcludes(pool, &pod.Spec)).To(BeFalse(), "contradictory pool constraints are not a useful isolation proof")
	g.Expect(replacementBudgetRequiresUser(pool)).To(BeFalse())
	pool = placementTestPool()
	pod.Spec.NodeSelector = nil
	pod.Spec.Affinity = &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{}}},
	}}
	g.Expect(replacementBudgetHardExcludes(pool, &pod.Spec)).To(BeFalse(), "empty or unknown terms conservatively fail closed")
	for _, operator := range []corev1.NodeSelectorOperator{corev1.NodeSelectorOpGt, corev1.NodeSelectorOpLt} {
		pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions = []corev1.NodeSelectorRequirement{{
			Key: v1beta1.LabelSKUCPU, Operator: operator, Values: []string{"not-an-integer"},
		}}
		g.Expect(replacementBudgetHardExcludes(pool, &pod.Spec)).To(BeFalse(), "invalid numeric requirements are not proofs")
	}
}

func TestReplacementBudgetPlacementLifecycle(t *testing.T) {
	g := NewWithT(t)
	world := newPlacementTestWorld(t)
	scope := world.scope()
	activationCtx, cancel := context.WithCancel(t.Context())
	var cleanup func(context.Context) error
	g.Expect(scope.Activate(activationCtx, world.registrar(&cleanup))).To(Succeed())
	g.Expect(scope.active).To(BeTrue())
	g.Expect(world.requests).To(HaveLen(1))
	g.Expect(world.requests[0].install).To(BeTrue())
	g.Expect(scope.Verify(t.Context(), nil)).To(Succeed())

	// A real owner can change its own fields while the scope holds placement.
	// The fake supplies these observations; it does not prove SSA merge semantics.
	changed := world.deployment(world.name)
	changed.Spec.Template.Spec.Containers[0].Image = "registry.k8s.io/pause:3.9"
	changed.Annotations["example.com/revision"] = "intervening-owner-update"
	changed.Spec.Template.Annotations["example.com/revision"] = "intervening-owner-update"
	changed.Generation++
	changed.ResourceVersion = "20"
	world.publishRollout(changed)
	g.Expect(scope.Verify(t.Context(), nil)).To(Succeed())

	world.ordinaryCleanupDone = true
	cancel()
	g.Expect(activationCtx.Err()).To(HaveOccurred())
	g.Expect(cleanup(t.Context())).To(Succeed(), "cleanup must use its fresh supplied context")
	restored := world.deployment(world.name)
	g.Expect(restored.Spec.Template.Spec.NodeSelector).To(BeNil())
	g.Expect(restored.Spec.Template.Spec.Containers[0].Image).To(Equal(changed.Spec.Template.Spec.Containers[0].Image))
	g.Expect(restored.Annotations).To(Equal(changed.Annotations))
	g.Expect(restored.Spec.Template.Annotations).To(Equal(changed.Spec.Template.Annotations))
	g.Expect(world.requests).To(HaveLen(2))
	g.Expect(world.requests[1].install).To(BeFalse())
	g.Expect(world.requests[1].resourceVersion).To(Equal("20"), "rollback must not use its activation RV or preimage")
	g.Expect(world.forbidden).To(BeEmpty())
	g.Expect(scope.Verify(t.Context(), nil)).ToNot(Succeed(), "a released scope cannot authorize the budget")
	g.Expect(cleanup(t.Context())).To(Succeed(), "repeated cleanup is read-only after confirmed release")
	g.Expect(world.requests).To(HaveLen(2))
	g.Expect(scope.Activate(t.Context(), world.registrar(&cleanup))).ToNot(Succeed(), "one scope cannot be reused for a patch fight")
}

func TestReplacementBudgetPlacementNoOps(t *testing.T) {
	for _, kind := range []string{"taint excluded", "already hard isolated", "unsupported but hard excluded"} {
		t.Run(kind, func(t *testing.T) {
			g := NewWithT(t)
			world := newPlacementTestWorld(t)
			deployment := world.deployment(world.name)
			switch kind {
			case "taint excluded":
				deployment.Spec.Template.Spec.Tolerations = nil
			case "already hard isolated":
				deployment.Spec.Template.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}
				deployment.ManagedFields[0].FieldsV1 = placementTestFields(`{"f:spec":{"f:template":{"f:spec":{"f:nodeSelector":{},"f:containers":{}}}}}`)
			case "unsupported but hard excluded":
				deployment.Spec.Template.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}
			}
			world.publishRollout(deployment)
			if kind == "unsupported but hard excluded" {
				pod := world.pod(world.name)
				pod.OwnerReferences = nil
				world.put(pod)
			}
			scope := world.scope()
			var cleanup func(context.Context) error
			g.Expect(scope.Activate(t.Context(), world.registrar(&cleanup))).To(Succeed())
			world.ordinaryCleanupDone = true
			g.Expect(cleanup(t.Context())).To(Succeed())
			g.Expect(world.requests).To(BeEmpty(), "read-only placement must not acquire or release SSA intent")
			g.Expect(world.forbidden).To(BeEmpty())
		})
	}
	g := NewWithT(t)
	world := newPlacementTestWorld(t)
	g.Expect(world.scope().manager).ToNot(Equal(world.scope().manager), "field managers must be distinct per scope")
	g.Expect(world.scope().Activate(t.Context(), nil)).ToNot(Succeed(), "mutation without a cleanup registrar is forbidden")
	g.Expect(world.requests).To(BeEmpty())
}

func TestReplacementBudgetPlacementPlanningFailures(t *testing.T) {
	tests := []struct {
		name   string
		change func(*placementTestWorld)
	}{
		{name: "bare compatible Pod", change: func(w *placementTestWorld) { p := w.pod(w.name); p.OwnerReferences = nil; w.put(p) }},
		{name: "Job is not a supported mutation owner", change: func(w *placementTestWorld) {
			p := w.pod(w.name)
			p.OwnerReferences[0].Kind = "Job"
			p.OwnerReferences[0].APIVersion = "batch/v1"
			w.put(p)
		}},
		{name: "missing Pod UID", change: func(w *placementTestWorld) { p := w.pod(w.name); p.UID = ""; w.put(p) }},
		{name: "Pod replaced between list and GET", change: func(w *placementTestWorld) {
			w.client.PrependReactor("get", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				pod := w.pod(w.name)
				pod.UID = "replacement-pod-uid"
				return true, pod, nil
			})
		}},
		{name: "Pod GET returns another identity", change: func(w *placementTestWorld) {
			w.client.PrependReactor("get", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				pod := w.pod(w.name)
				pod.Namespace = "different-namespace"
				return true, pod, nil
			})
		}},
		{name: "Pod GET error is not hidden", change: func(w *placementTestWorld) {
			w.client.PrependReactor("get", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("pod read failed")
			})
		}},
		{name: "Pod list error is not hidden", change: func(w *placementTestWorld) {
			w.client.PrependReactor("list", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("pod list failed")
			})
		}},
		{name: "missing ReplicaSet", change: func(w *placementTestWorld) { w.remove(w.replicaSet(w.name)) }},
		{name: "replaced ReplicaSet UID", change: func(w *placementTestWorld) { rs := w.replicaSet(w.name); rs.UID = "replacement-rs"; w.put(rs) }},
		{name: "missing Deployment", change: func(w *placementTestWorld) { w.remove(w.deployment(w.name)) }},
		{name: "replaced Deployment UID", change: func(w *placementTestWorld) { d := w.deployment(w.name); d.UID = "replacement-deployment"; w.put(d) }},
		{name: "missing Deployment RV", change: func(w *placementTestWorld) { d := w.deployment(w.name); d.ResourceVersion = ""; w.put(d) }},
		{name: "wrong controller name with matching UID", change: func(w *placementTestWorld) {
			rs := w.replicaSet(w.name)
			rs.OwnerReferences[0].Name = "different-name"
			w.put(rs)
		}},
		{name: "Pod labels do not match its ReplicaSet", change: func(w *placementTestWorld) {
			p := w.pod(w.name)
			p.Labels = map[string]string{"app": "different"}
			w.put(p)
		}},
		{name: "multiple controlling owners", change: func(w *placementTestWorld) {
			p := w.pod(w.name)
			p.OwnerReferences = append(p.OwnerReferences, *p.OwnerReferences[0].DeepCopy())
			w.put(p)
		}},
		{name: "no existing healthy system placement", change: func(w *placementTestWorld) {
			node := w.node()
			node.Labels[v1beta1.AKSLabelMode] = v1beta1.ModeUser
			w.put(node)
		}},
		{name: "Update-owned compatible template", change: func(w *placementTestWorld) {
			d := w.deployment(w.name)
			d.ManagedFields[0].Operation = metav1.ManagedFieldsOperationUpdate
			w.put(d)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			world := newPlacementTestWorld(t)
			tt.change(world)
			scope := world.scope()
			var cleanup func(context.Context) error
			g.Expect(scope.Activate(t.Context(), world.registrar(&cleanup))).ToNot(Succeed())
			g.Expect(scope.Verify(t.Context(), nil)).ToNot(Succeed(), "failed activation must not authorize a budget")
			world.ordinaryCleanupDone = true
			g.Expect(cleanup(t.Context())).To(Succeed(), "never-written targets require no API mutation")
			g.Expect(world.requests).To(BeEmpty())
			g.Expect(world.forbidden).To(BeEmpty())
		})
	}
}

func TestReplacementBudgetPlacementWritableContract(t *testing.T) {
	tests := []struct {
		name   string
		change func(*appsv1.Deployment)
	}{
		{name: "nonempty selector", change: func(d *appsv1.Deployment) {
			d.Spec.Template.Spec.NodeSelector = map[string]string{"example.com/other": "value"}
		}},
		{name: "empty but present selector", change: func(d *appsv1.Deployment) { d.Spec.Template.Spec.NodeSelector = map[string]string{} }},
		{name: "absent but owned selector", change: func(d *appsv1.Deployment) {
			d.ManagedFields = append(d.ManagedFields, placementTestOwner("foreign", budgetPlacementSelectorFields))
		}},
		{name: "owned ancestor", change: func(d *appsv1.Deployment) {
			d.ManagedFields = append(d.ManagedFields, placementTestOwner("foreign", `{"f:spec":{}}`))
		}},
		{name: "malformed atomic child ownership", change: func(d *appsv1.Deployment) {
			d.ManagedFields = append(d.ManagedFields, placementTestOwner("foreign", `{"f:spec":{"f:template":{"f:spec":{"f:nodeSelector":{"f:example.com/other":{}}}}}}`))
		}},
		{name: "malformed managed fields JSON", change: func(d *appsv1.Deployment) { d.ManagedFields[0].FieldsV1 = placementTestFields(`invalid`) }},
		{name: "null managed fields", change: func(d *appsv1.Deployment) { d.ManagedFields[0].FieldsV1 = placementTestFields(`null`) }},
		{name: "unknown managed fields version", change: func(d *appsv1.Deployment) { d.ManagedFields[0].APIVersion = "apps/unknown" }},
		{name: "unknown operation", change: func(d *appsv1.Deployment) { d.ManagedFields[0].Operation = "Unknown" }},
		{name: "missing fields", change: func(d *appsv1.Deployment) { d.ManagedFields[0].FieldsV1 = nil }},
		{name: "no verified template owner", change: func(d *appsv1.Deployment) { d.ManagedFields = nil }},
		{name: "template Update owner", change: func(d *appsv1.Deployment) { d.ManagedFields[0].Operation = metav1.ManagedFieldsOperationUpdate }},
		{name: "discovery-labelled Deployment", change: func(d *appsv1.Deployment) { d.Labels[coretest.DiscoveryLabel] = "test" }},
		{name: "deleting Deployment", change: func(d *appsv1.Deployment) { d.DeletionTimestamp = ptr.To(metav1.Now()) }},
		{name: "missing UID", change: func(d *appsv1.Deployment) { d.UID = "" }},
		{name: "missing RV", change: func(d *appsv1.Deployment) { d.ResourceVersion = "" }},
		{name: "paused rollout", change: func(d *appsv1.Deployment) { d.Spec.Paused = true }},
		{name: "Recreate rollout", change: func(d *appsv1.Deployment) { d.Spec.Strategy.Type = appsv1.RecreateDeploymentStrategyType }},
		{name: "zero replicas", change: func(d *appsv1.Deployment) { d.Spec.Replicas = ptr.To[int32](0) }},
		{name: "owner requires user placement", change: func(d *appsv1.Deployment) {
			d.Spec.Template.Spec.Affinity = &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{
					Key: v1beta1.AKSLabelMode, Operator: corev1.NodeSelectorOpIn, Values: []string{v1beta1.ModeUser},
				}}}}},
			}}
		}},
	}
	NewWithT(t).Expect(validateBudgetPlacementWritable(placementTestDeployment("controller"))).To(Succeed())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deployment := placementTestDeployment("controller")
			tt.change(deployment)
			NewWithT(t).Expect(validateBudgetPlacementWritable(deployment)).ToNot(Succeed())
		})
	}
	for _, field := range []string{"uid", "resourceVersion"} {
		t.Run("request refuses missing "+field, func(t *testing.T) {
			deployment := placementTestDeployment("controller")
			if field == "uid" {
				deployment.UID = ""
			} else {
				deployment.ResourceVersion = ""
			}
			_, err := budgetPlacementSelectorPatch(deployment, true)
			NewWithT(t).Expect(err).To(HaveOccurred())
		})
	}
}

func TestReplacementBudgetPlacementMutationFailures(t *testing.T) {
	for _, mode := range []string{"no-op", "response-only", "persist-then-error", "extra-field", "delete-then-error"} {
		t.Run(mode, func(t *testing.T) {
			g := NewWithT(t)
			world := newPlacementTestWorld(t)
			world.mode = mode
			scope := world.scope()
			var cleanup func(context.Context) error
			g.Expect(scope.Activate(t.Context(), world.registrar(&cleanup))).ToNot(Succeed())
			g.Expect(scope.active).To(BeFalse())
			g.Expect(world.requests).To(HaveLen(1))
			g.Expect(scope.targets[0].attempted).To(BeTrue(), "a lost response must not lose the rollback target")
			world.mode = "normal"
			world.ordinaryCleanupDone = true
			err := cleanup(t.Context())
			if mode == "delete-then-error" {
				g.Expect(err).To(HaveOccurred(), "missing is not a successful rollback and must not upsert")
				g.Expect(world.requests).To(HaveLen(1))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(world.deployment(world.name).Spec.Template.Spec.NodeSelector).To(BeNil())
			}
			if mode == "extra-field" {
				g.Expect(world.deployment(world.name).Spec.Template.Spec.Containers[0].Image).To(Equal("unexpected-owner-image"), "cleanup must not restore a full preimage")
			}
			g.Expect(world.forbidden).To(BeEmpty(), "no-op Create/Delete/Update stubs cannot hide unsafe calls")
		})
	}
}

func TestReplacementBudgetPlacementConflictRetries(t *testing.T) {
	for _, mode := range []string{"version-conflict-once", "same-version-conflict", "adopt-on-conflict"} {
		t.Run(mode, func(t *testing.T) {
			g := NewWithT(t)
			world := newPlacementTestWorld(t)
			world.mode = mode
			scope := world.scope()
			var cleanup func(context.Context) error
			err := scope.Activate(t.Context(), world.registrar(&cleanup))
			world.mode = "normal"
			world.ordinaryCleanupDone = true
			if mode == "version-conflict-once" {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(world.requests).To(HaveLen(2))
				g.Expect(world.requests[0].resourceVersion).To(Equal("1"))
				g.Expect(world.requests[1].resourceVersion).To(Equal("2"))
				g.Expect(cleanup(t.Context())).To(Succeed())
				g.Expect(world.deployment(world.name).Annotations).To(HaveKeyWithValue("example.com/revision", "concurrent-change"))
			} else {
				g.Expect(err).To(HaveOccurred())
				g.Expect(world.requests).To(HaveLen(1), "neither unchanged-RV conflicts nor owner adoption permit repeated apply")
				if mode == "adopt-on-conflict" {
					g.Expect(cleanup(t.Context())).ToNot(Succeed())
					g.Expect(world.deployment(world.name).Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(v1beta1.AKSLabelMode, v1beta1.ModeSystem))
				} else {
					g.Expect(cleanup(t.Context())).To(Succeed())
				}
			}
			g.Expect(world.forbidden).To(BeEmpty())
		})
	}
}

func TestReplacementBudgetPlacementRequiresActualRollout(t *testing.T) {
	g := NewWithT(t)
	world := newPlacementTestWorld(t)
	world.autoRollout = false
	world.reuseOriginalOnRelease = true
	originalPod := world.pod(world.name)
	scope := world.scope()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reads := 0
	world.afterMutationGet = func() {
		reads++
		if reads == 2 {
			cancel()
		} // First GET confirms intent; next GET begins the rollout check.
	}
	var cleanup func(context.Context) error
	g.Expect(scope.Activate(ctx, world.registrar(&cleanup))).ToNot(Succeed())
	g.Expect(scope.targets[0].confirmed).To(BeTrue(), "template write happened, but cannot authorize budget creation")
	g.Expect(scope.active).To(BeFalse())
	g.Expect(world.requests).To(HaveLen(1))
	g.Expect(world.forbidden).To(BeEmpty(), "do not delete old Pods to force a rollout")
	world.afterMutationGet = nil
	world.autoRollout = true
	world.ordinaryCleanupDone = true
	g.Expect(cleanup(t.Context())).To(Succeed())
	remaining, err := world.client.Tracker().Get(placementPods, originalPod.Namespace, originalPod.Name)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(remaining.(*corev1.Pod).UID).To(Equal(originalPod.UID), "restoration must not require deleting unchanged healthy original Pods")
	g.Expect(world.forbidden).To(BeEmpty())
}

func TestReplacementBudgetPlacementRolloutObservations(t *testing.T) {
	tests := []struct {
		name   string
		change func(*placementTestWorld)
	}{
		{name: "old observed generation", change: func(w *placementTestWorld) { d := w.deployment(w.name); d.Status.ObservedGeneration--; w.put(d) }},
		{name: "not all updated replicas", change: func(w *placementTestWorld) { d := w.deployment(w.name); d.Status.UpdatedReplicas = 0; w.put(d) }},
		{name: "owner not available", change: func(w *placementTestWorld) { d := w.deployment(w.name); d.Status.AvailableReplicas = 0; w.put(d) }},
		{name: "Pod not ready", change: func(w *placementTestWorld) {
			p := w.pod(w.name)
			p.Status.Conditions[0].Status = corev1.ConditionFalse
			w.put(p)
		}},
		{name: "container not running", change: func(w *placementTestWorld) {
			p := w.pod(w.name)
			p.Status.ContainerStatuses[0].State.Running = nil
			w.put(p)
		}},
		{name: "Pod still pending", change: func(w *placementTestWorld) { p := w.pod(w.name); p.Status.Phase = corev1.PodPending; w.put(p) }},
		{name: "Pod has incompatible selector", change: func(w *placementTestWorld) {
			p := w.pod(w.name)
			p.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeUser}
			w.put(p)
		}},
		{name: "Pod has stale ReplicaSet UID", change: func(w *placementTestWorld) { p := w.pod(w.name); p.OwnerReferences[0].UID = "old-rs-uid"; w.put(p) }},
		{name: "ReplicaSet has stale Deployment UID", change: func(w *placementTestWorld) {
			rs := w.replicaSet(w.name)
			rs.OwnerReferences[0].UID = "old-deployment-uid"
			w.put(rs)
		}},
		{name: "old ReplicaSet template", change: func(w *placementTestWorld) {
			rs := w.replicaSet(w.name)
			rs.Spec.Template.Spec.NodeSelector = nil
			w.put(rs)
		}},
		{name: "actual Node has user mode", change: func(w *placementTestWorld) {
			n := w.node()
			n.Labels[v1beta1.AKSLabelMode] = v1beta1.ModeUser
			w.put(n)
		}},
		{name: "actual Node not ready", change: func(w *placementTestWorld) {
			n := w.node()
			n.Status.Conditions[0].Status = corev1.ConditionFalse
			w.put(n)
		}},
		{name: "old terminating Pod remains", change: func(w *placementTestWorld) {
			p := w.pod(w.name)
			p.Name += "-old"
			p.UID += "-old"
			p.DeletionTimestamp = ptr.To(metav1.Now())
			w.put(p)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			world := newPlacementTestWorld(t)
			scope := world.scope()
			var cleanup func(context.Context) error
			g.Expect(scope.Activate(t.Context(), world.registrar(&cleanup))).To(Succeed())
			tt.change(world)
			_, err := scope.rolloutReady(t.Context(), world.deployment(world.name), scope.targets[0], "isolated")
			g.Expect(err).To(HaveOccurred())
			g.Expect(world.requests).To(HaveLen(1), "rollout inspection is read-only")
			g.Expect(world.forbidden).To(BeEmpty())
		})
	}
}

func TestReplacementBudgetPlacementCleanupDiscrepancies(t *testing.T) {
	tests := []struct {
		name   string
		change func(*placementTestWorld)
	}{
		{name: "coowned selector", change: func(w *placementTestWorld) {
			d := w.deployment(w.name)
			d.ManagedFields = append(d.ManagedFields, placementTestOwner("foreign", budgetPlacementSelectorFields))
			w.put(d)
		}},
		{name: "external selector change", change: func(w *placementTestWorld) {
			d := w.deployment(w.name)
			d.Spec.Template.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeUser}
			d.ManagedFields = []metav1.ManagedFieldsEntry{placementTestOwner("foreign", budgetPlacementSelectorFields)}
			w.put(d)
		}},
		{name: "lost selector is not silently restored", change: func(w *placementTestWorld) {
			d := w.deployment(w.name)
			d.Spec.Template.Spec.NodeSelector = nil
			d.ManagedFields = d.ManagedFields[:1]
			w.put(d)
		}},
		{name: "same-name replacement", change: func(w *placementTestWorld) {
			d := w.deployment(w.name)
			d.UID = "replacement-uid"
			w.put(d)
		}},
		{name: "missing Deployment is not success", change: func(w *placementTestWorld) { w.remove(w.deployment(w.name)) }},
		{name: "broadened scope intent is not omitted", change: func(w *placementTestWorld) {
			d := w.deployment(w.name)
			d.ManagedFields[1].FieldsV1 = placementTestFields(`{"f:spec":{"f:replicas":{},"f:template":{"f:spec":{"f:nodeSelector":{}}}}}`)
			w.put(d)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			world := newPlacementTestWorld(t)
			scope := world.scope()
			var cleanup func(context.Context) error
			g.Expect(scope.Activate(t.Context(), world.registrar(&cleanup))).To(Succeed())
			tt.change(world)
			g.Expect(scope.Verify(t.Context(), nil)).ToNot(Succeed(), "durability checkpoints must reject ownership or identity changes")
			world.ordinaryCleanupDone = true
			g.Expect(cleanup(t.Context())).ToNot(Succeed())
			g.Expect(world.requests).To(HaveLen(1), "foreign or missing intent must not cause another apply")
			g.Expect(world.forbidden).To(BeEmpty())
		})
	}
}

func TestReplacementBudgetPlacementCleanupRequiresEffectsAndHealth(t *testing.T) {
	for _, mode := range []string{"no-op", "response-only", "stalled rollout"} {
		t.Run(mode, func(t *testing.T) {
			g := NewWithT(t)
			world := newPlacementTestWorld(t)
			scope := world.scope()
			var cleanup func(context.Context) error
			g.Expect(scope.Activate(t.Context(), world.registrar(&cleanup))).To(Succeed())
			world.ordinaryCleanupDone = true
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "stalled rollout" {
				world.autoRollout = false
				reads := 0
				world.afterMutationGet = func() {
					if world.deployment(world.name).Spec.Template.Spec.NodeSelector == nil {
						reads++
						if reads == 2 {
							cancel()
						}
					}
				}
			} else {
				world.mode = mode
			}
			g.Expect(cleanup(ctx)).ToNot(Succeed(), "a successful SSA response is not proof of restored placement or health")
			g.Expect(world.requests).To(HaveLen(2))
			world.mode = "normal"
			world.autoRollout = true
			world.afterMutationGet = nil
			if mode == "stalled rollout" {
				world.publishRollout(world.deployment(world.name))
			}
			g.Expect(cleanup(t.Context())).To(Succeed())
			if mode == "stalled rollout" {
				g.Expect(world.requests).To(HaveLen(2), "already released intent must not be re-applied to finish a rollout")
			} else {
				g.Expect(world.requests).To(HaveLen(3))
			}
			g.Expect(world.forbidden).To(BeEmpty())
		})
	}
}

func TestReplacementBudgetPlacementPlansBeforeAnyWrite(t *testing.T) {
	g := NewWithT(t)
	world := newPlacementTestWorld(t)
	world.addDeployment("zzz-unsupported-controller")
	unsupported := world.deployment("zzz-unsupported-controller")
	unsupported.ManagedFields[0].Operation = metav1.ManagedFieldsOperationUpdate
	world.put(unsupported)
	scope := world.scope()
	var cleanup func(context.Context) error
	g.Expect(scope.Activate(t.Context(), world.registrar(&cleanup))).ToNot(Succeed())
	world.ordinaryCleanupDone = true
	g.Expect(cleanup(t.Context())).To(Succeed())
	g.Expect(world.requests).To(BeEmpty(), "a later unsupported target must prevent partial activation of earlier targets")
	g.Expect(world.forbidden).To(BeEmpty())
}

func TestReplacementBudgetPlacementCleanupContinues(t *testing.T) {
	g := NewWithT(t)
	world := newPlacementTestWorld(t)
	world.addDeployment("zzz-other-controller")
	scope := world.scope()
	var cleanup func(context.Context) error
	g.Expect(scope.Activate(t.Context(), world.registrar(&cleanup))).To(Succeed())
	other := world.deployment("zzz-other-controller")
	other.ManagedFields = append(other.ManagedFields, placementTestOwner("foreign", budgetPlacementSelectorFields))
	world.put(other)
	world.ordinaryCleanupDone = true
	g.Expect(cleanup(t.Context())).ToNot(Succeed())
	g.Expect(world.deployment(world.name).Spec.Template.Spec.NodeSelector).To(BeNil(), "another target's discrepancy cannot abandon our safe rollback")
	g.Expect(world.deployment(other.Name).Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(v1beta1.AKSLabelMode, v1beta1.ModeSystem))
	g.Expect(world.requests).To(HaveLen(3))
	g.Expect(world.forbidden).To(BeEmpty())
}

func TestReplacementBudgetPlacementOwnerAndPodMustAgree(t *testing.T) {
	g := NewWithT(t)
	world := newPlacementTestWorld(t)
	pod := world.pod(world.name)
	pod.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}
	world.put(pod)
	scope := world.scope()
	var cleanup func(context.Context) error
	g.Expect(scope.Activate(t.Context(), world.registrar(&cleanup))).ToNot(Succeed(), "an isolated current Pod cannot hide a compatible future template or justify an unproven restoration")
	g.Expect(world.requests).To(BeEmpty())

	// A new compatible owner cannot be hidden by the initial target set.
	world = newPlacementTestWorld(t)
	scope = world.scope()
	g.Expect(scope.Activate(t.Context(), world.registrar(&cleanup))).To(Succeed())
	world.addDeployment("new-background-controller")
	g.Expect(scope.Verify(t.Context(), nil)).ToNot(Succeed())
	fixture := world.deployment("new-background-controller")
	g.Expect(scope.Verify(t.Context(), []*appsv1.Deployment{fixture})).To(Succeed(), "only the exact fixture owner UID is allowed")
	wrong := fixture.DeepCopy()
	wrong.UID = "wrong-fixture-uid"
	g.Expect(scope.Verify(t.Context(), []*appsv1.Deployment{wrong})).ToNot(Succeed(), "same name is not a fixture exemption")
	g.Expect(world.requests).To(HaveLen(1))
	g.Expect(world.forbidden).To(BeEmpty())
}

// This second real API-server test calls the actual mutation/rollback helpers.
// It does not reuse or replace the independent baseline SSA protocol assertions.
func TestReplacementBudgetPlacementSSAImplementation(t *testing.T) {
	kube := newPlacementImplementationAPI(t)
	for _, scenario := range []string{"owner changes preserved", "coownership rejected", "missing target", "replaced UID"} {
		t.Run(scenario, func(t *testing.T) {
			g := NewWithT(t)
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			owner := ssaContractOwner()
			deployments := kube.AppsV1().Deployments(owner.Namespace)
			ownerOptions := metav1.PatchOptions{FieldManager: "deployment-owner", Force: ptr.To(true)}
			created, err := ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
			g.Expect(err).ToNot(HaveOccurred())
			scope := newReplacementBudgetPlacement(kube, placementTestPool())
			target := &budgetPlacementTarget{key: types.NamespacedName{Namespace: created.Namespace, Name: created.Name}, uid: created.UID, writeSelector: true}
			g.Expect(scope.mutateSelector(ctx, target, true)).To(Succeed())
			g.Expect(target.confirmed).To(BeTrue())
			reapplied, err := ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(scope.validateOwned(reapplied)).To(Succeed())
			switch scenario {
			case "owner changes preserved":
				owner.Spec.Template.Spec.Containers[0].Image = "registry.k8s.io/pause:3.9"
				owner.Annotations["example.com/revision"] = "owner-update"
				owner.Spec.Template.Annotations["example.com/revision"] = "owner-update"
				_, err := ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(scope.mutateSelector(ctx, target, false)).To(Succeed())
				restored := ssaContractGet(t, ctx, deployments, owner.Name)
				g.Expect(scope.validateRestored(restored)).To(Succeed())
				g.Expect(restored.Spec.Template.Spec.Containers[0].Image).To(Equal(owner.Spec.Template.Spec.Containers[0].Image))
				g.Expect(restored.Annotations).To(Equal(owner.Annotations))
				g.Expect(restored.Spec.Template.Annotations).To(Equal(owner.Spec.Template.Annotations))
			case "coownership rejected":
				owner.Spec.Template.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}
				_, err := ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
				g.Expect(err).ToNot(HaveOccurred())
				before := ssaContractGet(t, ctx, deployments, owner.Name)
				g.Expect(scope.mutateSelector(ctx, target, false)).ToNot(Succeed())
				g.Expect(ssaContractGet(t, ctx, deployments, owner.Name)).To(Equal(before))
			case "missing target", "replaced UID":
				g.Expect(deployments.Delete(ctx, owner.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &target.uid}})).To(Succeed())
				if scenario == "replaced UID" {
					replacement, err := ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(replacement.UID).ToNot(Equal(target.uid))
					g.Expect(scope.mutateSelector(ctx, target, false)).ToNot(Succeed())
					g.Expect(ssaContractGet(t, ctx, deployments, owner.Name)).To(Equal(replacement))
				} else {
					g.Expect(scope.mutateSelector(ctx, target, false)).ToNot(Succeed())
					_, err := deployments.Get(ctx, owner.Name, metav1.GetOptions{})
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "rollback must not upsert")
				}
			}
		})
	}
}

func newPlacementImplementationAPI(t *testing.T) kubernetes.Interface {
	t.Helper()
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		assets = "/usr/local/kubebuilder/bin"
	}
	for _, name := range []string{"kube-apiserver", "etcd", "kubectl"} {
		info, err := os.Stat(filepath.Join(assets, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			t.Fatalf("implementation SSA test requires installed envtest asset %s: %v", name, err)
		}
	}
	environment := &envtest.Environment{
		UseExistingCluster: ptr.To(false), DownloadBinaryAssets: false,
		ControlPlane: envtest.ControlPlane{
			APIServer: &envtest.APIServer{Path: filepath.Join(assets, "kube-apiserver")},
			Etcd:      &envtest.Etcd{Path: filepath.Join(assets, "etcd")}, KubectlPath: filepath.Join(assets, "kubectl"),
		},
		ControlPlaneStartTimeout: time.Minute, ControlPlaneStopTimeout: time.Minute,
	}
	t.Cleanup(func() { NewWithT(t).Expect(environment.Stop()).To(Succeed()) })
	config, err := environment.Start()
	NewWithT(t).Expect(err).ToNot(HaveOccurred())
	kube, err := kubernetes.NewForConfig(config)
	NewWithT(t).Expect(err).ToNot(HaveOccurred())
	version, err := kube.Discovery().ServerVersion()
	NewWithT(t).Expect(err).ToNot(HaveOccurred())
	t.Logf("actual placement implementation uses real Kubernetes %s", version.GitVersion)
	return kube
}

// The fake below supplies explicitly controlled Kubernetes observations, not SSA
// semantics. Only the real API-server tests prove apply/ownership behavior. Every
// actual implementation request is checked; Create/Delete/Update are forbidden,
// and no-op/response-only modes prove that a successful stub cannot mask a bug.
type placementTestWorld struct {
	t                      *testing.T
	client                 *kubefake.Clientset
	name                   string
	manager                string
	registered             bool
	ordinaryCleanupDone    bool
	autoRollout            bool
	reuseOriginalOnRelease bool
	mode                   string
	requests               []placementTestRequest
	forbidden              []string
	mutations              int
	afterMutationGet       func()
}

type placementTestRequest struct {
	install         bool
	resourceVersion string
}

var (
	placementDeployments = appsv1.SchemeGroupVersion.WithResource("deployments")
	placementReplicaSets = appsv1.SchemeGroupVersion.WithResource("replicasets")
	placementPods        = corev1.SchemeGroupVersion.WithResource("pods")
	placementNodes       = corev1.SchemeGroupVersion.WithResource("nodes")
)

func newPlacementTestWorld(t *testing.T) *placementTestWorld {
	t.Helper()
	world := &placementTestWorld{t: t, name: "background-controller", autoRollout: true, mode: "normal", client: kubefake.NewSimpleClientset()}
	world.put(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "system-node", UID: "node-uid", ResourceVersion: "1", Labels: map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	})
	world.addDeployment(world.name)
	world.client.PrependReactor("patch", "deployments", world.patch)
	world.client.PrependReactor("get", "deployments", func(action ktesting.Action) (bool, runtime.Object, error) {
		if world.afterMutationGet == nil || world.mutations == 0 {
			return false, nil, nil
		}
		obj, err := world.client.Tracker().Get(placementDeployments, action.GetNamespace(), action.(ktesting.GetAction).GetName())
		world.afterMutationGet()
		return true, obj, err
	})
	world.client.PrependReactor("*", "*", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetVerb() == "get" || action.GetVerb() == "list" || action.GetVerb() == "patch" && action.GetResource().Resource == "deployments" {
			return false, nil, nil
		}
		world.forbidden = append(world.forbidden, action.GetVerb()+" "+action.GetResource().Resource)
		return true, nil, fmt.Errorf("forbidden implementation action %s", world.forbidden[len(world.forbidden)-1])
	})
	return world
}

func (w *placementTestWorld) scope() *replacementBudgetPlacement {
	scope := newReplacementBudgetPlacement(w.client, placementTestPool())
	scope.pollInterval = time.Millisecond
	scope.timeout = 5 * time.Second
	w.manager = scope.manager
	return scope
}

func placementTestPool() *karpv1.NodePool {
	pool := (&common.Environment{}).DefaultNodePool(&v1beta1.AKSNodeClass{ObjectMeta: metav1.ObjectMeta{Name: "class"}})
	replacementBudgetResources(pool)
	return pool
}

func (w *placementTestWorld) registrar(cleanup *func(context.Context) error) func(func(context.Context) error) {
	return func(callback func(context.Context) error) { w.registered = true; *cleanup = callback }
}

func placementTestFields(fields string) *metav1.FieldsV1 {
	return &metav1.FieldsV1{Raw: []byte(fields)}
}

func placementTestOwner(manager, fields string) metav1.ManagedFieldsEntry {
	return metav1.ManagedFieldsEntry{Manager: manager, Operation: metav1.ManagedFieldsOperationApply, APIVersion: "apps/v1", FieldsType: "FieldsV1", FieldsV1: placementTestFields(fields)}
}

func placementTestDeployment(name string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system", Name: name, UID: types.UID(name + "-uid"), ResourceVersion: "1", Generation: 1,
			Labels: map[string]string{"app": name}, Annotations: map[string]string{"example.com/revision": "original"},
			ManagedFields: []metav1.ManagedFieldsEntry{placementTestOwner("deployment-owner", `{"f:spec":{"f:template":{"f:spec":{"f:containers":{},"f:affinity":{}}}}}`)},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1), Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}, Annotations: map[string]string{"example.com/revision": "original"}},
				Spec: corev1.PodSpec{
					Containers:  []corev1.Container{{Name: "controller", Image: "registry.k8s.io/pause:3.10"}},
					Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}, {Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute}},
				},
			},
		},
	}
}

func (w *placementTestWorld) addDeployment(name string) {
	w.publishRollout(placementTestDeployment(name))
}

func (w *placementTestWorld) deployment(name string) *appsv1.Deployment {
	w.t.Helper()
	object, err := w.client.Tracker().Get(placementDeployments, "kube-system", name)
	NewWithT(w.t).Expect(err).ToNot(HaveOccurred())
	return object.(*appsv1.Deployment).DeepCopy()
}

func (w *placementTestWorld) pod(name string) *corev1.Pod {
	w.t.Helper()
	deployment := w.deployment(name)
	object, err := w.client.Tracker().Get(placementPods, "kube-system", fmt.Sprintf("%s-pod-%d", name, deployment.Generation))
	NewWithT(w.t).Expect(err).ToNot(HaveOccurred())
	return object.(*corev1.Pod).DeepCopy()
}

func (w *placementTestWorld) replicaSet(name string) *appsv1.ReplicaSet {
	w.t.Helper()
	deployment := w.deployment(name)
	object, err := w.client.Tracker().Get(placementReplicaSets, "kube-system", fmt.Sprintf("%s-rs-%d", name, deployment.Generation))
	NewWithT(w.t).Expect(err).ToNot(HaveOccurred())
	return object.(*appsv1.ReplicaSet).DeepCopy()
}

func (w *placementTestWorld) node() *corev1.Node {
	w.t.Helper()
	object, err := w.client.Tracker().Get(placementNodes, "", "system-node")
	NewWithT(w.t).Expect(err).ToNot(HaveOccurred())
	return object.(*corev1.Node).DeepCopy()
}

func placementTestResource(object runtime.Object) (schema.GroupVersionResource, metav1.Object) {
	switch object := object.(type) {
	case *appsv1.Deployment:
		return placementDeployments, object
	case *appsv1.ReplicaSet:
		return placementReplicaSets, object
	case *corev1.Pod:
		return placementPods, object
	case *corev1.Node:
		return placementNodes, object
	default:
		panic(fmt.Sprintf("unsupported observation object %T", object))
	}
}

func (w *placementTestWorld) put(object runtime.Object) {
	w.t.Helper()
	resource, metadata := placementTestResource(object)
	err := w.client.Tracker().Update(resource, object, metadata.GetNamespace())
	if apierrors.IsNotFound(err) {
		err = w.client.Tracker().Add(object)
	}
	NewWithT(w.t).Expect(err).ToNot(HaveOccurred())
}

func (w *placementTestWorld) remove(object runtime.Object) {
	w.t.Helper()
	resource, metadata := placementTestResource(object)
	NewWithT(w.t).Expect(w.client.Tracker().Delete(resource, metadata.GetNamespace(), metadata.GetName())).To(Succeed())
}

func (w *placementTestWorld) publishRollout(deployment *appsv1.Deployment) {
	w.t.Helper()
	// Model observed controller output directly in the tracker. The implementation
	// must never call Delete/Create/Update itself; those API actions are rejected.
	pods, err := w.client.Tracker().List(placementPods, corev1.SchemeGroupVersion.WithKind("Pod"), deployment.Namespace)
	NewWithT(w.t).Expect(err).ToNot(HaveOccurred())
	for i := range pods.(*corev1.PodList).Items {
		pod := &pods.(*corev1.PodList).Items[i]
		if pod.Labels["app"] == deployment.Name {
			w.remove(pod)
		}
	}
	deployment.Status = appsv1.DeploymentStatus{ObservedGeneration: deployment.Generation, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}
	w.put(deployment)
	rsName := fmt.Sprintf("%s-rs-%d", deployment.Name, deployment.Generation)
	rsUID := types.UID(fmt.Sprintf("%s-rs-%d", deployment.UID, deployment.Generation))
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: rsName, Namespace: deployment.Namespace, UID: rsUID, ResourceVersion: deployment.ResourceVersion,
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: deployment.Name, UID: deployment.UID, Controller: ptr.To(true)}},
		},
		Spec: appsv1.ReplicaSetSpec{Replicas: ptr.To[int32](1), Selector: deployment.Spec.Selector.DeepCopy(), Template: *deployment.Spec.Template.DeepCopy()},
	}
	w.put(rs)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-pod-%d", deployment.Name, deployment.Generation), Namespace: deployment.Namespace,
			UID: types.UID(fmt.Sprintf("%s-pod-%d", deployment.UID, deployment.Generation)), ResourceVersion: deployment.ResourceVersion, Labels: maps.Clone(deployment.Spec.Template.Labels),
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: rs.Name, UID: rs.UID, Controller: ptr.To(true)}},
		},
		Spec: *deployment.Spec.Template.Spec.DeepCopy(),
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			ContainerStatuses: []corev1.ContainerStatus{{Name: "controller", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}},
		},
	}
	pod.Spec.NodeName = "system-node"
	w.put(pod)
}

//nolint:gocyclo // Keep explicit, independently tested fault modes beside the observed-request recorder.
func (w *placementTestWorld) patch(action ktesting.Action) (bool, runtime.Object, error) {
	w.t.Helper()
	g := NewWithT(w.t)
	request := action.(ktesting.PatchAction)
	options := action.(interface{ GetPatchOptions() metav1.PatchOptions }).GetPatchOptions()
	g.Expect(request.GetPatchType()).To(Equal(types.ApplyPatchType))
	g.Expect(options).To(Equal(metav1.PatchOptions{FieldManager: w.manager}), "no force, dry-run, or borrowed manager")
	g.Expect(w.registered).To(BeTrue(), "rollback must be registered before the first possible mutation")
	before := w.deployment(request.GetName())
	var intent map[string]interface{}
	g.Expect(json.Unmarshal(request.GetPatch(), &intent)).To(Succeed())
	_, install := intent["spec"]
	expected := map[string]interface{}{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]interface{}{
		"name": before.Name, "namespace": before.Namespace, "uid": string(before.UID), "resourceVersion": before.ResourceVersion,
	}}
	if install {
		expected["spec"] = map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{"nodeSelector": map[string]interface{}{v1beta1.AKSLabelMode: v1beta1.ModeSystem}}}}
	} else {
		g.Expect(w.ordinaryCleanupDone).To(BeTrue(), "release must follow ordinary budget-resource teardown")
	}
	g.Expect(intent).To(Equal(expected), "minimal UID/RV intent only, without discovery labels or foreign fields")
	w.requests = append(w.requests, placementTestRequest{install: install, resourceVersion: before.ResourceVersion})
	if w.mode == "no-op" {
		return true, before, nil
	}
	if w.mode == "same-version-conflict" {
		return true, nil, apierrors.NewConflict(placementDeployments.GroupResource(), before.Name, errors.New("unchanged version"))
	}
	if len(w.requests) == 1 && (w.mode == "version-conflict-once" || w.mode == "adopt-on-conflict") {
		before.ResourceVersion = "2"
		before.Annotations["example.com/revision"] = "concurrent-change"
		if w.mode == "adopt-on-conflict" {
			before.Spec.Template.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}
			before.ManagedFields = append(before.ManagedFields, placementTestOwner("foreign", budgetPlacementSelectorFields))
		}
		w.put(before)
		return true, nil, apierrors.NewConflict(placementDeployments.GroupResource(), before.Name, errors.New("version changed"))
	}
	after := before.DeepCopy()
	version, err := strconv.Atoi(before.ResourceVersion)
	g.Expect(err).ToNot(HaveOccurred())
	after.ResourceVersion = strconv.Itoa(version + 1)
	after.Generation++
	if install {
		after.Spec.Template.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}
		after.ManagedFields = append(after.ManagedFields, placementTestOwner(w.manager, budgetPlacementSelectorFields))
	} else {
		after.Spec.Template.Spec.NodeSelector = nil
		var retained []metav1.ManagedFieldsEntry
		for _, entry := range after.ManagedFields {
			if entry.Manager != w.manager {
				retained = append(retained, entry)
			}
		}
		after.ManagedFields = retained
	}
	if w.mode == "response-only" {
		return true, after, nil
	}
	if w.mode == "delete-then-error" {
		w.remove(before)
		return true, nil, errors.New("response lost after external deletion")
	}
	if w.mode == "extra-field" {
		after.Spec.Template.Spec.Containers[0].Image = "unexpected-owner-image"
	}
	w.mutations++
	if !install && w.reuseOriginalOnRelease {
		after.Status = appsv1.DeploymentStatus{ObservedGeneration: after.Generation, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}
		w.put(after) // Original healthy Pods and their original ReplicaSet remain.
	} else if w.autoRollout {
		w.publishRollout(after)
	} else {
		w.put(after)
	}
	if w.mode == "persist-then-error" {
		return true, nil, errors.New("response lost after persisted apply")
	}
	return true, after, nil
}
