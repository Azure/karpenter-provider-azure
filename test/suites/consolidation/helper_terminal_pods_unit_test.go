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
	"errors"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"
)

func TestTerminalPodCleanupRemovesOwnedTerminalPods(t *testing.T) {
	for _, phase := range []corev1.PodPhase{corev1.PodFailed, corev1.PodSucceeded} {
		t.Run(string(phase), func(t *testing.T) {
			g := NewWithT(t)
			world := newTerminalPodTestWorld(t)
			pod := world.pod("terminal")
			pod.Status.Phase = phase
			world.put(pod)
			deployment, replicaSet := world.deployment(), world.replicaSet()

			g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, world.record)).To(Succeed())

			g.Expect(world.podExists("terminal")).To(BeFalse(), "owned terminal Pod objects must not remain in the utilization population")
			g.Expect(world.deployment()).To(Equal(deployment))
			g.Expect(world.replicaSet()).To(Equal(replicaSet))
			g.Expect(world.forbiddenWrites).To(BeEmpty())
		})
	}
}

func TestTerminalPodCleanupRequestMean(t *testing.T) {
	g := NewWithT(t)
	world := newTerminalPodTestWorld(t)
	world.removeFixturePod("terminal")
	// Synthetic requests reproduce an unweighted mean, not a new threshold:
	// (27960/7820 + 1420/1900) / 8 = 160571/297160 > 0.5.
	// Removing six terminal 1-CPU Pods alone gives 132071/297160 < 0.5.
	running := []int{6, 0, 5, 1, 1, 5, 2, 0}
	for i, count := range running {
		name, allocatable := fmt.Sprintf("node-%d", i), "7820m"
		if i == 4 {
			allocatable = "1900m"
		}
		world.put(&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name), ResourceVersion: "1", Labels: map[string]string{karpv1.NodePoolLabelKey: "fixture"}},
			Status:     corev1.NodeStatus{Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(allocatable)}},
		})
		for j := 0; j < count; j++ {
			world.put(world.newPod(fmt.Sprintf("running-%d-%d", i, j), name, corev1.PodRunning, "1"))
		}
		// One synthetic DaemonSet Pod represents 420m of effective node overhead.
		daemon := world.newPod(fmt.Sprintf("agent-%d", i), name, corev1.PodRunning, "420m")
		daemon.Namespace = "kube-system"
		daemon.OwnerReferences = []metav1.OwnerReference{terminalPodTestController("DaemonSet", "agent", "daemonset-agent")}
		world.put(daemon)
	}
	for i := 0; i < 6; i++ {
		world.put(world.newPod(fmt.Sprintf("failed-%d", i), "node-3", corev1.PodFailed, "1"))
	}
	addon := world.newPod("addon", "node-1", corev1.PodRunning, "20m")
	addon.Namespace = "kube-system"
	addon.OwnerReferences = []metav1.OwnerReference{terminalPodTestController("ReplicaSet", "addon", "replicaset-addon")}
	world.put(addon)
	beforeNonTerminal := world.nonTerminalPods()
	beforeNodes := world.nodes()
	g.Expect(world.cpuRequests()).To(Equal(map[string]int64{
		"node-0": 6420, "node-1": 440, "node-2": 5420, "node-3": 7420,
		"node-4": 1420, "node-5": 5420, "node-6": 2420, "node-7": 420,
	}))
	g.Expect(world.averageCPU()).To(BeNumerically(">", 0.5))

	g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, world.record)).To(Succeed())

	g.Expect(world.averageCPU()).To(BeNumerically("<", 0.5), "six retained terminal requests must be removed without changing the mean or its threshold")
	g.Expect(world.cpuRequests()).To(Equal(map[string]int64{
		"node-0": 6420, "node-1": 440, "node-2": 5420, "node-3": 1420,
		"node-4": 1420, "node-5": 5420, "node-6": 2420, "node-7": 420,
	}))
	g.Expect(world.nonTerminalPods()).To(Equal(beforeNonTerminal), "all twenty application Pods, DaemonSets and addon requests must be unchanged")
	g.Expect(world.nodes()).To(Equal(beforeNodes), "cohort membership and allocatable denominators must be unchanged")
	g.Expect(world.deletes).To(HaveLen(6))
	g.Expect(world.forbiddenWrites).To(BeEmpty())
}

func TestTerminalPodCleanupEvidenceBeforeDelete(t *testing.T) {
	g := NewWithT(t)
	world := newTerminalPodTestWorld(t)
	original := world.pod("terminal")
	original.Status.Reason = "SyntheticFailure"
	original.Status.Message = "synthetic terminal status must survive deletion"
	original.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "pause", State: corev1.ContainerState{
		Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "SyntheticExit", Message: "container failure evidence"},
	}}}
	world.put(original)

	g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, world.record)).To(Succeed())

	g.Expect(world.evidence).To(HaveLen(1), "preserve failure evidence before removing the only copy of a terminal Pod")
	g.Expect(world.evidence[0]).To(Equal(original))
	g.Expect(world.events).To(Equal([]string{"record:pod-terminal:10", "delete:pod-terminal:10"}))
	g.Expect(world.podExists("terminal")).To(BeFalse())
}

