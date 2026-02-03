/*
Copyright The Kubernetes Authors.

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

package provisioning

import (
	"context"

	corev1 "k8s.io/api/core/v1"
)

// PodInjector is an interface for injecting fake pods into the provisioning logic
// This allows providers to implement proactive scale-up by adding fake pods
// based on workload definitions (Deployments, Jobs, etc.) before real pods become pending
type PodInjector interface {
	// InjectPods takes the list of real pending pods and returns an augmented list
	// that includes fake pods for proactive scale-up. The fake pods should:
	// - Have resource requests matching expected workload
	// - Include scheduling constraints (node selectors, affinity, tolerations)
	// - Be clearly marked/labeled as fake pods
	// - Never be created as actual Kubernetes Pod objects
	InjectPods(ctx context.Context, realPods []*corev1.Pod) []*corev1.Pod
}

// DefaultPodInjector is a no-op implementation that doesn't inject any pods
type DefaultPodInjector struct{}

func (d *DefaultPodInjector) InjectPods(ctx context.Context, realPods []*corev1.Pod) []*corev1.Pod {
	return realPods
}
