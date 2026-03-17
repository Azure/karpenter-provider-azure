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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	// Label key for artifact streaming - matches AKS RP label
	artifactStreamingEnabledLabelKey = "kubernetes.azure.com/artifactstreaming-enabled"

	artifactStreamingTestTimeout = 5 * time.Minute
)

var _ = Describe("ArtifactStreaming", func() {
	BeforeEach(func() {
		if env.InClusterController {
			Skip("ArtifactStreaming tests require NPS (Node Provisioning Service) - only supported in NAP/managed Karpenter mode")
		}
	})

	It("should set artifact streaming label and enable infrastructure when explicitly enabled", func() {
		enabled := true
		nodeClass.Spec.ArtifactStreaming = &v1beta1.ArtifactStreaming{
			Enabled: &enabled,
		}

		pod := coretest.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)

		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		Expect(node.Labels).To(HaveKeyWithValue(artifactStreamingEnabledLabelKey, "true"))
		verifyArtifactStreamingOnNode(node, true)
	})

	It("should set artifact streaming label and enable infrastructure when not specified (defaults to enabled)", func() {
		// nodeClass.Spec.ArtifactStreaming is nil by default

		pod := coretest.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)

		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		Expect(node.Labels).To(HaveKeyWithValue(artifactStreamingEnabledLabelKey, "true"))
		verifyArtifactStreamingOnNode(node, true)
	})

	It("should not set artifact streaming label or enable infrastructure when explicitly disabled", func() {
		disabled := false
		nodeClass.Spec.ArtifactStreaming = &v1beta1.ArtifactStreaming{
			Enabled: &disabled,
		}

		pod := coretest.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)

		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		Expect(node.Labels).ToNot(HaveKey(artifactStreamingEnabledLabelKey))
		verifyArtifactStreamingOnNode(node, false)
	})
})

// verifyArtifactStreamingOnNode checks for artifact streaming infrastructure (overlaybd process/config) on the node.
func verifyArtifactStreamingOnNode(node *corev1.Node, expectEnabled bool) {
	By(fmt.Sprintf("Verifying artifact streaming infrastructure on node %s (expect enabled: %v)", node.Name, expectEnabled))

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
			else \
				echo "overlaybd-tcmu process NOT FOUND"; \
			fi && \
			echo "--- Checking containerd config for overlaybd ---" && \
			if [ -f /host/etc/containerd/config.toml ]; then \
				if grep -q overlaybd /host/etc/containerd/config.toml 2>/dev/null; then \
					echo "overlaybd FOUND in containerd config"; \
				else \
					echo "overlaybd NOT FOUND in containerd config"; \
				fi; \
			else \
				echo "containerd config file not found"; \
			fi && \
			echo "=== Artifact streaming check complete ==="`,
		},
		NodeSelector: map[string]string{
			corev1.LabelHostname: node.Name,
		},
		RestartPolicy: corev1.RestartPolicyNever,
	})

	testPod.Spec.HostPID = true
	testPod.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{
		Privileged: &privileged,
	}
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
		env.ExpectDeleted(testPod)
	}()

	Eventually(func(g Gomega) {
		var currentPod corev1.Pod
		g.Expect(env.Client.Get(env.Context, client.ObjectKey{Name: testPod.Name, Namespace: testPod.Namespace}, &currentPod)).To(Succeed())
		g.Expect(currentPod.Status.Phase).To(Or(Equal(corev1.PodRunning), Equal(corev1.PodSucceeded)))
	}).WithTimeout(2 * time.Minute).Should(Succeed())

	var logs string
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
		logs = buf.String()
	}).WithTimeout(artifactStreamingTestTimeout).Should(Succeed())

	By(fmt.Sprintf("Artifact streaming check output from node %s:\n%s", node.Name, logs))

	hasOverlaybdProcess := strings.Contains(logs, "overlaybd-tcmu process FOUND")
	hasContainerdConfig := strings.Contains(logs, "overlaybd FOUND in containerd config")
	actuallyEnabled := hasOverlaybdProcess || hasContainerdConfig

	if expectEnabled {
		Expect(actuallyEnabled).To(BeTrue(), fmt.Sprintf("Artifact streaming should be enabled on node %s\nLogs:\n%s", node.Name, logs))
	} else {
		Expect(actuallyEnabled).To(BeFalse(), fmt.Sprintf("Artifact streaming should be disabled on node %s\nLogs:\n%s", node.Name, logs))
	}
}
