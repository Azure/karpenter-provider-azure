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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	// Label key for artifact streaming state
	artifactStreamingStateLabelKey = "kubernetes.azure.com/artifact-streaming-state"

	// Test timeouts
	artifactStreamingTestTimeout = 5 * time.Minute
)

// ArtifactStreamingTestResult holds the results of artifact streaming verification
type ArtifactStreamingTestResult struct {
	Logs    string
	Enabled bool
	Success bool
}

var _ = Describe("ArtifactStreaming", func() {
	// Note: These tests verify Karpenter's configuration flow (labels, drift detection)
	// AND check for artifact streaming infrastructure on nodes.
	// Infrastructure verification (overlaybd daemon/config) requires NPS on NAP/managed clusters.

	BeforeEach(func() {
		if env.InClusterController {
			Skip("ArtifactStreaming tests require NPS (Node Provisioning Service) - only supported in NAP/managed Karpenter mode")
		}
	})

	// =========================================================================
	// ARTIFACT STREAMING LABEL AND DRIFT TEST
	// Verifies that Karpenter correctly sets labels and triggers drift when config changes
	// =========================================================================
	It("should set artifact streaming labels and detect drift on config change", func() {
		By("[PART 1: ENABLE ARTIFACT STREAMING] Configuring NodeClass with ArtifactStreaming enabled")
		nodeClass.Spec.ArtifactStreaming = &v1beta1.ArtifactStreamingSettings{
			Mode: lo.ToPtr(v1beta1.ArtifactStreamingModeEnabled),
		}

		By("Creating unschedulable pod to trigger node provisioning")
		enabledPod := coretest.Pod()
		env.ExpectCreated(nodeClass, nodePool, enabledPod)

		By("Waiting for node to be provisioned with artifact streaming enabled")
		enabledNode := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthy(enabledPod)

		By(fmt.Sprintf("Node %s created with ArtifactStreaming enabled", enabledNode.Name))

		expectNodeArtifactStreamingLabel(enabledNode, "enabled")

		By("Verifying artifact streaming infrastructure on the node")
		enabledResult := getArtifactStreamingStatusFromNode(enabledNode)
		expectArtifactStreamingResult(enabledResult, true)

		// PART 2: Test drift detection
		By("[PART 2: DISABLE ARTIFACT STREAMING] Disabling ArtifactStreaming to test drift detection")
		nodeClass.Spec.ArtifactStreaming = &v1beta1.ArtifactStreamingSettings{
			Mode: lo.ToPtr(v1beta1.ArtifactStreamingModeDisabled),
		}
		env.ExpectUpdated(nodeClass)

		By("Waiting for new node to be provisioned with disabled ArtifactStreaming (drift will replace the old node)")
		newNodes := env.EventuallyExpectCreatedNodeCount("==", 2)
		var disabledNode *corev1.Node
		for i := range newNodes {
			if newNodes[i].Name != enabledNode.Name {
				disabledNode = newNodes[i]
				break
			}
		}
		Expect(disabledNode).ToNot(BeNil(), "Should have provisioned a new node due to drift")

		By(fmt.Sprintf("New node %s provisioned to replace old node %s", disabledNode.Name, enabledNode.Name))

		expectNodeArtifactStreamingLabel(disabledNode, "disabled")

		By("Verifying artifact streaming is disabled on the new node")
		disabledResult := getArtifactStreamingStatusFromNode(disabledNode)
		expectArtifactStreamingResult(disabledResult, false)
	})

	It("should provision a node with artifact streaming disabled by default (Unspecified mode)", func() {
		By("Configuring NodeClass with ArtifactStreaming mode Unspecified")
		nodeClass.Spec.ArtifactStreaming = &v1beta1.ArtifactStreamingSettings{
			Mode: lo.ToPtr(v1beta1.ArtifactStreamingModeUnspecified),
		}

		pod := coretest.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)

		By("Waiting for node to be provisioned")
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthy(pod)

		By(fmt.Sprintf("Node %s created with ArtifactStreaming Unspecified (defaults to disabled)", node.Name))

		expectNodeArtifactStreamingLabel(node, "disabled")
		expectArtifactStreamingResult(getArtifactStreamingStatusFromNode(node), false)
	})

	It("should provision a node without artifact streaming when not specified", func() {
		By("Creating NodeClass without ArtifactStreaming configuration")
		// nodeClass.Spec.ArtifactStreaming is nil by default

		pod := coretest.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)

		By("Waiting for node to be provisioned")
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthy(pod)

		By(fmt.Sprintf("Node %s created without ArtifactStreaming configuration", node.Name))

		expectNodeArtifactStreamingLabel(node, "disabled")
		expectArtifactStreamingResult(getArtifactStreamingStatusFromNode(node), false)
	})
})

// =========================================================================
// HELPER FUNCTIONS
// =========================================================================

// expectArtifactStreamingResult verifies the artifact streaming verification result matches expected state.
func expectArtifactStreamingResult(result ArtifactStreamingTestResult, expectedEnabled bool) {
	By(fmt.Sprintf("Artifact streaming verification - Expected enabled: %v, Actual enabled: %v", expectedEnabled, result.Enabled))
	By(fmt.Sprintf("Verification logs:\n%s", result.Logs))
	Expect(result.Success).To(BeTrue(), "Artifact streaming verification should succeed")
	Expect(result.Enabled).To(Equal(expectedEnabled), fmt.Sprintf("Artifact streaming should be %v", map[bool]string{true: "enabled", false: "disabled"}[expectedEnabled]))
}

