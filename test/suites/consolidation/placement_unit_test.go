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
	"reflect"
	"strconv"
	"strings"
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

// These legacy faults have fake-only knowledge: no-op and response-only never
// persist, while persist-then-error persists before returning. The real-storage
// TestReplacementBudgetPlacementLateApply counterexample shows why the first two
// must now expect unresolved cleanup: their absence observations cannot tell the
// helper that the submitted write will never commit. The fault behavior is unchanged.
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
			} else if mode == "no-op" || mode == "response-only" {
				g.Expect(err).To(HaveOccurred(), "fake-only knowledge of non-persistence is not a request completion fence")
				g.Expect(err.Error()).To(ContainSubstring("unresolved selector apply"))
				g.Expect(scope.targets[0].released).To(BeFalse(), "a later Restore must still be able to recover a late write")
				g.Expect(world.requests).To(HaveLen(1), "do not manufacture a fence with a no-op apply")
				g.Expect(world.deployment(world.name).Spec.Template.Spec.NodeSelector).To(BeNil())
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

// Bookkeeping is checked against the actual helper's captured Patch actions.
// This fake does not prove persistence; the unchanged real-API cases do that.
func TestReplacementBudgetPlacementSubmittedApplyOutcomes(t *testing.T) {
	tests := []struct {
		name           string
		activationOK   bool
		cleanupOK      bool
		clientOutcomes []budgetPlacementApplyOutcome
	}{
		{name: "normal", activationOK: true, cleanupOK: true, clientOutcomes: []budgetPlacementApplyOutcome{budgetPlacementApplyAcknowledged, budgetPlacementApplyAcknowledged}},
		{name: "no-op", clientOutcomes: []budgetPlacementApplyOutcome{budgetPlacementApplyUnknown}},
		{name: "response-only", clientOutcomes: []budgetPlacementApplyOutcome{budgetPlacementApplyAcknowledged}},
		{name: "persist-then-error", cleanupOK: true, clientOutcomes: []budgetPlacementApplyOutcome{budgetPlacementApplyUnknown, budgetPlacementApplyAcknowledged}},
		{name: "same-version-conflict", cleanupOK: true, clientOutcomes: []budgetPlacementApplyOutcome{budgetPlacementApplyRejected}},
		{name: "version-conflict-once", activationOK: true, cleanupOK: true, clientOutcomes: []budgetPlacementApplyOutcome{budgetPlacementApplyRejected, budgetPlacementApplyAcknowledged, budgetPlacementApplyAcknowledged}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			world := newPlacementTestWorld(t)
			world.mode = tt.name
			scope := world.scope()
			var cleanup func(context.Context) error
			err := scope.Activate(t.Context(), world.registrar(&cleanup))
			g.Expect(err == nil).To(Equal(tt.activationOK), "activation error: %v", err)
			g.Expect(scope.targets).To(HaveLen(1))
			world.mode = "normal"
			world.ordinaryCleanupDone = true
			err = cleanup(t.Context())
			g.Expect(err == nil).To(Equal(tt.cleanupOK), "cleanup error: %v", err)
			target := scope.targets[0]
			g.Expect(target.released).To(Equal(tt.cleanupOK))
			g.Expect(target.submittedApplies).To(HaveLen(len(tt.clientOutcomes)))

			var patches []ktesting.PatchAction
			for _, action := range world.client.Actions() {
				if action.GetVerb() == "patch" && action.GetResource().Resource == "deployments" {
					patches = append(patches, action.(ktesting.PatchAction))
				}
			}
			g.Expect(patches).To(HaveLen(len(target.submittedApplies)))
			var diagnostics []map[string]interface{}
			for i, submitted := range target.submittedApplies {
				var actual struct {
					Metadata struct {
						UID             types.UID `json:"uid"`
						ResourceVersion string    `json:"resourceVersion"`
					} `json:"metadata"`
					Spec json.RawMessage `json:"spec"`
				}
				g.Expect(json.Unmarshal(patches[i].GetPatch(), &actual)).To(Succeed())
				g.Expect(submitted.uid).To(Equal(actual.Metadata.UID))
				g.Expect(submitted.resourceVersion).To(Equal(actual.Metadata.ResourceVersion))
				g.Expect(submitted.install).To(Equal(len(actual.Spec) != 0))
				g.Expect(submitted.outcome).To(Equal(tt.clientOutcomes[i]), "a later GET must not rewrite the client-observed outcome")
				if tt.cleanupOK && submitted.outcome != budgetPlacementApplyRejected {
					g.Expect(submitted.fencedAtResourceVersion).ToNot(BeEmpty())
					g.Expect(submitted.fencedAtResourceVersion).ToNot(Equal(submitted.resourceVersion))
				} else {
					g.Expect(submitted.fencedAtResourceVersion).To(BeEmpty())
				}
				diagnostics = append(diagnostics, map[string]interface{}{
					"uid": submitted.uid, "resourceVersion": submitted.resourceVersion, "install": submitted.install,
					"outcome": string(submitted.outcome), "fencedAtResourceVersion": submitted.fencedAtResourceVersion,
				})
			}
			g.Expect(scope.Diagnostics()["targets"].([]map[string]interface{})[0]["submittedApplies"]).To(Equal(diagnostics))
			g.Expect(world.forbidden).To(BeEmpty())
		})
	}
}

