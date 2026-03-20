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

package validation

import (
	"sort"
	"strings"
	"testing"
)

// These snapshots are taken from the AKS RP codebase and must be kept in sync.
// When these tests fail, it means the AKS RP has changed its validation rules
// and this package needs to be updated to match.
//
// HOW TO UPDATE:
// 1. Check the AKS RP files listed below for changes
// 2. Update the snapshots in this test to match
// 3. Update the AllowedAKSUserLabels/AllowedAKSUserTaintKeys in aksrules.go
// 4. Regenerate CRDs with `make verify`
//
// AKS RP source files to check:
//   - toolkit/constvalues/k8slabels/labels.go (AKSPrefixValue, GetK8sSystemLabelKeys, GetAgentBakerGeneratedLabelKeys)
//   - resourceprovider/server/microsoft.com/containerservice/server/validation/utils/utils_agentpool.go (isWhiteListAKSLabels, validateAgentPoolNodeLabelPrefix)
//   - resourceprovider/server/microsoft.com/containerservice/server/validation/nodetaints/nodetaintsvalidator.go (isTaintAnAllowedAKSSystemTaint)

// TestAKSLabelDomainMatchesRP verifies AKSLabelDomain matches RP's AKSPrefixValue.
func TestAKSLabelDomainMatchesRP(t *testing.T) {
	// RP source: toolkit/constvalues/k8slabels/labels.go
	// AKSPrefixValue = "kubernetes.azure.com"
	expected := "kubernetes.azure.com"
	if AKSLabelDomain != expected {
		t.Errorf("AKSLabelDomain = %q, want %q (RP AKSPrefixValue)", AKSLabelDomain, expected)
	}
}

// TestAllowedAKSUserLabelsMatchRPAllowlist verifies that all RP-allowed AKS
// user label keys are present in our allowlist.
//
// Snapshot from RP's isWhiteListAKSLabels() in utils_agentpool.go.
// This function defines which kubernetes.azure.com/* labels are allowed
// for user input on AgentPool/Machine API.
func TestAllowedAKSUserLabelsMatchRPAllowlist(t *testing.T) {
	// These are the labels allowed by RP's isWhiteListAKSLabels().
	// They are allowed with specific value constraints (e.g., must match VNet config),
	// but at the CRD level we only check the key — value validation happens at RP.
	rpAllowedLabels := []string{
		"kubernetes.azure.com/scalesetpriority",
		"kubernetes.azure.com/network-name",
		"kubernetes.azure.com/network-subnet",
		"kubernetes.azure.com/network-subscription",
		"kubernetes.azure.com/network-resourcegroup",
		"kubernetes.azure.com/nodenetwork-vnetguid",
		"kubernetes.azure.com/podnetwork-subscription",
		"kubernetes.azure.com/podnetwork-resourcegroup",
		"kubernetes.azure.com/podnetwork-name",
		"kubernetes.azure.com/podnetwork-subnet",
		"kubernetes.azure.com/podnetwork-delegationguid",
	}

	allowedSet := make(map[string]bool)
	for _, l := range AllowedAKSUserLabels {
		allowedSet[l] = true
	}

	for _, rpLabel := range rpAllowedLabels {
		if !allowedSet[rpLabel] {
			t.Errorf("RP-allowed label %q is missing from AllowedAKSUserLabels", rpLabel)
		}
	}
}

// TestAllowedAKSUserTaintKeysMatchRP verifies that all RP-allowed AKS
// system taint keys are present in our taint allowlist.
//
// Snapshot from RP's isTaintAnAllowedAKSSystemTaint() in nodetaintsvalidator.go.
func TestAllowedAKSUserTaintKeysMatchRP(t *testing.T) {
	// These are the taint keys allowed by RP's isTaintAnAllowedAKSSystemTaint().
	// Each has specific value+effect constraints at RP level, but CRD-level only checks keys.
	rpAllowedTaintKeys := []string{
		"kubernetes.azure.com/scalesetpriority", // spot taint: value=spot, effect=NoSchedule
		"kubernetes.azure.com/mode",             // gateway taint: value=gateway, effect=NoSchedule
		"kubernetes.azure.com/hostedvm",         // hobo taint
	}

	allowedSet := make(map[string]bool)
	for _, k := range AllowedAKSUserTaintKeys {
		allowedSet[k] = true
	}

	for _, rpKey := range rpAllowedTaintKeys {
		if !allowedSet[rpKey] {
			t.Errorf("RP-allowed taint key %q is missing from AllowedAKSUserTaintKeys", rpKey)
		}
	}
}

// TestBlockedK8sSystemLabelKeysMatchRP verifies our blocked K8s system label
// keys match RP's GetK8sSystemLabelKeys().
//
// Snapshot from RP's GetK8sSystemLabelKeys() in k8slabels/labels.go.
func TestBlockedK8sSystemLabelKeysMatchRP(t *testing.T) {
	rpBlockedKeys := []string{
		"beta.kubernetes.io/arch",
		"beta.kubernetes.io/instance-type",
		"beta.kubernetes.io/os",
		"failure-domain.beta.kubernetes.io/region",
		"failure-domain.beta.kubernetes.io/zone",
		"failure-domain.kubernetes.io/zone",
		"failure-domain.kubernetes.io/region",
		"kubernetes.io/arch",
		"kubernetes.io/hostname",
		"kubernetes.io/os",
		"kubernetes.io/instance-type",
		"node.kubernetes.io/instance-type",
		"topology.kubernetes.io/region",
		"topology.kubernetes.io/zone",
	}

	ours := sorted(BlockedK8sSystemLabelKeys)
	theirs := sorted(rpBlockedKeys)

	if strings.Join(ours, ",") != strings.Join(theirs, ",") {
		t.Errorf("BlockedK8sSystemLabelKeys mismatch with RP GetK8sSystemLabelKeys():\n  ours:   %v\n  theirs: %v", ours, theirs)
	}
}

// TestBlockedAgentBakerLabelKeysMatchRP verifies our blocked AgentBaker label
// keys match RP's GetAgentBakerGeneratedLabelKeys().
//
// Snapshot from RP's GetAgentBakerGeneratedLabelKeys() in k8slabels/labels.go.
func TestBlockedAgentBakerLabelKeysMatchRP(t *testing.T) {
	rpBlockedKeys := []string{
		"agentpool",
		"storageprofile",
		"storagetier",
		"accelerator",
	}

	ours := sorted(BlockedAgentBakerLabelKeys)
	theirs := sorted(rpBlockedKeys)

	if strings.Join(ours, ",") != strings.Join(theirs, ",") {
		t.Errorf("BlockedAgentBakerLabelKeys mismatch with RP GetAgentBakerGeneratedLabelKeys():\n  ours:   %v\n  theirs: %v", ours, theirs)
	}
}

func sorted(s []string) []string {
	result := make([]string, len(s))
	copy(result, s)
	sort.Strings(result)
	return result
}
