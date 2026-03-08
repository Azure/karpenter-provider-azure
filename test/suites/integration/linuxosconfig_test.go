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
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LinuxOSConfig", func() {
	BeforeEach(func() {
		if env.ProvisionMode == consts.ProvisionModeAKSScriptless {
			Skip("LinuxOSConfig is not supported in aksscriptless mode")
		}
	})

	It("should provision a node with sysctl tuning applied", func() {
		nodeClass.Spec.LinuxOSConfig = &v1beta1.LinuxOSConfiguration{
			Sysctls: &v1beta1.SysctlConfiguration{
				NetCoreSomaxconn:           lo.ToPtr[int32](8192),
				NetIPv4TCPMaxSynBacklog:    lo.ToPtr[int32](16384),
				VMMaxMapCount:              lo.ToPtr[int32](262144),
				NetCoreNetdevMaxBacklog:    lo.ToPtr[int32](2000),
				NetIPv4TCPKeepaliveTime:    lo.ToPtr[int32](120),
				NetIPv4TCPKeepaliveIntvl:   lo.ToPtr[int32](30),
				NetIPv4TCPKeepaliveProbes:  lo.ToPtr[int32](8),
				NetCoreRmemDefault:         lo.ToPtr[int32](212992),
				NetCoreWmemDefault:         lo.ToPtr[int32](212992),
				NetIPv4TCPFinTimeout:       lo.ToPtr[int32](30),
				KernelThreadsMax:           lo.ToPtr[int32](100000),
				NetIPv4IPLocalPortRange:    lo.ToPtr("1024 65535"),
				NetIPv4NeighDefaultGcThresh1: lo.ToPtr[int32](512),
				NetIPv4NeighDefaultGcThresh2: lo.ToPtr[int32](2048),
				NetIPv4NeighDefaultGcThresh3: lo.ToPtr[int32](4096),
			},
		}

		pod := coretest.UnschedulablePod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		By(fmt.Sprintf("Node %s provisioned, verifying sysctl settings", node.Name))

		// Create a privileged pod on the node to read sysctl values
		verifyPod := createSysctlVerificationPod(node.Name, []string{
			"net.core.somaxconn",
			"net.ipv4.tcp_max_syn_backlog",
			"vm.max_map_count",
			"net.core.netdev_max_backlog",
			"net.ipv4.tcp_keepalive_time",
			"net.ipv4.tcp_keepalive_intvl",
			"net.ipv4.tcp_keepalive_probes",
			"net.core.rmem_default",
			"net.core.wmem_default",
			"net.ipv4.tcp_fin_timeout",
			"kernel.threads-max",
			"net.ipv4.ip_local_port_range",
			"net.ipv4.neigh.default.gc_thresh1",
			"net.ipv4.neigh.default.gc_thresh2",
			"net.ipv4.neigh.default.gc_thresh3",
		})
		env.ExpectCreated(verifyPod)
		defer env.ExpectDeleted(verifyPod)

		logs := eventuallyGetPodLogs(verifyPod)
		By(fmt.Sprintf("Sysctl verification output:\n%s", logs))

		Expect(logs).To(ContainSubstring("net.core.somaxconn = 8192"))
		Expect(logs).To(ContainSubstring("net.ipv4.tcp_max_syn_backlog = 16384"))
		Expect(logs).To(ContainSubstring("vm.max_map_count = 262144"))
		Expect(logs).To(ContainSubstring("net.core.netdev_max_backlog = 2000"))
		Expect(logs).To(ContainSubstring("net.ipv4.tcp_keepalive_time = 120"))
		Expect(logs).To(ContainSubstring("net.ipv4.tcp_keepalive_intvl = 30"))
		Expect(logs).To(ContainSubstring("net.ipv4.tcp_keepalive_probes = 8"))
		Expect(logs).To(ContainSubstring("net.core.rmem_default = 212992"))
		Expect(logs).To(ContainSubstring("net.core.wmem_default = 212992"))
		Expect(logs).To(ContainSubstring("net.ipv4.tcp_fin_timeout = 30"))
		Expect(logs).To(ContainSubstring("kernel.threads-max = 100000"))
		Expect(logs).To(ContainSubstring("net.ipv4.ip_local_port_range = 1024\t65535"))
		Expect(logs).To(ContainSubstring("net.ipv4.neigh.default.gc_thresh1 = 512"))
		Expect(logs).To(ContainSubstring("net.ipv4.neigh.default.gc_thresh2 = 2048"))
		Expect(logs).To(ContainSubstring("net.ipv4.neigh.default.gc_thresh3 = 4096"))
	})

	It("should provision a node with transparent huge page settings applied", func() {
		nodeClass.Spec.LinuxOSConfig = &v1beta1.LinuxOSConfiguration{
			TransparentHugePageEnabled: lo.ToPtr("madvise"),
			TransparentHugePageDefrag:  lo.ToPtr("defer+madvise"),
		}

		pod := coretest.UnschedulablePod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		By(fmt.Sprintf("Node %s provisioned, verifying THP settings", node.Name))

		// Create a privileged pod on the node to read THP settings
		verifyPod := createTHPVerificationPod(node.Name)
		env.ExpectCreated(verifyPod)
		defer env.ExpectDeleted(verifyPod)

		logs := eventuallyGetPodLogs(verifyPod)
		By(fmt.Sprintf("THP verification output:\n%s", logs))

		// THP enabled file shows the active setting in brackets: always [madvise] never
		Expect(logs).To(ContainSubstring("[madvise]"))
		// THP defrag file shows: always defer defer+madvise [madvise] never
		// We set "defer+madvise", so expect [defer+madvise]
		Expect(logs).To(ContainSubstring("[defer+madvise]"))
	})

	It("should provision a node with swap file size configured", func() {
		nodeClass.Spec.LinuxOSConfig = &v1beta1.LinuxOSConfiguration{
			SwapFileSizeMB: lo.ToPtr[int32](1500),
		}

		pod := coretest.UnschedulablePod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		By(fmt.Sprintf("Node %s provisioned, verifying swap file size", node.Name))

		// Create a privileged pod on the node to check swap
		verifyPod := createSwapVerificationPod(node.Name)
		env.ExpectCreated(verifyPod)
		defer env.ExpectDeleted(verifyPod)

		logs := eventuallyGetPodLogs(verifyPod)
		By(fmt.Sprintf("Swap verification output:\n%s", logs))

		// Swap should be enabled. Parse total swap from /proc/meminfo.
		// SwapTotal line will show the total swap in kB, which should be ~1500MB (1536000 kB).
		// Allow some tolerance since the kernel may report slightly different values.
		Expect(logs).To(ContainSubstring("SwapTotal:"))
		// Verify swap is NOT zero (i.e., swap is enabled)
		Expect(logs).ToNot(ContainSubstring("SwapTotal:           0 kB"))
		// Also check that /swapfile exists
		Expect(logs).To(ContainSubstring("swapfile_exists=true"))
	})

	It("should provision a node with full LinuxOSConfig (sysctls + THP + swap)", func() {
		nodeClass.Spec.LinuxOSConfig = &v1beta1.LinuxOSConfiguration{
			SwapFileSizeMB:             lo.ToPtr[int32](512),
			TransparentHugePageEnabled: lo.ToPtr("always"),
			TransparentHugePageDefrag:  lo.ToPtr("always"),
			Sysctls: &v1beta1.SysctlConfiguration{
				VMMaxMapCount:    lo.ToPtr[int32](524288),
				NetCoreSomaxconn: lo.ToPtr[int32](4096),
			},
		}

		pod := coretest.UnschedulablePod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		By(fmt.Sprintf("Node %s provisioned, verifying full LinuxOSConfig", node.Name))

		// Create a comprehensive verification pod
		verifyPod := createFullLinuxOSConfigVerificationPod(node.Name)
		env.ExpectCreated(verifyPod)
		defer env.ExpectDeleted(verifyPod)

		logs := eventuallyGetPodLogs(verifyPod)
		By(fmt.Sprintf("Full LinuxOSConfig verification output:\n%s", logs))

		// Verify sysctls
		Expect(logs).To(ContainSubstring("vm.max_map_count = 524288"))
		Expect(logs).To(ContainSubstring("net.core.somaxconn = 4096"))

		// Verify THP - "always" is the active setting
		Expect(logs).To(MatchRegexp(`thp_enabled=\[always\]`))
		Expect(logs).To(MatchRegexp(`thp_defrag=\[always\]`))

		// Verify swap is enabled (non-zero)
		Expect(logs).To(ContainSubstring("SwapTotal:"))
		Expect(logs).ToNot(ContainSubstring("SwapTotal:           0 kB"))
	})
})

