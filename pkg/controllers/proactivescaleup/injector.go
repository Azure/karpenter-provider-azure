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

package proactivescaleup

import (
	"context"
	"fmt"

	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
)

const (
	// FakePodLabelKey identifies fake pods for proactive scale-up
	FakePodLabelKey = "karpenter.azure.com/fake-pod"
	// FakePodLabelValue is the label value for fake pods
	FakePodLabelValue = "true"
)

// Injector implements the PodInjector interface from Karpenter core
// It monitors workloads and injects fake pods for the gap between desired and actual pods
type Injector struct {
	kubeClient client.Client
}

// NewInjector creates a new pod injector for proactive scale-up
func NewInjector(kubeClient client.Client) *Injector {
	return &Injector{
		kubeClient: kubeClient,
	}
}

// InjectPods implements the PodInjector interface
// It adds fake pods based on workload definitions (Deployments, Jobs, etc.)
func (i *Injector) InjectPods(ctx context.Context, realPods []*corev1.Pod) []*corev1.Pod {
	opts := options.FromContext(ctx)
	if opts == nil || !opts.ProactiveScaleupEnabled {
		return realPods
	}

	// Get all workloads
	deployments := &appsv1.DeploymentList{}
	if err := i.kubeClient.List(ctx, deployments); err != nil {
		return realPods
	}

	replicaSets := &appsv1.ReplicaSetList{}
	if err := i.kubeClient.List(ctx, replicaSets); err != nil {
		return realPods
	}

	statefulSets := &appsv1.StatefulSetList{}
	if err := i.kubeClient.List(ctx, statefulSets); err != nil {
		return realPods
	}

	jobs := &batchv1.JobList{}
	if err := i.kubeClient.List(ctx, jobs); err != nil {
		return realPods
	}

	// Check limits
	nodes := &corev1.NodeList{}
	if err := i.kubeClient.List(ctx, nodes); err == nil && len(nodes.Items) >= opts.NodeLimit {
		return realPods // At node limit, don't inject
	}

	// Count real pods
	if len(realPods) >= opts.PodInjectionLimit {
		return realPods // At pod limit, don't inject
	}

	// Generate fake pods for workload gaps
	fakePods := make([]*corev1.Pod, 0)
	maxFakePodsToCreate := opts.PodInjectionLimit - len(realPods)

	// Process Deployments
	for idx := range deployments.Items {
		if maxFakePodsToCreate <= 0 {
			break
		}
		created := i.processDeployment(&deployments.Items[idx], realPods, maxFakePodsToCreate)
		fakePods = append(fakePods, created...)
		maxFakePodsToCreate -= len(created)
	}

	// Process ReplicaSets (not owned by Deployments)
	for idx := range replicaSets.Items {
		if maxFakePodsToCreate <= 0 {
			break
		}
		if isOwnedByDeployment(&replicaSets.Items[idx]) {
			continue
		}
		created := i.processReplicaSet(&replicaSets.Items[idx], realPods, maxFakePodsToCreate)
		fakePods = append(fakePods, created...)
		maxFakePodsToCreate -= len(created)
	}

	// Process StatefulSets
	for idx := range statefulSets.Items {
		if maxFakePodsToCreate <= 0 {
			break
		}
		created := i.processStatefulSet(&statefulSets.Items[idx], realPods, maxFakePodsToCreate)
		fakePods = append(fakePods, created...)
		maxFakePodsToCreate -= len(created)
	}

	// Process Jobs
	for idx := range jobs.Items {
		if maxFakePodsToCreate <= 0 {
			break
		}
		created := i.processJob(&jobs.Items[idx], realPods, maxFakePodsToCreate)
		fakePods = append(fakePods, created...)
		maxFakePodsToCreate -= len(created)
	}

	// Return combined list
	return append(realPods, fakePods...)
}

func (i *Injector) processDeployment(deployment *appsv1.Deployment, realPods []*corev1.Pod, maxCreate int) []*corev1.Pod {
	if deployment.Spec.Replicas == nil {
		return nil
	}

	desired := int(*deployment.Spec.Replicas)
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return nil
	}

	// Count existing pods for this deployment
	count := countPodsForWorkload(realPods, selector, deployment.Namespace)
	
	// Calculate gap
	gap := desired - count
	if gap <= 0 {
		return nil
	}

	if gap > maxCreate {
		gap = maxCreate
	}

	// Create fake pods
	fakePods := make([]*corev1.Pod, 0, gap)
	for j := 0; j < gap; j++ {
		fakePod := createFakePodFromTemplate(&deployment.Spec.Template, deployment.Namespace, deployment.Name, "Deployment", j)
		fakePods = append(fakePods, fakePod)
	}

	return fakePods
}

