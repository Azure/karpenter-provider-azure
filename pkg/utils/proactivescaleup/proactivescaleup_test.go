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

package proactivescaleup_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/utils/proactivescaleup"
)

func TestIsFakePod(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name:     "nil pod",
			pod:      nil,
			expected: false,
		},
		{
			name: "pod without labels",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
				},
			},
			expected: false,
		},
		{
			name: "pod with wrong label value",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Labels: map[string]string{
						proactivescaleup.FakePodLabelKey: "wrong-value",
					},
				},
			},
			expected: false,
		},
		{
			name: "valid fake pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Labels: map[string]string{
						proactivescaleup.FakePodLabelKey: proactivescaleup.FakePodLabelValue,
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := proactivescaleup.IsFakePod(tt.pod)
			if result != tt.expected {
				t.Errorf("IsFakePod() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCreatePlaceholderPodSpec(t *testing.T) {
	pod := proactivescaleup.CreatePlaceholderPodSpec("test-pod", "default", "100m", "128Mi")

	if pod == nil {
		t.Fatal("CreatePlaceholderPodSpec() returned nil")
	}

	if pod.Name != "test-pod" {
		t.Errorf("pod.Name = %v, want %v", pod.Name, "test-pod")
	}

	if pod.Namespace != "default" {
		t.Errorf("pod.Namespace = %v, want %v", pod.Namespace, "default")
	}

	if pod.Spec.Priority == nil || *pod.Spec.Priority != -1000 {
		t.Errorf("pod.Spec.Priority = %v, want %v", pod.Spec.Priority, -1000)
	}

	if pod.Labels[proactivescaleup.FakePodLabelKey] != proactivescaleup.FakePodLabelValue {
		t.Errorf("pod label not set correctly")
	}

	if len(pod.Spec.Containers) != 1 {
		t.Errorf("expected 1 container, got %v", len(pod.Spec.Containers))
	}

	container := pod.Spec.Containers[0]
	if container.Name != "pause" {
		t.Errorf("container.Name = %v, want %v", container.Name, "pause")
	}

	cpuRequest := container.Resources.Requests[corev1.ResourceCPU]
	if cpuRequest.String() != "100m" {
		t.Errorf("CPU request = %v, want %v", cpuRequest.String(), "100m")
	}

	memRequest := container.Resources.Requests[corev1.ResourceMemory]
	if memRequest.String() != "128Mi" {
		t.Errorf("Memory request = %v, want %v", memRequest.String(), "128Mi")
	}

	if len(pod.Spec.Tolerations) != 1 {
		t.Errorf("expected 1 toleration, got %v", len(pod.Spec.Tolerations))
	}

	if pod.Spec.Tolerations[0].Operator != corev1.TolerationOpExists {
		t.Errorf("toleration operator = %v, want %v", pod.Spec.Tolerations[0].Operator, corev1.TolerationOpExists)
	}

	// Verify it's detected as a fake pod
	if !proactivescaleup.IsFakePod(pod) {
		t.Error("CreatePlaceholderPodSpec() created pod that IsFakePod() doesn't recognize")
	}
}