func TestReplacementBudgetPlacementEarlierUnknownApplySurvivesRejection(t *testing.T) {
	for _, mode := range []string{"no-op", "response-only"} {
		t.Run(mode, func(t *testing.T) {
			g := NewWithT(t)
			world := newPlacementTestWorld(t)
			world.mode = mode
			scope := world.scope()
			var cleanup func(context.Context) error
			g.Expect(scope.Activate(t.Context(), world.registrar(&cleanup))).ToNot(Succeed())
			target := scope.targets[0]
			// Exercise another actual submission at the same RV. A conclusive
			// rejection of this request cannot resolve the earlier unknown one.
			world.mode = "same-version-conflict"
			g.Expect(scope.mutateSelector(t.Context(), target, true)).ToNot(Succeed())
			g.Expect(world.requests).To(HaveLen(2))
			g.Expect(target.submittedApplies).To(HaveLen(2))
			g.Expect(target.submittedApplies[0].outcome).ToNot(Equal(budgetPlacementApplyRejected))
			g.Expect(target.submittedApplies[1].outcome).To(Equal(budgetPlacementApplyRejected))
			g.Expect(target.submittedApplies[0].resourceVersion).To(Equal(target.submittedApplies[1].resourceVersion))
			world.mode = "normal"
			world.ordinaryCleanupDone = true
			err := cleanup(t.Context())
			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(ContainSubstring("unresolved selector apply"))
			g.Expect(target.released).To(BeFalse())
			g.Expect(target.submittedApplies[0].fencedAtResourceVersion).To(BeEmpty())
			g.Expect(world.requests).To(HaveLen(2), "checking absence must not invent a write fence")
			g.Expect(world.forbidden).To(BeEmpty())
		})
	}
}

func TestReplacementBudgetPlacementUnknownReleaseRecovery(t *testing.T) {
	for _, mode := range []string{"persist-then-error", "same-version-conflict"} {
		t.Run(mode, func(t *testing.T) {
			g := NewWithT(t)
			world := newPlacementTestWorld(t)
			scope := world.scope()
			var cleanup func(context.Context) error
			g.Expect(scope.Activate(t.Context(), world.registrar(&cleanup))).To(Succeed())
			world.ordinaryCleanupDone = true
			world.mode = mode
			g.Expect(cleanup(t.Context())).ToNot(Succeed())
			target := scope.targets[0]
			g.Expect(target.confirmed).To(BeTrue())
			g.Expect(target.released).To(BeFalse())
			g.Expect(target.submittedApplies).To(HaveLen(2))
			g.Expect(target.submittedApplies[1].install).To(BeFalse())
			world.mode = "normal"
			if mode == "persist-then-error" {
				g.Expect(target.submittedApplies[1].outcome).To(Equal(budgetPlacementApplyUnknown))
				g.Expect(world.deployment(world.name).Spec.Template.Spec.NodeSelector).To(BeNil())
				g.Expect(cleanup(t.Context())).To(Succeed(), "an unknown omission may already have restored placement")
				g.Expect(target.released).To(BeTrue())
				g.Expect(target.submittedApplies[1].fencedAtResourceVersion).ToNot(BeEmpty())
				g.Expect(target.submittedApplies[1].outcome).To(Equal(budgetPlacementApplyUnknown), "fencing must not invent a response outcome")
			} else {
				g.Expect(target.submittedApplies[1].outcome).To(Equal(budgetPlacementApplyRejected))
				// Independent fixture actor, not a helper write or an SSA proof:
				// remove the selector after our omission was conclusively rejected.
				changed := world.deployment(world.name)
				changed.Spec.Template.Spec.NodeSelector = nil
				changed.ManagedFields = changed.ManagedFields[:1]
				changed.ResourceVersion = "3"
				changed.Generation++
				world.publishRollout(changed)
				err := cleanup(t.Context())
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("confirmed selector intent disappeared"))
				g.Expect(target.released).To(BeFalse(), "a rejected omission cannot excuse externally lost ownership")
			}
			g.Expect(world.requests).To(HaveLen(2), "recovery or discrepancy reporting must not send a no-op omission")
			g.Expect(world.forbidden).To(BeEmpty())
		})
	}
}