func TestTerminalPodCleanupDeletePreconditions(t *testing.T) {
	g := NewWithT(t)
	world := newTerminalPodTestWorld(t)
	original := world.pod("terminal")

	g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, world.record)).To(Succeed())

	g.Expect(world.deletes).To(HaveLen(1), "observe the actual API delete, not only an ownership predicate")
	g.Expect(world.deletes[0].key).To(Equal(types.NamespacedName{Namespace: original.Namespace, Name: original.Name}))
	g.Expect(world.deletes[0].options.Preconditions).To(Equal(&metav1.Preconditions{
		UID: ptr.To(original.UID), ResourceVersion: ptr.To(original.ResourceVersion),
	}))
	g.Expect(world.podExists("terminal")).To(BeFalse())
}

func TestTerminalPodCleanupKeepsNonTargets(t *testing.T) {
	tests := []struct {
		name   string
		change func(*terminalPodTestWorld)
	}{
		{name: "running", change: func(w *terminalPodTestWorld) { p := w.pod("terminal"); p.Status.Phase = corev1.PodRunning; w.put(p) }},
		{name: "pending", change: func(w *terminalPodTestWorld) { p := w.pod("terminal"); p.Status.Phase = corev1.PodPending; w.put(p) }},
		{name: "unknown", change: func(w *terminalPodTestWorld) { p := w.pod("terminal"); p.Status.Phase = corev1.PodUnknown; w.put(p) }},
		{name: "terminating_running", change: func(w *terminalPodTestWorld) {
			p := w.pod("terminal")
			p.Status.Phase = corev1.PodRunning
			p.DeletionTimestamp = ptr.To(metav1.NewTime(time.Unix(1700000000, 0)))
			p.Finalizers = []string{"example.com/retain"}
			w.put(p)
		}},
		{name: "foreign_deployment_same_labels", change: func(w *terminalPodTestWorld) {
			d := w.deployment()
			d.Name = "foreign"
			d.UID = "deployment-foreign"
			w.put(d)
			rs := w.replicaSet()
			rs.Name = "foreign-rs"
			rs.UID = "replicaset-foreign"
			rs.OwnerReferences = []metav1.OwnerReference{terminalPodTestController("Deployment", d.Name, d.UID)}
			w.put(rs)
			p := w.pod("terminal")
			p.OwnerReferences = []metav1.OwnerReference{terminalPodTestController("ReplicaSet", rs.Name, rs.UID)}
			w.put(p)
		}},
		{name: "foreign_deployment_same_name_different_uid", change: func(w *terminalPodTestWorld) {
			rs := w.replicaSet()
			rs.OwnerReferences[0].UID = "deployment-previous-incarnation"
			w.put(rs)
		}},
		{name: "foreign_namespace", change: func(w *terminalPodTestWorld) {
			p := w.pod("terminal")
			w.removeFixturePod(p.Name)
			p.Namespace = "other"
			w.put(p)
		}},
		{name: "daemonset_pod", change: func(w *terminalPodTestWorld) {
			p := w.pod("terminal")
			p.OwnerReferences = []metav1.OwnerReference{terminalPodTestController("DaemonSet", "agent", "daemonset-agent")}
			w.put(p)
		}},
		{name: "no_pods", change: func(w *terminalPodTestWorld) { w.removeFixturePod("terminal") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			world := newTerminalPodTestWorld(t)
			tt.change(world)
			before := world.pods()

			g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, world.record)).To(Succeed())

			g.Expect(world.pods()).To(Equal(before))
			g.Expect(world.deletes).To(BeEmpty())
			g.Expect(world.evidence).To(BeEmpty())
			g.Expect(world.forbiddenWrites).To(BeEmpty())
		})
	}
}

