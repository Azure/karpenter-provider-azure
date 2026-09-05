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
		if !env.IsMachineModeOrNPS() {
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

// =========================================================================
// Mode=Preferred instance type gate
//
// Status.LocalDNSState=Enabled feeds instanceTypeParameters.LocalDNSEnabled,
// which activates a hard instance type filter (>= 4 vCPU). A NodePool pinned
// to smaller SKUs therefore loses every candidate the moment LocalDNS turns
// on, and its pods stay Pending behind karpenter core's generic "no instance
// type satisfied requirements". The gate refuses to enable in that case.
// =========================================================================
var _ = Describe("LocalDNS Preferred instance type gate", func() {
	BeforeEach(func() {
		if !env.IsMachineModeOrNPS() {
			Skip("LocalDNS tests require NPS (Node Provisioning Service) - only supported in NAP/managed Karpenter mode")
		}
		skipUnlessPreferredReachesInstanceTypeGate()
	})

	It("should resolve Preferred to Disabled when the NodePool admits no LocalDNS-compatible instance type", func() {
		By("Configuring the NodeClass with LocalDNS Mode=Preferred")
		nodeClass.Spec.LocalDNS = preferredLocalDNS()

		By(fmt.Sprintf("Pinning the NodePool to %s, which is below the LocalDNS resource floor", localDNSIncompatibleSKU))
		pinNodePoolToSKU(nodePool, localDNSIncompatibleSKU)

		env.ExpectCreated(nodeClass, nodePool)

		By("Expecting the gate to resolve LocalDNS to Disabled with reason NoCompatibleInstanceTypes")
		expectLocalDNSResolution(nodeClass, v1beta1.LocalDNSStateDisabled, "NoCompatibleInstanceTypes")

		By("Expecting the NodePool to still provision - the whole point of the gate")
		pod := createDNSTestPod("microsoft.com", nil)
		env.ExpectCreated(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthy(pod)

		Expect(node.Labels[corev1.LabelInstanceTypeStable]).To(Equal(localDNSIncompatibleSKU),
			"node should have been provisioned with the pinned SKU")

		By("Verifying LocalDNS is off on the provisioned node")
		expectNodeLocalDNSLabel(node, "disabled")
		expectDNSResult(getDNSResultFromNode(node), azureDNSIP, "Host network DNS should use default DNS")
		expectDNSResult(getDNSResultFromPod(pod), coreDNSServiceIP, "Test pod should use default DNS")

		By("✓ Verified LocalDNS stayed off and the NodePool kept provisioning")
	})

	It("should resolve Preferred to Enabled when the NodePool admits a LocalDNS-compatible instance type", func() {
		By("Configuring the NodeClass with LocalDNS Mode=Preferred")
		nodeClass.Spec.LocalDNS = preferredLocalDNS()

		By(fmt.Sprintf("Pinning the NodePool to %s, which meets the LocalDNS resource floor", localDNSCompatibleSKU))
		pinNodePoolToSKU(nodePool, localDNSCompatibleSKU)

		env.ExpectCreated(nodeClass, nodePool)

		By("Expecting the gate to pass and LocalDNS to resolve to Enabled")
		expectLocalDNSResolution(nodeClass, v1beta1.LocalDNSStateEnabled, v1beta1.ConditionTypeLocalDNSReady)

		By("Provisioning a node and verifying LocalDNS is active on it")
		pod := createDNSTestPod("microsoft.com", nil)
		env.ExpectCreated(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthy(pod)

		Expect(node.Labels[corev1.LabelInstanceTypeStable]).To(Equal(localDNSCompatibleSKU),
			"node should have been provisioned with the pinned SKU")

		expectNodeLocalDNSLabel(node, "enabled")
		expectDNSResult(getDNSResultFromNode(node), localDNSNodeListenerIP, "Host network DNS should use LocalDNS node listener")
		expectDNSResult(getDNSResultFromPod(pod), localDNSClusterListenerIP, "Test pod should use LocalDNS cluster listener")

		By("✓ Verified LocalDNS was enabled and is serving DNS on the node")
	})

	It("should keep LocalDNS Enabled when the NodePool is narrowed to small SKUs afterwards", func() {
		By("Configuring the NodeClass with LocalDNS Mode=Preferred and a compatible NodePool")
		nodeClass.Spec.LocalDNS = preferredLocalDNS()
		pinNodePoolToSKU(nodePool, localDNSCompatibleSKU)
		env.ExpectCreated(nodeClass, nodePool)

		expectLocalDNSResolution(nodeClass, v1beta1.LocalDNSStateEnabled, v1beta1.ConditionTypeLocalDNSReady)

		By(fmt.Sprintf("Narrowing the NodePool to %s, below the LocalDNS resource floor", localDNSIncompatibleSKU))
		pinNodePoolToSKU(nodePool, localDNSIncompatibleSKU)
		env.ExpectUpdated(nodePool)

		By("Expecting LocalDNS to stay Enabled across at least one Preferred requeue interval")
		// Sticky-Enabled is deliberate: flipping back to Disabled would change the
		// NodeClass hash, drift every NodeClaim, and reimage the whole pool. The
		// window has to exceed the 5m Preferred requeue to prove the gate re-ran.
		Consistently(func(g Gomega) {
			var nc v1beta1.AKSNodeClass
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), &nc)).To(Succeed())
			g.Expect(lo.FromPtr(nc.Status.LocalDNSState)).To(Equal(v1beta1.LocalDNSStateEnabled))
		}).WithTimeout(6 * time.Minute).WithPolling(30 * time.Second).Should(Succeed())

		By("✓ Verified sticky-Enabled survives a NodePool narrowing")
	})

	It("should resolve Preferred to Disabled when no NodePool references the AKSNodeClass", func() {
		By("Creating a Preferred NodeClass with no NodePool referencing it")
		nodeClass.Spec.LocalDNS = preferredLocalDNS()
		env.ExpectCreated(nodeClass)

		By("Expecting LocalDNS to resolve to Disabled with reason NoReferencingNodePools")
		expectLocalDNSResolution(nodeClass, v1beta1.LocalDNSStateDisabled, "NoReferencingNodePools")

		By("✓ Verified the gate defers rather than enabling with nothing to evaluate against")
	})
})

