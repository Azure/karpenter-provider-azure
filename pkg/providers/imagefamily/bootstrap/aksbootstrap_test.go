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

package bootstrap

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestKubeBinaryURL(t *testing.T) {
	cases := []struct {
		name     string
		version  string
		expected string
	}{
		{
			name:     "Test version 1.24.x",
			version:  "1.24.5",
			expected: fmt.Sprintf("%s/kubernetes/v1.24.5/binaries/kubernetes-node-linux-amd64.tar.gz", globalAKSMirror),
		},
		{
			name:     "Test version 1.25.x",
			version:  "1.25.2",
			expected: fmt.Sprintf("%s/kubernetes/v1.25.2/binaries/kubernetes-node-linux-amd64.tar.gz", globalAKSMirror),
		},
		{
			name:     "Test version 1.26.x",
			version:  "1.26.0",
			expected: fmt.Sprintf("%s/kubernetes/v1.26.0/binaries/kubernetes-node-linux-amd64.tar.gz", globalAKSMirror),
		},
		{
			name:     "Test version 1.27.x",
			version:  "1.27.1",
			expected: fmt.Sprintf("%s/kubernetes/v1.27.1/binaries/kubernetes-node-linux-amd64.tar.gz", globalAKSMirror),
		},
		{
			name:     "Test version 1.33.x",
			version:  "1.33.7",
			expected: fmt.Sprintf("%s/kubernetes/v1.33.7/binaries/kubernetes-node-linux-amd64.tar.gz", globalAKSMirror),
		},
		{
			name:     "Test version 1.34.x",
			version:  "1.34.0",
			expected: "",
		},
		{
			name:     "Test version 1.35.x",
			version:  "1.35.5",
			expected: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual := kubeBinaryURL(tc.version, "amd64")
			if actual != tc.expected {
				t.Errorf("Expected %s but got %s", tc.expected, actual)
			}
		})
	}
}

func TestGetCredentialProviderURL(t *testing.T) {
	tests := []struct {
		version string
		arch    string
		url     string
	}{
		{
			version: "1.33.0",
			arch:    "amd64",
			url:     fmt.Sprintf("%s/cloud-provider-azure/v1.33.0/binaries/azure-acr-credential-provider-linux-amd64-v1.33.0.tar.gz", globalAKSMirror),
		},
		{
			version: "1.32.0",
			arch:    "amd64",
			url:     fmt.Sprintf("%s/cloud-provider-azure/v1.32.5/binaries/azure-acr-credential-provider-linux-amd64-v1.32.5.tar.gz", globalAKSMirror),
		},
		{
			version: "1.31.0",
			arch:    "amd64",
			url:     fmt.Sprintf("%s/cloud-provider-azure/v1.31.6/binaries/azure-acr-credential-provider-linux-amd64-v1.31.6.tar.gz", globalAKSMirror),
		},
		{
			version: "1.30.2",
			arch:    "amd64",
			url:     fmt.Sprintf("%s/cloud-provider-azure/v1.30.12/binaries/azure-acr-credential-provider-linux-amd64-v1.30.12.tar.gz", globalAKSMirror),
		},
		{
			version: "1.30.0",
			arch:    "amd64",
			url:     fmt.Sprintf("%s/cloud-provider-azure/v1.30.12/binaries/azure-acr-credential-provider-linux-amd64-v1.30.12.tar.gz", globalAKSMirror),
		},
		{
			version: "1.30.0",
			arch:    "arm64",
			url:     fmt.Sprintf("%s/cloud-provider-azure/v1.30.12/binaries/azure-acr-credential-provider-linux-arm64-v1.30.12.tar.gz", globalAKSMirror),
		},
		{
			version: "1.29.2",
			arch:    "amd64",
			url:     "",
		},
		{
			version: "1.29.0",
			arch:    "amd64",
			url:     "",
		},
		{
			version: "1.29.0",
			arch:    "arm64",
			url:     "",
		},
		{
			version: "1.28.7",
			arch:    "amd64",
			url:     "",
		},
	}
	for _, tt := range tests {
		url := CredentialProviderURL(tt.version, tt.arch)
		if url != tt.url {
			t.Errorf("for version %s expected %s, got %s", tt.version, tt.url, url)
		}
	}
}