func TestReplacementBudgetPlacementApplyFenceRequirements(t *testing.T) {
	tests := []struct {
		name       string
		uid        types.UID
		rv         string
		outcome    budgetPlacementApplyOutcome
		noHistory  bool
		wantError  bool
		wantFenced bool
	}{
		{name: "missing history", noHistory: true, wantError: true},
		{name: "missing submitted UID", rv: "before", outcome: budgetPlacementApplyUnknown, wantError: true},
		{name: "missing submitted RV", uid: "controller-uid", outcome: budgetPlacementApplyUnknown, wantError: true},
		{name: "foreign submitted UID", uid: "replacement-uid", rv: "before", outcome: budgetPlacementApplyUnknown, wantError: true},
		{name: "unknown outcome kind", uid: "controller-uid", rv: "before", outcome: "invalid", wantError: true},
		{name: "unknown unchanged RV", uid: "controller-uid", rv: "current", outcome: budgetPlacementApplyUnknown, wantError: true},
		{name: "acknowledged unchanged RV", uid: "controller-uid", rv: "current", outcome: budgetPlacementApplyAcknowledged, wantError: true},
		{name: "rejected unchanged RV", uid: "controller-uid", rv: "current", outcome: budgetPlacementApplyRejected},
		{name: "unknown different opaque RV", uid: "controller-uid", rv: "before", outcome: budgetPlacementApplyUnknown, wantFenced: true},
		{name: "acknowledged different opaque RV", uid: "controller-uid", rv: "before", outcome: budgetPlacementApplyAcknowledged, wantFenced: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			current := placementTestDeployment("controller")
			current.ResourceVersion = "current"
			target := &budgetPlacementTarget{key: types.NamespacedName{Namespace: current.Namespace, Name: current.Name}, uid: current.UID, attempted: true}
			if !tt.noHistory {
				target.submittedApplies = []budgetPlacementApply{{uid: tt.uid, resourceVersion: tt.rv, install: true, outcome: tt.outcome}}
			}
			err := target.resolveSubmittedApplies(current)
			g.Expect(err != nil).To(Equal(tt.wantError), "resolution error: %v", err)
			g.Expect(target.released).To(BeFalse(), "request fencing alone cannot certify restored intent and healthy Pods")
			if !tt.noHistory {
				g.Expect(target.submittedApplies[0].outcome).To(Equal(tt.outcome))
				g.Expect(target.submittedApplies[0].fencedAtResourceVersion != "").To(Equal(tt.wantFenced))
			}
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

// The no-op and response-only release controls also never persist in this fake.
// They check readback, not the outcome of an outstanding asynchronous request.
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

// This fault is at the submitted-request boundary: the actual helper receives
// cancellation, but its captured apply can still reach real API-server storage.
// Channels order completion after Restore returns, not sleeps or synthetic CAS.
// This does not induce an API-server timeout or run a Deployment controller.
func TestReplacementBudgetPlacementLateApply(t *testing.T) {
	kube := newPlacementImplementationAPI(t)
	tests := []struct {
		name string
		run  func(*testing.T, *placementLateApplyFixture, placementLateApplyRequest)
	}{
		{name: "unchanged resourceVersion remains recoverable", run: testPlacementLateApplyUnchangedVersion},
		{name: "advanced resourceVersion fences pending apply", run: testPlacementLateApplyAdvancedVersion},
		{name: "replacement UID is never adopted", run: testPlacementLateApplyReplacement},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPlacementLateApplyFixture(t, kube)
			request := fixture.submit()
			tt.run(t, fixture, request)
		})
	}
}

func testPlacementLateApplyUnchangedVersion(t *testing.T, f *placementLateApplyFixture, request placementLateApplyRequest) {
	firstErr := f.restoreBeforeCompletion()
	firstReleased := f.target.released
	f.requireCleanupObserved(f.baseline)
	if len(f.requests) != 1 || !reflect.DeepEqual(f.get(), f.baseline) {
		t.Fatal("late-apply prerequisite: cleanup must leave the absent selector and submitted UID/RV unchanged while apply is held")
	}

	result := f.complete()
	if result.err != nil || result.deployment == nil {
		t.Fatalf("late-apply prerequisite: the unchanged-UID/RV apply must actually commit after cleanup returns: %v", result.err)
	}
	stored := f.get()
	if stored.UID != request.uid || stored.ResourceVersion == request.resourceVersion || stored.Generation <= f.baseline.Generation {
		t.Fatalf("late-apply prerequisite: storage did not record the delayed template change: uid=%s rv=%s generation=%d", stored.UID, stored.ResourceVersion, stored.Generation)
	}
	if !reflect.DeepEqual(stored, result.deployment) {
		t.Fatal("late-apply prerequisite: the successful response must equal a fresh persisted GET")
	}
	assertSSAContractSelector(t, f.baseline, stored, f.scope.manager)
	t.Logf("late-apply storage proved: uid=%s submittedRV=%s committedRV=%s; first Restore error=%v released=%t", request.uid, request.resourceVersion, stored.ResourceVersion, firstErr, firstReleased)

	// These assertions deliberately do not stop the test: the same target must
	// also demonstrate whether a later Restore can recover the real stored field.
	if firstErr == nil {
		t.Error("Restore returned success while a submitted apply could still commit against the unchanged UID/resourceVersion")
	}
	if firstReleased {
		t.Error("Restore marked an unresolved apply released from an unchanged-resourceVersion absence observation")
	}
	if err := f.restore(); err != nil {
		t.Errorf("subsequent Restore must recover and relinquish the late-installed selector: %v", err)
	}
	restored := f.get()
	if err := f.scope.validateRestored(restored); err != nil {
		t.Errorf("subsequent Restore did not actually remove the late-installed selector and its ownership from storage: %v", err)
	}
	if restored.UID != request.uid || restored.ResourceVersion == stored.ResourceVersion {
		t.Errorf("subsequent Restore must persist omission on the original UID, not merely validate: uid=%s rv=%s", restored.UID, restored.ResourceVersion)
	}
	if len(f.requests) != 2 || f.requests[1].install {
		t.Errorf("subsequent Restore must submit one real omission-only apply; got %d helper requests", len(f.requests))
	}
	if !f.target.released {
		t.Error("subsequent Restore must confirm release after removing the persisted intent")
	}
	pod, err := f.kube.CoreV1().Pods(f.originalPod.Namespace).Get(f.ctx, f.originalPod.Name, metav1.GetOptions{})
	if err != nil || pod == nil || pod.UID != f.originalPod.UID {
		t.Fatalf("late-apply prerequisite: the original healthy Pod must remain available, not be replaced by the fixture: %v", err)
	}
}

func testPlacementLateApplyAdvancedVersion(t *testing.T, f *placementLateApplyFixture, request placementLateApplyRequest) {
	// This is an independent owner update, not a helper write invented as a fence.
	// The owner continues to omit the independently owned selector from its intent.
	changedOwner := f.owner.DeepCopy()
	changedOwner.Annotations["example.com/revision"] = "intervening-owner-update"
	changed, err := ssaContractApply(t, f.ctx, f.kube.AppsV1().Deployments(f.owner.Namespace), f.owner.Name, changedOwner, metav1.PatchOptions{FieldManager: "deployment-owner"})
	if err != nil {
		t.Fatalf("late-apply prerequisite: apply the independent owner update: %v", err)
	}
	if changed == nil || changed.UID != request.uid || changed.ResourceVersion == request.resourceVersion {
		t.Fatal("late-apply prerequisite: the independent owner update, not status setup, must already advance RV on the same UID")
	}
	if changed.Annotations["example.com/revision"] != "intervening-owner-update" || !reflect.DeepEqual(f.get(), changed) {
		t.Fatal("late-apply prerequisite: the owner change must actually persist before health publication")
	}
	current := f.publishAvailable(changed)
	if current.UID != request.uid || current.ResourceVersion == request.resourceVersion {
		t.Fatal("late-apply prerequisite: the owner update must advance the stored RV without replacing the UID")
	}
	if err := f.scope.validateRestored(current); err != nil {
		t.Fatalf("late-apply prerequisite: the advanced-RV object must still have no selector or scope ownership: %v", err)
	}
	if _, err := f.scope.rolloutReady(f.ctx, current, f.target, "restored"); err != nil {
		t.Fatalf("late-apply prerequisite: the advanced-RV control must have healthy original placement: %v", err)
	}
	firstErr := f.restoreBeforeCompletion()
	f.requireCleanupObserved(current)
	result := f.complete()
	if !apierrors.IsConflict(result.err) || !strings.Contains(result.err.Error(), "the object has been modified") {
		t.Fatalf("late-apply prerequisite: the real API server must reject the stale RV as a storage conflict, not a mock/setup error: %v", result.err)
	}
	if !reflect.DeepEqual(f.get(), current) {
		t.Fatal("late-apply prerequisite: the rejected stale-RV request changed real storage")
	}
	t.Logf("late-apply RV fence proved: uid=%s submittedRV=%s observedRV=%s rejection=%v", request.uid, request.resourceVersion, current.ResourceVersion, result.err)
	if firstErr != nil || !f.target.released {
		t.Errorf("Restore must accept absent intent once an observed new RV fences the pending apply: error=%v released=%t", firstErr, f.target.released)
	}
	if err := f.restore(); err != nil {
		t.Errorf("Restore after a real stale-RV rejection must confirm absence: %v", err)
	}
	if len(f.requests) != 1 || !reflect.DeepEqual(f.get(), current) {
		t.Error("the fenced absent selector requires no helper mutation, and the owner's update must be preserved")
	}
}

func testPlacementLateApplyReplacement(t *testing.T, f *placementLateApplyFixture, request placementLateApplyRequest) {
	deployments := f.kube.AppsV1().Deployments(f.owner.Namespace)
	if err := deployments.Delete(f.ctx, f.owner.Name, metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{UID: &request.uid}, PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
	}); err != nil {
		t.Fatalf("late-apply prerequisite: delete only the fixture's original Deployment: %v", err)
	}
	if _, err := deployments.Get(f.ctx, f.owner.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("late-apply prerequisite: the original Deployment must actually be absent before replacement: %v", err)
	}
	replacement, err := ssaContractApply(t, f.ctx, deployments, f.owner.Name, f.owner, metav1.PatchOptions{FieldManager: "deployment-owner"})
	if err != nil || replacement == nil || replacement.UID == "" || replacement.UID == request.uid {
		t.Fatalf("late-apply prerequisite: create a same-name Deployment with a genuinely new UID: %v", err)
	}
	firstErr := f.restoreBeforeCompletion()
	f.requireCleanupObserved(replacement)
	result := f.complete()
	if !apierrors.IsInvalid(result.err) && !apierrors.IsConflict(result.err) {
		t.Fatalf("late-apply prerequisite: the original submitted request must be rejected against the replacement: %v", result.err)
	}
	if !reflect.DeepEqual(f.get(), replacement) {
		t.Fatal("late-apply prerequisite: the original pending apply changed the replacement in storage")
	}

	// Isolate UID rejection from stale-RV rejection with a separate setup-actor
	// probe. Do not retarget or rewrite the helper's captured pending request.
	var uidProbe map[string]interface{}
	if err := json.Unmarshal(request.data, &uidProbe); err != nil {
		t.Fatalf("late-apply prerequisite: decode the independent UID probe: %v", err)
	}
	metadata := uidProbe["metadata"].(map[string]interface{})
	metadata["resourceVersion"] = replacement.ResourceVersion
	if metadata["uid"] != string(request.uid) {
		t.Fatal("late-apply prerequisite: the UID probe must retain the originally submitted UID")
	}
	data, err := json.Marshal(uidProbe)
	if err != nil {
		t.Fatalf("late-apply prerequisite: encode the independent UID probe: %v", err)
	}
	_, err = deployments.Patch(f.ctx, f.owner.Name, types.ApplyPatchType, data, request.options)
	if (!apierrors.IsInvalid(err) && !apierrors.IsConflict(err)) || !strings.Contains(strings.ToLower(err.Error()), "uid") {
		t.Fatalf("late-apply prerequisite: real UID rejection must survive a fresh replacement RV: %v", err)
	}
	if !reflect.DeepEqual(f.get(), replacement) {
		t.Fatal("late-apply prerequisite: the rejected UID-only probe changed the replacement")
	}
	t.Logf("late-apply UID fence proved: originalUID=%s replacementUID=%s freshRV=%s rejection=%v", request.uid, replacement.UID, replacement.ResourceVersion, err)
	if firstErr == nil || f.target.released || f.target.uid != request.uid {
		t.Errorf("Restore must refuse, not release or adopt, a replacement UID: error=%v released=%t targetUID=%s", firstErr, f.target.released, f.target.uid)
	}
	if err := f.restore(); err == nil {
		t.Error("repeated Restore must continue to refuse the replacement UID")
	}
	if len(f.requests) != 1 || !reflect.DeepEqual(f.get(), replacement) {
		t.Error("neither Restore may mutate or adopt the same-name replacement")
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

type placementLateApplyRequest struct {
	install         bool
	uid             types.UID
	resourceVersion string
	data            []byte
	options         metav1.PatchOptions
}

type placementLateApplyResult struct {
	deployment *appsv1.Deployment
	err        error
}

// The client-go fake is only a request adapter here: its tracker is empty and
// never answers a request. Every read and completed write uses real API storage.
// Only the first apply is held, and its bytes survive the caller's cancellation.
// The setup actor publishes health because envtest has no workload controllers;
// it never changes the selector or advances the Deployment RV during the hold.
// The advanced-RV test explicitly drives a separate owner update as its control.
type placementLateApplyFixture struct {
	t                    *testing.T
	ctx                  context.Context
	kube                 kubernetes.Interface
	owner                *appsv1.Deployment
	baseline             *appsv1.Deployment
	originalPod          *corev1.Pod
	scope                *replacementBudgetPlacement
	target               *budgetPlacementTarget
	cancelActivation     context.CancelFunc
	submitted            chan placementLateApplyRequest
	release              chan struct{}
	completed            chan placementLateApplyResult
	firstRestoreReturned chan struct{}
	observeCleanup       bool
	cleanupReads         []*appsv1.Deployment
	requests             []placementLateApplyRequest
}

func newPlacementLateApplyFixture(t *testing.T, kube kubernetes.Interface) *placementLateApplyFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	owner := ssaContractOwner()
	owner.Spec.Replicas = ptr.To[int32](1)
	owner.Labels["app"] = owner.Name
	owner.Spec.Selector.MatchLabels["app"] = owner.Name
	owner.Spec.Template.Labels["app"] = owner.Name
	owner.Spec.Template.Spec.ServiceAccountName = owner.Name
	owner.Spec.Template.Spec.AutomountServiceAccountToken = ptr.To(false)
	f := &placementLateApplyFixture{
		t: t, ctx: ctx, kube: kube, owner: owner,
		submitted: make(chan placementLateApplyRequest, 1), release: make(chan struct{}),
		completed: make(chan placementLateApplyResult, 1), firstRestoreReturned: make(chan struct{}),
	}
	created, err := ssaContractApply(t, ctx, kube.AppsV1().Deployments(owner.Namespace), owner.Name, owner, metav1.PatchOptions{FieldManager: "deployment-owner"})
	if err != nil || created == nil || created.UID == "" || created.ResourceVersion == "" {
		t.Fatalf("late-apply prerequisite: create the SSA-owned Deployment with server-assigned UID/RV: %v", err)
	}
	if _, err := kube.CoreV1().ServiceAccounts(owner.Namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: owner.Name, Namespace: owner.Namespace}, AutomountServiceAccountToken: ptr.To(false),
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("late-apply prerequisite: create the fixture Pod's ServiceAccount: %v", err)
	}
	node, err := kube.CoreV1().Nodes().Create(ctx, &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: owner.Name + "-node", Labels: map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}},
	}, metav1.CreateOptions{})
	if err != nil || node == nil || node.UID == "" || node.ResourceVersion == "" {
		t.Fatalf("late-apply prerequisite: create the fixture Node with server-assigned UID/RV: %v", err)
	}
	if _, err := kube.CoreV1().Nodes().Patch(ctx, node.Name, types.MergePatchType, placementLateApplyStatusPatch(t, node, corev1.NodeStatus{
		Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
	}), metav1.PatchOptions{}, "status"); err != nil {
		t.Fatalf("late-apply prerequisite: publish the fixture Node's health: %v", err)
	}
	rs, err := kube.AppsV1().ReplicaSets(owner.Namespace).Create(ctx, &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: owner.Name + "-rs", Namespace: owner.Namespace,
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: created.Name, UID: created.UID, Controller: ptr.To(true)}},
		},
		Spec: appsv1.ReplicaSetSpec{Replicas: ptr.To[int32](1), Selector: created.Spec.Selector.DeepCopy(), Template: *created.Spec.Template.DeepCopy()},
	}, metav1.CreateOptions{})
	if err != nil || rs == nil || rs.UID == "" || rs.ResourceVersion == "" {
		t.Fatalf("late-apply prerequisite: create the original ReplicaSet with its real Deployment UID: %v", err)
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: owner.Name + "-pod", Namespace: owner.Namespace, Labels: maps.Clone(created.Spec.Template.Labels),
			Annotations:     maps.Clone(created.Spec.Template.Annotations),
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: rs.Name, UID: rs.UID, Controller: ptr.To(true)}},
		},
		Spec: *created.Spec.Template.Spec.DeepCopy(),
	}
	pod.Spec.NodeName = node.Name
	pod, err = kube.CoreV1().Pods(owner.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil || pod == nil || pod.UID == "" || pod.ResourceVersion == "" {
		t.Fatalf("late-apply prerequisite: create the original bound Pod with its real ReplicaSet UID: %v", err)
	}
	var containerStatuses []corev1.ContainerStatus
	for _, container := range pod.Spec.Containers {
		containerStatuses = append(containerStatuses, corev1.ContainerStatus{
			Name: container.Name, Image: container.Image, ImageID: "fixture-image", Ready: true,
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		})
	}
	f.originalPod, err = kube.CoreV1().Pods(owner.Namespace).Patch(ctx, pod.Name, types.MergePatchType, placementLateApplyStatusPatch(t, pod, corev1.PodStatus{
		Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}, ContainerStatuses: containerStatuses,
	}), metav1.PatchOptions{}, "status")
	if err != nil || f.originalPod == nil || f.originalPod.UID != pod.UID {
		t.Fatalf("late-apply prerequisite: publish the original Pod's health without replacing its UID: %v", err)
	}
	f.baseline = f.publishAvailable(created)
	adapter := kubefake.NewSimpleClientset()
	adapter.PrependReactor("*", "*", f.react)
	f.scope = newReplacementBudgetPlacement(adapter, placementTestPool())
	f.scope.timeout = time.Minute
	f.target = &budgetPlacementTarget{
		key: types.NamespacedName{Namespace: created.Namespace, Name: created.Name}, uid: created.UID, writeSelector: true,
	}
	f.scope.targets = []*budgetPlacementTarget{f.target}
	// Use the actual baseline and mutation entry points, as in the implementation
	// SSA tests above. Real UID-linked original Pods make a successful first
	// Restore possible; an empty inventory must not manufacture the regression.
	if err := f.scope.captureBaseline(ctx, f.target); err != nil {
		t.Fatalf("late-apply prerequisite: actual helper must capture healthy original placement: %v", err)
	}
	if f.target.beforeActivation.Len() != 1 || !f.target.beforeActivation.Has(f.originalPod.UID) || f.target.baselinePodSelector != nil {
		t.Fatal("late-apply prerequisite: capture exactly the healthy original Pod with absent selector")
	}
	return f
}

