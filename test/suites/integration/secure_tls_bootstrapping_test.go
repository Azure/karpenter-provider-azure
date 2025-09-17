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

package integration_test

import (
	. "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

var _ = FDescribe("Secure TLS Bootstrapping", func() {

	It("should provision nodes successfully with secure TLS bootstrapping enabled", func() {
		if !env.InClusterController {
			Skip("Test requires InClusterController mode to set --enable-secure-tls-bootstrapping flag")
		}
		env.ExpectSettingsOverridden(
			corev1.EnvVar{Name: "ENABLE_SECURE_TLS_BOOTSTRAPPING", Value: "true"},
		)
		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)
	})

	// disable until secure TLS bootstrapping is fully rolled out
	PIt("should provision nodes without requiring bootstrap token when secure TLS bootstrapping is enabled", func() {
		if !env.InClusterController {
			Skip("Test requires InClusterController mode to set --enable-secure-tls-bootstrapping flag")
		}
		env.ExpectSettingsOverridden(
			corev1.EnvVar{Name: "ENABLE_SECURE_TLS_BOOTSTRAPPING", Value: "true"},
			corev1.EnvVar{Name: "KUBELET_BOOTSTRAP_TOKEN", Value: ""},
		)
		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)
	})
})
