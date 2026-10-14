# Adding a Pool-Level Feature to Karpenter (AKS Machine API Path)

**Author:** AI-assisted guide based on existing patterns

**Last updated:** Mar 15, 2026

**Status:** Living document

## Overview

Many AKS features follow a "passthrough" pattern in Karpenter: a field is added to the AKSNodeClass CRD, and its value flows to the AKS Machine API with minimal or no Karpenter-owned logic. Examples include EncryptionAtHost, OSDiskSizeGB, MaxPods, FIPS mode, and LinuxOSConfig.

This guide codifies the pattern to reduce ramp-up time for feature owners and Karpenter developers.

## Complexity Spectrum

| Level | Example | Scope | Typical LOC |
|-------|---------|-------|-------------|
| **Trivial** | EncryptionAtHost | CRD field already exists, just wire to Machine API | ~70 lines, 2 files |
| **Small** | LocalDNS conversion | CRD exists, add type conversion + tests | ~340 lines, 4 files |
| **Medium** | LinuxOSConfig | New CRD field + types + Machine API + bootstrapping + E2E | ~2000 lines, ~20 files |

## Step-by-Step Checklist

### Phase 1: CRD Definition (skip if field already exists)

**Files:**
- `pkg/apis/v1beta1/aksnodeclass.go` — Add field to `AKSNodeClassSpec`
- `pkg/apis/v1alpha2/aksnodeclass.go` — Mirror for older API version

**What to do:**
1. Add the field to `AKSNodeClassSpec` with appropriate Go type
2. Add kubebuilder validation markers (`+kubebuilder:validation:Minimum`, `+kubebuilder:validation:Enum`, etc.)
3. Add CEL `+kubebuilder:validation:XValidation` rules for cross-field validation if needed
4. Consider a helper method like `GetEncryptionAtHost()` for nil-safe access with defaults

**Template:**
```go
// +kubebuilder:validation:Optional
// +kubebuilder:validation:Minimum=10
// +kubebuilder:validation:Maximum=250
MaxPods *int32 `json:"maxPods,omitempty"`
```

### Phase 2: Code Generation

**Run:**
```bash
make generate
```

**This updates:**
- `pkg/apis/v1beta1/zz_generated.deepcopy.go`
- `pkg/apis/v1alpha2/zz_generated.deepcopy.go`
- `pkg/apis/crds/karpenter.azure.com_aksnodeclasses.yaml`

**Then copy the CRD YAML:**
```bash
cp pkg/apis/crds/karpenter.azure.com_aksnodeclasses.yaml charts/karpenter-crd/templates/karpenter.azure.com_aksnodeclasses.yaml
```

### Phase 3: Machine API Wiring

**File:** `pkg/providers/instance/aksmachineinstancehelpers.go` (or `instance/machine/helpers.go` after module split)

**What to do:**
1. Add a `configure<Feature>()` helper function
2. Wire it into `buildAKSMachineTemplate()` to set the correct field on `armcontainerservice.Machine`

**Template for a pure passthrough:**
```go
func configure<Feature>(nodeClass *v1beta1.AKSNodeClass, aksMachine *armcontainerservice.Machine) {
    if nodeClass.Spec.<Field> != nil {
        aksMachine.Properties.<Section>.<Field> = nodeClass.Spec.<Field>
    }
}
```

**Template for a conversion (enum mapping):**
```go
func configure<Feature>(nodeClass *v1beta1.AKSNodeClass, aksMachine *armcontainerservice.Machine) {
    if nodeClass.Spec.<Field> == nil {
        return
    }
    switch *nodeClass.Spec.<Field> {
    case v1beta1.<Value1>:
        aksMachine.Properties.<Section>.<Field> = lo.ToPtr(armcontainerservice.<ARMValue1>)
    case v1beta1.<Value2>:
        aksMachine.Properties.<Section>.<Field> = lo.ToPtr(armcontainerservice.<ARMValue2>)
    }
}
```

### Phase 4: Unit Tests

**File:** `pkg/cloudprovider/suite_aksmachineapi_features_test.go`

**What to test:**
1. Feature enabled → field appears on AKS Machine template
2. Feature disabled → field is nil/absent
3. Feature unspecified → default behavior
4. Edge cases specific to the feature