func TestTerminalPodCleanupRejectsIdentityMismatch(t *testing.T) {
	tests := []struct {
		name   string
		change func(*terminalPodTestWorld, *terminalPodCleanupTarget)
	}{
		{name: "target_namespace_missing", change: func(_ *terminalPodTestWorld, target *terminalPodCleanupTarget) { target.key.Namespace = "" }},
		{name: "target_name_missing", change: func(_ *terminalPodTestWorld, target *terminalPodCleanupTarget) { target.key.Name = "" }},
		{name: "target_uid_missing", change: func(_ *terminalPodTestWorld, target *terminalPodCleanupTarget) { target.uid = "" }},
		{name: "deployment_uid_missing", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) { d := w.deployment(); d.UID = ""; w.put(d) }},
		{name: "deployment_same_name_new_uid", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			d := w.deployment()
			d.UID = "deployment-replacement"
			w.put(d)
		}},
		{name: "deployment_get_wrong_namespace", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			d := w.deployment()
			d.Namespace = "other"
			w.replyGet("deployments", d)
		}},
		{name: "deployment_get_wrong_name", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			d := w.deployment()
			d.Name = "other"
			w.replyGet("deployments", d)
		}},
		{name: "replicaset_uid_missing", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			rs := w.replicaSet()
			rs.UID = ""
			w.put(rs)
		}},
		{name: "replicaset_same_name_new_uid", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			rs := w.replicaSet()
			rs.UID = "replicaset-replacement"
			w.put(rs)
		}},
		{name: "replicaset_get_wrong_namespace", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			rs := w.replicaSet()
			rs.Namespace = "other"
			w.replyGet("replicasets", rs)
		}},
		{name: "replicaset_get_wrong_name", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			rs := w.replicaSet()
			rs.Name = "other"
			w.replyGet("replicasets", rs)
		}},
		{name: "replicaset_controller_missing", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			rs := w.replicaSet()
			rs.OwnerReferences = nil
			w.put(rs)
		}},
		{name: "replicaset_controller_uid_missing", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			rs := w.replicaSet()
			rs.OwnerReferences[0].UID = ""
			w.put(rs)
		}},
		{name: "replicaset_owner_not_controlling", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			rs := w.replicaSet()
			rs.OwnerReferences[0].Controller = ptr.To(false)
			w.put(rs)
		}},
		{name: "replicaset_owner_wrong_api_version", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			rs := w.replicaSet()
			rs.OwnerReferences[0].APIVersion = "example.com/v1"
			w.put(rs)
		}},
		{name: "replicaset_controllers_ambiguous", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			rs := w.replicaSet()
			rs.OwnerReferences = append(rs.OwnerReferences, terminalPodTestController("Deployment", "other", "deployment-other"))
			w.put(rs)
		}},
		{name: "replicaset_controller_name_mismatch", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			rs := w.replicaSet()
			rs.OwnerReferences[0].Name = "other"
			w.put(rs)
		}},
		{name: "pod_uid_missing", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			p := w.pod("terminal")
			p.UID = ""
			w.put(p)
		}},
		{name: "pod_resource_version_missing", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			p := w.pod("terminal")
			p.ResourceVersion = ""
			w.put(p)
		}},
		{name: "pod_get_wrong_namespace", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			p := w.pod("terminal")
			p.Namespace = "other"
			w.replyGet("pods", p)
		}},
		{name: "pod_get_wrong_name", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			p := w.pod("terminal")
			p.Name = "other"
			w.replyGet("pods", p)
		}},
		{name: "pod_controller_missing", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			p := w.pod("terminal")
			p.OwnerReferences = nil
			w.put(p)
		}},
		{name: "pod_controller_uid_missing", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			p := w.pod("terminal")
			p.OwnerReferences[0].UID = ""
			w.put(p)
		}},
		{name: "pod_owner_not_controlling", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			p := w.pod("terminal")
			p.OwnerReferences[0].Controller = ptr.To(false)
			w.put(p)
		}},
		{name: "pod_owner_wrong_api_version", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			p := w.pod("terminal")
			p.OwnerReferences[0].APIVersion = "example.com/v1"
			w.put(p)
		}},
		{name: "pod_controllers_ambiguous", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			p := w.pod("terminal")
			p.OwnerReferences = append(p.OwnerReferences, terminalPodTestController("ReplicaSet", "other", "replicaset-other"))
			w.put(p)
		}},
		{name: "pod_controller_name_mismatch", change: func(w *terminalPodTestWorld, _ *terminalPodCleanupTarget) {
			p := w.pod("terminal")
			p.OwnerReferences[0].Name = "other"
			w.put(p)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			world := newTerminalPodTestWorld(t)
			target := world.target
			tt.change(world, &target)
			before := world.pods()

			g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, target, world.record)).ToNot(Succeed(), "an unverified current owner chain cannot authorize deletion")

			g.Expect(world.pods()).To(Equal(before))
			g.Expect(world.deletes).To(BeEmpty())
			g.Expect(world.evidence).To(BeEmpty())
			g.Expect(world.forbiddenWrites).To(BeEmpty())
		})
	}
}

func TestTerminalPodCleanupPropagatesAPIErrors(t *testing.T) {
	tests := []struct{ name, verb, resource string }{
		{name: "deployment_get", verb: "get", resource: "deployments"},
		{name: "pod_list", verb: "list", resource: "pods"},
		{name: "replicaset_get", verb: "get", resource: "replicasets"},
		{name: "pod_get", verb: "get", resource: "pods"},
		{name: "pod_delete", verb: "delete", resource: "pods"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			world := newTerminalPodTestWorld(t)
			world.kube.PrependReactor(tt.verb, tt.resource, func(ktesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("injected API failure")
			})

			err := normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, world.record)

			g.Expect(err).To(MatchError(ContainSubstring("injected API failure")))
			g.Expect(world.podExists("terminal")).To(BeTrue())
			g.Expect(world.forbiddenWrites).To(BeEmpty())
		})
	}
}

