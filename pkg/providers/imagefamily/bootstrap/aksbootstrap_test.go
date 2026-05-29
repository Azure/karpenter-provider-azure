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
			"cpu": "400m",
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
		"--kube-reserved":                 "cpu=400m",               // TODO: test multiple resource
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

// TestSortedKVStrings asserts the helper produces a lexicographically-sorted slice regardless
// of underlying map iteration order. This is the property that lets KubeletNodeLabels /
// KubeletFlags render byte-deterministically (see comment in applyOptions).
func TestSortedKVStrings(t *testing.T) {
	g := NewWithT(t)
	m := map[string]string{
		"zeta":  "1",
		"alpha": "2",
		"mike":  "3",
		"bravo": "4",
	}
	// Run many iterations to defeat Go map randomization and ensure the result is stable.
	first := sortedKVStrings(m, "=")
	g.Expect(first).To(Equal([]string{"alpha=2", "bravo=4", "mike=3", "zeta=1"}))
	for i := 0; i < 100; i++ {
		g.Expect(sortedKVStrings(m, "=")).To(Equal(first))
	}
}

// TestAKSBootstrapScript_DeterministicAcrossCalls is a regression test for Fleet batching:
// the rendered bootstrap script must be byte-identical across independent calls when the
// input is identical. Otherwise the BatchKey hash (which includes ScriptlessCustomData)
// differs per NodeClaim and the Fleet client emits one PUT per VM instead of batching.
//
// Before the fix, KUBELET_NODE_LABELS and KUBELET_FLAGS were serialized via lo.MapToSlice
// over a Go map, whose iteration order is randomized — so two consecutive calls produced
// the same content in different orders.
func TestAKSBootstrapScript_DeterministicAcrossCalls(t *testing.T) {
	g := NewWithT(t)
	// Many labels & kubelet config entries — enough that random ordering would almost
	// always produce a different concatenated string across calls.
	labels := map[string]string{
		"karpenter.sh/nodepool":                          "general-purpose",
		"karpenter.azure.com/aksnodeclass":               "default",
		"karpenter.azure.com/sku-family":                 "D",
		"karpenter.azure.com/sku-name":                   "Standard_D2ls_v5",
		"karpenter.azure.com/sku-cpu":                    "2",
		"karpenter.azure.com/sku-memory":                 "4096",
		"karpenter.azure.com/sku-version":                "5",
		"karpenter.azure.com/sku-networking-accelerated": "true",
		"kubernetes.azure.com/cluster":                   "MC_test_test_eastus",
		"kubernetes.azure.com/os-sku":                    "Ubuntu",
		"node.kubernetes.io/instance-type":               "Standard_D2ls_v5",
		"topology.kubernetes.io/region":                  "eastus",
		"topology.kubernetes.io/zone":                    "eastus-1",
	}

	mkAKS := func() AKS {
		return AKS{
			Options: Options{
				ClusterName:     "test",
				ClusterEndpoint: "https://test:443",
				Labels:          labels,
				CABundle:        lo.ToPtr("Y2EK"),
				KubeletConfig: &KubeletConfiguration{
					KubeletConfiguration: v1beta1.KubeletConfiguration{
						CPUManagerPolicy:            lo.ToPtr("static"),
						ImageGCHighThresholdPercent: lo.ToPtr[int32](80),
						ImageGCLowThresholdPercent:  lo.ToPtr[int32](70),
					},
					SystemReserved: map[string]string{"cpu": "200m", "memory": "1Gi"},
					KubeReserved:   map[string]string{"cpu": "100m", "memory": "512Mi"},
					EvictionHard:   map[string]string{"memory.available": "100Mi"},
					EvictionSoft:   map[string]string{"memory.available": "200Mi"},
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						"memory.available": {Duration: 30 * time.Second},
					},
				},
				SubnetID: "/subscriptions/x/resourceGroups/y/providers/Microsoft.Network/virtualNetworks/v/subnets/s",
			},
			Arch:                           "amd64",
			TenantID:                       "tid",
			SubscriptionID:                 "sid",
			Location:                       "eastus",
			KubeletIdentityClientID:        "kid",
			ResourceGroup:                  "rg",
			NetworkSecurityGroupName:       "aks-agentpool-12345678-nsg",
			RouteTableName:                 "aks-agentpool-12345678-routetable",
			APIServerName:                  "test:443",
			KubeletClientTLSBootstrapToken: "abcdef.0123456789abcdef",
			NetworkPlugin:                  "azure",
			NetworkPolicy:                  "",
			KubernetesVersion:              "1.30.0",
		}
	}

	first, err := mkAKS().Script()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(first).NotTo(BeEmpty())

	// Run many iterations to defeat Go map randomization. Every call must produce
	// byte-identical output for Fleet batching to coalesce same-spec NodeClaims.
	for i := 0; i < 25; i++ {
		next, err := mkAKS().Script()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(next).To(Equal(first), "Script() output must be deterministic across calls (iteration %d)", i)
	}
}
