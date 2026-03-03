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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	// Standard LocalDNS configuration durations
	cacheDuration = 3600 * time.Second
	staleDuration = 3600 * time.Second

	// Complete KubeDNS overrides configuration
	completeKubeDNSOverrides = []v1beta1.LocalDNSZoneOverride{
		{
			Zone:               ".",
			CacheDuration:      karpv1.NillableDuration{Duration: &cacheDuration},
			ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
			ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
			MaxConcurrent:      lo.ToPtr(int32(1000)),
			Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
			QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
			ServeStale:         v1beta1.LocalDNSServeStaleVerify,
			ServeStaleDuration: karpv1.NillableDuration{Duration: &staleDuration},
		},
		{
			Zone:               "cluster.local",
			CacheDuration:      karpv1.NillableDuration{Duration: &cacheDuration},
			ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
			ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
			MaxConcurrent:      lo.ToPtr(int32(1000)),
			Protocol:           v1beta1.LocalDNSProtocolForceTCP,
			QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
			ServeStale:         v1beta1.LocalDNSServeStaleImmediate,
			ServeStaleDuration: karpv1.NillableDuration{Duration: &staleDuration},
		},
	}

	// Complete VnetDNS overrides configuration
	completeVnetDNSOverrides = []v1beta1.LocalDNSZoneOverride{
		{
			Zone:               ".",
			CacheDuration:      karpv1.NillableDuration{Duration: &cacheDuration},
			ForwardDestination: v1beta1.LocalDNSForwardDestinationVnetDNS,
			ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
			MaxConcurrent:      lo.ToPtr(int32(1000)),
			Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
			QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
			ServeStale:         v1beta1.LocalDNSServeStaleVerify,
			ServeStaleDuration: karpv1.NillableDuration{Duration: &staleDuration},
		},
		{
			Zone:               "cluster.local",
			CacheDuration:      karpv1.NillableDuration{Duration: &cacheDuration},
			ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
			ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
			MaxConcurrent:      lo.ToPtr(int32(1000)),
			Protocol:           v1beta1.LocalDNSProtocolForceTCP,
			QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
			ServeStale:         v1beta1.LocalDNSServeStaleImmediate,
			ServeStaleDuration: karpv1.NillableDuration{Duration: &staleDuration},
		},
	}
)

