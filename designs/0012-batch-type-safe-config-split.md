<!-- Proposal B: Type-Safe Config Split for Batch VM Creation -->
<!-- Origin: PR #1455 review discussion -->
<!-- https://github.com/Azure/karpenter-provider-azure/pull/1455#discussion_r2901097563 -->

# Proposal B: Type-Safe Config Split for Batch VM Creation

## Status

**Implemented** — `MachineTemplate` with `SharedMachineConfig` and `PerMachineConfig`
structs enforces the split at the template creation boundary. The batch system's
runtime field registry (`batch_field_registry.go`) remains as a complementary
safety net within the `AKSMachinesAPI` interface boundary.

## Context

The batch VM creation system groups machines by shared configuration and sends
per-machine fields (name, zones, tags) via a separate HTTP header. The current
approach uses a runtime field registry (`batch_field_registry.go`) with:

- **Proposal A** (implemented in PR #1455): `ClearPerMachineFields()` /
  `ClearReadOnlyFields()` in the `instance` package, co-located with the
  template builder.
- **Proposal C** (implemented in PR #1455): Batch-awareness tests that catch
  regressions when a per-NodeClaim field is added without registering it.

These provide runtime and test-time safety, but the classification of
shared vs per-machine fields is not enforced by the type system.

## Problem

When a developer adds a new field to `buildAKSMachineTemplate()`, they must
manually decide whether it's shared (same for all machines in a batch) or
per-machine (varies per NodeClaim). If they forget to register a per-machine
field in `ClearPerMachineFields()`:

1. The batch grouping hash includes the field, breaking batching for machines
   that should batch together.
2. The field is included in the shared API body instead of the per-machine
   header, causing all machines in a batch to get the first machine's value.

Proposal C catches scenario (2) at test time, and Proposal A makes the
registration discoverable. But neither prevents the mistake at compile time.

## Proposed Solution

Split the machine template construction into two typed structs at the point
of creation in `buildAKSMachineTemplate()`:

```go
// SharedMachineConfig contains fields identical across all machines in a batch.
// The batch grouping hash is computed from this struct.
type SharedMachineConfig struct {
    Hardware        *armcontainerservice.MachineHardwareProfile
    Kubernetes      *armcontainerservice.MachineKubernetesProfile
    OperatingSystem *armcontainerservice.MachineOSProfile
    Network         *armcontainerservice.MachineNetworkProperties
    Priority        *armcontainerservice.ScaleSetPriority
    Mode            *armcontainerservice.AgentPoolMode
    NodeImageVersion *string
    Security        *armcontainerservice.MachineSecurityProfile
}

// PerMachineConfig contains fields that vary per machine (per NodeClaim or
// per offering selection). These travel via the BatchPutMachine HTTP header.
type PerMachineConfig struct {
    MachineName string
    Zones       []string
    Tags        map[string]string
}

// MachineTemplate is the full template returned by buildAKSMachineTemplate.
type MachineTemplate struct {
    Shared     SharedMachineConfig
    PerMachine PerMachineConfig
}
```

The template builder would return `MachineTemplate` instead of
`armcontainerservice.Machine`. The batch grouper hashes only `Shared`.
The coordinator sends `Shared` in the API body and `PerMachine` in the header.

A `ToAzureMachine()` method reassembles into `armcontainerservice.Machine` for
the non-batch (single-VM) code path.

## Trade-offs

**Pros:**
- Compile-time enforcement: adding a field to the wrong struct is a type error
- No runtime registry (`ClearPerMachineFields`) needed
- Clear API boundary between shared and per-machine config

**Cons:**
- Larger refactor touching `buildAKSMachineTemplate` and all callers
- Must track Azure SDK `MachineProperties` changes in two places (the SDK
  struct and our `SharedMachineConfig`)
- The `ToAzureMachine()` reassembly is boilerplate that must stay in sync
- Proposals A+C already provide adequate safety for the current field set

## Decision

Implemented. The type-safe split is applied at the template creation boundary:

- `buildAKSMachineTemplate()` returns `*MachineTemplate` (see `machine_template.go`)
- The caller reassembles via `ToAzureMachine()` for the `AKSMachinesAPI` interface
- The batch system continues to use `ClearPerMachineFields`/`ClearReadOnlyFields`
  internally because it receives a flat `armcontainerservice.Machine` through the
  interface boundary

This gives compile-time enforcement where mistakes actually happen (template
construction) while keeping the batch internals stable. The runtime registry
is complementary, not redundant — it protects the batch system's own boundary.

## References

- PR #1455 review discussion: https://github.com/Azure/karpenter-provider-azure/pull/1455#discussion_r2901097563
- `batch_field_registry.go`: Current runtime registry (Proposal A)
- `batch_field_registry_test.go`: Batch-awareness tests (Proposal C)
