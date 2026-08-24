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
	"encoding/base64"
	"strings"
	"testing"

	"github.com/samber/lo"
)

// minFieldsTemplate returns an AKS bootstrap struct populated with the minimum
// fields required for Script() to render without panicking. Only fields that
// flow into the flag-computation section of applyOptions are meaningful for
// these tests; everything else is boilerplate for the template renderer.
func minFieldsTemplate(nodeHardeningEnabled bool) AKS {
	return AKS{
		Options: Options{
			ClusterName:     "test-cluster",
			ClusterEndpoint: "https://test-cluster",
			CABundle:        lo.ToPtr(""),
			SubnetID:        "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
			// Kubelet TLS bootstrap token / non-empty labels are not required for the enforce
			// flag path; leaving them zero keeps this test scoped to what we're asserting.
			NodeHardeningEnabled: nodeHardeningEnabled,
		},
		Arch:                           "amd64",
		TenantID:                       "00000000-0000-0000-0000-000000000000",
		SubscriptionID:                 "00000000-0000-0000-0000-000000000000",
		KubeletIdentityClientID:        "00000000-0000-0000-0000-000000000000",
		Location:                       "eastus",
		ResourceGroup:                  "rg",
		NetworkSecurityGroupName:       "aks-nsg",
		RouteTableName:                 "aks-rt",
		APIServerName:                  "test-cluster",
		KubeletClientTLSBootstrapToken: "",
		NetworkPlugin:                  "azure",
		NetworkPolicy:                  "",
		KubernetesVersion:              "1.32.0",
	}
}

// Decode the base64-encoded custom-data script that Script() returns and extracts the KUBELET_FLAGS assignment.
func kubeletFlagsFromScript(t *testing.T, script string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(script)
	if err != nil {
		t.Fatalf("decoding base64 script: %v", err)
	}
	s := string(raw)
	start := strings.Index(s, "KUBELET_FLAGS=")
	if start < 0 {
		t.Fatalf("KUBELET_FLAGS not found in bootstrap script")
	}
	end := strings.Index(s[start:], "KUBELET_NODE_LABELS")
	if end < 0 {
		t.Fatalf("KUBELET_NODE_LABELS terminator not found after KUBELET_FLAGS")
	}
	return s[start : start+end]
}

// TestApplyOptions_EnforceNodeAllocatable_NodeHardeningEnabled asserts that
// enabling node hardening flips --enforce-node-allocatable from the kubelet
// default ("pods") to the hardened list ("pods,kube-reserved,system-reserved").
//
// AgentBaker's cse_helpers.sh::ensureKubeletCgroupHierarchy uses this flag as
// the signal to create the /kubelet.slice systemd unit and inject
// --kube-reserved-cgroup / --system-reserved-cgroup itself (see
// Azure/AgentBaker#8497), so Karpenter must write ONLY this flag — writing
// the two cgroup flags here would duplicate AgentBaker's work and could get
// out of sync with its canonical values.
func TestApplyOptions_EnforceNodeAllocatable_NodeHardeningEnabled(t *testing.T) {
	aks := minFieldsTemplate(true)
	script, err := aks.Script()
	if err != nil {
		t.Fatalf("Script(): %v", err)
	}
	flags := kubeletFlagsFromScript(t, script)

	want := "--enforce-node-allocatable=pods,kube-reserved,system-reserved"
	if !strings.Contains(flags, want) {
		t.Fatalf("expected %q in KUBELET_FLAGS, got:\n%s", want, flags)
	}
	// AgentBaker owns these two — Karpenter must not preemptively write them.
	for _, unwanted := range []string{"--kube-reserved-cgroup", "--system-reserved-cgroup"} {
		if strings.Contains(flags, unwanted) {
			t.Fatalf("Karpenter must not write %s (AgentBaker owns it); got:\n%s", unwanted, flags)
		}
	}
}

func TestApplyOptions_EnforceNodeAllocatable_NodeHardeningDisabled(t *testing.T) {
	aks := minFieldsTemplate(false)
	script, err := aks.Script()
	if err != nil {
		t.Fatalf("Script(): %v", err)
	}
	flags := kubeletFlagsFromScript(t, script)

	want := "--enforce-node-allocatable=pods"
	if !strings.Contains(flags, want) {
		t.Fatalf("expected %q in KUBELET_FLAGS, got:\n%s", want, flags)
	}
	// Guard against accidental leakage of the hardened value when the option is off.
	unwanted := "kube-reserved,system-reserved"
	if strings.Contains(flags, unwanted) {
		t.Fatalf("expected hardened enforce list to be absent when NodeHardeningEnabled=false; got:\n%s", flags)
	}
}