func TestTerminalPodCleanupEvidenceFailure(t *testing.T) {
	g := NewWithT(t)
	world := newTerminalPodTestWorld(t)
	err := normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, func(*corev1.Pod) error {
		return errors.New("evidence capture failed")
	})
	g.Expect(err).To(MatchError(ContainSubstring("evidence capture failed")))
	g.Expect(world.podExists("terminal")).To(BeTrue())
	g.Expect(world.deletes).To(BeEmpty(), "do not delete the terminal evidence when preserving it failed")
}

func TestTerminalPodCleanupRequiresEvidenceRecorder(t *testing.T) {
	g := NewWithT(t)
	world := newTerminalPodTestWorld(t)
	g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, nil)).ToNot(Succeed())
	g.Expect(world.podExists("terminal")).To(BeTrue())
	g.Expect(world.deletes).To(BeEmpty())
}

func TestTerminalPodCleanupCanceledContext(t *testing.T) {
	g := NewWithT(t)
	world := newTerminalPodTestWorld(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := normalizeTerminalDeploymentPods(ctx, world.kube, world.target, world.record)
	g.Expect(errors.Is(err, context.Canceled)).To(BeTrue())
	g.Expect(world.kube.Actions()).To(BeEmpty(), "a canceled cleanup must not start API operations")
	g.Expect(world.podExists("terminal")).To(BeTrue())
}

func TestTerminalPodCleanupCancellationBeforeDelete(t *testing.T) {
	g := NewWithT(t)
	world := newTerminalPodTestWorld(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := normalizeTerminalDeploymentPods(ctx, world.kube, world.target, func(pod *corev1.Pod) error {
		g.Expect(world.record(pod)).To(Succeed())
		cancel()
		return nil
	})
	g.Expect(errors.Is(err, context.Canceled)).To(BeTrue())
	g.Expect(world.deletes).To(BeEmpty(), "the fake API does not enforce context cancellation; the cleanup must stop before writing")
	g.Expect(world.podExists("terminal")).To(BeTrue())
}

func TestTerminalPodCleanupAllowsOwnerProgress(t *testing.T) {
	tests := []struct {
		name                          string
		deployment, replicaSet, empty bool
	}{
		{name: "deployment_progress", deployment: true},
		{name: "replicaset_progress", replicaSet: true},
		{name: "both_owners_progress", deployment: true, replicaSet: true},
		{name: "no_terminal_pods_during_progress", deployment: true, replicaSet: true, empty: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			world := newTerminalPodTestWorld(t)
			if tt.deployment {
				d := world.deployment()
				d.Generation++
				d.ResourceVersion = "21"
				d.Spec.Template.Spec.Containers[0].Image = "registry.k8s.io/pause:3.9"
				d.Status = appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 3, ReadyReplicas: 2}
				world.put(d)
			}
			if tt.replicaSet {
				rs := world.replicaSet()
				rs.Generation++
				rs.ResourceVersion = "22"
				rs.Spec.Template.Spec.Containers[0].Image = "registry.k8s.io/pause:3.8"
				rs.Status = appsv1.ReplicaSetStatus{ObservedGeneration: 1, Replicas: 4, ReadyReplicas: 1}
				world.put(rs)
			}
			if tt.empty {
				world.removeFixturePod("terminal")
			}
			deployment, replicaSet := world.deployment(), world.replicaSet()

			g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, world.record)).To(Succeed(), "terminal deletion ownership is not serving-rollout proof")

			g.Expect(world.podExists("terminal")).To(BeFalse())
			g.Expect(world.deployment()).To(Equal(deployment))
			g.Expect(world.replicaSet()).To(Equal(replicaSet))
			g.Expect(world.forbiddenWrites).To(BeEmpty())
		})
	}
}

func TestTerminalPodCleanupDoesNotRequirePodLabels(t *testing.T) {
	g := NewWithT(t)
	world := newTerminalPodTestWorld(t)
	pod := world.pod("terminal")
	pod.Labels = nil
	world.put(pod)
	g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, world.record)).To(Succeed())
	g.Expect(world.podExists("terminal")).To(BeFalse(), "a current controlling UID chain proves ownership without template or label equality")
}

