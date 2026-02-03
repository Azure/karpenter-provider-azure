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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/Azure/karpenter-provider-azure/pkg/controllers/proactivescaleup"
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
			name: "pod with wrong label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Labels: map[string]string{
						"some-label": "value",
					},
				},
			},
			expected: false,
		},
		{
			name: "fake pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "fake-pod",
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

func TestCountPodsLogic(t *testing.T) {
	// Create test deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-deployment",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: func() *int32 { r := int32(10); return &r }(),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
						},
					},
				},
			},
		},
	}

	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		t.Fatalf("Failed to create selector: %v", err)
	}

	// Create test pods
	realPod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "real-pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app": "test",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	realPod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "real-pod-2",
			Namespace: "default",
			Labels: map[string]string{
				"app": "test",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}

	fakePod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fake-pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app":                            "test",
				proactivescaleup.FakePodLabelKey: proactivescaleup.FakePodLabelValue,
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}

	// Test counting
	allPods := []corev1.Pod{*realPod1, *realPod2, *fakePod1}
	
	realCount := 0
	fakeCount := 0
	for i := range allPods {
		pod := &allPods[i]
		if pod.Namespace != deployment.Namespace {
			continue
		}
		if !selector.Matches(labels.Set(pod.Labels)) {
			continue
		}
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		
		if proactivescaleup.IsFakePod(pod) {
			fakeCount++
		} else {
			realCount++
		}
	}

	if realCount != 2 {
		t.Errorf("Expected 2 real pods, got %d", realCount)
	}
	if fakeCount != 1 {
		t.Errorf("Expected 1 fake pod, got %d", fakeCount)
	}

	// Calculate gap
	desired := int(*deployment.Spec.Replicas)
	gap := desired - realCount - fakeCount
	
	expectedGap := 7 // 10 desired - 2 real - 1 fake = 7
	if gap != expectedGap {
		t.Errorf("Expected gap of %d, got %d", expectedGap, gap)
	}
}
