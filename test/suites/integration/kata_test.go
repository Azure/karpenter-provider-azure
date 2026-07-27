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
	"bytes"
	"context"
	"io"
	"strings"
	"time"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// kataRuntimeClassName is the RuntimeClass AKS creates on clusters with Pod Sandboxing enabled.
const kataRuntimeClassName = "kata-vm-isolation"

var _ = Describe("Kata (Pod Sandboxing)", func() {
	BeforeEach(func() {
		// Kata can only be provisioned on a provision mode that can express the workload runtime.
		if !env.IsMachineModeOrNPS() {
			Skip("Kata Pod Sandboxing requires an AKS Machine API provision mode (or NPS)")
		}

		// Kata requires AzureLinux and a nested-virt-capable gen-2 SKU. Constrain to a known-good SKU
		// for determinism and to keep cost predictable.
		nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
		nodeClass.Spec.WorkloadRuntime = lo.ToPtr(v1beta1.WorkloadRuntimeKataVMIsolation)
		nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      corev1.LabelInstanceTypeStable,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{"Standard_D4s_v5"},
		})
	})

	// The point of the feature: a pod that asks for the Kata RuntimeClass must actually run inside a
	// sandbox VM. Note there is no nodeSelector on the pod — the RuntimeClass admission controller
	// injects the kata node selector, which is what Karpenter's advertised labels have to match for
	// scale-up to happen at all.
	It("should run a runtimeClassName pod inside a sandbox VM", func() {
		pod := env.Pod(coretest.PodOptions{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "kata-sandbox-", Namespace: "default"},
			Image:      "mcr.microsoft.com/cbl-mariner/base/core:2.0",
			// Print the guest kernel so we can compare it against the host's.
			Command:       []string{"sh", "-c", "uname -r && sleep 300"},
			RestartPolicy: corev1.RestartPolicyNever,
		})
		pod.Spec.RuntimeClassName = lo.ToPtr(kataRuntimeClassName)

		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.EventuallyExpectInitializedNodeCount("==", 1)

		Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
		node := env.GetNode(pod.Spec.NodeName)

		// Karpenter's advertised labels are what let the injected nodeSelector match.
		Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelKataVMIsolation, "true"))

		// A Kata pod runs its own kernel, so the guest kernel must differ from the node's. This is the
		// check that distinguishes a real sandbox from a pod that silently fell back to runc.
		guestKernel := strings.TrimSpace(strings.SplitN(podLogs(pod), "\n", 2)[0])
		By("guest kernel: " + guestKernel + ", host kernel: " + node.Status.NodeInfo.KernelVersion)
		Expect(guestKernel).ToNot(BeEmpty())
		Expect(guestKernel).ToNot(Equal(node.Status.NodeInfo.KernelVersion),
			"pod kernel matches the host kernel, so it did not run in a Kata sandbox VM")
	})

	// Karpenter advertises and stamps BOTH the current (kata-vm-isolation) and legacy
	// (kata-mshv-vm-isolation) labels, but AKS only stamps the label derived from the WorkloadRuntime
	// enum it receives. If the AKS RP pruned kubernetes.azure.com/* labels it didn't set, the spelling
	// Karpenter projected but AKS didn't stamp would disappear from the real Node — a mismatch that can
	// register as drift. This asserts both labels survive on the real Node and that it isn't drifted.
	It("should keep both kata labels and not flag the node as drifted", func() {
		deployment := coretest.Deployment(coretest.DeploymentOptions{
			Replicas: 1,
			PodOptions: coretest.PodOptions{
				NodeSelector: map[string]string{v1beta1.AKSLabelKataVMIsolation: "true"},
			},
		})

		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)
		env.EventuallyExpectInitializedNodeCount("==", 1)
		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]

		node := env.GetNode(pods[0].Spec.NodeName)
		Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelKataVMIsolation, "true"))
		Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelKataMshvVMIsolation, "true"))

		env.ConsistentlyExpectNoDisruptions(1, 2*time.Minute)
		Expect(nodeClaim.StatusConditions().Get(karpv1.ConditionTypeDrifted).IsTrue()).To(BeFalse())
	})
})

// podLogs returns the pod's current logs, waiting for the pod to start producing them.
func podLogs(pod *corev1.Pod) string {
	var logs string
	Eventually(func(g Gomega) {
		g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
		g.Expect(pod.Status.Phase).To(Or(Equal(corev1.PodRunning), Equal(corev1.PodSucceeded)))

		req := env.KubeClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
			Container: pod.Spec.Containers[0].Name,
		})
		stream, err := req.Stream(context.Background())
		g.Expect(err).To(Succeed())
		defer stream.Close()

		buf := new(bytes.Buffer)
		_, err = io.Copy(buf, stream)
		g.Expect(err).To(Succeed())
		logs = buf.String()
		g.Expect(strings.TrimSpace(logs)).ToNot(BeEmpty())
	}).WithTimeout(2 * time.Minute).Should(Succeed())
	return logs
}