**Template:**
```go
It("should set <Feature> when specified", func() {
    nodeClass.Spec.<Field> = lo.ToPtr(<value>)
    ExpectApplied(ctx, env.Client, nodePool, nodeClass)
    pod := coretest.UnschedulablePod()
    ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, prov, pod)
    Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateRequests).To(HaveLen(1))
    machine := azureEnv.AKSMachinesAPI.AKSMachineCreateRequests[0].AKSMachine
    Expect(machine.Properties.<Section>.<Field>).To(Equal(lo.ToPtr(<expectedValue>)))
})
```

### Phase 5: Drift Detection (if applicable)

**File:** `pkg/apis/v1beta1/aksnodeclass.go`

If the new field affects node configuration and should trigger drift when changed, bump `AKSNodeClassHashVersion`:

```go
const AKSNodeClassHashVersion = "v4" // was "v3"
```

The hash is computed over the entire `AKSNodeClassSpec`, so adding a new field automatically includes it in drift calculation — you just need to bump the version to trigger re-evaluation.

### Phase 6: Instance Type Filtering (if applicable)

**File:** `pkg/providers/instancetype/instancetypes.go`

Some features only work with specific VM SKUs. If your feature has SKU constraints:

1. Add a filter function: `isInstanceTypeSupportedBy<Feature>(instanceType, nodeClass)`
2. Wire it into `createInstanceTypes()` or `filterInstanceTypes()`
3. Optionally add a well-known label for scheduling simulation

**Example (EncryptionAtHost):**
```go
func isInstanceTypeSupportedByEncryptionAtHost(instanceType *skewer.SKU, nodeClass *v1beta1.AKSNodeClass) bool {
    if !nodeClass.GetEncryptionAtHost() {
        return true // feature not requested, all SKUs are fine
    }
    return instanceType.HasCapability("EncryptionAtHostSupported")
}
```

### Phase 7: Bootstrapping Client Path (for VM-based provisioning)

If the feature also needs to work on the VM provisioning path (self-hosted Karpenter), thread the field through:

**Files:**
- `pkg/providers/imagefamily/resolver.go`
- `pkg/providers/imagefamily/{ubuntu_2204,ubuntu_2404,azlinux,...}.go`
- `pkg/providers/imagefamily/customscriptsbootstrap/provisionclientbootstrap.go`
- `pkg/providers/imagefamily/customscriptsbootstrap/utils.go` (if conversion needed)

### Phase 8: E2E Tests

**File:** `test/suites/integration/<feature>_test.go`

E2E tests create a real NodePool + AKSNodeClass with the feature configured, provision a pod, and verify the resulting node has the expected configuration (e.g., checking sysctl values via a privileged verification pod).

## Currently Unwired Machine API Fields

These fields exist in the Machine API SDK but are not yet wired from Karpenter CRDs. Each is a candidate for future pool-level feature work:

| Machine API Field | Section | Notes |
|---|---|---|
| `PodSubnetID` | Network | Per-pool pod subnet |
| `EnableNodePublicIP` | Network | Public IP for nodes |
| `NodePublicIPPrefixID` | Network | Custom public IP prefix |
| `IPTags` | Network | IP tagging |
| `GPUInstanceProfile` | Hardware | MIG partitioning (A100/H100) |
| `KubeletDiskType` | Kubernetes | OS vs Temp disk for kubelet |
| `WorkloadRuntime` | Kubernetes | Kata containers |
| `ArtifactStreamingProfile` | Kubernetes | Image streaming |
| `EnableVTPM` | Security | Virtual TPM |
| `EnableSecureBoot` | Security | Secure boot |
| `LinuxProfile` | OperatingSystem | In-flight on branch |
| `WindowsProfile` | OperatingSystem | Windows node support |

## Machine API Side Considerations

When implementing a new feature, ensure the Machine API supports the field on their side. The process should be:

1. Verify the field exists in the `armcontainerservice` SDK (check the `Machine` struct)
2. If not, coordinate with the Machine API team for timely support
3. Be aware that CRD API shapes may differ from ARM API patterns (e.g., K8s uses enums as strings, ARM may use typed enums)

## Tips

- **Check existing PRs** for the pattern. Search for `configure` in `aksmachineinstancehelpers.go` to see all existing implementations.
- **The commented-out nil fields** in `buildAKSMachineTemplate()` serve as placeholders marking where wiring will go.
- **SDK version matters** — some fields require specific SDK versions. Check `go.mod` for the current `armcontainerservice` version.
- **Chart snapshot tests** (`ccp/charts/tests/*/snapshots/`) may need regeneration if Helm values change.
