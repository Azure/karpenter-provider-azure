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
	It("should create a node with full LocalDNS configuration (overrides)", func() {
		By("Cordoning existing nodes in the managed cluster")
		nodeList := &corev1.NodeList{}
		Expect(env.Client.List(env.Context, nodeList)).To(Succeed())
		for i := range nodeList.Items {
			node := &nodeList.Items[i]
			// Skip nodes that are already cordoned or are Karpenter-managed nodes
			if node.Spec.Unschedulable {
				continue
			}
			// Cordon the node by setting it to unschedulable
			stored := node.DeepCopy()
			node.Spec.Unschedulable = true
			Expect(env.Client.Patch(env.Context, node, client.StrategicMergeFrom(stored))).To(Succeed())
			By(fmt.Sprintf("Cordoned existing node: %s", node.Name))
		}

		By("Configuring NodeClass with full LocalDNS configuration including overrides")
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
			Mode:             v1beta1.LocalDNSModeRequired,
			KubeDNSOverrides: completeKubeDNSOverrides,
			VnetDNSOverrides: completeVnetDNSOverrides,
		}

		By("Creating inflate deployment to trigger node provisioning")
		deployment := coretest.Deployment(coretest.DeploymentOptions{
			Replicas: 1,
			PodOptions: coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "inflate",
					},
				},
				Image: "mcr.microsoft.com/azurelinux/busybox:1.36",
				Command: []string{
					"sh", "-c",
					"while true; do nslookup microsoft.com | grep Server: && sleep 30; done",
				},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("256M"),
					},
				},
			},
		})
		deployment.Name = "inflate"
		env.ExpectCreated(nodeClass, nodePool, deployment)

		By("Waiting for node to be provisioned")
		env.EventuallyExpectCreatedNodeCount("==", 1)

		By("Waiting for inflate pod to be scheduled and running")
		var inflatePod *corev1.Pod
		Eventually(func(g Gomega) {
			podList := &corev1.PodList{}
			g.Expect(env.Client.List(env.Context, podList, client.InNamespace("default"), client.MatchingLabels{"app": "inflate"})).To(Succeed())
			g.Expect(podList.Items).To(HaveLen(1))
			inflatePod = &podList.Items[0]
			g.Expect(inflatePod.Status.Phase).To(Equal(corev1.PodRunning))
		}).WithTimeout(5 * time.Minute).Should(Succeed())

		node := env.Monitor.CreatedNodes()[0]

		By(fmt.Sprintf("✓ Node %s successfully created with full LocalDNS configuration", node.Name))

		By("Verifying node has the localdns-state label")
		Eventually(func(g Gomega) {
			var currentNode corev1.Node
			g.Expect(env.Client.Get(env.Context, client.ObjectKey{Name: node.Name}, &currentNode)).To(Succeed())

			labelValue, exists := currentNode.Labels["kubernetes.azure.com/localdns-state"]
			g.Expect(exists).To(BeTrue(), fmt.Sprintf("Node %s should have localdns-state label", node.Name))
			g.Expect(labelValue).To(Equal("enabled"), "LocalDNS state should be enabled")

			By(fmt.Sprintf("✓ Node %s has localdns-state=enabled label", node.Name))
		}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		//	By("Verifying LocalDNS configuration is active from the provisioned node (host network)")
		result := GetDNSResultFromNode(node)
		By(fmt.Sprintf("DNS resolution results from node (host network): DNSIP=%s, Success=%t", result.DNSIP, result.Success))
		By(fmt.Sprintf("Node DNS logs:\n%s", result.Logs))

		// For host network DNS resolution on LocalDNS-enabled nodes, we expect:
		// - 169.254.10.10 (localDNSNodeListenerIP) for node-level DNS queries
		Expect(result.DNSIP).To(Equal(localDNSNodeListenerIP),
			fmt.Sprintf("Host network DNS should use LocalDNS node listener (%s), but found %s",
				localDNSNodeListenerIP, result.DNSIP))

		By("Verifying DNS resolution from the inflate pod (pod network)")
		inflateResult := GetDNSResultFromPod(inflatePod)

		By(fmt.Sprintf("DNS resolution results from inflate pod: DNSIP=%s, Success=%t", inflateResult.DNSIP, inflateResult.Success))
		By(fmt.Sprintf("Inflate pod logs:\n%s", inflateResult.Logs))
		// Pods with default dnsPolicy (ClusterFirst) use CoreDNS service IP
		// CoreDNS pods themselves use LocalDNS on LocalDNS-enabled nodes
		// Verify that the inflate pod is using the expected DNS configuration
		// For LocalDNS-enabled nodes, CoreDNS pods themselves use LocalDNS
		// This means the inflate pod should see LocalDNS cluster listener in the logs
		Expect(inflateResult.DNSIP).To(Equal(localDNSClusterListenerIP),
			fmt.Sprintf("Inflate pod should use LocalDNS cluster listener (%s), but found %s",
				localDNSClusterListenerIP, inflateResult.DNSIP))

		By("✓ Verified LocalDNS is configured on the node")
		By("Adding sleep to allow for system stabilization")
	})
})

const (
	// LocalDNS listener IPs
	localDNSClusterListenerIP = "169.254.10.11" // Handles external DNS and in-cluster DNS
	localDNSNodeListenerIP    = "169.254.10.10" // Handles external DNS from CoreDNS pods

	// Standard DNS IPs
	// azureDNSIP       = "168.63.129.16" // Azure's upstream DNS
	// coreDNSServiceIP = "10.0.0.10"     // Default CoreDNS service IP in AKS

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

	if localDNS.Mode != "" {
		By(fmt.Sprintf("DEBUG: NodeClass.Spec.LocalDNS.Mode = %s", localDNS.Mode))
	} else {
		By("DEBUG: NodeClass.Spec.LocalDNS.Mode is empty")
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

// GetDNSResultFromNode gets DNS resolution results by creating a pod with hostNetwork on the specified node
func GetDNSResultFromNode(node *corev1.Node) DNSTestResult {
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
			"nslookup kubernetes.default.svc.cluster.local && sleep 30",
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

	// Wait for pod to complete
	Eventually(func(g Gomega) {
		var currentPod corev1.Pod
		g.Expect(env.Client.Get(env.Context, client.ObjectKey{Name: testPod.Name, Namespace: testPod.Namespace}, &currentPod)).To(Succeed())
		g.Expect(currentPod.Status.Phase).ToNot(Equal(corev1.PodPending), "Pod should have started")
	}).WithTimeout(2 * time.Minute).Should(Succeed())

	// Get the logs from the pod
	result = GetDNSResultFromPod(testPod)

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
