# AzureNodeClass — Generic Azure VM Provisioning

**Author:** @comtalyst

**Last updated:** April 2, 2026

**Status:** In Progress

## Overview

`AzureNodeClass` extends karpenter-provider-azure to support Kubernetes clusters running on Azure VMs that are not managed by AKS. This enables Karpenter as a general-purpose VM autoscaler for any Kubernetes distribution on Azure (kubeadm, k3s, Talos, etc.).

### Goals

* Provision Azure VMs for non-AKS Kubernetes clusters via a dedicated `AzureNodeClass` CRD
* Support user-provided VM images and bootstrap scripts
* Coexist with `AKSNodeClass` in the same controller binary without regressions
* Provide a clean extension point for future NodeClass types

### Non-Goals

* Windows VM support
* Automatic image updates or OS patching for non-AKS VMs
* Karpenter-managed node bootstrap (users bring their own)
* AKS billing, CSE, or identifying extensions on non-AKS VMs

## Architecture

### Two CRDs, One Provider

Both `AKSNodeClass` (v1beta1) and `AzureNodeClass` (v1alpha1) live in the same controller binary under group `karpenter.azure.com`. Upstream Karpenter's `GetSupportedNodeClasses()` supports multiple NodeClass types natively.

### NodeClass Interface

A narrow `NodeClass` interface in `pkg/apis/v1beta1/` captures only the VM-level properties consumed by shared helpers (`resolveSubnetID`, `Tags`, `configureSecurityProfile`):

```go
type NodeClass interface {
    client.Object
    GetVNETSubnetID() *string
    GetTags() map[string]string
    GetEncryptionAtHost() bool
    Hash() string
    StatusConditions() status.ConditionSet
}
```

AKS-specific properties (Kubelet, ImageFamily, LocalDNS, ArtifactStreaming) are NOT on this interface — they're accessed via the concrete `*AKSNodeClass` type.

### Separate BeginCreate Methods

`BeginCreateAKS` and `BeginCreateAzureVM` are separate methods on the VM provider because the two paths diverge in image resolution, bootstrap, LB/NSG, extensions, identity, storage profile, and error handling. No adapter pattern — each method takes its concrete NodeClass type.

### CloudProvider Dispatch

Runtime dispatch is based on `NodeClassRef.Kind`, not provision mode:

- `Create()`: routes to `createAzureVMNodeClaim()` or existing AKS path
- `GetInstanceTypes()`: verifies NodeClass exists, calls `ListForAzureNodeClass()` on the instance type provider for AzureNodeClass (or `List()` for AKSNodeClass)
- `IsDrifted()`: hash-based drift for AzureNodeClass, full drift suite for AKS

### Controllers

Each NodeClass type has its own controller set. The naming convention is symmetric: `AKSNodeClassController` / `AzureNodeClassController`.

| Controller | AKSNodeClass | AzureNodeClass | Agnostic |
|-----------|:---:|:---:|:---:|
| Hash | ✅ | ✅ | |
| Status | ✅ (4 sub-reconcilers) | ✅ (validation only) | |
| Termination | ✅ | ✅ | |
| In-place update | ✅ | ✅ (tags only) | |
| GC (instance) | | | ✅ |
| GC (NIC) | | | ✅ |
| Instance type | | | ✅ |

## Decisions

TODO: both Azure VM and AKS VM floats the same way
TODO: separate controllers
TODO: CIG

### Decision 1: Interface, Not Adapter

**Chosen:** `NodeClass` interface that both types implement natively.

**Rejected:** Adapter pattern (converting `AzureNodeClass` → `AKSNodeClass`).

The adapter creates lie-objects with nil AKS-specific fields (`Status.KubernetesVersion`, `Status.Images`) that downstream code wasn't designed for. Concretely: `OSDiskSizeGB=nil` produces 0 GiB ephemeral-storage, breaking scheduling. The interface approach forces each integration point to explicitly decide what each NodeClass type provides.

### Decision 2: Separate Instance Type Provider Methods

The instance type provider has two methods: `List(*AKSNodeClass)` for AKS and `ListForAzureNodeClass(*AzureNodeClass)` for AzureNodeClass. This follows the same pattern as `BeginCreateAKS` / `BeginCreateAzureVM` — each method takes its concrete type, no adapter or zero-value placeholder. Internally, both methods share the same core logic (`NewInstanceType`, `createOfferings`, `isSupported`) via primitive parameters.

### Decision 3: AzureNodeClass is v1alpha1

Breaking changes are expected before GA. The CRD can evolve independently of AKSNodeClass.

## AzureNodeClass Parity with AKSNodeClass

Not all AKSNodeClass features apply to AzureNodeClass. Assessment:

