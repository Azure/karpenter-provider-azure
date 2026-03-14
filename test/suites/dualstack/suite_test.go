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

package dualstack_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

var env *azure.Environment
var nodeClass *v1beta1.AKSNodeClass
var nodePool *karpv1.NodePool

func TestDualStack(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	AfterSuite(func() {
		env.Stop()
	})
	RunSpecs(t, "DualStack")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

// hasIPv4 returns true if the given IP string is IPv4
func hasIPv4(ip net.IP) bool { return ip.To4() != nil }

// hasIPv6 returns true if the given IP string is IPv6 (and not IPv4-mapped)
func hasIPv6(ip net.IP) bool { return ip.To4() == nil && ip.To16() != nil }

var _ = Describe("DualStack", func() {
	It("should provision a node with both IPv4 and IPv6 addresses and schedule dual-stack pods", func() {
		dep := coretest.Deployment(coretest.DeploymentOptions{
			Replicas: 1,
			PodOptions: coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "dualstack-test"},
				},
			},
		})
		selector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
		env.ExpectCreated(nodePool, nodeClass, dep)

		env.EventuallyExpectHealthyPodCount(selector, 1)
		nodes := env.ExpectCreatedNodeCount("==", 1)
		node := nodes[0]

		By("verifying the node was provisioned by Karpenter")
		Expect(node.Labels).To(HaveKey(karpv1.NodePoolLabelKey),
			"node should have karpenter.sh/nodepool label")

		By("verifying the node has both IPv4 and IPv6 internal IPs")
		var nodeIPv4, nodeIPv6 bool
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				ip := net.ParseIP(addr.Address)
				Expect(ip).ToNot(BeNil(), "failed to parse node IP: %s", addr.Address)
				if hasIPv4(ip) {
					nodeIPv4 = true
				} else if hasIPv6(ip) {
					nodeIPv6 = true
				}
			}
		}
		Expect(nodeIPv4).To(BeTrue(), "node should have an IPv4 internal IP")
		Expect(nodeIPv6).To(BeTrue(), "node should have an IPv6 internal IP")

		By("verifying pods have dual-stack IPs")
		pods := env.EventuallyExpectHealthyPodCount(selector, 1)
		pod := pods[0]
		var podIPv4, podIPv6 bool
		for _, podIP := range pod.Status.PodIPs {
			ip := net.ParseIP(podIP.IP)
			Expect(ip).ToNot(BeNil(), "failed to parse pod IP: %s", podIP.IP)
			if hasIPv4(ip) {
				podIPv4 = true
			} else if hasIPv6(ip) {
				podIPv6 = true
			}
		}
		Expect(podIPv4).To(BeTrue(), "pod should have an IPv4 IP")
		Expect(podIPv6).To(BeTrue(), "pod should have an IPv6 IP")

		By("verifying a dual-stack service gets both IPv4 and IPv6 ClusterIPs")
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dualstack-svc",
				Namespace: "default",
			},
			Spec: corev1.ServiceSpec{
				IPFamilyPolicy: func() *corev1.IPFamilyPolicy { p := corev1.IPFamilyPolicyRequireDualStack; return &p }(),
				IPFamilies:     []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol},
				Selector:       map[string]string{"app": "dualstack-test"},
				Ports: []corev1.ServicePort{
					{Port: 80, TargetPort: intstr.FromInt(80)},
				},
			},
		}
		env.ExpectCreated(svc)
		// Wait for the service to get ClusterIPs assigned
		Eventually(func(g Gomega) {
			updatedSvc := env.ExpectExists(svc).(*corev1.Service)
			g.Expect(len(updatedSvc.Spec.ClusterIPs)).To(BeNumerically(">=", 2),
				fmt.Sprintf("service should have at least 2 ClusterIPs, got: %v", updatedSvc.Spec.ClusterIPs))

			var svcIPv4, svcIPv6 bool
			for _, clusterIP := range updatedSvc.Spec.ClusterIPs {
				ip := net.ParseIP(clusterIP)
				g.Expect(ip).ToNot(BeNil())
				if hasIPv4(ip) {
					svcIPv4 = true
				} else if hasIPv6(ip) {
					svcIPv6 = true
				}
			}
			g.Expect(svcIPv4).To(BeTrue(), "service should have an IPv4 ClusterIP")
			g.Expect(svcIPv6).To(BeTrue(), "service should have an IPv6 ClusterIP")
		}).WithTimeout(30 * time.Second).WithPolling(5 * time.Second).Should(Succeed())

		env.ExpectDeleted(dep, svc)
	})
})