func placementLateApplyStatusPatch(t *testing.T, object metav1.Object, status interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{"uid": object.GetUID(), "resourceVersion": object.GetResourceVersion()}, "status": status,
	})
	if err != nil {
		t.Fatalf("late-apply prerequisite: encode the setup actor's status patch: %v", err)
	}
	return data
}

func (f *placementLateApplyFixture) publishAvailable(deployment *appsv1.Deployment) *appsv1.Deployment {
	f.t.Helper()
	// Setup/controller observation only: before submission, after the independent
	// owner update, or after a real omission has already removed the selector.
	// Never run on a cleanup GET or use a status write to fence the pending apply.
	status := appsv1.DeploymentStatus{ObservedGeneration: deployment.Generation, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}
	after, err := f.kube.AppsV1().Deployments(deployment.Namespace).Patch(f.ctx, deployment.Name, types.MergePatchType,
		placementLateApplyStatusPatch(f.t, deployment, status), metav1.PatchOptions{}, "status")
	if err != nil || after == nil {
		f.t.Fatalf("late-apply prerequisite: publish controlled Deployment health: %v", err)
	}
	if after.UID != deployment.UID || after.Generation != deployment.Generation || !reflect.DeepEqual(after.Spec, deployment.Spec) {
		f.t.Fatal("late-apply prerequisite: the setup status update must not alter identity or template")
	}
	return f.get()
}