var _ = Describe("LocalDNS", func() {
	BeforeEach(func() {
		if env.InClusterController {
			Skip("LocalDNS tests require NPS (Node Provisioning Service) - only supported in NAP/managed Karpenter mode")
		}
	})

	// =========================================================================
	// Happy path LOCALDNS CONFIG TEST
	// =========================================================================
	It("should enable and disable localdns", func() {
		By("[PART 1: ENABLE LOCALDNS] Configuring NodeClass with full LocalDNS configuration including overrides")
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
			Mode:             v1beta1.LocalDNSModeRequired,
			KubeDNSOverrides: completeKubeDNSOverrides,
			VnetDNSOverrides: completeVnetDNSOverrides,
		}

		By("Creating unschedulable pods to trigger node provisioning on new Karpenter node")
		enabledExternalPod := createDNSTestPod("microsoft.com", nil)
		enabledInternalPod := createDNSTestPod("kubernetes.default.svc.cluster.local", nil)
		env.ExpectCreated(nodeClass, nodePool, enabledExternalPod, enabledInternalPod)

		By("Waiting for node to be provisioned")
		enabledNode := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthy(enabledExternalPod)
		env.EventuallyExpectHealthy(enabledInternalPod)

		By(fmt.Sprintf("✓ Node %s successfully created with full LocalDNS configuration", enabledNode.Name))

		expectNodeLocalDNSLabel(enabledNode, "enabled")

		By("Verifying LocalDNS configuration is active from the provisioned node (host network)")
		expectDNSResult(getDNSResultFromNode(enabledNode), localDNSNodeListenerIP, "Host network DNS should use LocalDNS node listener")

		By("Verifying external DNS resolution from the test pod (pod network)")
		expectDNSResult(getDNSResultFromPod(enabledExternalPod), localDNSClusterListenerIP, "Test pod should use LocalDNS cluster listener for external DNS")

		By("Verifying internal DNS resolution from the test pod (pod network)")
		expectDNSResult(getDNSResultFromPod(enabledInternalPod), localDNSClusterListenerIP, "Test pod should use LocalDNS cluster listener for internal DNS")

		By("✓ Verified LocalDNS is configured on the node")

		// PART 2
		By("[PART 2: DISABLE LOCALDNS] Disabling LocalDNS to test configuration change")
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
			Mode:             v1beta1.LocalDNSModeDisabled,
			KubeDNSOverrides: completeKubeDNSOverrides,
			VnetDNSOverrides: completeVnetDNSOverrides,
		}
		env.ExpectUpdated(nodeClass)

		By("Waiting for new node to be provisioned with disabled LocalDNS (drift will replace the old node)")
		newNodes := env.EventuallyExpectCreatedNodeCount("==", 2)
		var disabledNode *corev1.Node
		for i := range newNodes {
			if newNodes[i].Name != enabledNode.Name {
				disabledNode = newNodes[i]
				break
			}
		}
		Expect(disabledNode).ToNot(BeNil(), "Should have provisioned a new node")

		By(fmt.Sprintf("New node %s provisioned to replace old node %s", disabledNode.Name, enabledNode.Name))

		By("Waiting for LocalDNS to be disabled on the new node")
		expectNodeLocalDNSLabel(disabledNode, "disabled")

		By("Creating pods with node selector to ensure they schedule on the new disabled node")
		disabledExternalPod := createDNSTestPod("microsoft.com", map[string]string{
			corev1.LabelHostname: disabledNode.Name,
		})
		disabledInternalPod := createDNSTestPod("kubernetes.default.svc.cluster.local", map[string]string{
			corev1.LabelHostname: disabledNode.Name,
		})
		env.ExpectCreated(disabledExternalPod, disabledInternalPod)
		env.EventuallyExpectHealthy(disabledExternalPod)
		env.EventuallyExpectHealthy(disabledInternalPod)

		By("Verifying DNS resolution uses default DNS after LocalDNS is disabled")
		expectDNSResult(getDNSResultFromNode(disabledNode), azureDNSIP, "Host network DNS should use default DNS")

		By("Verifying external DNS resolution from the test pod uses default DNS (pod network)")
		expectDNSResult(getDNSResultFromPod(disabledExternalPod), coreDNSServiceIP, "Test pod should use default DNS for external DNS")

		By("Verifying internal DNS resolution from the test pod uses default DNS (pod network)")
		expectDNSResult(getDNSResultFromPod(disabledInternalPod), coreDNSServiceIP, "Test pod should use default DNS for internal DNS")

		By("✓ Verified LocalDNS is properly disabled and DNS falls back to default configuration")
	})
})

const (
	// LocalDNS listener IPs
	localDNSClusterListenerIP = "169.254.10.11" // Handles external DNS and in-cluster DNS
	localDNSNodeListenerIP    = "169.254.10.10" // Handles external DNS from CoreDNS pods

	// Standard DNS IPs
	azureDNSIP       = "168.63.129.16" // Azure's upstream DNS
	coreDNSServiceIP = "10.0.0.10"     // Default CoreDNS service IP in AKS

	// Test timeouts
	dnsTestTimeout = 3 * time.Minute
)

// DNSTestResult holds the results of DNS resolution tests
type DNSTestResult struct {
	DNSIP   string // The DNS server IP detected from logs
	Logs    string // Full logs from the DNS query
	Success bool   // Whether the test succeeded
}

// =========================================================================
// HELPER FUNCTIONS
// =========================================================================

// expectDNSResult verifies the DNS resolution result matches expected DNS server IP.
// It logs the DNS resolution details and asserts that the detected DNS IP matches expectations.
func expectDNSResult(result DNSTestResult, expectedDNSIP string, description string) {
	By(fmt.Sprintf("DNS resolution results: DNSIP=%s, Success=%t", result.DNSIP, result.Success))
	By(fmt.Sprintf("DNS logs:\n%s", result.Logs))
	Expect(result.DNSIP).To(Equal(expectedDNSIP),
		fmt.Sprintf("%s (%s), but found %s", description, expectedDNSIP, result.DNSIP))
}