func TestTerminalPodCleanupVerifiesAbsence(t *testing.T) {
	tests := []struct {
		name      string
		finalizer bool
	}{
		{name: "delete_acknowledged_but_object_retained"},
		{name: "finalizer_retains_terminal_object", finalizer: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			world := newTerminalPodTestWorld(t)
			world.keepAfterDelete = true
			if tt.finalizer {
				p := world.pod("terminal")
				p.Finalizers = []string{"example.com/retain"}
				world.put(p)
			}
			before := world.pod("terminal")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			postDeleteReads := 0
			world.kube.PrependReactor("*", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				if len(world.deletes) > 0 && (action.GetVerb() == "get" || action.GetVerb() == "list") {
					postDeleteReads++
					cancel() // deterministic bound, not a wall-clock timeout or a sleep
				}
				return false, nil, nil
			})

			g.Expect(normalizeTerminalDeploymentPods(ctx, world.kube, world.target, world.record)).ToNot(Succeed(), "an acknowledged delete is not proof of absence")

			g.Expect(world.deletes).To(HaveLen(1))
			g.Expect(postDeleteReads).To(BeNumerically(">", 0))
			g.Expect(world.pod("terminal")).To(Equal(before))
			g.Expect(world.forbiddenWrites).To(BeEmpty(), "cleanup must not remove an unrelated finalizer")
		})
	}
}

func TestTerminalPodCleanupRejectsRetainedObjectWithoutCancellation(t *testing.T) {
	tests := []struct {
		name      string
		finalizer bool
	}{
		{name: "delete_acknowledged_but_object_retained"},
		{name: "finalizer_retains_terminal_object", finalizer: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			world := newTerminalPodTestWorld(t)
			// Exercise finalizer retention independently of the acknowledged-delete switch.
			world.keepAfterDelete = !tt.finalizer
			before := world.pod("terminal")
			before.Status.Reason = "SyntheticFailure"
			before.Status.Message = "terminal failure evidence must survive an acknowledged delete"
			if tt.finalizer {
				before.Finalizers = []string{"example.com/retain"}
			}
			world.put(before)
			recordEvent := fmt.Sprintf("record:%s:%s", before.UID, before.ResourceVersion)
			world.beforeDelete = func() {
				g.Expect(world.evidence).To(Equal([]*corev1.Pod{before}), "capture evidence before the actual delete request")
				g.Expect(world.events).To(Equal([]string{recordEvent}))
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			confirmationGets := 0
			world.kube.PrependReactor("*", "*", func(action ktesting.Action) (bool, runtime.Object, error) {
				// Leave the first confirmation GET uncanceled so retention, rather
				// than ctx.Err(), must explain the failure. Bound accidental retries
				// only when an unexpected subsequent operation is attempted.
				if confirmationGets > 0 {
					cancel()
					t.Fatalf("unexpected cleanup operation after retention confirmation: %s %s", action.GetVerb(), action.GetResource().Resource)
				}
				if len(world.deletes) > 0 && action.GetVerb() == "get" && action.GetResource() == terminalPodTestPods && action.GetSubresource() == "" {
					g.Expect(action.GetNamespace()).To(Equal(before.Namespace))
					g.Expect(action.(ktesting.GetAction).GetName()).To(Equal(before.Name))
					g.Expect(ctx.Err()).To(Succeed(), "the first confirmation GET must not be canceled")
					confirmationGets++
				}
				return false, nil, nil // Let the fake return the unchanged retained object.
			})

			err := normalizeTerminalDeploymentPods(ctx, world.kube, world.target, world.record)

			g.Expect(ctx.Err()).To(Succeed(), "retention must be reported before the retry guard cancels the context")
			g.Expect(errors.Is(err, context.Canceled)).To(BeFalse())
			g.Expect(err).To(MatchError(fmt.Sprintf(
				"terminal Pod %s/%s still exists after deleting uid=%s: observed uid=%s rv=%s",
				before.Namespace, before.Name, before.UID, before.UID, before.ResourceVersion,
			)), "an acknowledged delete of a retained object must not return success")
			g.Expect(world.deletes).To(HaveLen(1), "observe exactly one actual API delete")
			g.Expect(world.deletes[0].key).To(Equal(types.NamespacedName{Namespace: before.Namespace, Name: before.Name}))
			g.Expect(world.deletes[0].options.Preconditions).To(Equal(&metav1.Preconditions{
				UID: ptr.To(before.UID), ResourceVersion: ptr.To(before.ResourceVersion),
			}))
			g.Expect(confirmationGets).To(Equal(1), "confirm retention with exactly one fresh GET")
			g.Expect(world.pod("terminal")).To(Equal(before), "leave the entire retained object, including finalizers, unchanged")
			g.Expect(world.evidence).To(Equal([]*corev1.Pod{before}))
			g.Expect(world.events).To(Equal([]string{recordEvent, fmt.Sprintf("delete:%s:%s", before.UID, before.ResourceVersion)}))
			g.Expect(world.forbiddenWrites).To(BeEmpty(), "cleanup must not remove finalizers or write other resources")
		})
	}
}