func (i *Injector) processReplicaSet(rs *appsv1.ReplicaSet, realPods []*corev1.Pod, maxCreate int) []*corev1.Pod {
	if rs.Spec.Replicas == nil {
		return nil
	}

	desired := int(*rs.Spec.Replicas)
	selector, err := metav1.LabelSelectorAsSelector(rs.Spec.Selector)
	if err != nil {
		return nil
	}

	count := countPodsForWorkload(realPods, selector, rs.Namespace)
	gap := desired - count
	if gap <= 0 {
		return nil
	}

	if gap > maxCreate {
		gap = maxCreate
	}

	fakePods := make([]*corev1.Pod, 0, gap)
	for j := 0; j < gap; j++ {
		fakePod := createFakePodFromTemplate(&rs.Spec.Template, rs.Namespace, rs.Name, "ReplicaSet", j)
		fakePods = append(fakePods, fakePod)
	}

	return fakePods
}

func (i *Injector) processStatefulSet(sts *appsv1.StatefulSet, realPods []*corev1.Pod, maxCreate int) []*corev1.Pod {
	if sts.Spec.Replicas == nil {
		return nil
	}

	desired := int(*sts.Spec.Replicas)
	selector, err := metav1.LabelSelectorAsSelector(sts.Spec.Selector)
	if err != nil {
		return nil
	}

	count := countPodsForWorkload(realPods, selector, sts.Namespace)
	gap := desired - count
	if gap <= 0 {
		return nil
	}

	if gap > maxCreate {
		gap = maxCreate
	}

	fakePods := make([]*corev1.Pod, 0, gap)
	for j := 0; j < gap; j++ {
		fakePod := createFakePodFromTemplate(&sts.Spec.Template, sts.Namespace, sts.Name, "StatefulSet", j)
		fakePods = append(fakePods, fakePod)
	}

	return fakePods
}

func (i *Injector) processJob(job *batchv1.Job, realPods []*corev1.Pod, maxCreate int) []*corev1.Pod {
	if job.Spec.Completions == nil {
		return nil
	}

	desired := int(*job.Spec.Completions)
	selector, err := metav1.LabelSelectorAsSelector(job.Spec.Selector)
	if err != nil {
		return nil
	}

	count := countPodsForWorkload(realPods, selector, job.Namespace)
	gap := desired - count
	if gap <= 0 {
		return nil
	}

	if gap > maxCreate {
		gap = maxCreate
	}

	fakePods := make([]*corev1.Pod, 0, gap)
	for j := 0; j < gap; j++ {
		fakePod := createFakePodFromTemplate(&job.Spec.Template, job.Namespace, job.Name, "Job", j)
		fakePods = append(fakePods, fakePod)
	}

	return fakePods
}

func createFakePodFromTemplate(template *corev1.PodTemplateSpec, namespace, ownerName, ownerKind string, index int) *corev1.Pod {
	// Calculate total resource requests from template
	totalCPU := resource.NewQuantity(0, resource.DecimalSI)
	totalMemory := resource.NewQuantity(0, resource.BinarySI)
	
	for _, container := range template.Spec.Containers {
		if cpu, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
			totalCPU.Add(cpu)
		}
		if mem, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
			totalMemory.Add(mem)
		}
	}

	fakePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("fake-pod-%s-%s-%d", ownerKind, ownerName, index),
			Namespace: namespace,
			UID:       types.UID(fmt.Sprintf("fake-pod-uid-%s-%s-%d", ownerKind, ownerName, index)),
			Labels: lo.Assign(template.Labels, map[string]string{
				FakePodLabelKey: FakePodLabelValue,
			}),
			Annotations: lo.Assign(template.Annotations, map[string]string{
				"karpenter.azure.com/proactive-scaleup": "true",
			}),
		},
		Spec: corev1.PodSpec{
			// Single pause container with same total resources
			Containers: []corev1.Container{
				{
					Name:  "pause",
					Image: "registry.k8s.io/pause:3.9",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *totalCPU,
							corev1.ResourceMemory: *totalMemory,
						},
					},
				},
			},
			// Use low priority
			Priority: lo.ToPtr(int32(-1000)),
			// Copy scheduling constraints from template
			NodeSelector:              template.Spec.NodeSelector,
			Affinity:                  template.Spec.Affinity,
			Tolerations:               template.Spec.Tolerations,
			TopologySpreadConstraints: template.Spec.TopologySpreadConstraints,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodScheduled,
					Status: corev1.ConditionFalse,
					Reason: "Unschedulable",
				},
			},
		},
	}

	return fakePod
}

func countPodsForWorkload(pods []*corev1.Pod, selector labels.Selector, namespace string) int {
	count := 0
	for _, pod := range pods {
		if pod.Namespace != namespace {
			continue
		}
		if !selector.Matches(labels.Set(pod.Labels)) {
			continue
		}
		// Don't count completed/failed pods or fake pods
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		if isFakePod(pod) {
			continue
		}
		count++
	}
	return count
}

func isFakePod(pod *corev1.Pod) bool {
	if pod == nil || pod.Labels == nil {
		return false
	}
	return pod.Labels[FakePodLabelKey] == FakePodLabelValue
}

func isOwnedByDeployment(rs *appsv1.ReplicaSet) bool {
	for _, owner := range rs.OwnerReferences {
		if owner.Kind == "Deployment" {
			return true
		}
	}
	return false
}
