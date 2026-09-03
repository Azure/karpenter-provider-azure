# Azure Compute Fleet Batching for Karpenter

**Author:** Abhijeet Warhekar

---

## Overview

Today, when Karpenter scales up a cluster, it fires one VM PUT call per NodeClaim in `scriptless` mode. A burst of 50 pods needing new nodes means 50 independent API calls, each negotiating capacity on its own, each subject to throttling, and none taking advantage of Azure's multi-SKU allocation intelligence.

This design introduces the **Azure Compute Fleet API** (`Microsoft.AzureFleet/fleets`) as a transparent batching layer between Karpenter and the Azure compute platform, similar to the approach used by [AKS Machines batch creation](./0010-aks-machines-batch-creation.md). Instead of N individual VM PUT calls, the cloud provider groups compatible Create requests and provisions them in a single Fleet PUT. Fleet natively handles multi-SKU selection, zone placement, and capacity optimization - capabilities that are impossible to replicate with individual VM calls.

Fleet integration is selected cluster-wide via `PROVISION_MODE=fleet`. It is purely additive: existing single-VM provisioning paths remain unchanged and are the default. No per-NodePool or per-AKSNodeClass toggle is introduced.

---

## Goals

1. **Reduce provisioning API overhead:** N NodeClaims produce 1 Fleet PUT instead of N VM PUTs
2. **Leverage Fleet allocation intelligence:** multi-SKU capacity selection, zone balancing, Spot eviction-risk optimization
3. **Reuse existing patterns:** generic batcher, Promise interface, allocation strategy filter, ICE cache, existing GC framework
4. **Support both on-demand and Spot:** with appropriate allocation strategies per capacity type
5. **Maintain existing paths:** Fleet integration is additive; existing provisioning paths remain unchanged

---

## Design Summary

Three coupled decisions define this design. They form a self-reinforcing bundle optimized for low latency and simplicity:

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Completion signal | Synchronous PUT response (no LRO poll) | Minimizes critical-path latency; VMs are available immediately after PUT returns |
| nodeclaim-name tag | Out-of-band `fleettag` reconcile controller | Removes tagging from the critical path; tag is diagnostics-only, not a GC signal |
| Surplus VM reclamation | Generic Instance GC (ProviderID-based) | Reuses existing infrastructure; avoids tag-race false positives since it keys on ProviderID, not tags |

**End-to-end flow:**
1. PUT the Fleet (synchronous - no LRO poll)
2. LIST Fleet VMs via Fleet SDK `ListVirtualMachines` API
3. FIFO-pair each NodeClaim with a well-formed VM (in-memory, fast)
4. Unblock callers immediately with assignment + ProviderID
5. `FleetMemberPromise.Wait()` polls GET VM until `provisioningState` reaches terminal state (fast failure detection for ICE cache feedback)
6. `fleettag` controller PATCHes `nodeclaim-name` tag out-of-band (for Azure-side diagnostics)
7. Generic Instance GC reclaims surplus VMs whose ProviderID has no matching NodeClaim

---

## End-to-End Flow

### 1. Generic Batcher (reused from `pkg/utils/batcher`)

The batcher accumulates incoming `FleetVMProvisionRequest` entries and groups them by batch key. It fires on three configurable triggers:
- **Idle timeout:** fires when no new request arrives within a short window (allows fast single-node scale-up)
- **Max timeout:** fires unconditionally after an upper-bound duration (caps worst-case latency)
- **Max batch size:** fires when the pending count reaches a threshold (avoids unbounded accumulation)

On fire, the batcher atomically swaps the pending map and dispatches each grouped batch to the executor for Fleet creation.

### 2. Fleet Executor

The executor transforms a pending batch into a Fleet CreateOrUpdate call and distributes results:

1. **Compute fleet name:** `fleet-{clusterName}-{hash8}-{suffix}` — the suffix (e.g., a timestamp or short random token) ensures no conflicts when the same batch key fires multiple times, since Launch-mode Fleets are immutable after creation. The exact suffix strategy is TBD (timestamp preferred over random to avoid conflict).
2. **Build Fleet PUT body** from the representative request. All requests in a batch share the same VM template (image, subnet, security profile, etc.) because the batch key guarantees identical Fleet bodies at capacity=1.
3. **Submit PUT** via `BeginCreateOrUpdate` - treated as synchronous. The Fleet API's sync path returns immediately with the created Fleet resource. No LRO polling is performed.
4. **List VMs** via `ListVirtualMachinesPager` - retrieves the VMs Fleet created, including their actual VMSize and Zone (chosen by Fleet's allocation algorithm).
5. **Run FIFO assignment** (in-memory): pairs VMs to NodeClaims in insertion order.
6. **Distribute `FleetSharedState`** to all blocked callers, unblocking their promises.

**Batch splitting:** When a single batch exceeds `MaxFleetCapacity` (1000 VMs - a conservative limit below Azure's 10,000 platform cap), it is split into parallel sub-batches. Each sub-batch creates its own Fleet resource concurrently and distributes results independently to its subset of requests.

### 3. FleetMemberPromise

The promise implements a two-phase design that separates fast assignment from slow provisioning verification:

- **`ResolveAssignment()`** - Synchronous, fast, in-memory. Called immediately after the executor completes. Reads the FIFO assignment from `FleetSharedState` and populates VM stub fields (ID, Name, VMSize, Zone, Location). Returns ProviderID so core can create the NodeClaim and begin its lifecycle without waiting for full VM provisioning.

- **`Wait()`** - Asynchronous, runs in a background goroutine inside `handleInstancePromise`. Polls `GET /virtualMachines/{name}?$expand=instanceView` every 5 seconds until `provisioningState` reaches `Succeeded` or `Failed`. On success, the full VM object (Tags, TimeCreated, ImageReference) is available for informational purposes. On failure, the error handler is invoked to mark the SKU/zone unavailable in the ICE cache, `Cleanup()` deletes the failed VM, and the error is returned so the NodeClaim is retried immediately with updated offerings.

- **`Cleanup()`** - Routes VM deletion through `VMProvider.Delete`, the same path used by user-initiated `CloudProvider.Delete`. This inherits in-process deduplication, idempotency checks, and `ForceDeletion=true` semantics.

### 4. Fleet Tag Controller

A periodic singleton reconcile controller that applies the `nodeclaim-name` owner tag to Fleet-provisioned VMs after they have been assigned to a NodeClaim:

1. Lists Fleet-provisioned VMs (those carrying the `karpenter.azure.com_fleet-name` tag)
2. Builds a ProviderID-to-NodeClaim-name map from live NodeClaims in the cluster
3. PATCHes `karpenter.azure.com_nodeclaim-name` onto each assigned-but-untagged VM whose ProviderID maps to a live NodeClaim
4. Leaves surplus VMs (those with no ProviderID match) untagged - they are reclaimed by Instance GC

**Purpose:** This tag is only for operator debugging through the Azure portal or CLI. It lets operators identify which NodeClaim owns a VM without cluster access, but it is never used for assignment, lifecycle correctness, or GC decisions. All garbage collection is ProviderID-based.

---

## Batching

### Batch Key

NodeClaims coalesce into the same Fleet PUT when their `BatchKey` hash matches. The key is computed by:

1. Building a capacity=1 Fleet PUT body from the request (the canonical single-VM representation)
2. Wrapping with ICG/ICB fields (not expressible in the typed SDK struct, injected via raw JSON patch)
3. SHA256 hashing the JSON-serialized result
4. Prefixing with `{nodepool}/{capacityType}/{hash16}` for human-readable log/metric identification

The batch key **is** the hash of the capacity=1 Fleet PUT body itself. There is no separate field list to maintain or risk drifting from the actual request. Any field that affects the Fleet request automatically affects the key. Two requests batch together if and only if their normalized Fleet bodies are byte-identical. This eliminates the class of bugs where a new field is added to the Fleet body but forgotten in a separate batch key struct.

**Excluded from hash** (per-VM or additive): VM name, per-NodeClaim tags, capacity count. These differ across requests in the same batch but do not affect VM template compatibility.

**Determinism invariants:**
- **Go map iteration order:** All map-derived fields (labels, flags) are sorted by key before serialization, ensuring identical bytes across invocations for identical inputs.
- **Image version pinning:** The resolved image URL is part of customData and therefore part of the hash. If a new image version is published mid-batch-window, affected NodeClaims get different hashes and land in separate Fleets. The mitigation is to pin the image version once per reconcile loop so all NodeClaims in a batch see the same URL. (TBD - not yet implemented; result without pinning is functionally correct but suboptimal batching.)
- **ICG/ICB fields:** Wrapped alongside the Fleet body before hashing since they are not expressible in the SDK struct but affect placement.

### Example: 8 NodeClaims Produce 4 Fleet Calls

| NodeClaims | Fleet | Why separate |
|---|---|---|
| NC-1, NC-2, NC-3 (D2s/D4s, on-demand, Ubuntu) | Fleet 1: capacity=3, Regular | Baseline batch |
| NC-4, NC-5, NC-6 (D2s/D4s, spot, Ubuntu) | Fleet 2: capacity=3, Spot | Different capacity type → different priority profile |
| NC-7 (D2s/D4s, on-demand, AzureLinux) | Fleet 3: capacity=1 | Different image → different customData → different hash |
| NC-8 (D8s, on-demand, Ubuntu) | Fleet 4: capacity=1 | Different SKU list → different vmSizesProfile → different hash |

Spot and Regular NodeClaims always land in separate Fleets because the capacity type determines which priority profile (regularPriorityProfile vs spotPriorityProfile) is present in the body.

---

## VM-to-NodeClaim Assignment

After the Fleet PUT succeeds and VMs are listed, the executor must pair VMs back to NodeClaims. This post-creation assignment step is unique to Fleet (in single-VM mode, the 1:1 mapping is implicit because the VM name matches the NodeClaim name).

The algorithm is **pure FIFO**: the Nth request (in submission order) is paired with the Nth well-formed VM (in list order). No re-matching by SKU or zone is performed because the pairing records ownership rather than selecting placement.

**Data structures:**

```go
VMAssignmentRequest {
    NodeClaimName   string
    AcceptableSKUs  []string
    AcceptableZones []string
    InstanceTypes   map[string]*cloudprovider.InstanceType
}

FleetAssignment {
    VM           *armcompute.VirtualMachine
    InstanceType *cloudprovider.InstanceType
    Zone         string
}
```

**Why FIFO is correct:** Fleet selects each VM's concrete SKU and zone from the same candidate lists (`vmSizesProfile` and zones) shared by every request in the batch. Since all requests in a batch produced identical Fleet bodies at capacity=1, every VM returned for that Fleet satisfies the inputs of every request, even when Fleet chooses different SKUs or zones for individual VMs. Re-matching by SKU or zone would add complexity without improving correctness.

**Zone/SKU derivation:** The specific SKU and zone assigned to each VM are read from the Fleet `ListVirtualMachines` API response - `VMSize` and `Zone` fields per VM. Karpenter never assumes what was requested; it always reports what Fleet actually chose. The AKS-label zone is constructed from the numeric ARM zone + Location (e.g., ARM zone "1" in eastus becomes `eastus-1`).

---

## Fleet API Request

**Endpoint:** `PUT /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.AzureFleet/fleets/{fleetName}?api-version=2024-11-01`

**Fleet naming:** `fleet-{clusterName}-{hash8}-{suffix}` — each invocation produces a distinct name because Launch-mode Fleets are immutable (cannot be updated after creation). The suffix (timestamp or short random token) prevents conflicts; see Fleet Executor step 1 for details.

**Key body fields:**
- `properties.mode`: `"Launch"` - immutable, one-shot Fleet. Azure auto-deletes the Fleet resource shell after a time interval; no Karpenter-side cleanup needed.
- `properties.vmSizesProfile`: top-10 candidate SKUs (price-sorted, truncated from `FilterInstanceOfferings` output)
- `properties.regularPriorityProfile` or `spotPriorityProfile`: target capacity + allocation strategy
- `properties.computeProfile.baseVirtualMachineProfile`: full VM template (OS, storage, network, security, identity, extensions)

**Allocation strategies:**
- **Regular (on-demand):** `LowestPrice` - no eviction risk, so cheapest compatible SKU is the best choice.
- **Spot:** `PriceCapacityOptimized` - balances price against eviction probability by selecting SKUs with excess capacity.
- Allocation strategy is not user-configurable in initial release; it will be evaluated later based on feedback.

---

## Tags

**Set at Fleet creation** (applied uniformly to all VMs via the Fleet body):
- `karpenter.azure.com_cluster`: cluster name
- `karpenter.sh_nodepool`: nodepool name
- `karpenter.azure.com_fleet-name`: fleet name (for discovery and diagnostics)
- `karpenter.azure.com_batch-key-hash`: hash prefix (for correlation)
- User-defined tags from `options.AdditionalTags` + `nodeClass.Spec.Tags`

These tags are known before VM creation because they apply uniformly to the entire batch.

**Set post-creation** (by `fleettag` controller, out-of-band):
- `karpenter.azure.com_nodeclaim-name`: `{nodeClaim.Name}`

This is the only tag that cannot be set at Fleet creation time. The Fleet provisions multiple VMs for multiple NodeClaims, and which VM maps to which NodeClaim is only known after FIFO assignment completes. The PATCH merges with existing tags so none are dropped.

---

## GET VM Polling in `FleetMemberPromise.Wait()`

After assignment, the VM may still be in `Creating` state. Without polling, `Wait()` would return immediately, core would set `Launched=true`, and Karpenter would wait for the kubelet to register a Node. If the VM subsequently fails provisioning, the failure is invisible: the NodeClaim sits in `Launched` for 15 minutes until the registration TTL expires, the ICE cache is never updated (so the same SKU/zone may be retried), and operators get no error code in logs.

**With polling:**
- Polls `GET /virtualMachines/{name}?$expand=instanceView` with exponential backoff starting at 5 seconds and capped at 30 seconds between requests
- **On `Succeeded`:** Updates `.VM` with the full VM object (Tags, TimeCreated, ImageReference, ProvisioningState). Flow proceeds normally - `Launched=true`, kubelet boots, Node registers.
- **On `Failed`:** Parses the ARM error code → marks the SKU/zone unavailable in the ICE cache → `Cleanup()` deletes the failed VM → error returned so the NodeClaim is retried immediately with updated offerings. Failure detection takes ~5-60 seconds instead of 15 minutes.

---

## Garbage Collection

| Resource | Mechanism | Details |
|---|---|---|
| Assigned Fleet VMs | Normal Karpenter lifecycle | NodeClaim owns via ProviderID; standard disruption/termination paths apply |
| Surplus Fleet VMs | Generic Instance GC | No matching NodeClaim ProviderID → deleted after standard grace period |
| Empty Fleet shell | Auto-deleted by Azure | Launch-mode Fleet resources are automatically cleaned up by the platform after a time interval |

**Why this works without a dedicated Fleet GC controller:** Assignment is synchronous (no LRO poll window), so there is no long "provisioned but unassigned" window where VMs sit in limbo. Surplus VMs (Fleet created more than needed, or NodeClaim was deleted mid-flight) simply never receive a NodeClaim ProviderID reference and are correctly reclaimed by Instance GC on its standard cadence. Because Instance GC keys on ProviderID rather than the `nodeclaim-name` tag, there is no false-positive window even though tagging is asynchronous.

If a NodeClaim is deleted while its Fleet PUT is in-flight, the resulting VM becomes a GC obligation. Relying on Instance GC is acceptable - no explicit in-flight cancellation handling is needed.

---

## Crash Recovery

If the Karpenter pod restarts during in-flight Fleet operations, the in-memory VM-to-NodeClaim assignments are lost. This is a tradeoff from delegating VM naming and placement to Fleet: unlike single-VM mode, Karpenter cannot reconstruct an uncommitted assignment from a VM name. VMs created before the crash can therefore remain unassigned until Instance GC reclaims them after its standard grace period.

- **Fleet + VMs remain in ARM:** They are durable cloud resources discoverable via tags and resource group listing.
- **Surplus/unassigned VMs:** Reclaimed by Instance GC after the standard grace period. No persisted in-flight state is required.
- **Empty Fleet shells:** Auto-deleted by Azure (Launch mode).
- **NodeClaim re-discovery:** `NodeClaim.Status.ProviderID` connects the cluster-side object back to the ARM VM for diagnostics and lifecycle operations.

GC-based cleanup is sufficient for crash recovery, but it accepts temporary VM leakage until GC runs. Persisted state or resume logic is not a GA requirement.

---

## Error Handling

### Fleet-Level Errors (PUT fails)

When the Fleet PUT itself fails, the error is distributed to all promises in the batch as a batch-wide error. All affected NodeClaims are requeued by core for retry. Common Fleet error responses:

| HTTP Status | Error Code | Meaning |
|---|---|---|
| 409 Conflict | QuotaExceeded | Various quota limits (regional cores, VM family, low-priority) |
| 409 Conflict | OperationNotAllowed | CAS blocks all input sizes for the requested priority |
| 409 Conflict | Conflict | Insufficient capacity for requested sizes |
| 400 Bad Request | InvalidParameter | maxPrice too low for all Spot SKUs |
| 404 Not Found | NotFound | Zero matching sizes for specified attributes |

### VM-Level Errors (individual VM fails provisioning)

Detected by `FleetMemberPromise.Wait()` polling. The error handler parses the ARM error code, marks the specific SKU/zone combination as unavailable in the ICE cache, and returns an error. `FilterInstanceOfferings` automatically skips marked entries on the next scheduling cycle, so subsequent batches avoid the failed capacity.

### Partial Success

Fleet may create fewer VMs than the requested capacity (e.g., due to per-SKU capacity shortfall). The executor assigns as many VMs as available via FIFO. Unmatched NodeClaims receive `InsufficientCapacityError` and are requeued by core's standard NodeClaim retry logic. The next scheduling cycle re-evaluates offerings with updated ICE cache state.

---

## Capacity-Type Label Derivation

Fleet does not set `vm.Properties.Priority` on Regular VMs (unlike the standalone VM path which explicitly sets `Priority: Regular`). The capacity-type label is therefore derived from which priority profile the Fleet body used:
- `regularPriorityProfile` present → `karpenter.sh/capacity-type: on-demand`
- `spotPriorityProfile` present → `karpenter.sh/capacity-type: spot`

This is determined at batch-key time (capacity type is part of the hash) and never ambiguous.

---

## SKU Filtering Pre-Fleet

The existing `FilterInstanceOfferings()` pipeline runs before any Fleet request is built. Its output is truncated to the **top 10 SKUs** before enqueueing into the batcher. Since the list is already price-sorted by the allocation strategy provider, taking the top 10 yields the cheapest compatible SKUs.

Constraints already enforced by the existing filter:
- **HyperV generation compatibility:** Every SKU must support the chosen image's generation (Gen1 vs Gen2). Enforced via `LabelSKUHyperVGeneration` requirement from the image family.
- **Uniform image compatibility:** No per-SKU image override exists in the Fleet body. All SKUs in a Fleet must boot the same image. Guaranteed because a single image is resolved per batch (all requests share the same image via the batch key).
- **ICE cache exclusion:** SKU/zone combinations previously marked unavailable by the ICE cache are filtered out before they reach the Fleet request.

---

## Bootstrap

Fleet mode uses the **scriptless bootstrap** path exclusively. The AKS node image's built-in bootstrap script reads cluster join parameters from `customData` (cloud-init format) and registers with the API server without requiring a Custom Script Extension (CSE). This is the same bootstrap path used by `PROVISION_MODE=aksscriptless`.

The Fleet body includes an `ExtensionProfile` for attaching VM extensions at create time. The billing extension is always included. CSE support via the extension profile is wired in code but has not been tested end-to-end.

Fleet mode does not support NAP (`USE_SIG=false`). Only self-hosted Community Gallery images are used in initial release.

---

## Non-Goals

1. **Per-NodePool Fleet toggle** - cluster-wide `PROVISION_MODE` only
2. **NAP/SIG image support** - self-hosted Community Gallery only until `USE_SIG` is wired
3. **Fleet "Maintain capacity" mode** - conflicts with Karpenter's lifecycle ownership model (Karpenter manages disruption/replacement, not Fleet)
4. **Per-VM SKU/zone specification** - Fleet picks from the candidate list, Karpenter does not override individual placements
5. **VMSS-based placement management** - Fleet uses VMSS Flex internally for ICG/ICB SKUs, but Karpenter does not manage or reference the VMSS directly