// expectNodeArtifactStreamingLabel verifies that a node has the expected artifact-streaming-state label value.
// This function waits for the label to appear on the node with the correct value.
func expectNodeArtifactStreamingLabel(node *corev1.Node, expectedValue string) {
	By(fmt.Sprintf("Verifying node %s has artifact-streaming-state=%s label", node.Name, expectedValue))
	Eventually(func(g Gomega) {
		var currentNode corev1.Node
		g.Expect(env.Client.Get(env.Context, client.ObjectKey{Name: node.Name}, &currentNode)).To(Succeed())

		labelValue, exists := currentNode.Labels[artifactStreamingStateLabelKey]
		g.Expect(exists).To(BeTrue(), fmt.Sprintf("Node %s should have artifact-streaming-state label", node.Name))
		g.Expect(labelValue).To(Equal(expectedValue), fmt.Sprintf("ArtifactStreaming state should be %s", expectedValue))
	}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

// getArtifactStreamingStatusFromNode creates a privileged pod on the node to check for artifact streaming indicators
func getArtifactStreamingStatusFromNode(node *corev1.Node) ArtifactStreamingTestResult {
	var result ArtifactStreamingTestResult

	By(fmt.Sprintf("Creating privileged pod on node %s to verify artifact streaming status", node.Name))

	// Create a privileged pod that can access host processes and filesystem
	privileged := true
	testPod := coretest.Pod(coretest.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "artifact-streaming-check-",
			Namespace:    "default",
		},
		Image: "mcr.microsoft.com/cbl-mariner/base/core:2.0",
		Command: []string{
			"sh", "-c",
			`echo "=== Checking for artifact streaming indicators ===" && \
			echo "--- Checking for overlaybd-tcmu process ---" && \
			if ps aux 2>/dev/null | grep -v grep | grep -q overlaybd-tcmu; then \
				echo "overlaybd-tcmu process FOUND"; \
				ps aux | grep overlaybd-tcmu | grep -v grep; \
			else \
				echo "overlaybd-tcmu process NOT FOUND"; \
			fi && \
			echo "--- Checking for /etc/overlaybd directory ---" && \
			if [ -d /host/etc/overlaybd ]; then \
				echo "/etc/overlaybd directory FOUND"; \
				ls -la /host/etc/overlaybd/ 2>/dev/null || echo "Could not list directory"; \
			else \
				echo "/etc/overlaybd directory NOT FOUND"; \
			fi && \
			echo "--- Checking containerd config for overlaybd ---" && \
			if [ -f /host/etc/containerd/config.toml ]; then \
				if grep -q overlaybd /host/etc/containerd/config.toml 2>/dev/null; then \
					echo "overlaybd FOUND in containerd config"; \
					grep -A5 -B5 overlaybd /host/etc/containerd/config.toml 2>/dev/null || true; \
				else \
					echo "overlaybd NOT FOUND in containerd config"; \
				fi; \
			else \
				echo "containerd config file not found"; \
			fi && \
			echo "=== Artifact streaming check complete ===" && \
			sleep 30`,
		},
		NodeSelector: map[string]string{
			corev1.LabelHostname: node.Name,
		},
		RestartPolicy: corev1.RestartPolicyNever,
	})

	// Configure privileged access
	testPod.Spec.HostPID = true
	testPod.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{
		Privileged: &privileged,
	}
	// Mount host filesystem
	testPod.Spec.Volumes = []corev1.Volume{
		{
			Name: "host-root",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/",
				},
			},
		},
	}
	testPod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
		{
			Name:      "host-root",
			MountPath: "/host",
			ReadOnly:  true,
		},
	}

	env.ExpectCreated(testPod)
	defer func() {
		By("Cleaning up artifact streaming check pod")
		env.ExpectDeleted(testPod)
	}()

	// Wait for pod to be running or completed
	Eventually(func(g Gomega) {
		var currentPod corev1.Pod
		g.Expect(env.Client.Get(env.Context, client.ObjectKey{Name: testPod.Name, Namespace: testPod.Namespace}, &currentPod)).To(Succeed())
		g.Expect(currentPod.Status.Phase).To(Or(Equal(corev1.PodRunning), Equal(corev1.PodSucceeded)), "Pod should be running or completed")
	}).WithTimeout(2 * time.Minute).Should(Succeed())

	// Get logs from the pod
	Eventually(func(g Gomega) {
		g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(testPod), testPod)).To(Succeed())

		if testPod.Status.Phase != corev1.PodRunning && testPod.Status.Phase != corev1.PodSucceeded {
			return
		}

		req := env.KubeClient.CoreV1().Pods(testPod.Namespace).GetLogs(testPod.Name, &corev1.PodLogOptions{
			Container: testPod.Spec.Containers[0].Name,
		})
		podLogs, err := req.Stream(context.Background())
		g.Expect(err).To(Succeed())
		defer podLogs.Close()

		buf := new(bytes.Buffer)
		_, err = io.Copy(buf, podLogs)
		g.Expect(err).To(Succeed())

		result.Logs = buf.String()
		result.Success = true
	}).WithTimeout(artifactStreamingTestTimeout).Should(Succeed())

	// Parse the logs to determine if artifact streaming is enabled
	result.Enabled = isArtifactStreamingEnabled(result.Logs)

	By(fmt.Sprintf("Artifact streaming check output from node %s:\n%s", node.Name, result.Logs))

	return result
}

// isArtifactStreamingEnabled parses the verification logs to determine if artifact streaming is enabled
func isArtifactStreamingEnabled(logs string) bool {
	// Check for positive indicators
	hasOverlaybdProcess := strings.Contains(logs, "overlaybd-tcmu process FOUND")
	hasOverlaybdConfig := strings.Contains(logs, "/etc/overlaybd directory FOUND")
	hasContainerdConfig := strings.Contains(logs, "overlaybd FOUND in containerd config")

	// Any of these indicators means artifact streaming is enabled
	return hasOverlaybdProcess || hasOverlaybdConfig || hasContainerdConfig
}
