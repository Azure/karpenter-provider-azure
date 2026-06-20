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

package windows_test

import (
	"fmt"
	"net/netip"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

const expectDualStackEnvVar = "E2E_EXPECT_DUAL_STACK"

func requireWindowsMachineAPICluster() {
	GinkgoHelper()
	if !env.IsAKSMachineAPIMode() {
		Skip("Windows node provisioning is only supported in AKS Machine API provision mode")
	}
	if env.MachineAgentPoolName != "aksmanagedap" {
		Skip("Windows machines require the reserved NAP-managed agent pool (aksmanagedap); skipping on custom machine pool")
	}
}

func requireDualStackCluster() {
	GinkgoHelper()
	if os.Getenv(expectDualStackEnvVar) != "true" {
		Skip(fmt.Sprintf("Windows dual-stack validation requires %s=true", expectDualStackEnvVar))
	}
}

func expectPodHasIPv4AndIPv6PodIPs(pod *corev1.Pod) {
	GinkgoHelper()
	Expect(hasIPv4AndIPv6PodIPs(pod.Status.PodIPs)).To(BeTrue(), "expected pod %s/%s to have IPv4 and IPv6 PodIPs, got %v", pod.Namespace, pod.Name, pod.Status.PodIPs)
}

func hasIPv4AndIPv6PodIPs(podIPs []corev1.PodIP) bool {
	var hasIPv4, hasIPv6 bool
	for _, podIP := range podIPs {
		addr, err := netip.ParseAddr(podIP.IP)
		if err != nil {
			continue
		}
		hasIPv4 = hasIPv4 || addr.Is4()
		hasIPv6 = hasIPv6 || addr.Is6()
	}
	return hasIPv4 && hasIPv6
}

func TestHasIPv4AndIPv6PodIPs(t *testing.T) {
	for _, tc := range []struct {
		name string
		ips  []corev1.PodIP
		want bool
	}{
		{name: "empty", ips: nil, want: false},
		{name: "ipv4 only", ips: []corev1.PodIP{{IP: "10.0.0.4"}}, want: false},
		{name: "ipv6 only", ips: []corev1.PodIP{{IP: "fd00::4"}}, want: false},
		{name: "invalid plus ipv6", ips: []corev1.PodIP{{IP: "not-an-ip"}, {IP: "fd00::4"}}, want: false},
		{name: "dual stack", ips: []corev1.PodIP{{IP: "10.0.0.4"}, {IP: "fd00::4"}}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasIPv4AndIPv6PodIPs(tc.ips); got != tc.want {
				t.Fatalf("hasIPv4AndIPv6PodIPs(%v) = %v, want %v", tc.ips, got, tc.want)
			}
		})
	}
}

var _ = Describe("Windows DualStack", func() {
	BeforeEach(func() {
		requireWindowsMachineAPICluster()
		requireDualStackCluster()
	})

	It("should provision a Windows node and assign IPv4 and IPv6 pod IPs", func() {
		nodeClass := env.WindowsNodeClass()
		nodePool := env.WindowsNodePool(nodeClass)
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      corev1.LabelTopologyZone,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{zones.Regional},
		})

		deployment := test.Deployment(test.DeploymentOptions{
			Replicas: 1,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "windows-dualstack"},
				},
				Image: windowsPauseImage,
				NodeSelector: map[string]string{
					corev1.LabelOSStable: string(corev1.Windows),
				},
				Tolerations: []corev1.Toleration{{
					Key:      corev1.LabelOSStable,
					Operator: corev1.TolerationOpEqual,
					Value:    string(corev1.Windows),
					Effect:   corev1.TaintEffectNoSchedule,
				}},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
			},
		})

		env.ExpectCreated(nodeClass, nodePool, deployment)

		pods := env.EventuallyExpectHealthyDeploymentWithTimeout(25*time.Minute, deployment)
		expectPodHasIPv4AndIPv6PodIPs(pods[0])
		env.ExpectCreatedNodeCount("==", 1)

		node := env.GetNode(pods[0].Spec.NodeName)
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelOSStable, string(corev1.Windows)))
		Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelOSSKU, v1beta1.OSSKUWindows2022))
		Expect(node.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))
	})
})
