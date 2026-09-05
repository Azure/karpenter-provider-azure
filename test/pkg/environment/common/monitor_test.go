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

package common

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestAvgUtilizationDiagnostics(t *testing.T) {
	g := NewWithT(t)
	fixture := newUtilizationTestFixture()
	average, details := fixture.monitor.avgUtilization(corev1.ResourceCPU, fixture.deployment)
	// Preserve the unweighted mean across all Karpenter pools, including empty nodes:
	// (1900/3800 + 1900/7600 + 0/1900) / 3 = 0.25, not total requests / total allocatable.
	g.Expect(average).To(Equal(0.25))
	g.Expect(fixture.monitor.AvgUtilization(corev1.ResourceCPU)).To(Equal(average))
	g.Expect(fixture.monitor.MinUtilization(corev1.ResourceCPU)).To(BeZero())
	sample := decodeUtilizationSample(t, details())
	g.Expect(sample.Average).To(Equal("0.25"))
	g.Expect(sample.CohortSize).To(Equal(3))
	g.Expect(sample.NodeListResourceVersion).To(Equal("node-list-rv"))
	g.Expect(sample.PodListResourceVersion).To(Equal("pod-list-rv"))
	g.Expect(sample.Nodes).To(ContainElements(
		And(HaveKeyWithValue("name", "a"), HaveKeyWithValue("uid", "node-a"), HaveKeyWithValue("resourceVersion", "1"),
			HaveKeyWithValue("nodePool", "pool-a"), HaveKeyWithValue("requests", "1900m"), HaveKeyWithValue("allocatable", "3800m"), HaveKeyWithValue("ratio", "0.5"), HaveKeyWithValue("included", true)),
		And(HaveKeyWithValue("name", "b"), HaveKeyWithValue("nodePool", "other-pool"), HaveKeyWithValue("requests", "1900m"), HaveKeyWithValue("allocatable", "7600m"), HaveKeyWithValue("ratio", "0.25"), HaveKeyWithValue("included", true)),
		And(HaveKeyWithValue("name", "empty"), HaveKeyWithValue("requests", "0"), HaveKeyWithValue("ratio", "0"), HaveKeyWithValue("included", true)),
		And(HaveKeyWithValue("name", "system"), HaveKeyWithValue("included", false)),
		And(HaveKeyWithValue("name", "zero"), HaveKeyWithValue("included", false)),
	))
	g.Expect(sample.Pods).To(ContainElements(
		And(HaveKeyWithValue("name", "application"), HaveKeyWithValue("uid", "pod-application"), HaveKeyWithValue("resourceVersion", "1"),
			HaveKeyWithValue("nodeName", "a"), HaveKeyWithValue("requests", "1900m"), HaveKey("containers"), HaveKey("initContainers"), HaveKey("containerStatuses"), HaveKey("overhead"), HaveKey("owners")),
		And(HaveKeyWithValue("name", "terminal"), HaveKeyWithValue("phase", "Succeeded"), HaveKeyWithValue("requests", "400m"), HaveKeyWithValue("deletionTimestamp", Not(BeNil()))),
		And(HaveKeyWithValue("name", "pending"), HaveKeyWithValue("nodeName", "")),
	))
	g.Expect(sample.Deployments).To(ContainElement(And(
		HaveKeyWithValue("name", "workload"), HaveKeyWithValue("uid", "deployment-workload"), HaveKeyWithValue("resourceVersion", "1"),
		HaveKeyWithValue("generation", float64(2)), HaveKeyWithValue("observedGeneration", float64(1)),
		HaveKeyWithValue("desiredReplicas", float64(20)), HaveKeyWithValue("replicas", float64(50)), HaveKeyWithValue("readyReplicas", float64(20)),
	)))
	// Resource inputs are needed, not unrelated container environment values.
	g.Expect(details()).ToNot(ContainSubstring("not-for-diagnostics"))
}

func TestAvgUtilizationResizeInputs(t *testing.T) {
	g := NewWithT(t)
	fixture := newUtilizationTestFixture()
	changed := fixture.application.DeepCopy()
	changed.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("4")
	changed.Spec.InitContainers = nil
	g.Expect(fixture.client.Update(context.Background(), changed)).To(Succeed())
	changed.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name: "app",
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1500m")},
		},
		AllocatedResources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1500m")},
	}}
	changed.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodResizePending, Status: corev1.ConditionTrue, Reason: corev1.PodReasonInfeasible}}
	g.Expect(fixture.client.Status().Update(context.Background(), changed)).To(Succeed())

	_, details := fixture.monitor.avgUtilization(corev1.ResourceCPU)
	sample := decodeUtilizationSample(t, details())
	// An infeasible resize uses the actuated/allocated requests, not the 4-CPU
	// desired request. Retain the condition explaining 1500m + 100m overhead.
	g.Expect(sample.Pods).To(ContainElement(And(
		HaveKeyWithValue("name", "application"), HaveKeyWithValue("requests", "1600m"),
		HaveKeyWithValue("conditions", ContainElement(And(HaveKeyWithValue("type", "PodResizePending"), HaveKeyWithValue("reason", "Infeasible")))),
	)))
}

