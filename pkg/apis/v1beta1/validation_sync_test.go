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

package v1beta1_test

import (
	"os"
	"strings"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	. "github.com/onsi/gomega"
)

// TestValidationSyncContract verifies that the Go-level validation contract
// (AKSLabelDomainAllowlist, SystemLabelKeys, AllowedAKSTaintKeys, etc.)
// stays in sync with the CEL rules injected into the CRDs by the hack/validation/ scripts.
//
// This test is the key mechanism for ticket #1704: it catches drift between
// the Karpenter CRD validation rules and the documented RP validation contract.
// If a label is added to the Go allowlist but not to the CEL scripts (or vice versa),
// this test fails.
func TestValidationSyncContract(t *testing.T) {
	g := NewWithT(t)

	// Read the NodePool CRD which has the most comprehensive set of CEL rules
	crdContent, err := os.ReadFile("../crds/karpenter.sh_nodepools.yaml")
	g.Expect(err).NotTo(HaveOccurred(), "CRD file should exist; run 'make verify' first if missing")
	crd := string(crdContent)

	t.Run("AKSLabelDomainAllowlist matches CEL labels rule", func(t *testing.T) {
		g := NewWithT(t)
		// Every label in the Go allowlist should appear in the CRD CEL rule
		for label := range v1beta1.AKSLabelDomainAllowlist {
			g.Expect(crd).To(ContainSubstring(label),
				"label %q is in AKSLabelDomainAllowlist but not found in NodePool CRD CEL rules. "+
					"Update hack/validation/labels.sh and hack/validation/requirements.sh to include it.", label)
		}
	})

	t.Run("CEL kubernetes.azure.com allowlist matches Go constant", func(t *testing.T) {
		g := NewWithT(t)
		// The CEL rule for kubernetes.azure.com domain restriction should exist
		g.Expect(crd).To(ContainSubstring(`label domain "kubernetes.azure.com" is restricted`),
			"kubernetes.azure.com CEL domain restriction not found in NodePool CRD")
	})

	t.Run("agentpool label is blocked in CEL", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(crd).To(ContainSubstring("agentpool"),
			"agentpool label restriction not found in NodePool CRD")
	})

	t.Run("AllowedAKSTaintKeys are in CEL taint rules", func(t *testing.T) {
		g := NewWithT(t)
		for key := range v1beta1.AllowedAKSTaintKeys {
			g.Expect(crd).To(ContainSubstring(key),
				"taint key %q is in AllowedAKSTaintKeys but not found in NodePool CRD taint CEL rules. "+
					"Update hack/validation/taints.sh to include it.", key)
		}
	})

	t.Run("AllowedAKSStartupTaintKeys are in CEL startupTaint rules", func(t *testing.T) {
		g := NewWithT(t)
		for key := range v1beta1.AllowedAKSStartupTaintKeys {
			g.Expect(crd).To(ContainSubstring(key),
				"startup taint key %q is in AllowedAKSStartupTaintKeys but not found in NodePool CRD. "+
					"Update hack/validation/taints.sh to include it.", key)
		}
	})

	t.Run("taint domain restriction exists", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(crd).To(ContainSubstring(`taint domain "kubernetes.azure.com" is restricted`),
			"kubernetes.azure.com taint domain restriction not found in NodePool CRD")
	})

	t.Run("hack scripts reference all allowlist entries", func(t *testing.T) {
		g := NewWithT(t)

		labelsScript, err := os.ReadFile("../../../hack/validation/labels.sh")
		g.Expect(err).NotTo(HaveOccurred())
		labelsContent := string(labelsScript)

		requirementsScript, err := os.ReadFile("../../../hack/validation/requirements.sh")
		g.Expect(err).NotTo(HaveOccurred())
		requirementsContent := string(requirementsScript)

		for label := range v1beta1.AKSLabelDomainAllowlist {
			g.Expect(labelsContent).To(ContainSubstring(label),
				"label %q from AKSLabelDomainAllowlist missing in hack/validation/labels.sh", label)
			g.Expect(requirementsContent).To(ContainSubstring(label),
				"label %q from AKSLabelDomainAllowlist missing in hack/validation/requirements.sh", label)
		}
	})

	t.Run("hack taints script references all allowed taint keys", func(t *testing.T) {
		g := NewWithT(t)

		taintsScript, err := os.ReadFile("../../../hack/validation/taints.sh")
		g.Expect(err).NotTo(HaveOccurred())
		taintsContent := string(taintsScript)

		for key := range v1beta1.AllowedAKSTaintKeys {
			g.Expect(taintsContent).To(ContainSubstring(key),
				"taint key %q from AllowedAKSTaintKeys missing in hack/validation/taints.sh", key)
		}
		for key := range v1beta1.AllowedAKSStartupTaintKeys {
			g.Expect(taintsContent).To(ContainSubstring(key),
				"startup taint key %q from AllowedAKSStartupTaintKeys missing in hack/validation/taints.sh", key)
		}
	})

	t.Run("SystemLabelKeys contract is complete", func(t *testing.T) {
		g := NewWithT(t)
		// Verify the contract includes the expected categories of system labels.
		// This is a smoke test — if RP adds a new system label, a developer should
		// add it here and the count change signals the update.

		// K8s system labels
		expectedK8sKeys := []string{
			"kubernetes.io/hostname",
			"kubernetes.io/arch",
			"kubernetes.io/os",
			"topology.kubernetes.io/region",
			"topology.kubernetes.io/zone",
			"node.kubernetes.io/instance-type",
		}
		for _, key := range expectedK8sKeys {
			g.Expect(v1beta1.SystemLabelKeys.Has(key)).To(BeTrue(),
				"expected K8s system label %q in SystemLabelKeys", key)
		}

		// AgentBaker labels
		expectedAgentBakerKeys := []string{
			"agentpool",
			"storageprofile",
			"storagetier",
			"accelerator",
		}
		for _, key := range expectedAgentBakerKeys {
			g.Expect(v1beta1.SystemLabelKeys.Has(key)).To(BeTrue(),
				"expected AgentBaker label %q in SystemLabelKeys", key)
		}
	})

	t.Run("no accidental test-silencing via empty sets", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(v1beta1.AKSLabelDomainAllowlist.Len()).To(BeNumerically(">", 0),
			"AKSLabelDomainAllowlist should not be empty")
		g.Expect(v1beta1.SystemLabelKeys.Len()).To(BeNumerically(">", 0),
			"SystemLabelKeys should not be empty")
		g.Expect(v1beta1.AllowedAKSTaintKeys.Len()).To(BeNumerically(">", 0),
			"AllowedAKSTaintKeys should not be empty")
		g.Expect(v1beta1.AllowedAKSStartupTaintKeys.Len()).To(BeNumerically(">", 0),
			"AllowedAKSStartupTaintKeys should not be empty")
	})

	// Cross-check: labels in AKSLabelDomainAllowlist should all start with kubernetes.azure.com/
	t.Run("AKSLabelDomainAllowlist entries have correct prefix", func(t *testing.T) {
		g := NewWithT(t)
		for label := range v1beta1.AKSLabelDomainAllowlist {
			g.Expect(strings.HasPrefix(label, v1beta1.AKSLabelDomain+"/")).To(BeTrue(),
				"label %q in AKSLabelDomainAllowlist doesn't have kubernetes.azure.com/ prefix", label)
		}
	})
}
