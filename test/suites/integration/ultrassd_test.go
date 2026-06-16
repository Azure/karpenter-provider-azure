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
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("UltraSSD", func() {
	BeforeEach(func() {
		if !env.IsMachineModeOrNPS() {
			Skip("UltraSSD tests require NPS (Node Provisioning Service) - only supported in NAP/managed Karpenter mode")
		}
	})

	It("should enable UltraSSD when explicitly enabled", func() {
		enabled := true
		nodeClass.Spec.UltraSSD = &v1beta1.UltraSSD{
			Enabled: &enabled,
		}

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)

		env.EventuallyExpectInitializedNodeCount("==", 1)
		node := env.GetNode(pods[0].Spec.NodeName)
		verifyUltraSSDOnNode(node, true)
	})

	It("should disable UltraSSD when explicitly disabled", func() {
		enabled := false
		nodeClass.Spec.UltraSSD = &v1beta1.UltraSSD{
			Enabled: &enabled,
		}

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)

		env.EventuallyExpectInitializedNodeCount("==", 1)
		node := env.GetNode(pods[0].Spec.NodeName)
		verifyUltraSSDOnNode(node, false)
	})
})

func verifyUltraSSDOnNode(node *corev1.Node, expected bool) {
	vm := env.GetVM(node.Name)
	Expect(vm.Properties).ToNot(BeNil())

	if expected {
		Expect(vm.Properties.AdditionalCapabilities).ToNot(BeNil())
		Expect(vm.Properties.AdditionalCapabilities.UltraSSDEnabled).ToNot(BeNil())
		Expect(*vm.Properties.AdditionalCapabilities.UltraSSDEnabled).To(BeTrue())
		return
	}

	if vm.Properties.AdditionalCapabilities == nil || vm.Properties.AdditionalCapabilities.UltraSSDEnabled == nil {
		return
	}
	Expect(*vm.Properties.AdditionalCapabilities.UltraSSDEnabled).To(BeFalse())
}
