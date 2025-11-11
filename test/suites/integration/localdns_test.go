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
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LocalDNS", func() {
	BeforeEach(func() {
		if env.InClusterController {
			Skip("LocalDNS tests require NPS (Node Provisioning Service) - only supported in NAP/managed Karpenter mode")
		}
	})

	// =========================================================================
	// TEST CASE 0: VERIFY LOCALDNS LABEL ONLY (ENABLED)
	// =========================================================================
	It("should set localdns-state=enabled label when LocalDNS is enabled", func() {
		Skip("Temporarily disabled - not testing label")
		By("Enabling LocalDNS on NodeClass")
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
			Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
		}

		By("Creating a test pod to provision a node with LocalDNS enabled")
		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)

		By("Getting the provisioned node")
		node := env.Monitor.CreatedNodes()[0]

		By("Verifying node has localdns-state=enabled label")
		VerifyNodeLocalDNSLabel(node.Name, "enabled")

		By("✓ LocalDNS label verification test completed successfully")
	})

	// =========================================================================
	// TEST CASE 0b: VERIFY LOCALDNS LABEL ONLY (DISABLED)
	// =========================================================================
	It("should set localdns-state=disabled label when LocalDNS is disabled", func() {
		Skip("Temporarily disabled - not testing label")
		By("Disabling LocalDNS on NodeClass")
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
			Mode: lo.ToPtr(v1beta1.LocalDNSModeDisabled),
		}

		By("Creating a test pod to provision a node with LocalDNS disabled")
		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)

		By("Getting the provisioned node")
		node := env.Monitor.CreatedNodes()[0]

		By("Verifying node has localdns-state=disabled label")
		VerifyNodeLocalDNSLabel(node.Name, "disabled")

		By("✓ LocalDNS disabled label verification test completed successfully")
	})

	// =========================================================================
	// TEST CASE 1: VERIFY DNS RESOLUTION WITH LOCALDNS ENABLED
	// =========================================================================
	It("should resolve DNS using LocalDNS when enabled", func() {
		By("Enabling LocalDNS on NodeClass")
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
			Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
		}

		// Add a unique label to the nodePool to ensure a new node is provisioned
		nodePool.Spec.Template.Labels = lo.Assign(nodePool.Spec.Template.Labels, map[string]string{
			"localdns-test-id": "localdns-enabled",
		})

		By("Creating DNS test pod in default namespace to provision node with LocalDNS")
		dnsTestPod := test.Pod(test.PodOptions{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dnstest-localdns-default",
				Namespace: namespaceDefault,
			},
			Image:         dnsUtilsImage,
			Command:       []string{"sh", "-c", "nslookup mcr.microsoft.com 2>&1; sleep 3600"},
			RestartPolicy: corev1.RestartPolicyNever,
			NodeSelector: map[string]string{
				"localdns-test-id": "localdns-enabled",
			},
			Tolerations: []corev1.Toleration{
				{Key: "node.cilium.io/agent-not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: "karpenter.sh/unregistered", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: "node.cloudprovider.kubernetes.io/uninitialized", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "node.kubernetes.io/not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "node.kubernetes.io/not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
			},
			ResourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("32Mi"),
				},
			},
		})
		env.ExpectCreated(nodeClass, nodePool, dnsTestPod)
		env.EventuallyExpectHealthy(dnsTestPod)
		env.ExpectCreatedNodeCount("==", 1)

		By("Verifying CoreDNS is healthy")
		VerifyCoreDNSHealthy()

		By(fmt.Sprintf("Testing LocalDNS resolution from default namespace pod (name=%s, namespace=%s)", dnsTestPod.Name, dnsTestPod.Namespace))
		defaultNSResult := GetDNSResultFromPod(dnsTestPod)
		VerifyUsingLocalDNSClusterListener(defaultNSResult.DNSIP, "Default namespace DNS")

		By("Testing LocalDNS resolution from kube-system namespace")
		kubeSystemPod := test.Pod(test.PodOptions{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dnstest-localdns-kubesystem",
				Namespace: namespaceKubeSystem,
			},
			Image:         dnsUtilsImage,
			Command:       []string{"sh", "-c", "nslookup mcr.microsoft.com 2>&1; sleep 3600"},
			RestartPolicy: corev1.RestartPolicyNever,
			NodeSelector: map[string]string{
				"localdns-test-id": "localdns-enabled",
			},
			Tolerations: []corev1.Toleration{
				{Key: "node.cilium.io/agent-not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: "karpenter.sh/unregistered", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: "node.cloudprovider.kubernetes.io/uninitialized", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "node.kubernetes.io/not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "node.kubernetes.io/not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
			},
			ResourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("32Mi"),
				},
			},
		})
		env.ExpectCreated(kubeSystemPod)
		env.EventuallyExpectHealthy(kubeSystemPod)

		By(fmt.Sprintf("Testing LocalDNS resolution from kube-system namespace pod (name=%s, namespace=%s)", kubeSystemPod.Name, kubeSystemPod.Namespace))
		kubeSystemResult := GetDNSResultFromPod(kubeSystemPod)
		VerifyUsingLocalDNSNodeListener(kubeSystemResult.DNSIP, "Kube-system namespace DNS")

		By("Testing LocalDNS in-cluster DNS resolution")
		inClusterPod := test.Pod(test.PodOptions{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dnstest-incluster",
				Namespace: namespaceDefault,
			},
			Image:         dnsUtilsImage,
			Command:       []string{"sh", "-c", "nslookup kubernetes.default.svc.cluster.local 2>&1; sleep 3600"},
			RestartPolicy: corev1.RestartPolicyNever,
			NodeSelector: map[string]string{
				"localdns-test-id": "localdns-enabled",
			},
			Tolerations: []corev1.Toleration{
				{Key: "node.cilium.io/agent-not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: "karpenter.sh/unregistered", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: "node.cloudprovider.kubernetes.io/uninitialized", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "node.kubernetes.io/not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "node.kubernetes.io/not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
			},
			ResourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("32Mi"),
				},
			},
		})
		env.ExpectCreated(inClusterPod)
		env.EventuallyExpectHealthy(inClusterPod)

		By(fmt.Sprintf("Testing LocalDNS in-cluster DNS resolution from pod (name=%s, namespace=%s)", inClusterPod.Name, inClusterPod.Namespace))
		inClusterResult := GetDNSResultFromPod(inClusterPod)
		Expect(inClusterResult.Logs).To(ContainSubstring("kubernetes.default.svc.cluster.local"), "In-cluster DNS should resolve kubernetes service")
		VerifyUsingLocalDNSClusterListener(inClusterResult.DNSIP, "In-cluster DNS")

		By("✓ LocalDNS resolution test completed successfully")

		// DEBUGGING: Sleep to allow manual inspection of the node
		By("⏸️  PAUSING for 60 minutes to allow manual node inspection")
		By("   You can now inspect the node, pods, and DNS configuration")
		By("   Press Ctrl+C to stop the test when done")
		time.Sleep(60 * time.Minute)
	})

	// =========================================================================
	// TEST CASE 2: VERIFY DNS RESOLUTION WITH LOCALDNS DISABLED
	// =========================================================================
	It("should resolve DNS using CoreDNS when LocalDNS is disabled", func() {
		By("Disabling LocalDNS on NodeClass (using default CoreDNS)")
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
			Mode: lo.ToPtr(v1beta1.LocalDNSModeDisabled),
		}

		// Add a unique label to the nodePool to ensure a new node is provisioned
		nodePool.Spec.Template.Labels = lo.Assign(nodePool.Spec.Template.Labels, map[string]string{
			"localdns-test-id": "localdns-disabled",
		})

		By("Creating DNS test pod in default namespace to provision node with LocalDNS disabled")
		dnsTestPod := test.Pod(test.PodOptions{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dnstest-coredns-default",
				Namespace: namespaceDefault,
			},
			Image:         dnsUtilsImage,
			Command:       []string{"sh", "-c", "nslookup kubernetes.default.svc.cluster.local 2>&1; sleep 3600"},
			RestartPolicy: corev1.RestartPolicyNever,
			NodeSelector: map[string]string{
				"localdns-test-id": "localdns-disabled",
			},
			Tolerations: []corev1.Toleration{
				{Key: "node.cilium.io/agent-not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: "karpenter.sh/unregistered", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: "node.cloudprovider.kubernetes.io/uninitialized", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "node.kubernetes.io/not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "node.kubernetes.io/not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
			},
			ResourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("32Mi"),
				},
			},
		})
		env.ExpectCreated(nodeClass, nodePool, dnsTestPod)
		env.EventuallyExpectHealthy(dnsTestPod)
		env.ExpectCreatedNodeCount("==", 1)

		By("Verifying CoreDNS is healthy")
		VerifyCoreDNSHealthy()

		By(fmt.Sprintf("Testing CoreDNS resolution from default namespace pod (name=%s, namespace=%s)", dnsTestPod.Name, dnsTestPod.Namespace))
		defaultNSResult := GetDNSResultFromPod(dnsTestPod)
		Expect(defaultNSResult.Logs).To(ContainSubstring("kubernetes.default.svc.cluster.local"), "DNS resolution should succeed")
		VerifyUsingCoreDNS(defaultNSResult.DNSIP, "Default namespace DNS")
		VerifyNotUsingLocalDNS(defaultNSResult.DNSIP, "Default namespace DNS")

		By("✓ CoreDNS resolution test completed successfully")
		// DEBUGGING: Sleep to allow manual inspection of the node
		By("⏸️  PAUSING for 60 minutes to allow manual node inspection")
		By("   You can now inspect the node, pods, and DNS configuration")
		By("   Press Ctrl+C to stop the test when done")
		time.Sleep(60 * time.Minute)
	})

	// =========================================================================
	// TEST CASE 2b: VERIFY DNS RESOLUTION WITH LOCALDNS ENABLED USING DIG
	// =========================================================================
	It("should resolve DNS using LocalDNS with dig command from agnhost", func() {
		By("Enabling LocalDNS on NodeClass")
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
			Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
		}

		// Add a unique label to the nodePool to ensure a new node is provisioned
		nodePool.Spec.Template.Labels = lo.Assign(nodePool.Spec.Template.Labels, map[string]string{
			"localdns-test-id": "localdns-enabled-dig",
		})

		By("Creating DNS test pod with agnhost image to provision node with LocalDNS")
		agnhostPod := test.Pod(test.PodOptions{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dnstest-agnhost-dig",
				Namespace: namespaceDefault,
			},
			Image:         "registry.k8s.io/e2e-test-images/agnhost:2.39",
			Command:       []string{"sh", "-c", "dig mcr.microsoft.com 2>&1; sleep 3600"},
			RestartPolicy: corev1.RestartPolicyNever,
			NodeSelector: map[string]string{
				"localdns-test-id": "localdns-enabled-dig",
			},
			Tolerations: []corev1.Toleration{
				{Key: "node.cilium.io/agent-not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: "karpenter.sh/unregistered", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: "node.cloudprovider.kubernetes.io/uninitialized", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "node.kubernetes.io/not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "node.kubernetes.io/not-ready", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
			},
			ResourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
			},
		})
		env.ExpectCreated(nodeClass, nodePool, agnhostPod)
		env.EventuallyExpectHealthy(agnhostPod)
		env.ExpectCreatedNodeCount("==", 1)

		By("Verifying CoreDNS is healthy")
		VerifyCoreDNSHealthy()

		By(fmt.Sprintf("Testing LocalDNS resolution using dig from agnhost pod (name=%s, namespace=%s)", agnhostPod.Name, agnhostPod.Namespace))
		digResult := GetDNSResultFromPod(agnhostPod)
		By("Dig output from agnhost pod:\n" + digResult.Logs)

		// Parse DNS server from dig output (looks for ";; SERVER: " line)
		dnsServer := parseDNSServerFromDig(digResult.Logs)
		Expect(dnsServer).ToNot(BeEmpty(), "Should detect DNS server from dig output")

		By(fmt.Sprintf("DNS queries from agnhost pod use DNS server: %s", dnsServer))
		VerifyUsingLocalDNSClusterListener(dnsServer, "Agnhost dig DNS")

		By("✓ LocalDNS resolution with dig test completed successfully")

		// DEBUGGING: Sleep to allow manual inspection of the node
		By("⏸️  PAUSING for 60 minutes to allow manual node inspection")
		By("   You can now inspect the node, pods, and DNS configuration")
		By("   Press Ctrl+C to stop the test when done")
		time.Sleep(60 * time.Minute)
	})

	// =========================================================================
	// TEST CASE 3: FULL INTEGRATION TEST WITH LABEL AND DNS (ENABLED)
	// =========================================================================
	It("should enable LocalDNS and test LocalDNS resolution", func() {
		Skip("Temporarily disabled - not testing label")
		/* By("Enabling LocalDNS on NodeClass")
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
			}

			By(fmt.Sprintf("DEBUG: NodeClass.Spec.LocalDNS = %+v", nodeClass.Spec.LocalDNS))
			if nodeClass.Spec.LocalDNS != nil && nodeClass.Spec.LocalDNS.Mode != nil {
				By(fmt.Sprintf("DEBUG: LocalDNS Mode = %s", *nodeClass.Spec.LocalDNS.Mode))
			}

			By("Creating a test pod to provision a node with LocalDNS enabled")
			pod := test.Pod()
			env.ExpectCreated(nodeClass, nodePool, pod)

			By("DEBUG: Verifying NodeClass was created successfully")
			var createdNodeClass v1beta1.AKSNodeClass
			Eventually(func(g Gomega) {
				g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), &createdNodeClass)).To(Succeed())
				By(fmt.Sprintf("DEBUG: Retrieved NodeClass %s from cluster", createdNodeClass.Name))
				if createdNodeClass.Spec.LocalDNS != nil {
					By(fmt.Sprintf("DEBUG: NodeClass in cluster has LocalDNS: %+v", createdNodeClass.Spec.LocalDNS))
					if createdNodeClass.Spec.LocalDNS.Mode != nil {
						By(fmt.Sprintf("DEBUG: NodeClass in cluster LocalDNS.Mode = %s", *createdNodeClass.Spec.LocalDNS.Mode))
					}
				} else {
					By("DEBUG: NodeClass in cluster has NO LocalDNS field!")
				}
			}).Should(Succeed())

			env.EventuallyExpectHealthy(pod)
			env.ExpectCreatedNodeCount("==", 1)

			By("Getting the provisioned node")
			node := env.Monitor.CreatedNodes()[0]

			By("DEBUG: Running comprehensive node and NodeClass analysis")
			DebugNodeAndNodeClass(node.Name)

			By("Verifying node has localdns-state=enabled label")
			VerifyNodeLocalDNSLabel(node.Name, "enabled")

			By("Verifying CoreDNS is healthy")
			VerifyCoreDNSHealthy()

			By("Testing LocalDNS resolution from default namespace")
			defaultNSResult := RunLocalDNSResolutionFromDefaultNamespace()
			VerifyUsingLocalDNSClusterListener(defaultNSResult.DNSIP, "Default namespace DNS")

			By("Testing LocalDNS resolution from CoreDNS namespace (node listener)")
			coreDNSNSResult := RunLocalDNSResolutionFromCoreDNSPod()
			VerifyUsingLocalDNSNodeListener(coreDNSNSResult.DNSIP, "CoreDNS namespace DNS")

			By("Testing LocalDNS in-cluster DNS resolution")
			inClusterResult := RunLocalDNSInClusterResolution()
			VerifyUsingLocalDNSClusterListener(inClusterResult.DNSIP, "In-cluster DNS")

			By("✓ LocalDNS enabled test completed successfully")
		})

		// =========================================================================
		// TEST CASE 4: FULL INTEGRATION TEST WITH LABEL AND DNS (DISABLED)
		// =========================================================================
		It("should disable LocalDNS and test CoreDNS resolution", func() {
			Skip("Temporarily disabled - not testing label")
			By("Disabling LocalDNS on NodeClass (using default CoreDNS)")
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModeDisabled),
			}

			By("Creating a test pod to provision a node with LocalDNS disabled")
			pod := test.Pod()
			env.ExpectCreated(nodeClass, nodePool, pod)
			env.EventuallyExpectHealthy(pod)
			env.ExpectCreatedNodeCount("==", 1)

			By("Getting the provisioned node")
			node := env.Monitor.CreatedNodes()[0]

			By("Verifying node has localdns-state=disabled label")
			VerifyNodeLocalDNSLabel(node.Name, "disabled")

			By("Verifying CoreDNS is healthy")
			VerifyCoreDNSHealthy()

			By("Testing CoreDNS resolution from default namespace")
			defaultNSResult := RunCoreDNSResolutionFromDefaultNamespace()
			VerifyUsingCoreDNS(defaultNSResult.DNSIP, "Default namespace DNS")
			VerifyNotUsingLocalDNS(defaultNSResult.DNSIP, "Default namespace DNS")

			By("Testing upstream DNS resolution from CoreDNS pods")
			upstreamResult := RunUpstreamDNSResolution()
			VerifyUsingAzureDNS(upstreamResult.DNSIP, "Upstream DNS")
			VerifyNotUsingLocalDNS(upstreamResult.DNSIP, "Upstream DNS")

			By("✓ LocalDNS disabled test completed successfully") */
	})

	// =========================================================================
	// TEST CASE 5: ENABLE LOCALDNS, THEN DISABLE IT (LIFECYCLE TEST)
	// =========================================================================
	It("should enable LocalDNS, test LocalDNS resolution, disable LocalDNS, then test CoreDNS resolution", func() {
		// =================================================================
		// PHASE 1: ENABLE LOCALDNS
		// =================================================================
		/* Skip("Temporarily disabled - not testing label")
		By("Phase 1: Enabling LocalDNS on NodeClass")
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
			Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
		}

		By("Creating initial test pod to provision a node with LocalDNS enabled")
		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)

		By("Getting the provisioned node")
		node := env.Monitor.CreatedNodes()[0]

		By("Verifying node has localdns-state=enabled label")
		VerifyNodeLocalDNSLabel(node.Name, "enabled")

		By("Testing LocalDNS resolution from default namespace")
		defaultNSResult1 := RunLocalDNSResolutionFromDefaultNamespace()
		VerifyUsingLocalDNSClusterListener(defaultNSResult1.DNSIP, "Default namespace DNS (LocalDNS enabled)")

		By("Testing LocalDNS resolution from CoreDNS namespace (node listener)")
		coreDNSNSResult1 := RunLocalDNSResolutionFromCoreDNSPod()
		VerifyUsingLocalDNSNodeListener(coreDNSNSResult1.DNSIP, "CoreDNS namespace DNS (LocalDNS enabled)")

		By("Testing LocalDNS in-cluster DNS resolution")
		inClusterResult1 := RunLocalDNSInClusterResolution()
		VerifyUsingLocalDNSClusterListener(inClusterResult1.DNSIP, "In-cluster DNS (LocalDNS enabled)")

		By("✓ Phase 1 completed: LocalDNS is enabled and working")

		// =================================================================
		// PHASE 2: DISABLE LOCALDNS
		// =================================================================
		By("Phase 2: Disabling LocalDNS on NodeClass")
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
			Mode: lo.ToPtr(v1beta1.LocalDNSModeDisabled),
		}
		env.ExpectUpdated(nodeClass)

		By("Deleting the old node to trigger new node with updated configuration")
		env.ExpectDeleted(node)

		By("Creating new test pod to provision a new node with LocalDNS disabled")
		pod2 := test.Pod()
		env.ExpectCreated(pod2)
		env.EventuallyExpectHealthy(pod2)
		env.ExpectCreatedNodeCount("==", 1)

		By("Getting the new provisioned node")
		newNode := env.Monitor.CreatedNodes()[0]

		By("Verifying new node has localdns-state=disabled label")
		VerifyNodeLocalDNSLabel(newNode.Name, "disabled")

		By("Verifying CoreDNS is healthy")
		VerifyCoreDNSHealthy()

		By("Testing CoreDNS resolution from default namespace")
		defaultNSResult2 := RunCoreDNSResolutionFromDefaultNamespace()
		VerifyUsingCoreDNS(defaultNSResult2.DNSIP, "Default namespace DNS (LocalDNS disabled)")
		VerifyNotUsingLocalDNS(defaultNSResult2.DNSIP, "Default namespace DNS (LocalDNS disabled)")

		By("Testing upstream DNS resolution from CoreDNS pods")
		upstreamResult := RunUpstreamDNSResolution()
		VerifyUsingAzureDNS(upstreamResult.DNSIP, "Upstream DNS (LocalDNS disabled)")
		VerifyNotUsingLocalDNS(upstreamResult.DNSIP, "Upstream DNS (LocalDNS disabled)")

		By("✓ Phase 2 completed: LocalDNS is disabled and CoreDNS is working")

		// =================================================================
		// PHASE 3: VERIFY TRANSITION WORKED
		// =================================================================
		By("Phase 3: Verifying DNS transition from LocalDNS to CoreDNS")
		VerifyDifferentDNSServers(defaultNSResult1.DNSIP, defaultNSResult2.DNSIP, "Default namespace DNS (enabled vs disabled)")
		VerifyDifferentDNSServers(coreDNSNSResult1.DNSIP, upstreamResult.DNSIP, "CoreDNS namespace DNS (enabled vs disabled)")

		By("✓ Phase 3 completed: Verified clean transition from LocalDNS to CoreDNS")
		By("✓ Full lifecycle test completed successfully") */
	})
})

