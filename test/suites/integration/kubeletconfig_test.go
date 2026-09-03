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
	"fmt"
	"time"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("KubeletConfig", func() {
	It("should apply kubeReserved and evictionHard overrides to the node", func() {
		nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
			KubeReserved: map[string]v1beta1.KubeReservedValue{
				"cpu":    "250m",
				"memory": "512Mi",
			},
			EvictionHard: map[string]v1beta1.EvictionHardValue{
				"memory.available":  "333Mi",
				"nodefs.available":  "12%",
				"nodefs.inodesFree": "7%",
			},
			EvictionSoft: map[string]v1beta1.EvictionSoftValue{
				"memory.available": "500Mi",
			},
			EvictionSoftGracePeriod: map[string]metav1.Duration{
				"memory.available": {Duration: 90 * time.Second},
			},
			EvictionMaxPodGracePeriod: lo.ToPtr(int32(120)),
		}

		pod := coretest.UnschedulablePod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		By(fmt.Sprintf("Node %s provisioned, verifying kubelet flags", node.Name))
		verifyPod := createKubeletFlagsVerificationPod(node.Name)
		env.ExpectCreated(verifyPod)
		defer env.ExpectDeleted(verifyPod)

		flags := eventuallyGetPodLogs(verifyPod)
		Expect(flags).To(ContainSubstring("--kube-reserved="))
		Expect(flags).To(ContainSubstring("cpu=250m"))
		Expect(flags).To(ContainSubstring("memory=512Mi"))
		Expect(flags).To(ContainSubstring("--eviction-hard="))
		Expect(flags).To(ContainSubstring("memory.available<333Mi"))
		Expect(flags).To(ContainSubstring("nodefs.available<12%"))
		Expect(flags).To(ContainSubstring("nodefs.inodesFree<7%"))
		Expect(flags).To(ContainSubstring("--eviction-soft="))
		Expect(flags).To(ContainSubstring("memory.available<500Mi"))
		Expect(flags).To(ContainSubstring("--eviction-max-pod-grace-period=120"))
	})
})

func createKubeletFlagsVerificationPod(nodeName string) *corev1.Pod {
	pod := env.Pod(coretest.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "kubelet-flags-verify-",
			Namespace:    "default",
		},
		Image: "mcr.microsoft.com/azurelinux/busybox:1.36",
		Command: []string{
			"sh", "-c",
			"pid=$(pidof kubelet | awk '{print $1}'); test -n \"$pid\" && tr '\\0' ' ' < /proc/$pid/cmdline",
		},
		NodeSelector: map[string]string{
			corev1.LabelHostname: nodeName,
		},
		RestartPolicy: corev1.RestartPolicyNever,
	})
	pod.Spec.HostPID = true
	pod.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{
		Privileged: lo.ToPtr(true),
	}
	return pod
}
