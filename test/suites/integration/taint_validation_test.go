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
	corev1 "k8s.io/api/core/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Taint Validation", func() {
	It("should reject a NodePool with kubernetes.azure.com/ taints", func() {
		nodePool.Spec.Template.Spec.Taints = []corev1.Taint{
			{
				Key:    "kubernetes.azure.com/scalesetpriority",
				Value:  "spot",
				Effect: corev1.TaintEffectNoSchedule,
			},
		}
		err := env.Client.Create(env.Context, nodePool)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("kubernetes.azure.com"))
	})

	It("should reject a NodePool with kubernetes.azure.com/ startup taints", func() {
		nodePool.Spec.Template.Spec.StartupTaints = []corev1.Taint{
			{
				Key:    "kubernetes.azure.com/scalesetpriority",
				Value:  "spot",
				Effect: corev1.TaintEffectNoSchedule,
			},
		}
		err := env.Client.Create(env.Context, nodePool)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("kubernetes.azure.com"))
	})

	It("should accept a NodePool without kubernetes.azure.com/ taints", func() {
		nodePool.Spec.Template.Spec.Taints = []corev1.Taint{
			{
				Key:    "example.com/custom-taint",
				Value:  "true",
				Effect: corev1.TaintEffectNoSchedule,
			},
		}
		env.ExpectCreated(nodeClass, nodePool)
	})
})