func (f *placementLateApplyFixture) get() *appsv1.Deployment {
	f.t.Helper()
	current, err := f.kube.AppsV1().Deployments(f.owner.Namespace).Get(f.ctx, f.owner.Name, metav1.GetOptions{})
	if err != nil || current == nil || current.UID == "" || current.ResourceVersion == "" {
		f.t.Fatalf("late-apply prerequisite: read actual fixture storage with server-assigned UID/RV: %v", err)
	}
	return current
}

func (f *placementLateApplyFixture) react(action ktesting.Action) (bool, runtime.Object, error) {
	f.t.Helper()
	if action.GetSubresource() != "" {
		f.t.Fatalf("late-apply prerequisite: helper must not access subresource %q", action.GetSubresource())
	}
	switch action.GetVerb() + " " + action.GetResource().Resource {
	case "get deployments":
		current, err := f.kube.AppsV1().Deployments(action.GetNamespace()).Get(f.ctx, action.(ktesting.GetAction).GetName(), metav1.GetOptions{})
		if f.observeCleanup && err == nil && current != nil {
			f.cleanupReads = append(f.cleanupReads, current.DeepCopy())
		}
		return true, current, err
	case "list replicasets":
		current, err := f.kube.AppsV1().ReplicaSets(action.GetNamespace()).List(f.ctx, metav1.ListOptions{})
		return true, current, err
	case "list pods":
		current, err := f.kube.CoreV1().Pods(action.GetNamespace()).List(f.ctx, metav1.ListOptions{})
		return true, current, err
	case "get nodes":
		current, err := f.kube.CoreV1().Nodes().Get(f.ctx, action.(ktesting.GetAction).GetName(), metav1.GetOptions{})
		return true, current, err
	case "patch deployments":
		return f.patch(action)
	default:
		f.t.Fatalf("late-apply prerequisite: forbidden helper action %s %s; only reads and minimal selector apply are allowed", action.GetVerb(), action.GetResource().Resource)
		return true, nil, errors.New("forbidden helper action")
	}
}