func TestKubeletConfigMap(t *testing.T) {
	kubeletConfiguration := KubeletConfiguration{
		KubeletConfiguration: v1beta1.KubeletConfiguration{
			CPUManagerPolicy:            lo.ToPtr("static"),
			CPUCFSQuota:                 lo.ToPtr(true),
			CPUCFSQuotaPeriod:           metav1.Duration{},
			ImageGCHighThresholdPercent: lo.ToPtr[int32](42),
			ImageGCLowThresholdPercent:  lo.ToPtr[int32](24),
			TopologyManagerPolicy:       lo.ToPtr("best-effort"),
			AllowedUnsafeSysctls:        []string{"Allowed", "Unsafe", "Sysctls"},
			ContainerLogMaxSize:         lo.ToPtr("42Mi"),
			ContainerLogMaxFiles:        lo.ToPtr[int32](13),
			PodPidsLimit:                lo.ToPtr[int64](99),
		},
		MaxPods: 0,
		SystemReserved: map[string]string{
			"cpu": "200m",
		},
		KubeReserved: map[string]string{
			"cpu":    "180m",
			"memory": "2250Mi",
			"pid":    "1000",
		},
		EvictionHard: map[string]string{
			"memory.available": "100Mi",
		},
		EvictionSoft: map[string]string{
			"memory.available": "99Mi",
		},
		EvictionSoftGracePeriod: map[string]metav1.Duration{
			"memory.available": {Duration: 90 * time.Second},
		},
		EvictionMaxPodGracePeriod: to.Ptr[int32](11),
		ClusterDNSServiceIP:       "10.20.0.10",
	}

	expectedKubeletConfigs := map[string]string{
		"--allowed-unsafe-sysctls":        "Allowed,Unsafe,Sysctls",
		"--max-pods":                      "0",
		"--cpu-cfs-quota":                 "true",
		"--image-gc-high-threshold":       "42",
		"--image-gc-low-threshold":        "24",
		"--cpu-manager-policy":            "static",
		"--topology-manager-policy":       "best-effort",
		"--container-log-max-files":       "13",
		"--container-log-max-size":        "42Mi",
		"--pod-max-pids":                  "99",
		"--system-reserved":               "cpu=200m",               // TODO: test multiple resource
		"--eviction-hard":                 "memory.available<100Mi", // TODO: test multiple resource
		"--eviction-soft":                 "memory.available<99Mi",  // TODO: test multiple resource
		"--eviction-soft-grace-period":    "memory.available=1m30s",
		"--eviction-max-pod-grace-period": "11",
		"--cluster-dns":                   "10.20.0.10",
	}
	actualKubeletConfig := kubeletConfigToMap(&kubeletConfiguration)

	g := NewWithT(t)
	for k, v := range expectedKubeletConfigs {
		g.Expect(actualKubeletConfig[k]).To(Equal(v), fmt.Sprintf("parameter mismatch for %s", k))
	}
}

func TestKubeletConfigMapEnforceNodeAllocatable(t *testing.T) {
	g := NewWithT(t)

	hardened := kubeletConfigToMap(&KubeletConfiguration{
		KubeReserved:           map[string]string{"pid": "1000"},
		SystemReserved:         map[string]string{"pid": "1000"},
		EnforceNodeAllocatable: []string{"pods", "kube-reserved", "system-reserved"},
	})
	g.Expect(hardened["--kube-reserved"]).To(Equal("pid=1000"))
	g.Expect(hardened["--system-reserved"]).To(Equal("pid=1000"))
	g.Expect(hardened["--enforce-node-allocatable"]).To(Equal("pods,kube-reserved,system-reserved"))
	g.Expect(hardened["--kube-reserved-cgroup"]).To(Equal("/kubereserved.slice"))
	g.Expect(hardened["--system-reserved-cgroup"]).To(Equal("/system.slice"))

	nonHardened := kubeletConfigToMap(&KubeletConfiguration{EnforceNodeAllocatable: []string{"pods"}})
	g.Expect(nonHardened).ToNot(HaveKey("--kube-reserved-cgroup"))
	g.Expect(nonHardened).ToNot(HaveKey("--system-reserved-cgroup"))
}

func TestKubeletConfigMapEmptyConfigurationOmitsEnforceNodeAllocatable(t *testing.T) {
	g := NewWithT(t)

	configuration := kubeletConfigToMap(&KubeletConfiguration{})
	_, ok := configuration["--enforce-node-allocatable"]
	g.Expect(ok).To(BeFalse())
}

func TestKubeletConfigMapNodeHardeningSoftEviction(t *testing.T) {
	g := NewWithT(t)
	configuration := &KubeletConfiguration{
		EvictionSoft: map[string]string{
			"memory.available":  "1Gi",
			"nodefs.available":  "12%",
			"nodefs.inodesFree": "7%",
		},
		EvictionSoftGracePeriod: map[string]metav1.Duration{
			"memory.available":  {Duration: 30 * time.Second},
			"nodefs.available":  {Duration: 2 * time.Minute},
			"nodefs.inodesFree": {Duration: 2 * time.Minute},
		},
		EvictionMaxPodGracePeriod: lo.ToPtr(int32(60)),
	}

	flags := kubeletConfigToMap(configuration)
	g.Expect(strings.Split(flags["--eviction-soft"], ",")).To(ConsistOf(
		"memory.available<1Gi",
		"nodefs.available<12%",
		"nodefs.inodesFree<7%",
	))
	g.Expect(strings.Split(flags["--eviction-soft-grace-period"], ",")).To(ConsistOf(
		"memory.available=30s",
		"nodefs.available=2m0s",
		"nodefs.inodesFree=2m0s",
	))
	g.Expect(flags["--eviction-max-pod-grace-period"]).To(Equal("60"))
}

func TestKubeletConfigMapHardEviction(t *testing.T) {
	g := NewWithT(t)
	configuration := &KubeletConfiguration{
		EvictionHard: map[string]string{
			"memory.available":  "512Mi",
			"nodefs.available":  "10%",
			"nodefs.inodesFree": "5%",
			"pid.available":     "2000",
		},
	}

	flags := kubeletConfigToMap(configuration)
	g.Expect(strings.Split(flags["--eviction-hard"], ",")).To(ConsistOf(
		"memory.available<512Mi",
		"nodefs.available<10%",
		"nodefs.inodesFree<5%",
		"pid.available<2000",
	))
}
