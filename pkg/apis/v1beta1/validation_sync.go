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

package v1beta1

// Validation Sync Contract
//
// This file documents which AKS RP Machine API validation rules are mirrored
// in Karpenter's CRD validation (CEL rules) and Go-level restrictions.
//
// Machine API validates labels and taints by converting the Machine to a "fake"
// AgentPool and running the standard AgentPool validation pipeline. The validation
// is defined in aks-rp at:
//   - Labels: resourceprovider/.../validation/utils/utils_agentpool.go
//   - Taints: resourceprovider/.../validation/nodetaints/nodetaintsvalidator.go
//   - System label keys: toolkit/constvalues/k8slabels/labels.go
//
// This file is the single source of truth in Karpenter for which RP rules are
// enforced client-side. When RP changes validation rules, updating this file
// (and the corresponding CEL scripts in hack/validation/) keeps them in sync.
// The test file (validation_sync_test.go) verifies consistency between this
// contract and the actual CEL rules injected into the CRDs.
//
// ## How sync works
//
// 1. CEL rules in hack/validation/{labels,requirements,taints}.sh enforce
//    domain-level restrictions at CRD admission time.
// 2. This file defines the allowlists and blocked keys as Go constants, used by
//    RuntimeValidate and referenced by tests.
// 3. The test in validation_sync_test.go verifies that the Go constants match
//    the CEL allowlists (by checking the CRD YAML content for expected entries).
// 4. When RP adds/removes allowed labels or blocked keys, a developer updates
//    this file + the hack/validation/ scripts, and the test catches any mismatch.

import "k8s.io/apimachinery/pkg/util/sets"

// --- Label validation contract ---

// AKSLabelDomainAllowlist lists kubernetes.azure.com/* label keys that users
// ARE allowed to set on NodePool labels and requirements.
//
// These correspond to labels that either:
// - Are well-known labels Karpenter uses for scheduling (mode, scalesetpriority, etc.)
// - Are special-purpose labels that bypass the RP kubernetes.azure.com block
//   (ebpf-dataplane, cluster-health-monitor-checker-synthetic)
//
// RP source: isWhiteListAKSLabels() in utils_agentpool.go (value-conditional whitelist)
// plus additional labels that Karpenter itself needs to set/read.
var AKSLabelDomainAllowlist = sets.New(
	AKSLabelMode,              // kubernetes.azure.com/mode
	AKSLabelScaleSetPriority,  // kubernetes.azure.com/scalesetpriority
	AKSLabelFIPSEnabled,       // kubernetes.azure.com/fips_enabled
	AKSLabelOSSKU,             // kubernetes.azure.com/os-sku
	AKSLabelCluster,           // kubernetes.azure.com/cluster
	AKSLabelCPU,               // kubernetes.azure.com/sku-cpu
	AKSLabelMemory,            // kubernetes.azure.com/sku-memory
	AKSLabelEBPFDataplane,     // kubernetes.azure.com/ebpf-dataplane
	AKSLabelClusterHealthSyn,  // kubernetes.azure.com/cluster-health-monitor-checker-synthetic
)

// SystemLabelKeys are individual label keys that RP blocks from user assignment.
// These are exact key matches (case-insensitive on RP side).
//
// RP source: GetK8sSystemLabelKeys() + GetAgentBakerGeneratedLabelKeys() in
// toolkit/constvalues/k8slabels/labels.go
//
// Note: On the Karpenter side, most K8s system labels (topology.*, beta.kubernetes.io/*)
// are already blocked by upstream Karpenter's CRD CEL rules which restrict the
// kubernetes.io and k8s.io domains. We only need to additionally block the
// unprefixed AgentBaker-generated labels.
var SystemLabelKeys = sets.New(
	// K8s system labels — most are covered by upstream's kubernetes.io/k8s.io domain
	// restrictions. Listed here for completeness and drift detection.
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

	// AgentBaker-generated labels — NOT prefixed with kubernetes.azure.com,
	// so they need explicit blocking.
	AKSLabelLegacyAgentPool,      // "agentpool"
	AKSLabelLegacyStorageProfile, // "storageprofile"
	AKSLabelLegacyStorageTier,    // "storagetier"
	AKSLabelLegacyAccelerator,    // "accelerator"
)

// --- Taint validation contract ---

// AllowedAKSTaintKeys lists kubernetes.azure.com/* taint keys that users
// ARE allowed to set on NodePool taints (permanent taints).
//
// RP source: isTaintAnAllowedAKSSystemTaint() in nodetaintsvalidator.go
// The RP allows these specific key/value/effect combinations:
//   - kubernetes.azure.com/scalesetpriority=spot:NoSchedule
//   - kubernetes.azure.com/mode=gateway:NoSchedule
//   - kubernetes.azure.com/hostedvm (any value — for HostedSystem/hobo pools)
//
// We don't include hostedvm because Karpenter doesn't manage HostedSystem pools.
var AllowedAKSTaintKeys = sets.New(
	AKSLabelScaleSetPriority, // kubernetes.azure.com/scalesetpriority
	AKSLabelMode,             // kubernetes.azure.com/mode
)

// AllowedAKSStartupTaintKeys lists kubernetes.azure.com/* taint keys allowed
// in startupTaints (init taints). These are transient and may include keys
// not allowed in permanent taints.
var AllowedAKSStartupTaintKeys = sets.New(
	"egressgateway.kubernetes.azure.com/cni-not-ready",
)
