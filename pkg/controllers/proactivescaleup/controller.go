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
	"time"

	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
)

const (
	// FakePodLabelKey identifies fake pods for proactive scale-up
	FakePodLabelKey = "karpenter.azure.com/fake-pod"
	// FakePodLabelValue is the label value for fake pods
	FakePodLabelValue = "true"
	// FakePodOwnerLabel tracks which workload the fake pod is for
	FakePodOwnerLabel = "karpenter.azure.com/fake-pod-owner"
	// ReconcileInterval is how often the controller runs
	ReconcileInterval = 10 * time.Second
)

// Controller monitors workloads and injects fake pods for proactive scale-up
type Controller struct {
	kubeClient client.Client
}

// NewController creates a new proactive scale-up controller
func NewController(kubeClient client.Client) *Controller {
	return &Controller{
		kubeClient: kubeClient,
	}
}

// Reconcile monitors workloads and manages fake pods
func (c *Controller) Reconcile(ctx context.Context) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, "proactivescaleup")

	opts := options.FromContext(ctx)
	if opts == nil || !opts.ProactiveScaleupEnabled {
		// Feature disabled, clean up fake pods
		if err := c.cleanupAllFakePods(ctx); err != nil {
			return reconcile.Result{}, fmt.Errorf("cleaning up fake pods: %w", err)
		}
		return reconcile.Result{RequeueAfter: ReconcileInterval}, nil
	}

	// Get all workloads
	deployments := &appsv1.DeploymentList{}
	if err := c.kubeClient.List(ctx, deployments); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing deployments: %w", err)
	}

	replicaSets := &appsv1.ReplicaSetList{}
	if err := c.kubeClient.List(ctx, replicaSets); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing replicasets: %w", err)
	}

	statefulSets := &appsv1.StatefulSetList{}
	if err := c.kubeClient.List(ctx, statefulSets); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing statefulsets: %w", err)
	}

	jobs := &batchv1.JobList{}
	if err := c.kubeClient.List(ctx, jobs); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing jobs: %w", err)
	}

	// Get all pods
	pods := &corev1.PodList{}
	if err := c.kubeClient.List(ctx, pods); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing pods: %w", err)
	}

	// Get all nodes for limit checking
	nodes := &corev1.NodeList{}
	if err := c.kubeClient.List(ctx, nodes); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing nodes: %w", err)
	}

	// Check limits
	if len(nodes.Items) >= opts.NodeLimit {
		// At node limit, clean up fake pods
		if err := c.cleanupAllFakePods(ctx); err != nil {
			return reconcile.Result{}, fmt.Errorf("cleaning up fake pods due to node limit: %w", err)
		}
		return reconcile.Result{RequeueAfter: ReconcileInterval}, nil
	}

	// Count real and fake pods
	realPodCount, fakePodCount := c.countPods(pods.Items)
	
	if realPodCount+fakePodCount >= opts.PodInjectionLimit {
		// At pod limit, don't create more fake pods
		return reconcile.Result{RequeueAfter: ReconcileInterval}, nil
	}

	// Process each workload type and inject fake pods
	maxFakePodsToCreate := opts.PodInjectionLimit - realPodCount - fakePodCount
	
	// Process Deployments
	for i := range deployments.Items {
		if maxFakePodsToCreate <= 0 {
			break
		}
		created, err := c.processDeployment(ctx, &deployments.Items[i], pods.Items, maxFakePodsToCreate)
		if err != nil {
			return reconcile.Result{}, err
		}
		maxFakePodsToCreate -= created
	}

	// Process ReplicaSets (that aren't owned by Deployments)
	for i := range replicaSets.Items {
		if maxFakePodsToCreate <= 0 {
			break
		}
		if c.isOwnedByDeployment(&replicaSets.Items[i]) {
			continue // Skip, already handled via Deployment
		}
		created, err := c.processReplicaSet(ctx, &replicaSets.Items[i], pods.Items, maxFakePodsToCreate)
		if err != nil {
			return reconcile.Result{}, err
		}
		maxFakePodsToCreate -= created
	}

	// Process StatefulSets
	for i := range statefulSets.Items {
		if maxFakePodsToCreate <= 0 {
			break
		}
		created, err := c.processStatefulSet(ctx, &statefulSets.Items[i], pods.Items, maxFakePodsToCreate)
		if err != nil {
			return reconcile.Result{}, err
		}
		maxFakePodsToCreate -= created
	}

	// Process Jobs
	for i := range jobs.Items {
		if maxFakePodsToCreate <= 0 {
			break
		}
		created, err := c.processJob(ctx, &jobs.Items[i], pods.Items, maxFakePodsToCreate)
		if err != nil {
			return reconcile.Result{}, err
		}
		maxFakePodsToCreate -= created
	}

	return reconcile.Result{RequeueAfter: ReconcileInterval}, nil
}

