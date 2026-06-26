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
	"time"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Kata (Pod Sandboxing)", func() {
	BeforeEach(func() {
		// Kata can only be provisioned via the AKS Machine API path.
		if !env.IsMachineModeOrNPS() {
			Skip("Kata Pod Sandboxing requires an AKS Machine API provision mode (or NPS)")
		}
		// The feature is gated off by default; enable it on the running Karpenter for these tests.
		// ExpectSettingsOverridden only restarts Karpenter when the value actually changes, so this
		// is a no-op (and free) on the second spec.
		env.ExpectSettingsOverridden(corev1.EnvVar{Name: "ENABLE_KATA_POD_SANDBOXING", Value: "true"})

		// Kata requires AzureLinux + a gen-2, nested-virt-capable SKU. Constrain to a known gen-2 SKU
		// for determinism and to keep cost/scheduling predictable.
		nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
		nodeClass.Spec.WorkloadRuntime = lo.ToPtr(v1beta1.WorkloadRuntimeKataVMIsolation)
		nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      corev1.LabelInstanceTypeStable,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{"Standard_D2_v5"},
		})
	})

	AfterEach(func() {
		env.ExpectSettingsRemoved(corev1.EnvVar{Name: "ENABLE_KATA_POD_SANDBOXING"})
	})

	// This is the open question from PR #1721: Karpenter advertises and stamps BOTH the new
	// (kata-vm-isolation) and legacy (kata-mshv-vm-isolation) labels, but AKS only stamps the real
	// label derived from the WorkloadRuntime enum it receives. If the AKS RP prunes
	// kubernetes.azure.com/* labels it didn't set, the spelling Karpenter projected but AKS didn't
	// stamp would disappear from the real Node — a mismatch that can register as drift. This asserts
	// both labels survive on the real Node.
	It("should provision a Kata node that keeps both kata labels (new and legacy)", func() {
		// Select via the new-spelling label to mirror a real Pod Sandboxing workload.
		deployment := coretest.Deployment(coretest.DeploymentOptions{
			Replicas: 1,
			PodOptions: coretest.PodOptions{
				NodeSelector: map[string]string{v1beta1.AKSLabelKataVMIsolation: "true"},
			},
		})

		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)
		env.EventuallyExpectInitializedNodeCount("==", 1)

		node := env.GetNode(pods[0].Spec.NodeName)
		Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelKataVMIsolation, "true"))
		Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelKataMshvVMIsolation, "true"))
	})

	// If AKS prunes the unstamped spelling, this guards against the consequence: Karpenter must not
	// treat the node as drifted (which would recycle it). Consistently asserts the node is not marked
	// drifted over a window that comfortably covers a drift reconcile.
	It("should not flag the Kata node as drifted", func() {
		deployment := coretest.Deployment(coretest.DeploymentOptions{
			Replicas: 1,
			PodOptions: coretest.PodOptions{
				NodeSelector: map[string]string{v1beta1.AKSLabelKataVMIsolation: "true"},
			},
		})

		env.ExpectCreated(nodeClass, nodePool, deployment)
		env.EventuallyExpectHealthyDeployment(deployment)
		env.EventuallyExpectInitializedNodeCount("==", 1)
		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]

		env.ConsistentlyExpectNoDisruptions(1, 2*time.Minute)
		// Belt and braces: the NodeClaim should not carry the Drifted status condition.
		Expect(nodeClaim.StatusConditions().Get(karpv1.ConditionTypeDrifted).IsTrue()).To(BeFalse())
	})
})