const (
	// LocalDNS listener IPs
	localDNSClusterListenerIP = "169.254.10.11" // Handles external DNS and in-cluster DNS
	localDNSNodeListenerIP    = "169.254.10.10" // Handles external DNS from CoreDNS pods

	// Standard DNS IPs
	azureDNSIP       = "168.63.129.16" // Azure's upstream DNS
	coreDNSServiceIP = "10.0.0.10"     // Default CoreDNS service IP in AKS

	// Images
	dnsUtilsImage = "alpine:3.20.2" // Small image with nslookup built-in, proven to work in other integration tests

	// Namespaces
	namespaceDefault    = "default"
	namespaceKubeSystem = "kube-system"

	// Test timeouts
	dnsTestTimeout     = 3 * time.Minute
	healthCheckTimeout = 1 * time.Minute
	nodeLabelTimeout   = 2 * time.Minute
	pollInterval       = 10 * time.Second
)

// DNSTestResult holds the results of DNS resolution tests
type DNSTestResult struct {
	DNSIP   string // The DNS server IP detected from logs
	Logs    string // Full logs from the DNS query
	Success bool   // Whether the test succeeded
}

// =========================================================================
// NODE LABEL VERIFICATION
// =========================================================================

// DebugNodeAndNodeClass logs detailed information about a node and its NodeClass configuration
func DebugNodeAndNodeClass(nodeName string) {
	By(fmt.Sprintf("DEBUG: Analyzing node %s", nodeName))
	debugNodeClaims(nodeName)
	debugNodeLabels(nodeName)
}