const (
	// localDNSIncompatibleSKU is below the LocalDNS resource floor (2 vCPU), so
	// enabling LocalDNS would filter it out of the instance type list entirely.
	localDNSIncompatibleSKU = "Standard_D2s_v3"
	// localDNSCompatibleSKU meets the LocalDNS resource floor (4 vCPU).
	localDNSCompatibleSKU = "Standard_D4s_v3"

	// localDNSResolutionTimeout bounds how long we wait for the nodeclass.status
	// controller to resolve Mode=Preferred. The gate specs create the NodeClass
	// and its NodePool together, and the gate's verdict depends on the NodePool
	// being visible. Resolution is event-driven on both objects, but the two
	// creates race, so the first reconcile can still land before the NodePool is
	// observed and record NoReferencingNodePools. This has to leave room for the
	// requeue that corrects that -- keep it above the 3m floor set by
	// subnet.go's healthyRequeueInterval, which is the smallest interval
	// result.Min picks across the status subreconcilers.
	localDNSResolutionTimeout = 6 * time.Minute
)

// preferredLocalDNS returns the LocalDNS spec used by the gate tests: Preferred
// mode with the same complete override set the happy-path test uses, so that a
// gate failure is the only thing that can keep LocalDNS off.
func preferredLocalDNS() *v1beta1.LocalDNS {
	return &v1beta1.LocalDNS{
		Mode:             v1beta1.LocalDNSModePreferred,
		KubeDNSOverrides: completeKubeDNSOverrides,
		VnetDNSOverrides: completeVnetDNSOverrides,
	}
}