// processDeployment checks if a deployment needs fake pods and creates them
func (c *Controller) processDeployment(ctx context.Context, deployment *appsv1.Deployment, allPods []corev1.Pod, maxCreate int) (int, error) {
	if deployment.Spec.Replicas == nil {
		return 0, nil
	}

	desired := int(*deployment.Spec.Replicas)
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return 0, fmt.Errorf("parsing selector: %w", err)
	}

	// Count existing real and fake pods for this deployment
	realCount, fakeCount := c.countPodsForWorkload(allPods, selector, deployment.Namespace)
	
	// Calculate gap
	gap := desired - realCount - fakeCount
	if gap <= 0 {
		// No gap, cleanup any excess fake pods
		return 0, c.cleanupFakePodsForWorkload(ctx, deployment.Namespace, selector)
	}

	// Cap at maxCreate
	if gap > maxCreate {
		gap = maxCreate
	}

	// Create fake pods
	created := 0
	for i := 0; i < gap; i++ {
		if err := c.createFakePodFromTemplate(ctx, &deployment.Spec.Template, deployment.Namespace, deployment.Name, "Deployment"); err != nil {
			return created, err
		}
		created++
	}

	return created, nil
}

// processReplicaSet checks if a replicaset needs fake pods
func (c *Controller) processReplicaSet(ctx context.Context, rs *appsv1.ReplicaSet, allPods []corev1.Pod, maxCreate int) (int, error) {
	if rs.Spec.Replicas == nil {
		return 0, nil
	}

	desired := int(*rs.Spec.Replicas)
	selector, err := metav1.LabelSelectorAsSelector(rs.Spec.Selector)
	if err != nil {
		return 0, fmt.Errorf("parsing selector: %w", err)
	}

	realCount, fakeCount := c.countPodsForWorkload(allPods, selector, rs.Namespace)
	
	gap := desired - realCount - fakeCount
	if gap <= 0 {
		return 0, c.cleanupFakePodsForWorkload(ctx, rs.Namespace, selector)
	}

	if gap > maxCreate {
		gap = maxCreate
	}

	created := 0
	for i := 0; i < gap; i++ {
		if err := c.createFakePodFromTemplate(ctx, &rs.Spec.Template, rs.Namespace, rs.Name, "ReplicaSet"); err != nil {
			return created, err
		}
		created++
	}

	return created, nil
}

// processStatefulSet checks if a statefulset needs fake pods
func (c *Controller) processStatefulSet(ctx context.Context, sts *appsv1.StatefulSet, allPods []corev1.Pod, maxCreate int) (int, error) {
	if sts.Spec.Replicas == nil {
		return 0, nil
	}

	desired := int(*sts.Spec.Replicas)
	selector, err := metav1.LabelSelectorAsSelector(sts.Spec.Selector)
	if err != nil {
		return 0, fmt.Errorf("parsing selector: %w", err)
	}

	realCount, fakeCount := c.countPodsForWorkload(allPods, selector, sts.Namespace)
	
	gap := desired - realCount - fakeCount
	if gap <= 0 {
		return 0, c.cleanupFakePodsForWorkload(ctx, sts.Namespace, selector)
	}

	if gap > maxCreate {
		gap = maxCreate
	}

	created := 0
	for i := 0; i < gap; i++ {
		if err := c.createFakePodFromTemplate(ctx, &sts.Spec.Template, sts.Namespace, sts.Name, "StatefulSet"); err != nil {
			return created, err
		}
		created++
	}

	return created, nil
}

// processJob checks if a job needs fake pods
func (c *Controller) processJob(ctx context.Context, job *batchv1.Job, allPods []corev1.Pod, maxCreate int) (int, error) {
	if job.Spec.Completions == nil {
		return 0, nil
	}

	desired := int(*job.Spec.Completions)
	selector, err := metav1.LabelSelectorAsSelector(job.Spec.Selector)
	if err != nil {
		return 0, fmt.Errorf("parsing selector: %w", err)
	}

	realCount, fakeCount := c.countPodsForWorkload(allPods, selector, job.Namespace)
	
	gap := desired - realCount - fakeCount
	if gap <= 0 {
		return 0, c.cleanupFakePodsForWorkload(ctx, job.Namespace, selector)
	}

	if gap > maxCreate {
		gap = maxCreate
	}

	created := 0
	for i := 0; i < gap; i++ {
		if err := c.createFakePodFromTemplate(ctx, &job.Spec.Template, job.Namespace, job.Name, "Job"); err != nil {
			return created, err
		}
		created++
	}

	return created, nil
}