// debugNodeClaims logs information about NodeClaims and their associated NodeClass
func debugNodeClaims(nodeName string) {
	Eventually(func(g Gomega) {
		var nodeClaims karpv1.NodeClaimList
		g.Expect(env.Client.List(env.Context, &nodeClaims)).To(Succeed())
		By(fmt.Sprintf("DEBUG: Found %d NodeClaims in cluster", len(nodeClaims.Items)))

		for _, nc := range nodeClaims.Items {
			if nc.Status.NodeName == nodeName {
				logNodeClaimInfo(nc)
				fetchAndLogNodeClass(nc.Spec.NodeClassRef.Name)
			}
		}
	}).Should(Succeed())
}

// logNodeClaimInfo logs details about a NodeClaim
func logNodeClaimInfo(nc karpv1.NodeClaim) {
	By(fmt.Sprintf("DEBUG: Found NodeClaim %s for node %s", nc.Name, nc.Status.NodeName))
	By(fmt.Sprintf("DEBUG:   NodeClassRef.Name = %s", nc.Spec.NodeClassRef.Name))
	By(fmt.Sprintf("DEBUG:   NodeClassRef.Group = %s", nc.Spec.NodeClassRef.Group))
	By(fmt.Sprintf("DEBUG:   NodeClassRef.Kind = %s", nc.Spec.NodeClassRef.Kind))
}