func (f *placementLateApplyFixture) patch(action ktesting.Action) (bool, runtime.Object, error) {
	f.t.Helper()
	patch := action.(ktesting.PatchAction)
	options := action.(interface{ GetPatchOptions() metav1.PatchOptions }).GetPatchOptions()
	if patch.GetPatchType() != types.ApplyPatchType || !reflect.DeepEqual(options, metav1.PatchOptions{FieldManager: f.scope.manager}) {
		f.t.Fatal("late-apply prerequisite: helper must use unforced, non-dry-run apply with its own manager")
	}
	before := f.get()
	if action.GetNamespace() != before.Namespace || patch.GetName() != before.Name {
		f.t.Fatal("late-apply prerequisite: helper requested a different fixture identity")
	}
	var intent map[string]interface{}
	if err := json.Unmarshal(patch.GetPatch(), &intent); err != nil {
		f.t.Fatalf("late-apply prerequisite: decode actual submitted helper intent: %v", err)
	}
	_, install := intent["spec"]
	expected := map[string]interface{}{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]interface{}{
		"name": before.Name, "namespace": before.Namespace, "uid": string(before.UID), "resourceVersion": before.ResourceVersion,
	}}
	if install {
		expected["spec"] = map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{"nodeSelector": map[string]interface{}{v1beta1.AKSLabelMode: v1beta1.ModeSystem}}}}
	}
	if !reflect.DeepEqual(intent, expected) || before.UID == "" || before.ResourceVersion == "" {
		f.t.Fatalf("late-apply prerequisite: actual helper payload must contain exact UID/RV and only selector intent (or omission), got %s", patch.GetPatch())
	}
	metadata := intent["metadata"].(map[string]interface{})
	request := placementLateApplyRequest{
		install: install, uid: types.UID(metadata["uid"].(string)), resourceVersion: metadata["resourceVersion"].(string),
		data: append([]byte(nil), patch.GetPatch()...), options: options,
	}
	f.requests = append(f.requests, request)
	if install {
		if len(f.requests) != 1 || f.cancelActivation == nil {
			f.t.Fatal("late-apply prerequisite: only the initial submitted apply may be delayed")
		}
		f.submitted <- request
		go func() {
			select {
			case <-f.release:
				// The persistence side has its own lifetime, not the canceled
				// caller context. Replay exact bytes, with real server UID/RV CAS.
				after, err := f.kube.AppsV1().Deployments(before.Namespace).Patch(f.ctx, before.Name, types.ApplyPatchType, request.data, request.options)
				f.completed <- placementLateApplyResult{deployment: after, err: err}
			case <-f.ctx.Done():
				f.completed <- placementLateApplyResult{err: f.ctx.Err()}
			}
		}()
		f.cancelActivation()
		return true, nil, context.Canceled
	}
	after, err := f.kube.AppsV1().Deployments(before.Namespace).Patch(f.ctx, before.Name, types.ApplyPatchType, request.data, request.options)
	if err == nil {
		if after == nil || after.Spec.Template.Spec.NodeSelector != nil {
			f.t.Fatal("late-apply prerequisite: real omission must remove the selector before publishing restored health")
		}
		// Original Pods never rolled during the hold. Publish their observed
		// health only after the helper's real omission has persisted.
		f.publishAvailable(after)
	}
	return true, after, err
}