func TestTerminalPodCleanupPreconditionRaces(t *testing.T) {
	tests := []struct {
		name       string
		replaceUID bool
	}{
		{name: "same_name_new_uid", replaceUID: true},
		{name: "same_uid_new_resource_version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			world := newTerminalPodTestWorld(t)
			original := world.pod("terminal")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			world.beforeDelete = func() {
				p := world.pod("terminal")
				p.ResourceVersion = "11"
				if tt.replaceUID {
					p.UID = "pod-replacement"
				}
				world.put(p)
				cancel() // a failed stale delete cannot enter an unbounded retry in this test
			}

			g.Expect(normalizeTerminalDeploymentPods(ctx, world.kube, world.target, world.record)).ToNot(Succeed())

			g.Expect(world.deletes).To(HaveLen(1))
			g.Expect(world.deletes[0].options.Preconditions).To(Equal(&metav1.Preconditions{UID: ptr.To(original.UID), ResourceVersion: ptr.To(original.ResourceVersion)}))
			g.Expect(world.podExists("terminal")).To(BeTrue(), "the API precondition must protect the changed object")
			g.Expect(world.pod("terminal").ResourceVersion).To(Equal("11"))
			if tt.replaceUID {
				g.Expect(world.pod("terminal").UID).To(Equal(types.UID("pod-replacement")))
			}
		})
	}
}

func TestTerminalPodCleanupRechecksTerminalPhase(t *testing.T) {
	g := NewWithT(t)
	world := newTerminalPodTestWorld(t)
	world.kube.PrependReactor("get", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		p := world.pod("terminal")
		p.Status.Phase = corev1.PodRunning
		p.ResourceVersion = "11"
		world.put(p)
		return true, p, nil
	})

	g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, world.record)).To(Succeed())

	g.Expect(world.pod("terminal").Status.Phase).To(Equal(corev1.PodRunning), "use the fresh Pod observation rather than the listed terminal state")
	g.Expect(world.deletes).To(BeEmpty())
	g.Expect(world.evidence).To(BeEmpty())
}

func TestTerminalPodCleanupRefreshesPodObservation(t *testing.T) {
	g := NewWithT(t)
	world := newTerminalPodTestWorld(t)
	advanced := false
	world.kube.PrependReactor("list", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		listed, err := world.kube.Tracker().List(terminalPodTestPods, corev1.SchemeGroupVersion.WithKind("Pod"), action.GetNamespace())
		if !advanced {
			pod := world.pod("terminal")
			pod.ResourceVersion = "11"
			pod.Status.Message = "newer terminal failure evidence"
			world.put(pod)
			advanced = true
		}
		return true, listed, err // the caller's list is older than the current object
	})

	g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, world.record)).To(Succeed())

	g.Expect(world.evidence).To(HaveLen(1))
	g.Expect(world.evidence[0].ResourceVersion).To(Equal("11"))
	g.Expect(world.evidence[0].Status.Message).To(Equal("newer terminal failure evidence"))
	g.Expect(world.deletes).To(HaveLen(1))
	g.Expect(world.deletes[0].options.Preconditions).To(Equal(&metav1.Preconditions{
		UID: ptr.To(types.UID("pod-terminal")), ResourceVersion: ptr.To("11"),
	}))
	g.Expect(world.podExists("terminal")).To(BeFalse())
}

func TestTerminalPodCleanupAlreadyAbsent(t *testing.T) {
	tests := []struct{ name, verb string }{
		{name: "disappears_before_pod_get", verb: "get"},
		{name: "disappears_before_pod_delete", verb: "delete"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			world := newTerminalPodTestWorld(t)
			removed := false
			world.kube.PrependReactor(tt.verb, "pods", func(ktesting.Action) (bool, runtime.Object, error) {
				if !removed {
					world.removeFixturePod("terminal")
					removed = true
				}
				return true, nil, apierrors.NewNotFound(corev1.Resource("pods"), "terminal")
			})

			g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, world.record)).To(Succeed(), "a concurrently absent target is already cleaned up")

			g.Expect(world.podExists("terminal")).To(BeFalse())
			g.Expect(world.forbiddenWrites).To(BeEmpty())
		})
	}
}

func TestTerminalPodCleanupIsIdempotent(t *testing.T) {
	g := NewWithT(t)
	world := newTerminalPodTestWorld(t)
	g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, world.record)).To(Succeed())
	g.Expect(normalizeTerminalDeploymentPods(t.Context(), world.kube, world.target, world.record)).To(Succeed())
	g.Expect(world.deletes).To(HaveLen(1))
	g.Expect(world.evidence).To(HaveLen(1))
	g.Expect(world.podExists("terminal")).To(BeFalse())
}

var (
	terminalPodTestPods        = corev1.SchemeGroupVersion.WithResource("pods")
	terminalPodTestNodes       = corev1.SchemeGroupVersion.WithResource("nodes")
	terminalPodTestDeployments = appsv1.SchemeGroupVersion.WithResource("deployments")
	terminalPodTestReplicaSets = appsv1.SchemeGroupVersion.WithResource("replicasets")
)