func TestAvgUtilizationDiagnosticsRetainSample(t *testing.T) {
	g := NewWithT(t)
	fixture := newUtilizationTestFixture()
	average, details := fixture.monitor.avgUtilization(corev1.ResourceCPU, fixture.deployment)
	g.Expect(average).To(Equal(0.25))
	g.Expect(fixture.listCalls).To(Equal(2))
	g.Expect(fixture.getCalls).To(Equal(1))

	changed := fixture.application.DeepCopy()
	changed.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("9")
	g.Expect(fixture.client.Update(context.Background(), changed)).To(Succeed())
	changedDeployment := fixture.deployment.DeepCopy()
	changedDeployment.Status.Replicas = 20
	g.Expect(fixture.client.Status().Update(context.Background(), changedDeployment)).To(Succeed())

	first := details()
	g.Expect(details()).To(Equal(first))
	g.Expect(fixture.listCalls).To(Equal(2), "formatting the failure must not resample nodes or pods")
	g.Expect(fixture.getCalls).To(Equal(1), "formatting the failure must not refetch deployment state")
	sample := decodeUtilizationSample(t, first)
	g.Expect(sample.Average).To(Equal("0.25"))
	g.Expect(sample.Pods).To(ContainElement(And(HaveKeyWithValue("name", "application"), HaveKeyWithValue("resourceVersion", "1"), HaveKeyWithValue("requests", "1900m"))))
	g.Expect(sample.Deployments).To(ContainElement(HaveKeyWithValue("replicas", float64(50))))
}

func TestAvgUtilizationEmptyCohort(t *testing.T) {
	g := NewWithT(t)
	monitor := NewMonitor(context.Background(), fake.NewClientBuilder().WithScheme(scheme.Scheme).Build())
	average, details := monitor.avgUtilization(corev1.ResourceCPU)
	g.Expect(math.IsNaN(average)).To(BeTrue())
	sample := decodeUtilizationSample(t, details())
	g.Expect(sample.Average).To(Equal("NaN"))
	g.Expect(sample.CohortSize).To(BeZero())
}

func TestAvgUtilizationDeploymentSnapshotError(t *testing.T) {
	g := NewWithT(t)
	fixture := newUtilizationTestFixture()
	g.Expect(fixture.client.Delete(context.Background(), fixture.deployment)).To(Succeed())
	average, details := fixture.monitor.avgUtilization(corev1.ResourceCPU, fixture.deployment)
	g.Expect(average).To(Equal(0.25), "diagnostic collection must not alter the utilization result")
	sample := decodeUtilizationSample(t, details())
	g.Expect(sample.Deployments).To(ContainElement(And(HaveKeyWithValue("name", "workload"), HaveKeyWithValue("error", ContainSubstring("not found")))))
}

type utilizationTestSample struct {
	Average                 string
	CohortSize              int
	NodeListResourceVersion string
	PodListResourceVersion  string
	Nodes                   []map[string]interface{}
	Pods                    []map[string]interface{}
	Deployments             []map[string]interface{}
}

func decodeUtilizationSample(t *testing.T, details string) utilizationTestSample {
	t.Helper()
	g := NewWithT(t)
	g.Expect(details).ToNot(BeEmpty(), "the failing scalar needs the exact inputs that produced it")
	var sample utilizationTestSample
	g.Expect(json.Unmarshal([]byte(details), &sample)).To(Succeed())
	return sample
}

type utilizationTestFixture struct {
	monitor     *Monitor
	client      client.Client
	application *corev1.Pod
	deployment  *appsv1.Deployment
	listCalls   int
	getCalls    int
}

func newUtilizationTestFixture() *utilizationTestFixture {
	fixture := &utilizationTestFixture{}
	node := func(name, pool, cpu string) *corev1.Node {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID("node-" + name), ResourceVersion: "1", Labels: map[string]string{karpv1.NodePoolLabelKey: pool}},
			Status:     corev1.NodeStatus{Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu)}},
		}
	}
	pod := func(name, nodeName, cpu string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID("pod-" + name), ResourceVersion: "1"},
			Spec: corev1.PodSpec{NodeName: nodeName, Containers: []corev1.Container{{Name: "app", Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu)},
			}}}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		}
	}
	fixture.application = pod("application", "a", "1")
	fixture.application.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "workload-rs"}}
	fixture.application.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "PRIVATE", Value: "not-for-diagnostics"}}
	fixture.application.Spec.InitContainers = []corev1.Container{{Name: "init", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1800m")}}}}
	fixture.application.Spec.Overhead = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}
	fixture.application.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", AllocatedResources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1500m")}}}
	terminal := pod("terminal", "b", "400m")
	terminal.Status.Phase = corev1.PodSucceeded
	terminal.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
	terminal.Finalizers = []string{TestingFinalizer}
	pending := pod("pending", "", "100")
	pending.Status.Phase = corev1.PodPending
	replicas := int32(20)
	fixture.deployment = &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "workload", Namespace: "default", UID: "deployment-workload", ResourceVersion: "1", Generation: 2},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 50, ReadyReplicas: 20},
	}
	fixture.client = fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(
		node("a", "pool-a", "3800m"), node("b", "other-pool", "7600m"), node("empty", "pool-a", "1900m"), node("zero", "pool-a", "0"), node("system", "", "10"),
		fixture.application, pod("other", "b", "1500m"), terminal, pending, pod("system-pod", "system", "8"), fixture.deployment,
	).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			fixture.listCalls++
			if err := c.List(ctx, list, opts...); err != nil {
				return err
			}
			switch items := list.(type) {
			case *corev1.NodeList:
				items.ResourceVersion = "node-list-rv"
			case *corev1.PodList:
				items.ResourceVersion = "pod-list-rv"
			}
			return nil
		},
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			fixture.getCalls++
			return c.Get(ctx, key, obj, opts...)
		},
	}).Build()
	fixture.monitor = NewMonitor(context.Background(), fixture.client)
	fixture.listCalls = 0
	return fixture
}