| Feature | AKSNodeClass | AzureNodeClass | Rationale |
|---------|:---:|:---:|------|
| Image resolution from catalog | ✅ | N/A | User provides imageID directly |
| K8s version discovery | ✅ | N/A | User manages cluster version |
| Subnet validation (status) | ✅ | ✅ | Implemented in multi-sub PR |
| Disk encryption set validation | ✅ | N/A | No DES field on AzureNodeClass |
| Hash-based drift | ✅ | ✅ | |
| K8s version drift | ✅ | N/A | Karpenter doesn't manage cluster version |
| Image version drift | ✅ | N/A | imageID changes caught by hash drift |
| Kubelet identity drift | ✅ | N/A | AKS concept |
| Machine API drift | ✅ | N/A | AKS Machine API only |
| In-place tag updates | ✅ | ✅ (tags only) | |
| AKS-specific labels | ✅ | N/A | `kubernetes.azure.com/*` labels are AKS-only |
| Garbage collection | ✅ | ✅ | Works via shared VMProvider |

### AKSNodeClass Fields Not on AzureNodeClass

`AKSNodeClass` has several fields that are intentionally absent from `AzureNodeClass`. These fields are AKS-specific concepts that have no effect in non-AKS VM provisioning — they either drive AKS infrastructure that is skipped, or produce default/no-op values when the relevant AKS systems are not present.

| AKSNodeClass Field | Why absent from AzureNodeClass |
|--------------------|-------------------------------|
| `kubelet` | Configures kubelet via AKS CSE bootstrap scripts. AzureNodeClass skips CSE entirely — the user's image manages its own kubelet configuration. |
| `fipsMode` | Selects FIPS-compliant AKS VHD images and sets FIPS kubelet labels. AzureNodeClass uses user-provided images (`imageID`) — FIPS compliance is the user's responsibility at the image level. |
| `localDNS` | Configures per-node local DNS for AKS clusters and filters instance types by minimum vCPU count. Not applicable to non-AKS clusters which manage their own DNS infrastructure. |
| `artifactStreaming` | Enables ACR artifact streaming for AKS nodes and filters ARM64 instance types when enabled. ACR artifact streaming is an AKS-specific feature that requires AKS node registration. |

These fields were evaluated during design and confirmed to be inert in non-AKS provisioning paths. Adding them to `AzureNodeClass` would pollute the CRD API with options that have no effect, confusing users who expect every spec field to be meaningful.

If a future use case requires any of these concepts on non-AKS VMs (e.g., kubelet tuning for self-managed clusters), they should be added as new fields with non-AKS-specific semantics rather than inheriting the AKS field definitions.

### Fields Added After Re-examination

Two fields initially categorized as AKS-specific were found to have generic scheduling roles:

| Field | Generic Role | AKS-Specific Role (not used by AzureNodeClass) |
|-------|-------------|------------------------------------------------|
| `maxPods` | Controls `Capacity[corev1.ResourcePods]` — directly affects how many pods Karpenter schedules per node | Azure CNI secondary IP allocation, AKS Machine API MaxPods, kubelet `--max-pods` flag in CSE bootstrap |
| `imageFamily` | GPU instance type compatibility filtering — declares which OS family the custom image belongs to, enabling correct GPU SKU selection | AKS image catalog resolution (CIG/SIG image selection), OSSKU mapping for bootstrap scripts |

Both fields are present on `AzureNodeClass` with the same semantics as their generic roles. The AKS-specific roles (image catalog, bootstrap) are not triggered by `AzureNodeClass` since it uses separate creation paths. Both fields are wired through via `ListForAzureNodeClass()` on the instance type provider.

### CRD Deployment: AzureNodeClass Must Be Deployed Alongside AKSNodeClass

The `AzureNodeClass` type is registered in the Go scheme at import time (via `init()` in `pkg/apis/v1alpha1/doc.go`). This has two consequences:

1. **`WaitForCRDs()`** in the operator startup collects all types in the scheme with group `karpenter.azure.com` and waits for their CRDs to exist in the API server. If the `AzureNodeClass` CRD is not deployed, the operator blocks until timeout and crashes.

2. **Controller registration** — controllers that call `For(&v1alpha1.AzureNodeClass{})` fail at startup if controller-runtime's REST mapper can't discover the CRD.

**Resolution: always deploy the AzureNodeClass CRD**, including in NAP/AKS-only deployments. The CRD is inert when no `AzureNodeClass` objects are created — it adds zero runtime cost. The CRD YAML must be included in the Helm CRD chart (`charts/karpenter-crd/templates/`).

This is analogous to how `karpenter-provider-aws` always deploys `EC2NodeClass` regardless of whether the cluster is EKS-managed or self-managed.

An alternative (conditional type registration gated on provision mode) was considered but rejected: Go `init()` functions run before command-line flags are parsed, making it impossible to conditionally exclude types from the scheme without significant architectural changes to the startup sequence.

## Code Ownership Map

Every component in the codebase falls into one of these categories:

| Category | Description | Count |
|----------|-------------|:-----:|
| **Shared** | One implementation, both NodeClass types use it | 15 |
| **Shared via interface** | Accepts the `NodeClass` interface | 3 |
| **Dispatch** | One entry point, routes to separate logic per type | 5 |
| **Duplicated** | Same logic, different types (generification candidate) | 3 |
| **Separate** | Different logic, different types | 4 |
| **AKS-only** | Only used by `AKSNodeClass` | 10 |
| **AzureNodeClass-only** | Only used by `AzureNodeClass` | 5 |

### Full Component Map

| Component | AKS | Azure | Category |
|-----------|:---:|:-----:|----------|
| CRD definition | `v1beta1/aksnodeclass.go` | `v1alpha1/azurenodeclass.go` | Separate |
| NodeClass interface | implements | implements | Shared contract |
| Hash controller | `AKSNodeClassController` | `AzureNodeClassController` | Duplicated |
| Status controller | 4 sub-reconcilers | validation + subnet only | Separate |
| Status: images reconciler | ✅ | — | AKS-only |
| Status: K8s version reconciler | ✅ | — | AKS-only |
| Termination controller | `AKSNodeClassController` | `AzureNodeClassController` | Duplicated |
| In-place update controller | ✅ | ✅ | Dispatch |
| GC controller (instance) | ✅ | ✅ | Shared |
| GC controller (NIC) | ✅ | ✅ | Shared |
| Instance type controller | ✅ | ✅ | Shared |
| `CloudProvider.Create()` | → `createVMInstance` / `createAKSMachineInstance` | → `createAzureVMNodeClaim` | Dispatch |
| `CloudProvider.GetInstanceTypes()` | → `List()` | → `ListForAzureNodeClass()` | Dispatch |
| `CloudProvider.IsDrifted()` | → hash + k8s + image + kubelet + machine drift | → hash drift only | Dispatch |
| `CloudProvider.Delete/Get/List` | ✅ | ✅ | Shared |
| `VMProvider.BeginCreate()` | `beginLaunchInstance()` | — | AKS-only |
| `VMProvider.BeginCreateAzureVM()` | — | `beginLaunchAzureVM()` | AzureNodeClass-only |
| `VMProvider.Get/List/Delete` | ✅ | ✅ | Shared |
| Instance type provider `List()` | ✅ | — | AKS-only method |
| Instance type provider `ListForAzureNodeClass()` | — | ✅ | AzureNodeClass-only method |
| Instance type internals (NewInstanceType, createOfferings, isSupported) | ✅ | ✅ | Shared |
| `resolveSubnetID()` | ✅ | ✅ | Shared via interface |
| `Tags()` | ✅ | ✅ | Shared via interface |
| `configureSecurityProfile()` | ✅ | ✅ | Shared via interface |
| `configureOSProfile()` | ✅ | ✅ | Shared |
| `configureBillingProfile()` | ✅ | ✅ | Shared |
| `configureNetworkProfile()` | ✅ | ✅ | Shared |
| `resolveImageReference()` | — | ✅ | AzureNodeClass-only |
| `configureDataDisk()` | — | ✅ | AzureNodeClass-only |
| `mergeIdentities()` | — | ✅ | AzureNodeClass-only |
| `AZClientManager` | — | ✅ | AzureNodeClass-only |
| AKS bootstrap (scriptless, CSE) | ✅ | — | AKS-only |
| AKS image resolution (imagefamily) | ✅ | — | AKS-only |
| AKS LB provider | ✅ | — | AKS-only |
| AKS NSG provider | ✅ | — | AKS-only |
| AKS labels provider | ✅ | — | AKS-only |
| AKS Machine provider | ✅ | — | AKS-only |
| Pricing provider | ✅ | ✅ | Shared |
| Zone provider | ✅ | ✅ | Shared |
| Options validation | AKS fields required | azurevm skips AKS fields | Dispatch |
| Hash-based drift check | `areStaticFieldsDrifted()` | `isAzureNodeClassDrifted()` | Duplicated |
| K8s version / image / kubelet / machine drift | ✅ | — | AKS-only |

### Ownership Implications

- **AKS changes** (10 AKS-only components) cannot affect AzureNodeClass. No risk of cross-contamination.
- **AzureNodeClass changes** (5 AzureNodeClass-only components) cannot affect AKS. Partners contribute here.
- **Shared changes** (22 components) need care — but these are stable infrastructure (pricing, zones, VM lifecycle, GC, billing/network profile). Changes are rare and tested by both paths.
- **Duplicated components** (3) are candidates for future generification via type parameters, but the duplication is small (~120 lines each) and explicit.

## PR Stack

```
main
 └── comtalyst/vm-path-simplify (PR #1574)
      └── comtalyst/azurenodeclass-v4 (PR #1579) — core
           ├── comtalyst/multi-sub-v4 (PR #1580)
           ├── comtalyst/data-disk-v4 (PR #1581)
           └── comtalyst/instancetype-overrides-v4 (PR #1582)
```