type terminalPodTestDelete struct {
	key     types.NamespacedName
	options metav1.DeleteOptions
}

type terminalPodTestWorld struct {
	t               *testing.T
	kube            *kubefake.Clientset
	target          terminalPodCleanupTarget
	evidence        []*corev1.Pod
	events          []string
	deletes         []terminalPodTestDelete
	forbiddenWrites []string
	keepAfterDelete bool
	beforeDelete    func()
}

func newTerminalPodTestWorld(t *testing.T) *terminalPodTestWorld {
	t.Helper()
	world := &terminalPodTestWorld{t: t, kube: kubefake.NewSimpleClientset(), target: terminalPodCleanupTarget{
		key: types.NamespacedName{Namespace: "default", Name: "workload"}, uid: "deployment-workload",
	}}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "workload", UID: world.target.uid, ResourceVersion: "7", Generation: 2},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(20)), Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "workload"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "workload"}}, Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "pause", Image: "registry.k8s.io/pause:3.10", Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
				}}},
			}},
		},
	}
	world.put(deployment)
	world.put(&appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "workload-rs", UID: "replicaset-workload", ResourceVersion: "8", Generation: 1,
			OwnerReferences: []metav1.OwnerReference{terminalPodTestController("Deployment", deployment.Name, deployment.UID)}},
		Spec: appsv1.ReplicaSetSpec{Replicas: ptr.To(int32(20)), Selector: deployment.Spec.Selector.DeepCopy(), Template: *deployment.Spec.Template.DeepCopy()},
	})
	world.put(world.newPod("terminal", "node-0", corev1.PodFailed, "1"))
	// The tracker supplies in-memory API effects. Model supplied UID/RV
	// preconditions, which client-go's fake does not enforce. Like the API, an
	// unfenced delete can succeed; the tests require both actual preconditions.
	// Read-side changes exist only in explicitly injected observation/race cases.
	world.kube.PrependReactor("*", "*", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetVerb() == "get" || action.GetVerb() == "list" {
			return false, nil, nil
		}
		if action.GetVerb() != "delete" || action.GetResource() != terminalPodTestPods || action.GetSubresource() != "" {
			world.forbiddenWrites = append(world.forbiddenWrites, action.GetVerb()+" "+action.GetResource().Resource)
			return true, nil, fmt.Errorf("unexpected cleanup write: %s %s", action.GetVerb(), action.GetResource().Resource)
		}
		request := action.(ktesting.DeleteAction)
		options := request.GetDeleteOptions()
		world.deletes = append(world.deletes, terminalPodTestDelete{key: types.NamespacedName{Namespace: action.GetNamespace(), Name: request.GetName()}, options: *options.DeepCopy()})
		if world.beforeDelete != nil {
			world.beforeDelete()
		}
		object, err := world.kube.Tracker().Get(terminalPodTestPods, action.GetNamespace(), request.GetName())
		if err != nil {
			return true, nil, err
		}
		pod := object.(*corev1.Pod)
		preconditions := options.Preconditions
		if preconditions != nil && ((preconditions.UID != nil && *preconditions.UID != pod.UID) ||
			(preconditions.ResourceVersion != nil && *preconditions.ResourceVersion != pod.ResourceVersion)) {
			return true, nil, apierrors.NewConflict(corev1.Resource("pods"), pod.Name, errors.New("UID/resourceVersion precondition does not match current Pod"))
		}
		world.events = append(world.events, fmt.Sprintf("delete:%s:%s", pod.UID, pod.ResourceVersion))
		if world.keepAfterDelete || len(pod.Finalizers) > 0 {
			return true, nil, nil
		}
		return true, nil, world.kube.Tracker().Delete(terminalPodTestPods, pod.Namespace, pod.Name)
	})
	return world
}

func terminalPodTestController(kind, name string, uid types.UID) metav1.OwnerReference {
	return metav1.OwnerReference{APIVersion: "apps/v1", Kind: kind, Name: name, UID: uid, Controller: ptr.To(true)}
}

func (w *terminalPodTestWorld) newPod(name, node string, phase corev1.PodPhase, request string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name, UID: types.UID("pod-" + name), ResourceVersion: "10", Labels: map[string]string{"app": "workload"},
			OwnerReferences: []metav1.OwnerReference{terminalPodTestController("ReplicaSet", "workload-rs", "replicaset-workload")}},
		Spec: corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{Name: "pause", Image: "registry.k8s.io/pause:3.10", Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(request)},
		}}}},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func (w *terminalPodTestWorld) record(pod *corev1.Pod) error {
	w.evidence = append(w.evidence, pod.DeepCopy())
	w.events = append(w.events, fmt.Sprintf("record:%s:%s", pod.UID, pod.ResourceVersion))
	return nil
}