// expectNodeLocalDNSLabel verifies that a node has the expected localdns-state label value.
// This function waits for the label to appear on the node with the correct value.
func expectNodeLocalDNSLabel(node *corev1.Node, expectedValue string) {
	By(fmt.Sprintf("Verifying node %s has localdns-state=%s label", node.Name, expectedValue))
	Eventually(func(g Gomega) {
		var currentNode corev1.Node
		g.Expect(env.Client.Get(env.Context, client.ObjectKey{Name: node.Name}, &currentNode)).To(Succeed())

		labelValue, exists := currentNode.Labels["kubernetes.azure.com/localdns-state"]
		g.Expect(exists).To(BeTrue(), fmt.Sprintf("Node %s should have localdns-state label", node.Name))
		g.Expect(labelValue).To(Equal(expectedValue), fmt.Sprintf("LocalDNS state should be %s", expectedValue))

		By(fmt.Sprintf("✓ Node %s has localdns-state=%s label", node.Name, expectedValue))
	}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

// createDNSTestPod creates an unschedulable pod that continuously performs DNS lookups for a specific domain.
// The pod is designed to trigger Karpenter to provision a new node and verify DNS configuration.
// domain: the domain to query (e.g., "microsoft.com" or "kubernetes.default.svc.cluster.local")
// nodeSelector: optional node selector to target a specific node (nil for unschedulable pod)
func createDNSTestPod(domain string, nodeSelector map[string]string) *corev1.Pod {
	return coretest.UnschedulablePod(coretest.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"app": "localdns-test",
			},
		},
		Image: "mcr.microsoft.com/azurelinux/busybox:1.36",
		Command: []string{
			"sh", "-c",
			fmt.Sprintf("nslookup %s && sleep 30", domain),
		},
		ResourceRequirements: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("256M"),
			},
		},
		NodeSelector: nodeSelector,
	})
}

// getDNSResultFromPod gets DNS resolution results from an existing pod's logs
func getDNSResultFromPod(pod *corev1.Pod) DNSTestResult {
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

// getDNSResultFromNode gets DNS resolution results by creating a pod with hostNetwork on the specified node
func getDNSResultFromNode(node *corev1.Node) DNSTestResult {
	var result DNSTestResult

	By(fmt.Sprintf("Creating host-network pod on node %s to test DNS resolution", node.Name))

	// Create a pod with hostNetwork to test DNS from the node's perspective
	testPod := coretest.Pod(coretest.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "dns-test-",
			Namespace:    "default",
		},
		Image: "mcr.microsoft.com/azurelinux/busybox:1.36",
		Command: []string{
			"sh", "-c",
			"nslookup microsoft.com && sleep 30",
		},
		NodeSelector: map[string]string{
			corev1.LabelHostname: node.Name,
		},
		RestartPolicy: corev1.RestartPolicyNever,
	})
	// Use hostNetwork to test DNS from the node's network namespace
	testPod.Spec.HostNetwork = true

	env.ExpectCreated(testPod)
	defer func() {
		By("Cleaning up DNS test pod")
		env.ExpectDeleted(testPod)
	}()

	// Wait for pod to be running (not just scheduled)
	Eventually(func(g Gomega) {
		var currentPod corev1.Pod
		g.Expect(env.Client.Get(env.Context, client.ObjectKey{Name: testPod.Name, Namespace: testPod.Namespace}, &currentPod)).To(Succeed())
		g.Expect(currentPod.Status.Phase).To(Or(Equal(corev1.PodRunning), Equal(corev1.PodSucceeded)), "Pod should be running or completed")
	}).WithTimeout(2 * time.Minute).Should(Succeed())

	// Get the logs from the pod
	result = getDNSResultFromPod(testPod)

	By("DNS query output from node " + node.Name + ":\n" + result.Logs)

	return result
} // parseDNSServerIP extracts the DNS server IP from nslookup output

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