// fetchAndLogNodeClass fetches and logs details about a NodeClass
func fetchAndLogNodeClass(nodeClassName string) {
	var ncClass v1beta1.AKSNodeClass
	err := env.Client.Get(env.Context, client.ObjectKey{Name: nodeClassName}, &ncClass)
	if err != nil {
		By(fmt.Sprintf("DEBUG: ERROR fetching NodeClass %s: %v", nodeClassName, err))
		return
	}

	By(fmt.Sprintf("DEBUG: Successfully fetched NodeClass %s", ncClass.Name))
	By(fmt.Sprintf("DEBUG: NodeClass.APIVersion = %s", ncClass.APIVersion))
	By(fmt.Sprintf("DEBUG: NodeClass.Kind = %s", ncClass.Kind))
	logLocalDNSConfig(ncClass.Spec.LocalDNS)
}

// logLocalDNSConfig logs LocalDNS configuration details
func logLocalDNSConfig(localDNS *v1beta1.LocalDNS) {
	if localDNS == nil {
		By("DEBUG: NodeClass.Spec.LocalDNS is NIL - THIS IS THE PROBLEM!")
		By("DEBUG: This means the LocalDNS field was not persisted or the wrong NodeClass was referenced")
		return
	}

	By(fmt.Sprintf("DEBUG: NodeClass.Spec.LocalDNS IS SET: %+v", localDNS))

	if localDNS.Mode != nil {
		By(fmt.Sprintf("DEBUG: NodeClass.Spec.LocalDNS.Mode = %s", *localDNS.Mode))
	} else {
		By("DEBUG: NodeClass.Spec.LocalDNS.Mode is NIL")
	}

	if localDNS.VnetDNSOverrides != nil {
		By(fmt.Sprintf("DEBUG: NodeClass.Spec.LocalDNS.VnetDNSOverrides has %d entries", len(localDNS.VnetDNSOverrides)))
	}

	if localDNS.KubeDNSOverrides != nil {
		By(fmt.Sprintf("DEBUG: NodeClass.Spec.LocalDNS.KubeDNSOverrides has %d entries", len(localDNS.KubeDNSOverrides)))
	}
}