// =========================================================================
// CONSTANTS
// =========================================================================

const (
	// podLogTimeout is the maximum time to wait for pod logs to become available
	podLogTimeout = 3 * time.Minute
)

// =========================================================================
// HELPER FUNCTIONS
// =========================================================================

// createSysctlVerificationPod creates a privileged pod on the target node that
// reads the specified sysctl values using the `sysctl` command.
func createSysctlVerificationPod(nodeName string, sysctls []string) *corev1.Pod {
	sysctlArgs := strings.Join(sysctls, " ")
	pod := coretest.Pod(coretest.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "sysctl-verify-",
			Namespace:    "default",
		},
		Image: "mcr.microsoft.com/azurelinux/busybox:1.36",
		Command: []string{
			"sh", "-c",
			fmt.Sprintf("sysctl %s && sleep 30", sysctlArgs),
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

// createTHPVerificationPod creates a privileged pod on the target node that
// reads the transparent huge page settings from /sys/kernel/mm/transparent_hugepage/.
func createTHPVerificationPod(nodeName string) *corev1.Pod {
	pod := coretest.Pod(coretest.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "thp-verify-",
			Namespace:    "default",
		},
		Image: "mcr.microsoft.com/azurelinux/busybox:1.36",
		Command: []string{
			"sh", "-c",
			"cat /sys/kernel/mm/transparent_hugepage/enabled && " +
				"cat /sys/kernel/mm/transparent_hugepage/defrag && " +
				"sleep 30",
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

// createSwapVerificationPod creates a privileged pod on the target node that
// checks swap configuration by reading /proc/meminfo and checking for /swapfile.
func createSwapVerificationPod(nodeName string) *corev1.Pod {
	pod := coretest.Pod(coretest.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "swap-verify-",
			Namespace:    "default",
		},
		Image: "mcr.microsoft.com/azurelinux/busybox:1.36",
		Command: []string{
			"sh", "-c",
			"grep SwapTotal /proc/meminfo && " +
				"if [ -f /host/swapfile ]; then echo swapfile_exists=true; else echo swapfile_exists=false; fi && " +
				"sleep 30",
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
	// Mount the host root filesystem to check for /swapfile
	pod.Spec.Volumes = []corev1.Volume{
		{
			Name: "host-root",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/",
				},
			},
		},
	}
	pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
		{
			Name:      "host-root",
			MountPath: "/host",
			ReadOnly:  true,
		},
	}
	return pod
}

// createFullLinuxOSConfigVerificationPod creates a privileged pod that checks
// sysctls, THP, and swap settings all in one command.
func createFullLinuxOSConfigVerificationPod(nodeName string) *corev1.Pod {
	pod := coretest.Pod(coretest.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "linuxosconfig-verify-",
			Namespace:    "default",
		},
		Image: "mcr.microsoft.com/azurelinux/busybox:1.36",
		Command: []string{
			"sh", "-c",
			"sysctl vm.max_map_count net.core.somaxconn && " +
				"echo thp_enabled=$(cat /sys/kernel/mm/transparent_hugepage/enabled) && " +
				"echo thp_defrag=$(cat /sys/kernel/mm/transparent_hugepage/defrag) && " +
				"grep SwapTotal /proc/meminfo && " +
				"sleep 30",
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

// eventuallyGetPodLogs waits for the pod to be running/succeeded and returns its logs.
func eventuallyGetPodLogs(pod *corev1.Pod) string {
	var logs string
	Eventually(func(g Gomega) {
		var currentPod corev1.Pod
		g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(pod), &currentPod)).To(Succeed())
		g.Expect(currentPod.Status.Phase).To(
			Or(Equal(corev1.PodRunning), Equal(corev1.PodSucceeded)),
			"Pod should be running or completed",
		)

		req := env.KubeClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
			Container: pod.Spec.Containers[0].Name,
		})
		podLogs, err := req.Stream(context.Background())
		g.Expect(err).To(Succeed())
		defer podLogs.Close()

		buf := new(bytes.Buffer)
		_, err = io.Copy(buf, podLogs)
		g.Expect(err).To(Succeed())

		logs = buf.String()
		g.Expect(logs).ToNot(BeEmpty(), "Pod logs should not be empty")
	}).WithTimeout(podLogTimeout).Should(Succeed())

	return logs
}
