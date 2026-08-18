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

	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

const expectDualStackEnvVar = "E2E_EXPECT_DUAL_STACK"

func requireDualStackCluster() {
	GinkgoHelper()
	if os.Getenv(expectDualStackEnvVar) != "true" {
		Skip(fmt.Sprintf("dual-stack validation requires %s=true", expectDualStackEnvVar))
	}
}

func expectPodHasIPv4AndIPv6PodIPs(pod *corev1.Pod) {
	GinkgoHelper()
	Expect(hasIPv4AndIPv6PodIPs(pod.Status.PodIPs)).To(BeTrue(), "expected pod %s/%s to have IPv4 and IPv6 PodIPs, got %v", pod.Namespace, pod.Name, pod.Status.PodIPs)
}

func hasIPv4AndIPv6PodIPs(podIPs []corev1.PodIP) bool {
	addresses := make([]string, 0, len(podIPs))
	for _, podIP := range podIPs {
		addresses = append(addresses, podIP.IP)
	}
	return hasIPv4AndIPv6Addresses(addresses)
}

func hasIPv4AndIPv6NodeInternalIPs(node *corev1.Node) bool {
	addresses := make([]string, 0, len(node.Status.Addresses))
	for _, address := range node.Status.Addresses {
		if address.Type == corev1.NodeInternalIP {
			addresses = append(addresses, address.Address)
		}
	}
	return hasIPv4AndIPv6Addresses(addresses)
}

func hasIPv4AndIPv6Addresses(addresses []string) bool {
	var hasIPv4, hasIPv6 bool
	for _, address := range addresses {
		addr, err := netip.ParseAddr(address)
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

var _ = Describe("IPv6 DualStack", func() {
	BeforeEach(func() {
		requireDualStackCluster()
	})

	It("should provision a Linux node with IPv4 and IPv6 node and pod addresses", func() {
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      corev1.LabelTopologyZone,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{zones.Regional},
		})

		deployment := test.Deployment(test.DeploymentOptions{
			Replicas: 1,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "linux-dualstack"},
				},
				NodeSelector: map[string]string{
					corev1.LabelOSStable: string(corev1.Linux),
				},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
			},
		})

		env.ExpectCreated(nodeClass, nodePool, deployment)

		pods := env.EventuallyExpectHealthyDeploymentWithTimeout(15*time.Minute, deployment)
		expectPodHasIPv4AndIPv6PodIPs(pods[0])
		env.ExpectCreatedNodeCount("==", 1)

		var node *corev1.Node
		Eventually(func(g Gomega) {
			node = env.GetNode(pods[0].Spec.NodeName)
			g.Expect(hasIPv4AndIPv6NodeInternalIPs(node)).To(BeTrue(), "expected node %s to have IPv4 and IPv6 InternalIPs, got %v", node.Name, node.Status.Addresses)
		}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second)
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelOSStable, string(corev1.Linux)))
		Expect(node.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))
	})
})
