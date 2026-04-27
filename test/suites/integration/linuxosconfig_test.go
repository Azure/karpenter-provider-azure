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
	// How these tests work:
	//
	// 1. We create an AKSNodeClass with linuxOSConfig (sysctls, transparent huge pages, swap) and a NodePool.
	// 2. We schedule a pod that triggers Karpenter to provision a new node via AKS.
	// 3. Once the node is ready, we create a "verification pod" on that node to read back
	//    the kernel settings and confirm they match what we configured.
	//
	// How verification pods read settings:
	//   There's no Kubernetes API to read a node's kernel parameters. The only way is to
	//   run a process on the node and read from the kernel's virtual filesystems:
	//   - Sysctls: the `sysctl` command reads /proc/sys/ (e.g., /proc/sys/net/core/somaxconn)
	//   - Transparent huge pages: read /sys/kernel/mm/transparent_hugepage/enabled and .../defrag
	//   - Swap: read /proc/meminfo for SwapTotal
	//   The verification pod runs with privileged: true to access these host kernel paths,
	//   and we pin it to the target node using a nodeSelector on the hostname label.
	//   We then read the pod's stdout logs to get the values and assert on them.
	//
	// Why hostNetwork is needed for sysctl verification:
	//   Linux net.* sysctls are per-network-namespace. Each container gets its own network
	//   namespace with default values, even with privileged: true. Without hostNetwork,
	//   `sysctl net.core.somaxconn` returns the container's default (4096), not the host's
	//   configured value (e.g., 8192). Setting hostNetwork: true puts the pod in the host's
	//   network namespace so sysctl reads return the actual host values.
	//   Other sysctl namespaces (vm.*, fs.*, kernel.*) are kernel-global and visible from
	//   any container. Transparent huge pages (/sys/kernel/mm/) and swap (/proc/meminfo) are also kernel-global.

	BeforeEach(func() {
		// LinuxOSConfig is not wired through the aksscriptless bootstrap path.
		// Only skip for self-hosted aksscriptless (InClusterController=true, ProvisionMode=aksscriptless).
		// NAP E2E doesn't set PROVISION_MODE (defaults to aksscriptless) but runs via CCP
		// (InClusterController=false), which uses bootstrappingclient — LinuxOSConfig works there.
		if env.ProvisionMode == consts.ProvisionModeAKSScriptless && env.InClusterController {
			Skip("LinuxOSConfig is not wired through the aksscriptless bootstrap path")
		}
	})

	It("should provision a node with sysctl tuning applied", func() {
		nodeClass.Spec.LinuxOSConfig = &v1beta1.LinuxOSConfiguration{
			Sysctls: &v1beta1.SysctlConfiguration{
				FsAioMaxNr:                     lo.ToPtr[int32](131072),
				FsFileMax:                      lo.ToPtr[int32](12000),
				FsInotifyMaxUserWatches:        lo.ToPtr[int32](781250),
				FsNrOpen:                       lo.ToPtr[int32](2097152),
				KernelThreadsMax:               lo.ToPtr[int32](100000),
				NetCoreNetdevMaxBacklog:        lo.ToPtr[int32](2000),
				NetCoreOptmemMax:               lo.ToPtr[int32](40960),
				NetCoreRmemDefault:             lo.ToPtr[int32](262144),
				NetCoreRmemMax:                 lo.ToPtr[int32](134217728),
				NetCoreSomaxconn:               lo.ToPtr[int32](8192),
				NetCoreWmemDefault:             lo.ToPtr[int32](262144),
				NetCoreWmemMax:                 lo.ToPtr[int32](134217728),
				NetIPv4IPLocalPortRange:        lo.ToPtr("1024 65535"),
				NetIPv4NeighDefaultGcThresh1:   lo.ToPtr[int32](512),
				NetIPv4NeighDefaultGcThresh2:   lo.ToPtr[int32](2048),
				NetIPv4NeighDefaultGcThresh3:   lo.ToPtr[int32](4096),
				NetIPv4TCPFinTimeout:           lo.ToPtr[int32](30),
				NetIPv4TCPKeepaliveTime:        lo.ToPtr[int32](120),
				NetIPv4TCPKeepaliveIntvl:       lo.ToPtr[int32](30),
				NetIPv4TCPKeepaliveProbes:      lo.ToPtr[int32](8),
				NetIPv4TCPMaxSynBacklog:        lo.ToPtr[int32](8192),
				NetIPv4TCPMaxTwBuckets:         lo.ToPtr[int32](32000),
				NetIPv4TCPTwReuse:              lo.ToPtr(true),
				NetNetfilterNfConntrackMax:     lo.ToPtr[int32](524288),
				NetNetfilterNfConntrackBuckets: lo.ToPtr[int32](131072),
				VMMaxMapCount:                  lo.ToPtr[int32](128000),
				VMSwappiness:                   lo.ToPtr[int32](10),
				VMVfsCachePressure:             lo.ToPtr[int32](50),
			},
		}

		pod := coretest.UnschedulablePod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		By(fmt.Sprintf("Node %s provisioned, verifying sysctl settings", node.Name))

		// Create a privileged pod on the node to read sysctl values
		verifyPod := createSysctlVerificationPod(node.Name, []string{
			"fs.aio-max-nr",
			"fs.file-max",
			"fs.inotify.max_user_watches",
			"fs.nr_open",
			"kernel.threads-max",
			"net.core.netdev_max_backlog",
			"net.core.optmem_max",
			"net.core.rmem_default",
			"net.core.rmem_max",
			"net.core.somaxconn",
			"net.core.wmem_default",
			"net.core.wmem_max",
			"net.ipv4.ip_local_port_range",
			"net.ipv4.neigh.default.gc_thresh1",
			"net.ipv4.neigh.default.gc_thresh2",
			"net.ipv4.neigh.default.gc_thresh3",
			"net.ipv4.tcp_fin_timeout",
			"net.ipv4.tcp_keepalive_time",
			"net.ipv4.tcp_keepalive_intvl",
			"net.ipv4.tcp_keepalive_probes",
			"net.ipv4.tcp_max_syn_backlog",
			"net.ipv4.tcp_max_tw_buckets",
			"net.ipv4.tcp_tw_reuse",
			"net.netfilter.nf_conntrack_max",
			"net.netfilter.nf_conntrack_buckets",
			"vm.max_map_count",
			"vm.swappiness",
			"vm.vfs_cache_pressure",
		})
		env.ExpectCreated(verifyPod)
		defer env.ExpectDeleted(verifyPod)

		logs := eventuallyGetPodLogs(verifyPod)
		By(fmt.Sprintf("Sysctl verification output:\n%s", logs))

		Expect(logs).To(ContainSubstring("fs.aio-max-nr = 131072"))
		Expect(logs).To(ContainSubstring("fs.file-max = 12000"))
		Expect(logs).To(ContainSubstring("fs.inotify.max_user_watches = 781250"))
		Expect(logs).To(ContainSubstring("fs.nr_open = 2097152"))
		Expect(logs).To(ContainSubstring("kernel.threads-max = 100000"))
		Expect(logs).To(ContainSubstring("net.core.netdev_max_backlog = 2000"))
		Expect(logs).To(ContainSubstring("net.core.optmem_max = 40960"))
		Expect(logs).To(ContainSubstring("net.core.rmem_default = 262144"))
		Expect(logs).To(ContainSubstring("net.core.rmem_max = 134217728"))
		Expect(logs).To(ContainSubstring("net.core.somaxconn = 8192"))
		Expect(logs).To(ContainSubstring("net.core.wmem_default = 262144"))
		Expect(logs).To(ContainSubstring("net.core.wmem_max = 134217728"))
		Expect(logs).To(ContainSubstring("net.ipv4.ip_local_port_range = 1024\t65535"))
		Expect(logs).To(ContainSubstring("net.ipv4.neigh.default.gc_thresh1 = 512"))
		Expect(logs).To(ContainSubstring("net.ipv4.neigh.default.gc_thresh2 = 2048"))
		Expect(logs).To(ContainSubstring("net.ipv4.neigh.default.gc_thresh3 = 4096"))
		Expect(logs).To(ContainSubstring("net.ipv4.tcp_fin_timeout = 30"))
		Expect(logs).To(ContainSubstring("net.ipv4.tcp_keepalive_time = 120"))
		Expect(logs).To(ContainSubstring("net.ipv4.tcp_keepalive_intvl = 30"))
		Expect(logs).To(ContainSubstring("net.ipv4.tcp_keepalive_probes = 8"))
		Expect(logs).To(ContainSubstring("net.ipv4.tcp_max_syn_backlog = 8192"))
		Expect(logs).To(ContainSubstring("net.ipv4.tcp_max_tw_buckets = 32000"))
		Expect(logs).To(ContainSubstring("net.ipv4.tcp_tw_reuse = 1"))
		Expect(logs).To(ContainSubstring("net.netfilter.nf_conntrack_max = 524288"))
		Expect(logs).To(ContainSubstring("net.netfilter.nf_conntrack_buckets = 131072"))
		Expect(logs).To(ContainSubstring("vm.max_map_count = 128000"))
		Expect(logs).To(ContainSubstring("vm.swappiness = 10"))
		Expect(logs).To(ContainSubstring("vm.vfs_cache_pressure = 50"))
	})

	It("should provision a node with transparent huge page settings applied", func() {
		nodeClass.Spec.LinuxOSConfig = &v1beta1.LinuxOSConfiguration{
			TransparentHugePageEnabled: lo.ToPtr(v1beta1.TransparentHugePageEnabledMadvise),
			TransparentHugePageDefrag:  lo.ToPtr(v1beta1.TransparentHugePageDefragDeferMadvise),
		}

		pod := coretest.UnschedulablePod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		By(fmt.Sprintf("Node %s provisioned, verifying transparent huge page settings", node.Name))

		// Create a privileged pod on the node to read transparent huge page settings
		verifyPod := createTransparentHugePageVerificationPod(node.Name)
		env.ExpectCreated(verifyPod)
		defer env.ExpectDeleted(verifyPod)

		logs := eventuallyGetPodLogs(verifyPod)
		By(fmt.Sprintf("Transparent huge page verification output:\n%s", logs))

		// The kernel shows the active setting in brackets, e.g.: always [madvise] never
		// We use tagged output (transparent_hugepage_enabled=...) to distinguish the two files,
		// and match on the tag + bracketed value to avoid fragility if kernel adds new options.
		Expect(logs).To(MatchRegexp(`transparent_hugepage_enabled=.*\[madvise\]`))
		Expect(logs).To(MatchRegexp(`transparent_hugepage_defrag=.*\[defer\+madvise\]`))
	})

	It("should provision a node with swap file size configured", func() {
		nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
			FailSwapOn: lo.ToPtr(false),
		}
		nodeClass.Spec.LinuxOSConfig = &v1beta1.LinuxOSConfiguration{
			SwapFileSize: lo.ToPtr("1500Mi"),
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
		// SwapTotal line will show the total swap in kB, which should be ~1500MB.
		// Allow some tolerance since the kernel may report slightly different values.
		Expect(logs).To(ContainSubstring("SwapTotal:"))
		// Verify swap is NOT zero (i.e., swap is enabled)
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
// reads the specified sysctl values from the host's kernel.
// hostNetwork: true is required so net.* sysctls reflect the host's network namespace,
// not the container's default network namespace.
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
			fmt.Sprintf("sysctl %s", sysctlArgs),
		},
		NodeSelector: map[string]string{
			corev1.LabelHostname: nodeName,
		},
		RestartPolicy: corev1.RestartPolicyNever,
	})
	pod.Spec.HostPID = true
	pod.Spec.HostNetwork = true
	pod.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{
		Privileged: lo.ToPtr(true),
	}
	return pod
}

