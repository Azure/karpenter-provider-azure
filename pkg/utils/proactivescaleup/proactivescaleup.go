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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// FakePodLabelKey is the label key used to identify fake pods for proactive scale-up
	FakePodLabelKey = "karpenter.azure.com/proactive-scaleup"
	// FakePodLabelValue is the label value for fake pods
	FakePodLabelValue = "placeholder"
	// FakePodAnnotationKey is the annotation key for fake pods
	FakePodAnnotationKey = "karpenter.azure.com/proactive-scaleup-pod"
)

// IsFakePod checks if a pod is a fake pod created for proactive scale-up
func IsFakePod(pod *corev1.Pod) bool {
	if pod == nil || pod.Labels == nil {
		return false
	}
	return pod.Labels[FakePodLabelKey] == FakePodLabelValue
}

// CreatePlaceholderPodSpec creates a pod specification for proactive scale-up.
// These pods have very low priority so they can be preempted by real workloads.
//
// Parameters:
//   - name: The name for the placeholder pod
//   - namespace: The namespace where the pod should be created
//   - cpu: CPU request (e.g., "100m")
//   - memory: Memory request (e.g., "128Mi")
//
// Returns a pod specification that can be used to create a placeholder pod.
func CreatePlaceholderPodSpec(name, namespace, cpu, memory string) *corev1.Pod {
	priority := int32(-1000)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				FakePodLabelKey: FakePodLabelValue,
			},
			Annotations: map[string]string{
				FakePodAnnotationKey: "true",
			},
		},
		Spec: corev1.PodSpec{
			// Use very low priority so real pods can preempt these placeholder pods
			Priority: &priority,
			Containers: []corev1.Container{
				{
					Name:  "pause",
					Image: "registry.k8s.io/pause:3.9",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(cpu),
							corev1.ResourceMemory: resource.MustParse(memory),
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
			// Allow scheduling on any node
			Tolerations: []corev1.Toleration{
				{
					Operator: corev1.TolerationOpExists,
				},
			},
		},
	}
}