func (f *placementLateApplyFixture) submit() placementLateApplyRequest {
	f.t.Helper()
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	f.cancelActivation = cancel
	err := f.scope.mutateSelector(ctx, f.target, true)
	if !errors.Is(err, context.Canceled) || ctx.Err() != context.Canceled {
		f.t.Fatalf("late-apply prerequisite: the actual mutation helper must receive cancellation after submission: %v", err)
	}
	if !f.target.attempted || f.target.confirmed || f.target.released || len(f.requests) != 1 {
		f.t.Fatal("late-apply prerequisite: retain one attempted but unconfirmed, unreleased target")
	}
	select {
	case request := <-f.submitted:
		if request.uid != f.baseline.UID || request.resourceVersion != f.baseline.ResourceVersion || !reflect.DeepEqual(f.get(), f.baseline) {
			f.t.Fatal("late-apply prerequisite: submitted UID/RV must match unchanged storage with no persisted apply")
		}
		return request
	case <-f.ctx.Done():
		f.t.Fatalf("late-apply deadlock guard: no submitted request: %v", f.ctx.Err())
		return placementLateApplyRequest{}
	}
}

func (f *placementLateApplyFixture) restore() error {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, time.Minute)
	defer cancel()
	err := f.scope.Restore(ctx)
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		f.t.Fatalf("late-apply deadlock guard: Restore must finish from observations, not a guard timeout: %v", err)
	}
	return err
}