func (w *terminalPodTestWorld) put(object runtime.Object) {
	w.t.Helper()
	var resource schema.GroupVersionResource
	switch object.(type) {
	case *corev1.Pod:
		resource = terminalPodTestPods
	case *corev1.Node:
		resource = terminalPodTestNodes
	case *appsv1.Deployment:
		resource = terminalPodTestDeployments
	case *appsv1.ReplicaSet:
		resource = terminalPodTestReplicaSets
	default:
		w.t.Fatalf("unsupported test object %T", object)
	}
	metadata := object.(metav1.Object)
	err := w.kube.Tracker().Update(resource, object.DeepCopyObject(), metadata.GetNamespace())
	if apierrors.IsNotFound(err) {
		err = w.kube.Tracker().Add(object.DeepCopyObject())
	}
	NewWithT(w.t).Expect(err).To(Succeed())
}

func (w *terminalPodTestWorld) deployment() *appsv1.Deployment {
	w.t.Helper()
	object, err := w.kube.Tracker().Get(terminalPodTestDeployments, "default", "workload")
	NewWithT(w.t).Expect(err).To(Succeed())
	return object.(*appsv1.Deployment).DeepCopy()
}

func (w *terminalPodTestWorld) replicaSet() *appsv1.ReplicaSet {
	w.t.Helper()
	object, err := w.kube.Tracker().Get(terminalPodTestReplicaSets, "default", "workload-rs")
	NewWithT(w.t).Expect(err).To(Succeed())
	return object.(*appsv1.ReplicaSet).DeepCopy()
}

func (w *terminalPodTestWorld) pod(name string) *corev1.Pod {
	w.t.Helper()
	object, err := w.kube.Tracker().Get(terminalPodTestPods, "default", name)
	NewWithT(w.t).Expect(err).To(Succeed())
	return object.(*corev1.Pod).DeepCopy()
}

func (w *terminalPodTestWorld) podExists(name string) bool {
	w.t.Helper()
	_, err := w.kube.Tracker().Get(terminalPodTestPods, "default", name)
	if apierrors.IsNotFound(err) {
		return false
	}
	NewWithT(w.t).Expect(err).To(Succeed())
	return true
}

func (w *terminalPodTestWorld) removeFixturePod(name string) {
	w.t.Helper()
	NewWithT(w.t).Expect(w.kube.Tracker().Delete(terminalPodTestPods, "default", name)).To(Succeed())
}

func (w *terminalPodTestWorld) replyGet(resource string, object runtime.Object) {
	w.kube.PrependReactor("get", resource, func(ktesting.Action) (bool, runtime.Object, error) { return true, object.DeepCopyObject(), nil })
}

func (w *terminalPodTestWorld) pods() map[types.NamespacedName]*corev1.Pod {
	w.t.Helper()
	object, err := w.kube.Tracker().List(terminalPodTestPods, corev1.SchemeGroupVersion.WithKind("Pod"), "")
	NewWithT(w.t).Expect(err).To(Succeed())
	pods := map[types.NamespacedName]*corev1.Pod{}
	for _, pod := range object.(*corev1.PodList).Items {
		pods[types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}] = pod.DeepCopy()
	}
	return pods
}

func (w *terminalPodTestWorld) nonTerminalPods() map[types.NamespacedName]*corev1.Pod {
	pods := w.pods()
	for key, pod := range pods {
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			delete(pods, key)
		}
	}
	return pods
}

func (w *terminalPodTestWorld) nodes() map[string]*corev1.Node {
	w.t.Helper()
	object, err := w.kube.Tracker().List(terminalPodTestNodes, corev1.SchemeGroupVersion.WithKind("Node"), "")
	NewWithT(w.t).Expect(err).To(Succeed())
	nodes := map[string]*corev1.Node{}
	for _, node := range object.(*corev1.NodeList).Items {
		nodes[node.Name] = node.DeepCopy()
	}
	return nodes
}

func (w *terminalPodTestWorld) cpuRequests() map[string]int64 {
	requests := map[string]int64{}
	for _, pod := range w.pods() {
		requested := resources.RequestsForPods(pod)
		requests[pod.Spec.NodeName] += requested.Cpu().MilliValue()
	}
	return requests
}

func (w *terminalPodTestWorld) averageCPU() float64 {
	var objects []client.Object
	for _, node := range w.nodes() {
		objects = append(objects, node)
	}
	for _, pod := range w.pods() {
		objects = append(objects, pod)
	}
	// Rebuild the monitor's fake client from the same tracker after cleanup; do
	// not compute an expected result from a disconnected, manually filtered list.
	monitorClient := clientfake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(objects...).Build()
	return common.NewMonitor(w.t.Context(), monitorClient).AvgUtilization(corev1.ResourceCPU)
}