// createFakePodFromTemplate creates a fake pod based on a pod template
func (c *Controller) createFakePodFromTemplate(ctx context.Context, template *corev1.PodTemplateSpec, namespace, ownerName, ownerKind string) error {
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
			GenerateName: fmt.Sprintf("fake-pod-%s-", ownerName),
			Namespace:    namespace,
			Labels: lo.Assign(template.Labels, map[string]string{
				FakePodLabelKey:   FakePodLabelValue,
				FakePodOwnerLabel: fmt.Sprintf("%s/%s/%s", ownerKind, namespace, ownerName),
			}),
			Annotations: lo.Assign(template.Annotations, map[string]string{
				"karpenter.azure.com/proactive-scaleup":  "true",
				"karpenter.azure.com/do-not-disrupt":     "true",
				"karpenter.azure.com/do-not-consolidate": "true",
			}),
		},
		Spec: corev1.PodSpec{
			// Use single pause container with same total resources
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
			// Use low priority so real pods preempt these
			Priority: lo.ToPtr(int32(-1000)),
			// Copy scheduling constraints from template
			NodeSelector:      template.Spec.NodeSelector,
			Affinity:          template.Spec.Affinity,
			Tolerations:       template.Spec.Tolerations,
			TopologySpreadConstraints: template.Spec.TopologySpreadConstraints,
			RestartPolicy:     corev1.RestartPolicyNever,
		},
	}

	if err := c.kubeClient.Create(ctx, fakePod); err != nil {
		return fmt.Errorf("creating fake pod: %w", err)
	}

	return nil
}

// countPods counts real and fake pods
func (c *Controller) countPods(pods []corev1.Pod) (real, fake int) {
	for i := range pods {
		if IsFakePod(&pods[i]) {
			fake++
		} else {
			real++
		}
	}
	return
}

// countPodsForWorkload counts real and fake pods matching a selector
func (c *Controller) countPodsForWorkload(pods []corev1.Pod, selector labels.Selector, namespace string) (real, fake int) {
	for i := range pods {
		pod := &pods[i]
		if pod.Namespace != namespace {
			continue
		}
		if !selector.Matches(labels.Set(pod.Labels)) {
			continue
		}
		// Don't count completed/failed pods
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		
		if IsFakePod(pod) {
			fake++
		} else {
			real++
		}
	}
	return
}

// isOwnedByDeployment checks if a ReplicaSet is owned by a Deployment
func (c *Controller) isOwnedByDeployment(rs *appsv1.ReplicaSet) bool {
	for _, owner := range rs.OwnerReferences {
		if owner.Kind == "Deployment" {
			return true
		}
	}
	return false
}

// cleanupFakePodsForWorkload removes fake pods for a specific workload
func (c *Controller) cleanupFakePodsForWorkload(ctx context.Context, namespace string, selector labels.Selector) error {
	fakePods := &corev1.PodList{}
	if err := c.kubeClient.List(ctx, fakePods,
		client.InNamespace(namespace),
		client.MatchingLabels{FakePodLabelKey: FakePodLabelValue},
	); err != nil {
		return err
	}

	for i := range fakePods.Items {
		pod := &fakePods.Items[i]
		if selector.Matches(labels.Set(pod.Labels)) {
			if err := c.kubeClient.Delete(ctx, pod); err != nil && !errors.IsNotFound(err) {
				return err
			}
		}
	}

	return nil
}

// cleanupAllFakePods removes all fake pods
func (c *Controller) cleanupAllFakePods(ctx context.Context) error {
	fakePods := &corev1.PodList{}
	if err := c.kubeClient.List(ctx, fakePods, client.MatchingLabels{
		FakePodLabelKey: FakePodLabelValue,
	}); err != nil {
		return err
	}

	for i := range fakePods.Items {
		if err := c.kubeClient.Delete(ctx, &fakePods.Items[i]); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

// IsFakePod checks if a pod is a fake pod
func IsFakePod(pod *corev1.Pod) bool {
	if pod == nil || pod.Labels == nil {
		return false
	}
	return pod.Labels[FakePodLabelKey] == FakePodLabelValue
}

func (c *Controller) Register(ctx context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("proactivescaleup").
		WatchesRawSource(&timedSource{}).
		WithOptions(controller.Options{
			RateLimiter:             reasonable.RateLimiter(),
			MaxConcurrentReconciles: 1,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}

// timedSource triggers reconciliation at regular intervals
type timedSource struct{}

func (s *timedSource) Start(ctx context.Context, handler any) error {
	ticker := time.NewTicker(ReconcileInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Reconciliation happens via RequeueAfter
			}
		}
	}()
	return nil
}