// pinNodePoolToSKU restricts the NodePool to exactly one instance type. The
// default NodePool already requires sku-family D, so both SKUs used here stay
// consistent with it.
func pinNodePoolToSKU(nodePool *karpv1.NodePool, sku string) {
	coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
		Key:      corev1.LabelInstanceTypeStable,
		Operator: corev1.NodeSelectorOpIn,
		Values:   []string{sku},
	})
}

// expectLocalDNSResolution waits for the nodeclass.status controller to commit a
// LocalDNS state and asserts both the state and the reason recorded on
// LocalDNSReady. The reason is what an operator sees when a NodePool stops
// provisioning, so it is part of the contract, not incidental detail.
func expectLocalDNSResolution(nodeClass *v1beta1.AKSNodeClass, expectedState v1beta1.LocalDNSState, expectedReason string) {
	Eventually(func(g Gomega) {
		var nc v1beta1.AKSNodeClass
		g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), &nc)).To(Succeed())

		g.Expect(nc.Status.LocalDNSState).ToNot(BeNil(), "LocalDNSState should have been resolved")
		g.Expect(*nc.Status.LocalDNSState).To(Equal(expectedState))

		condition := nc.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
		g.Expect(condition).ToNot(BeNil(), "LocalDNSReady condition should be set")
		g.Expect(condition.Reason).To(Equal(expectedReason),
			fmt.Sprintf("LocalDNSReady reason was %q with message %q", condition.Reason, condition.Message))

		By(fmt.Sprintf("✓ LocalDNSState=%s, LocalDNSReady reason=%s", *nc.Status.LocalDNSState, condition.Reason))
	}).WithTimeout(localDNSResolutionTimeout).WithPolling(10 * time.Second).Should(Succeed())
}

// skipUnlessPreferredReachesInstanceTypeGate skips the gate specs when this
// cluster's Karpenter build cannot resolve Mode=Preferred as far as the
// instance type gate. Every gate ahead of it (k8s version floor, BYO CNI,
// Ubuntu 20.04, conflicting NetworkPolicy, upstream node-local-dns DaemonSet)
// short-circuits to Disabled with a bare LocalDNSReady reason, so a Preferred
// NodeClass with nothing referencing it lands on NoReferencingNodePools exactly
// when the gate is reachable. Probing beats hardcoding a version here: the
// version floor is a controller-side constant this suite cannot read.
func skipUnlessPreferredReachesInstanceTypeGate() {
	probe := env.DefaultAKSNodeClass()
	probe.Spec.LocalDNS = preferredLocalDNS()
	env.ExpectCreated(probe)
	defer env.ExpectDeleted(probe)

	var reason string
	Eventually(func(g Gomega) {
		var nc v1beta1.AKSNodeClass
		g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(probe), &nc)).To(Succeed())
		condition := nc.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
		g.Expect(condition).ToNot(BeNil())
		g.Expect(condition.IsTrue()).To(BeTrue(), "LocalDNS resolution should have completed")
		reason = condition.Reason
	}).WithTimeout(localDNSResolutionTimeout).WithPolling(10 * time.Second).Should(Succeed())

	if reason != "NoReferencingNodePools" {
		Skip(fmt.Sprintf("Mode=Preferred resolves before the instance type gate on this cluster (LocalDNSReady reason=%q); "+
			"the gate specs cannot exercise anything here", reason))
	}
}

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

// createDNSTestPod creates a pod that performs a DNS lookup for a specific domain.
// Use a direct pod because the test reads logs from each exact DNS probe and may use it to trigger provisioning.
// The pod is designed to trigger Karpenter to provision a new node when no node selector is provided and verify DNS configuration.
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

	// Use a direct pod because the test reads logs from this exact hostNetwork probe and deletes it immediately.
	testPod := env.Pod(coretest.PodOptions{
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