// debugNodeLabels logs information about a node's labels
func debugNodeLabels(nodeName string) {
	Eventually(func(g Gomega) {
		var node corev1.Node
		g.Expect(env.Client.Get(env.Context, client.ObjectKey{Name: nodeName}, &node)).To(Succeed())
		By(fmt.Sprintf("DEBUG: Node %s has %d total labels", nodeName, len(node.Labels)))

		categorizeAndLogLabels(node.Labels)
		checkLocalDNSStateLabel(node.Labels)
	}).Should(Succeed())
}

// categorizeAndLogLabels categorizes and logs node labels
func categorizeAndLogLabels(labels map[string]string) {
	azureLabels := []string{}
	dnsLabels := []string{}
	otherLabels := []string{}

	for key, value := range labels {
		labelStr := fmt.Sprintf("%s=%s", key, value)
		if strings.Contains(key, "dns") || strings.Contains(key, "DNS") {
			dnsLabels = append(dnsLabels, labelStr)
		} else if strings.Contains(key, "kubernetes.azure.com") {
			azureLabels = append(azureLabels, labelStr)
		} else if strings.Contains(key, "local") {
			otherLabels = append(otherLabels, labelStr)
		}
	}

	logLabelCategory("DNS-related", dnsLabels)
	logLabelCategory("Azure-specific", azureLabels)
	logLabelCategory("Other 'local'", otherLabels)
}

