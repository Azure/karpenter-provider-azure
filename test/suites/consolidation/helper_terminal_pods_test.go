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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// terminalPodCleanupTarget identifies an owner, not a serving rollout. Generation,
// template, replica status and readiness are deliberately not cleanup inputs.
type terminalPodCleanupTarget struct {
	key types.NamespacedName
	uid types.UID
}

// normalizeTerminalDeploymentPods removes only terminal Pods with a verified
// current Deployment -> ReplicaSet -> Pod controller identity chain. Evidence is
// recorded before each UID/RV-fenced delete. A retained object or any uncertain
// observation fails closed; this single pass does not retry writes or change the
// caller's setup deadline. The helper is not yet wired into the live suite.
func normalizeTerminalDeploymentPods(ctx context.Context, kube kubernetes.Interface, target terminalPodCleanupTarget, record func(*corev1.Pod) error) error {
	if ctx == nil {
		return fmt.Errorf("terminal Pod cleanup requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if kube == nil || record == nil {
		return fmt.Errorf("terminal Pod cleanup requires a Kubernetes client and evidence recorder")
	}
	if target.key.Namespace == "" || target.key.Name == "" || target.uid == "" {
		return fmt.Errorf("terminal Pod cleanup requires an exact Deployment namespace, name and UID: %s uid=%s", target.key, target.uid)
	}
	deployment, err := kube.AppsV1().Deployments(target.key.Namespace).Get(ctx, target.key.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get cleanup Deployment %s uid=%s: %w", target.key, target.uid, err)
	}
	if deployment == nil || deployment.Namespace != target.key.Namespace || deployment.Name != target.key.Name || deployment.UID != target.uid {
		return fmt.Errorf("cleanup Deployment GET did not return expected identity %s uid=%s", target.key, target.uid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pods, err := kube.CoreV1().Pods(target.key.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list Pods for cleanup Deployment %s uid=%s: %w", target.key, target.uid, err)
	}
	if pods == nil {
		return fmt.Errorf("Pod list returned nil for cleanup Deployment %s", target.key)
	}
	for i := range pods.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		listed := &pods.Items[i]
		if listed.Status.Phase != corev1.PodFailed && listed.Status.Phase != corev1.PodSucceeded {
			continue
		}
		if listed.Namespace != target.key.Namespace || listed.Name == "" || listed.UID == "" || listed.ResourceVersion == "" {
			return fmt.Errorf("terminal Pod list has incomplete or foreign identity: %s/%s uid=%s rv=%s", listed.Namespace, listed.Name, listed.UID, listed.ResourceVersion)
		}
		pod, err := kube.CoreV1().Pods(listed.Namespace).Get(ctx, listed.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue // The listed object disappeared without requiring a delete.
		}
		if err != nil {
			return fmt.Errorf("get terminal Pod %s/%s uid=%s: %w", listed.Namespace, listed.Name, listed.UID, err)
		}
		if pod == nil || pod.Namespace != listed.Namespace || pod.Name != listed.Name || pod.UID != listed.UID || pod.ResourceVersion == "" {
			return fmt.Errorf("terminal Pod GET did not preserve identity %s/%s uid=%s", listed.Namespace, listed.Name, listed.UID)
		}
		if pod.Status.Phase != corev1.PodFailed && pod.Status.Phase != corev1.PodSucceeded {
			continue // A stale terminal observation cannot authorize an active Pod delete.
		}
		controllers := 0
		for _, owner := range pod.OwnerReferences {
			if owner.Controller != nil && *owner.Controller {
				controllers++
			}
		}
		if controllers > 1 {
			return fmt.Errorf("multiple controlling owners for terminal Pod %s/%s", pod.Namespace, pod.Name)
		}
		// A different controller kind cannot be a child through a ReplicaSet.
		// Keep DaemonSets, Jobs and other foreign workloads untouched.
		if owner := metav1.GetControllerOf(pod); owner != nil && owner.Kind != "ReplicaSet" {
			continue
		}
		rsOwner, err := budgetPlacementController(pod, "ReplicaSet")
		if err != nil {
			return fmt.Errorf("resolve terminal Pod controller: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rs, err := kube.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, rsOwner.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get terminal Pod ReplicaSet %s/%s uid=%s: %w", pod.Namespace, rsOwner.Name, rsOwner.UID, err)
		}
		if rs == nil || rs.Namespace != pod.Namespace || rs.Name != rsOwner.Name || rs.UID != rsOwner.UID {
			return fmt.Errorf("terminal Pod ReplicaSet GET did not return expected identity %s/%s uid=%s", pod.Namespace, rsOwner.Name, rsOwner.UID)
		}
		deploymentOwner, err := budgetPlacementController(rs, "Deployment")
		if err != nil {
			return fmt.Errorf("resolve terminal Pod Deployment controller: %w", err)
		}
		if deploymentOwner.UID != target.uid {
			continue // Same labels or even the same Deployment name do not confer ownership.
		}
		if deploymentOwner.Name != target.key.Name {
			return fmt.Errorf("terminal Pod ReplicaSet references Deployment uid=%s with name %s, expected %s", target.uid, deploymentOwner.Name, target.key.Name)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := record(pod.DeepCopy()); err != nil {
			return fmt.Errorf("record terminal Pod %s/%s uid=%s evidence: %w", pod.Namespace, pod.Name, pod.UID, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		uid, rv := pod.UID, pod.ResourceVersion
		if err := kube.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
			Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv},
		}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete terminal Pod %s/%s uid=%s rv=%s: %w", pod.Namespace, pod.Name, uid, rv, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining, err := kube.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err := ctx.Err(); err != nil {
			return err
		}
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("confirm terminal Pod %s/%s uid=%s absence: %w", pod.Namespace, pod.Name, uid, err)
		}
		if remaining == nil {
			return fmt.Errorf("terminal Pod %s/%s absence GET returned nil without NotFound", pod.Namespace, pod.Name)
		}
		// Never strip finalizers, adopt a replacement UID, or equate an accepted
		// delete with absence. Leave the remaining object untouched and report it.
		return fmt.Errorf("terminal Pod %s/%s still exists after deleting uid=%s: observed uid=%s rv=%s", pod.Namespace, pod.Name, uid, remaining.UID, remaining.ResourceVersion)
	}
	return ctx.Err()
}