// createTransparentHugePageVerificationPod creates a privileged pod on the target node that
// reads the transparent huge page settings from /sys/kernel/mm/transparent_hugepage/.
// These settings are kernel-global (not namespaced), so a privileged container can read them directly.
// Output is tagged (transparent_hugepage_enabled=..., transparent_hugepage_defrag=...) so assertions can distinguish the two values.
func createTransparentHugePageVerificationPod(nodeName string) *corev1.Pod {
	pod := coretest.Pod(coretest.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "transparent-hugepage-verify-",
			Namespace:    "default",
		},
		Image: "mcr.microsoft.com/azurelinux/busybox:1.36",
		Command: []string{
			"sh", "-c",
			"echo transparent_hugepage_enabled=$(cat /sys/kernel/mm/transparent_hugepage/enabled) && " +
				"echo transparent_hugepage_defrag=$(cat /sys/kernel/mm/transparent_hugepage/defrag)",
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
// checks swap configuration by reading /proc/meminfo.
func createSwapVerificationPod(nodeName string) *corev1.Pod {
	pod := coretest.Pod(coretest.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "swap-verify-",
			Namespace:    "default",
		},
		Image: "mcr.microsoft.com/azurelinux/busybox:1.36",
		Command: []string{
			"sh", "-c",
			"grep SwapTotal /proc/meminfo",
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