// logLabelCategory logs a category of labels
func logLabelCategory(category string, labels []string) {
	if len(labels) > 0 {
		By(fmt.Sprintf("DEBUG: %s labels (%d):", category, len(labels)))
		for _, label := range labels {
			By(fmt.Sprintf("  - %s", label))
		}
	} else if category == "DNS-related" {
		By("DEBUG: NO DNS-related labels found")
	}
}

// checkLocalDNSStateLabel checks for the presence of the localdns-state label
func checkLocalDNSStateLabel(labels map[string]string) {
	if val, exists := labels["kubernetes.azure.com/localdns-state"]; exists {
		By(fmt.Sprintf("DEBUG: ✓ Found localdns-state label = %s", val))
	} else {
		By("DEBUG: ✗ localdns-state label NOT FOUND on node")
		By("DEBUG: Expected label key: kubernetes.azure.com/localdns-state")
		By("DEBUG: This label should be set by the bootstrap/provisioning process")
	}
}

// VerifyNodeLocalDNSLabel verifies that a node has the expected localdns-state label
func VerifyNodeLocalDNSLabel(nodeName string, expectedValue string) {
	Eventually(func(g Gomega) {
		var currentNode corev1.Node
		g.Expect(env.Client.Get(env.Context, client.ObjectKey{Name: nodeName}, &currentNode)).To(Succeed())

		// Debug: Print all labels on the node
		By(fmt.Sprintf("Debug: Node %s has %d labels", nodeName, len(currentNode.Labels)))
		for key, value := range currentNode.Labels {
			if strings.Contains(key, "localdns") || strings.Contains(key, "dns") {
				By(fmt.Sprintf("Debug: Label %s=%s", key, value))
			}
		}

		labelValue, exists := currentNode.Labels["kubernetes.azure.com/localdns-state"]
		if !exists {
			By(fmt.Sprintf("Debug: localdns-state label NOT FOUND on node %s", nodeName))
			By("Debug: All node labels:")
			for key, value := range currentNode.Labels {
				By(fmt.Sprintf("  %s=%s", key, value))
			}
		}

		g.Expect(exists).To(BeTrue(), fmt.Sprintf("Node %s should have localdns-state label. Found %d labels total", nodeName, len(currentNode.Labels)))
		g.Expect(labelValue).To(Equal(expectedValue), "LocalDNS state should be "+expectedValue)

		By("✓ Node " + nodeName + " has localdns-state=" + expectedValue + " label")
	}).WithTimeout(nodeLabelTimeout).WithPolling(pollInterval).Should(Succeed())
}