func (f *placementLateApplyFixture) restoreBeforeCompletion() error {
	f.t.Helper()
	f.observeCleanup = true
	err := f.restore()
	f.observeCleanup = false
	close(f.firstRestoreReturned)
	return err
}

func (f *placementLateApplyFixture) requireCleanupObserved(expected *appsv1.Deployment) {
	f.t.Helper()
	if len(f.cleanupReads) == 0 {
		f.t.Fatal("late-apply prerequisite: actual Restore must read real storage before delayed completion")
	}
	for _, observed := range f.cleanupReads {
		if !reflect.DeepEqual(observed, expected) {
			f.t.Fatalf("late-apply prerequisite: cleanup observed unexpected storage: uid=%s rv=%s", observed.UID, observed.ResourceVersion)
		}
		if err := f.scope.validateRestored(observed); err != nil {
			f.t.Fatalf("late-apply prerequisite: every pre-completion cleanup read must observe absent/unowned selector: %v", err)
		}
	}
}

func (f *placementLateApplyFixture) complete() placementLateApplyResult {
	f.t.Helper()
	select {
	case <-f.firstRestoreReturned:
	default:
		f.t.Fatal("late-apply prerequisite: completion must not be released until the first Restore returns")
	}
	if len(f.cleanupReads) == 0 {
		f.t.Fatal("late-apply prerequisite: completion requires a recorded real cleanup GET")
	}
	close(f.release)
	select {
	case result := <-f.completed:
		if errors.Is(result.err, context.Canceled) || errors.Is(result.err, context.DeadlineExceeded) {
			f.t.Fatalf("late-apply deadlock guard: persistence did not complete independently of caller cancellation: %v", result.err)
		}
		return result
	case <-f.ctx.Done():
		f.t.Fatalf("late-apply deadlock guard: pending apply did not finish: %v", f.ctx.Err())
		return placementLateApplyResult{}
	}
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