// VerifyAllNodesLocalDNSLabel verifies that all nodes have the expected localdns-state label
func VerifyAllNodesLocalDNSLabel(expectedValue string) {
	Eventually(func(g Gomega) {
		var nodes corev1.NodeList
		g.Expect(env.Client.List(env.Context, &nodes)).To(Succeed())
		g.Expect(nodes.Items).ToNot(BeEmpty(), "Should have at least one node")

		for _, node := range nodes.Items {
			labelValue, exists := node.Labels["kubernetes.azure.com/localdns-state"]
			g.Expect(exists).To(BeTrue(), "Node "+node.Name+" should have localdns-state label")
			g.Expect(labelValue).To(Equal(expectedValue), "Node "+node.Name+" LocalDNS state should be "+expectedValue)
		}

		By(fmt.Sprintf("✓ All %d nodes have localdns-state=%s label", len(nodes.Items), expectedValue))
	}).WithTimeout(nodeLabelTimeout).WithPolling(pollInterval).Should(Succeed())
}

// =========================================================================
// COREDNS HEALTH CHECKS
// =========================================================================

// VerifyCoreDNSHealthy verifies that CoreDNS deployment is healthy
func VerifyCoreDNSHealthy() {
	Eventually(func(g Gomega) {
		var coreDNSDeployment appsv1.Deployment
		g.Expect(env.Client.Get(env.Context, client.ObjectKey{Name: "coredns", Namespace: namespaceKubeSystem}, &coreDNSDeployment)).To(Succeed())
		g.Expect(coreDNSDeployment.Status.ReadyReplicas).To(BeNumerically(">", 0), "CoreDNS should have ready replicas")
		By(fmt.Sprintf("✓ CoreDNS deployment is healthy with %d ready replicas", coreDNSDeployment.Status.ReadyReplicas))
	}).WithTimeout(healthCheckTimeout).Should(Succeed())
}

// GetCoreDNSPods returns the list of CoreDNS pods
func GetCoreDNSPods() *corev1.PodList {
	coreDNSPods := &corev1.PodList{}
	Eventually(func(g Gomega) {
		g.Expect(env.Client.List(env.Context, coreDNSPods, client.InNamespace(namespaceKubeSystem), client.MatchingLabels{"k8s-app": "kube-dns"})).To(Succeed())
		g.Expect(coreDNSPods.Items).ToNot(BeEmpty(), "CoreDNS pods should exist")
	}).WithTimeout(healthCheckTimeout).Should(Succeed())

	By(fmt.Sprintf("Found %d CoreDNS pod(s)", len(coreDNSPods.Items)))
	return coreDNSPods
}

// =========================================================================
// VALIDATION HELPERS
// =========================================================================

// VerifyNotUsingLocalDNS verifies that the provided DNS IP is NOT a LocalDNS listener IP
func VerifyNotUsingLocalDNS(dnsIP string, context string) {
	Expect(dnsIP).ToNot(Equal(localDNSClusterListenerIP), context+" should NOT use LocalDNS cluster listener")
	Expect(dnsIP).ToNot(Equal(localDNSNodeListenerIP), context+" should NOT use LocalDNS node listener")
	By("✓ Confirmed " + context + " NOT using LocalDNS listener IPs")
}

// VerifyUsingLocalDNS verifies that the provided DNS IP IS a LocalDNS listener IP
func VerifyUsingLocalDNS(dnsIP string, context string) {
	isLocalDNS := dnsIP == localDNSClusterListenerIP || dnsIP == localDNSNodeListenerIP
	Expect(isLocalDNS).To(BeTrue(), context+" should use LocalDNS listener (either "+localDNSClusterListenerIP+" or "+localDNSNodeListenerIP+")")
	By("✓ Confirmed " + context + " IS using LocalDNS listener: " + dnsIP)
}

// VerifyUsingLocalDNSClusterListener verifies that the provided DNS IP is the LocalDNS cluster listener
func VerifyUsingLocalDNSClusterListener(dnsIP string, context string) {
	Expect(dnsIP).To(Equal(localDNSClusterListenerIP), context+" should use LocalDNS cluster listener ("+localDNSClusterListenerIP+")")
	By("✓ Confirmed " + context + " using LocalDNS cluster listener: " + dnsIP)
}

// VerifyUsingLocalDNSNodeListener verifies that the provided DNS IP is the LocalDNS node listener
func VerifyUsingLocalDNSNodeListener(dnsIP string, context string) {
	Expect(dnsIP).To(Equal(localDNSNodeListenerIP), context+" should use LocalDNS node listener ("+localDNSNodeListenerIP+")")
	By("✓ Confirmed " + context + " using LocalDNS node listener: " + dnsIP)
}

// VerifyUsingAzureDNS verifies that the provided DNS IP is Azure DNS
func VerifyUsingAzureDNS(dnsIP string, context string) {
	Expect(dnsIP).To(Equal(azureDNSIP), context+" should use Azure DNS ("+azureDNSIP+")")
	By("✓ Confirmed " + context + " using Azure DNS: " + dnsIP)
}

// VerifyUsingCoreDNS verifies that the provided DNS IP is the CoreDNS service IP
func VerifyUsingCoreDNS(dnsIP string, context string) {
	Expect(dnsIP).To(Equal(coreDNSServiceIP), context+" should use CoreDNS service ("+coreDNSServiceIP+")")
	By("✓ Confirmed " + context + " using CoreDNS service: " + dnsIP)
}

// VerifyDifferentDNSServers verifies that two DNS IPs are different
func VerifyDifferentDNSServers(dnsIP1, dnsIP2, context string) {
	Expect(dnsIP1).ToNot(Equal(dnsIP2), context+": DNS servers should be different")
	By("✓ Confirmed different DNS servers for " + context + ": " + dnsIP1 + " != " + dnsIP2)
}

// =========================================================================
// INTERNAL HELPER FUNCTIONS
// =========================================================================

// GetDNSResultFromPod gets DNS resolution results from an existing pod's logs
func GetDNSResultFromPod(pod *corev1.Pod) DNSTestResult {
	var result DNSTestResult

	Eventually(func(g Gomega) {
		g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(pod), pod)).To(Succeed())

		// Wait for pod to be running or completed
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodSucceeded {
			return
		}

		// Read pod logs
		req := env.KubeClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
			Container: pod.Spec.Containers[0].Name,
		})
		podLogs, err := req.Stream(context.Background())
		g.Expect(err).To(Succeed())
		defer podLogs.Close()

		buf := new(bytes.Buffer)
		_, err = io.Copy(buf, podLogs)
		g.Expect(err).To(Succeed())

		result.Logs = buf.String()
		By("DNS query logs from pod " + pod.Name + ":\n" + result.Logs)

		// Parse DNS server IP from logs
		result.DNSIP = parseDNSServerIP(result.Logs)
		g.Expect(result.DNSIP).ToNot(BeEmpty(), "Should have detected DNS server IP from logs")

		result.Success = true
	}).WithTimeout(dnsTestTimeout).Should(Succeed())

	return result
}

// parseDNSServerIP extracts the DNS server IP from nslookup output
// Example output:
// Server:    169.254.10.11
// Address:   169.254.10.11:53
func parseDNSServerIP(logs string) string {
	for _, line := range strings.Split(logs, "\n") {
		line = strings.TrimSpace(line)
		// Look for "Server:" line first (most reliable)
		if strings.HasPrefix(line, "Server:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1]
			}
		}
		// Fallback to "Address:" line if it contains the DNS server (not the queried address)
		if strings.HasPrefix(line, "Address:") && strings.Contains(line, "#53") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				// Remove port if present (e.g., "10.0.0.10#53" -> "10.0.0.10")
				ipPort := fields[1]
				if idx := strings.Index(ipPort, "#"); idx != -1 {
					return ipPort[:idx]
				}
				return ipPort
			}
		}
	}
	return ""
}

// parseDNSServerFromDig extracts the DNS server IP from dig output
// Example output:
// ;; SERVER: 169.254.10.11#53(169.254.10.11)
func parseDNSServerFromDig(logs string) string {
	for _, line := range strings.Split(logs, "\n") {
		line = strings.TrimSpace(line)
		// Look for ";; SERVER:" line
		if strings.HasPrefix(line, ";; SERVER:") {
			// Format: ";; SERVER: 169.254.10.11#53(169.254.10.11)"
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				// Extract IP from "169.254.10.11#53(169.254.10.11)"
				ipPort := fields[2]
				// Remove port and parentheses (e.g., "169.254.10.11#53(169.254.10.11)" -> "169.254.10.11")
				if idx := strings.Index(ipPort, "#"); idx != -1 {
					return ipPort[:idx]
				}
				if idx := strings.Index(ipPort, "("); idx != -1 {
					return ipPort[:idx]
				}
				return ipPort
			}
		}
	}
	return ""
}
