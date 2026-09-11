# Safe Azure-selected zone placement

**Author:** @comtalyst

**Last updated:** August 20, 2026

**Status:** Proposed

## Overview

Karpenter currently selects one concrete Azure availability zone before launching a node. That
behavior is safe because the selected offering, the cloud request, the returned NodeClaim labels,
and the registered Node all describe the same topology. This design preserves exact concrete-zone
placement as the default.

This document proposes an opt-in path in which Azure Compute selects one zone for an individual VM
from a provider-computed allowlist. Individual VMs use the Azure Compute placement policy `Any`;
VM scale sets use `Auto`. The policies are not interchangeable. The opt-in path fixes every
non-zone property first, derives `includeZones` from exact equivalent offerings, and does not report
the NodeClaim as launched until the selected zone is authoritatively observed and validated.

Correctness means that the selected zone belongs to the safe allowlist and that the VM, delegated
Machine, NodeClaim, and Node independently agree. It does not mean even distribution across zones;
`Any` provides allocation-time choice, not a balancing guarantee.

All interfaces introduced below are proposals, not descriptions of shipped Machine capability. The
design does not require an upstream Karpenter API change. Upstream immutable, multi-valued
requirements remain unchanged, and `CloudProvider.Create` hydrates one exact observed topology
label before the provider ID is published.

## Goals

- Preserve topology, volume, and device-allocation correctness while allowing Azure Compute to
  choose among an exact scheduler-safe set of zones.
- Keep concrete-zone placement as the default and retain current explicit zonal and regional
  behavior when the feature is disabled, unsupported, or ineligible.
- Use the same eligibility, validation, ownership, and result contracts in direct VM, delegated
  Machine, and header batch create paths.
- Make retries, cancellation, restart adoption, deletion, and orphan cleanup idempotent without
  creating duplicate cloud resources.
- Preserve compatibility across Node Auto Provisioning (NAP), self-hosted deployments, VM-based
  provisioning modes, and Machine-based provisioning modes.
- Keep drift and consolidation decisions based on the selected concrete offering and observed
  topology rather than unresolved placement intent.

## Non-Goals

- Guaranteeing equal or weighted spread across availability zones.
- Replacing provider-side SKU, quota, price, reservation, or capacity filtering.
- Sending every zone admitted by a flattened NodeClaim requirement to Azure Compute.
- Introducing `auto`, `any`, or another unresolved value into Kubernetes topology domains.
- Changing upstream Karpenter requirement or NodeClaim lifecycle APIs.
- Making VM scale set `Auto` semantics apply to individual VMs.
- Defining a production value for any timeout or rollout percentage without representative data.

## Current contracts

The current and proposed contracts must remain distinguishable:

| Surface | Contract |
| --- | --- |
| Karpenter scheduling | A NodeClaim may carry immutable, multi-valued requirements, but a launched NodeClaim has one concrete topology zone and provider ID. |
| Current provider behavior | The provider chooses one exact offering and concrete zone before dispatch. This remains the default. |
| Azure Compute individual VM | The public SDK defines `Any` for system-selected VM placement. Its placement model allows `includeZones` to constrain the selected zone; this design proposes always supplying an exact non-empty allowlist. |
| Azure Compute VM scale set | The public SDK defines `Auto` for VM scale sets; this design does not change VM scale set behavior. |
| Delegated Machine | The placement-intent and observed-zone split described in this document is proposed and requires advertised target capability before use. |
| Zone representation | Azure service state uses a raw zone such as `"3"`; Kubernetes topology uses a canonical zone such as `"westus2-3"`. |

The version-pinned Azure SDK for Go defines
[`Any` for VMs and `Auto` for VM scale sets](https://github.com/Azure/azure-sdk-for-go/blob/sdk/resourcemanager/compute/armcompute/v7.3.0/sdk/resourcemanager/compute/armcompute/constants.go#L3273-L3288).
Its public
[`Placement` model](https://github.com/Azure/azure-sdk-for-go/blob/sdk/resourcemanager/compute/armcompute/v7.3.0/sdk/resourcemanager/compute/armcompute/models.go#L4484-L4501)
states that a system-selected zone must be present in `includeZones` when that list is supplied. The
pinned
[individual-VM example](https://github.com/Azure/azure-sdk-for-go/blob/sdk/resourcemanager/compute/armcompute/v7.3.0/sdk/resourcemanager/compute/armcompute/virtualmachines_client_example_test.go#L4017-L4024)
sends `Any` with `includeZones` and no explicit top-level zone; its response retains
[the placement allowlist](https://github.com/Azure/azure-sdk-for-go/blob/sdk/resourcemanager/compute/armcompute/v7.3.0/sdk/resourcemanager/compute/armcompute/virtualmachines_client_example_test.go#L4083-L4088)
and exposes a
[concrete selected zone](https://github.com/Azure/azure-sdk-for-go/blob/sdk/resourcemanager/compute/armcompute/v7.3.0/sdk/resourcemanager/compute/armcompute/virtualmachines_client_example_test.go#L4139-L4140).
The example demonstrates the request/response shape but does not establish service-wide mutual
exclusion. Rejecting automatic placement together with an explicit top-level zone is therefore a
proposed provider and Machine-boundary validation rule in this design.

`RawZoneID` and `TopologyZone` are distinct typed values throughout the proposed implementation:

```text
RawZoneID("3")
TopologyZone("westus2-3")
```

A checked mapping uses the Azure location and a known provider offering. Parsing the last character
of a topology label is never valid.

## Decisions

### Decision 1: Preserve exact placement by default

Concrete provider-selected placement remains the default for existing and new NodeClasses. All
configuration names in this subsection are proposed and are not shipped interfaces.

The proposed NodeClass opt-in is:

```yaml
spec:
  zoneSelectionPolicy: AzureSelected
```

The proposed `spec.zoneSelectionPolicy` enum allows only `Explicit` and `AzureSelected`. An omitted
field defaults to `Explicit`, and API validation rejects any other value. `AzureSelected` requests
late binding but does not guarantee it: an ineligible claim or a definitive pre-dispatch capability
rejection follows the existing exact-offering path. It never authorizes fallback after dispatch may
have occurred.

The proposed deployment-wide `AzureSelectedZonePlacement` feature gate is a boolean that defaults to
`false` independently of the NodeClass field. NAP and self-hosted deployment configuration must both
map to this same provider gate with the same default and validation; an absent gate is false, and an
invalid value fails configuration rather than enabling origination. The complete origination matrix
is:

| `spec.zoneSelectionPolicy` | `AzureSelectedZonePlacement` | New-launch behavior |
| --- | --- | --- |
| `Explicit` | `false` | Exact placement. |
| `Explicit` | `true` | Exact placement. |
| `AzureSelected` | `false` | Exact placement. |
| `AzureSelected` | `true` | Azure-selected placement only when eligibility and target capability pass; otherwise exact placement before dispatch. |

The NodeClass policy controls future placement only and is excluded from NodeClass drift hashing.
The deployment gate is likewise an origination control, not Node state. Changing either value does
not drift or replace existing nodes. Drift, consolidation, replacement, deletion, and pricing
continue to use the selected exact offering and concrete observed zone. Regional placement remains
distinct from zonal placement.

Gate and policy changes affect origination only. Read, reconcile, adoption, and delete support for an
already-created Azure-selected resource remain enabled during upgrades, downgrades, or gate rollback.
This provides an immediate rollback path without stranding resources.

### Decision 2: Use `Any` with an exact equivalent-offering allowlist

For an individual VM, the request uses `zonePlacementPolicy: Any` and an explicit `includeZones`.
The allowlist is derived from an equivalence class of exact compatible offerings after all non-zone
semantics are fixed. It is not copied from flattened NodeClaim zones.

The equivalence comparison includes, where relevant:

- VM SKU and capacity type;
- zonal placement scope and current offering availability;
- effective price and ranking class;
- UltraSSD and other OS/data-disk constraints;
- capacity-reservation identity;
- networking, security, identity, image, and other immutable launch constraints that can change
  whether an offering is valid.

A hard singleton caused by node selection, affinity, a volume, storage topology, or Dynamic Resource
Allocation (DRA) remains explicit. A multi-zone DRA or topology constraint narrows the exact
equivalent-offering set before `includeZones` is constructed. Regional offerings never appear in
`includeZones`.

### Decision 3: Keep requested placement separate from observed `Machine.zones`

For proposed delegated Machine creation, immutable requested placement is stored separately from
observed topology. An automatic create omits top-level request `Machine.zones` and supplies
`Any + includeZones` as placement intent. After successful placement, the response projects exactly
one observed raw selected zone into top-level `Machine.zones`.

The placement intent and allowlist are immutable. `Machine.zones` is server-maintained observed
state for an automatic Machine. A client cannot supply both automatic placement and a top-level
zone, and sentinel values such as `auto` or `any` are rejected rather than treated as topology.
Explicit and regional Machine representations retain their existing behavior.

Projecting the concrete numeric zone through `Machine.zones` preserves read and delete compatibility
for older readers that understand concrete zones but cannot originate or mutate automatic placement.

### Decision 4: Resolve and validate the selected zone before launch returns

Direct VM mode performs an authoritative service GET and bounded poll before returning from
`CloudProvider.Create`; a create response or replay response is not used as selected-zone authority.
Machine mode persists exactly one observed raw zone before Machine success, and the provider reads
that persisted value before returning. The rule applies to both non-batch and header batch dispatch.

The provider validates allowlist membership and a matching exact offering, converts the raw value to
one canonical topology value, and returns that label together with the provider ID. A provider ID is
not published while placement is unresolved.

After registration, VM, Machine, NodeClaim, and Node consistency is checked from independent sources.
Node equality alone is insufficient because registration can copy NodeClaim labels onto the Node.

### Decision 5: Negotiate Machine and batch capability per target

Automatic Machine origination requires proposed target-scoped capability. A capability record has a
generation, an automatic-placement contract version, and supported header batch schema versions.
The target validates the generation and required capabilities before persistence or dispatch.

Header batch schema V2 is strict and versioned. Both automatic Machine capability and V2 support
must be advertised for a batched automatic create. Missing, stale, revoked, malformed, or unknown
capability fails closed before dispatch. Non-batch creates use the same generation check.

The provider may refresh capability and choose exact placement only after a definitive no-dispatch
response. There is no fallback to a new resource after dispatch may have occurred; that path uses
adoption or cleanup under the original ownership record.

### Decision 6: Make retry ownership and adoption durable

Before any outbound request, the provider persists deterministic resource identity, ownership state,
the exact allowlist, and a complete versioned immutable-launch fingerprint. It then uses a
compare-and-swap (CAS) or ETag-guarded transition from `Prepared` to `DispatchPossible` before
sending. That durable state closes the crash window in which a request can be accepted before the
provider records a response. The launch fingerprint covers NodeClaim identity, NodeClass identity
and hash, and the complete immutable cloud request, including image or VHD, network and subnet,
disks, security, identity, bootstrap digest, SKU, capacity, placement, and every other immutable
create field.

A retry adopts an existing VM or Machine only when the fingerprint and independently read immutable
service fields match. A mismatch is a launch conflict, never permission to create a duplicate.
Bootstrap content is represented by a digest rather than stored as plaintext ownership metadata.

A configurable placement-resolution bound, `Tresolve`, returns a recoverable result that preserves
the NodeClaim and cloud-resource ownership. Timing out does not abandon an accepted resource, mark
it as zone-specific insufficient capacity, or create another resource on retry.

## Detailed design

### Eligibility

Let `R` be finalized NodeClaim requirements, `I` the selected instance type, `C` its capacity type,
`L` the fixed immutable launch configuration, and `O(I)` the provider's exact offerings for `I`.
For a chosen exact offering `o*`, derive:

```text
Zsafe = {
  RawZoneID(o) |
    o in O(I)
    and o is currently available
    and o.capacityType == C
    and o.placementScope == zonal
    and o is compatible with R
    and nonZoneSemantics(o) == nonZoneSemantics(o*)
    and azureSelectedPlacementSupports(L, o)
}
```

Azure-selected placement is eligible only when all of the following are true:

1. The proposed NodeClass `spec.zoneSelectionPolicy` is `AzureSelected` and the proposed deployment
   gate `AzureSelectedZonePlacement` is `true`.
2. The launch is one individual zonal VM, not a regional VM or VM scale set.
3. The fixed launch configuration supports `Any` placement.
4. The applicable target advertises the required capability when delegated Machine creation is used.
5. `Zsafe` contains at least two raw zones.

`includeZones` is exactly `Zsafe`, sorted and deduplicated for deterministic requests and
fingerprints. One safe zone uses exact placement. No safe zone returns the existing no-capacity
result. Eligibility is recomputed from exact offerings; correlations lost by flattening NodeClaim
requirements are not reconstructed by assumption.

A singleton topology, bound-volume, storage, or DRA requirement forces exact placement. A
multi-zone constraint intersects `Zsafe`; it cannot add an offering that was not already equivalent.
Unsupported combinations remain on the exact path only when this is decided before automatic
request dispatch.

### Machine contract

This subsection defines a proposed interface. It is not a shipped API contract.

An automatic Machine create has no top-level request zone:

```yaml
zones: omitted
placement:
  zonePlacementPolicy: Any
  includeZones:
    - "1"
    - "3"
```

A successful Machine response exposes immutable intent and exactly one observed raw zone:

```yaml
zones:
  - "3"
placement:
  zonePlacementPolicy: Any
  includeZones:
    - "1"
    - "3"
```

The proposed boundary enforces these rules:

- as a proposed provider and Machine-boundary rule, exactly one explicit numeric request zone or
  automatic placement may be supplied, never both;
- automatic placement requires `Any` and at least two valid, deduplicated raw zone IDs;
- `Auto` is rejected for an individual Machine;
- `auto` and `any` are rejected as zone values;
- placement and `includeZones` are immutable after acceptance;
- an omitted placement or zone on an idempotent replay preserves accepted immutable state;
- a conflicting placement or observed zone is rejected;
- Machine success requires durable persistence of exactly one selected raw zone;
- Machine GET returns the persisted selected zone through top-level `Machine.zones` without
  requiring a fresh VM expansion.

Existing explicit and regional Machines keep their current representation. Readers predating this
proposal can read, reconcile, and delete an automatic Machine through its concrete observed zone,
but cannot originate or alter its placement intent.

### Capability and batch versioning

A proposed capability record is scoped to the concrete Machine target rather than process-wide:

```yaml
machineCapabilities:
  generation: "7"
  placementAnyVersion: 1
  headerBatchSchemaVersions:
    - 2
```

The generation changes whenever a target grants or revokes capability. Each automatic request
carries the generation the provider observed. The target validates target identity, current
generation, automatic-placement support, and dispatch strategy before accepting any entry. A cache
may reduce discovery calls but does not authorize a create by itself.

Header batch V2 carries the version and generation with per-Machine placement so one accumulation
window can preserve different allowlists:

```json
{
  "schemaVersion": 2,
  "capabilityGeneration": "7",
  "machines": [
    {
      "name": "machine-a",
      "placement": {
        "zonePlacementPolicy": "Any",
        "includeZones": ["1", "3"]
      }
    }
  ]
}
```

The V2 decoder rejects malformed content, unsupported versions, unknown fields, duplicate names,
missing entries, stale generations, and conflicts between placement and zones before dispatch. It
never reinterprets an unsupported automatic request as regional placement. Non-batch and batch
paths share the same validation and result semantics, including a durable per-Machine indication of
whether dispatch occurred.

Capability is advertised only after request validation, durable intent storage, selected-zone
writeback, GET projection, and the applicable dispatch parser are available for that target. The
provider allowlists the specific contract version it implements; it does not infer support from an
ordered API-version comparison.

### Launch fingerprint and lifecycle

The launch fingerprint is a versioned hash over a canonical serialization of:

- NodeClaim UID and deterministic cloud-resource identity;
- NodeClass identity and launch-relevant hash;
- image reference or VHD identity;
- network, subnet, public-IP, and interface configuration;
- OS and data disks, encryption, caching, and ephemeral-disk settings;
- security profile and host or placement constraints;
- managed identity and credential-independent identity configuration;
- a bootstrap or custom-data digest;
- SKU, capacity type, reservation identity, placement scope, policy, and `includeZones`;
- every other field that the cloud service treats as immutable at create time.

Canonicalization includes the fingerprint schema version and stable ordering for maps, sets, and
zone lists. Adoption requires both an exact fingerprint match and authoritative comparison of
readable immutable service fields. Fields that cannot safely be stored are represented by stable
digests.

The durable lifecycle is:

| State | Durable evidence | Allowed transitions |
| --- | --- | --- |
| Prepared | Owner, deterministic identity, allowlist, and launch fingerprint; no outbound request has started | CAS to `DispatchPossible`, or release ownership only with proof that no request was sent. |
| DispatchPossible | Persisted pre-send state, attempt owner/epoch, deterministic identity, canonical request digest, allowlist, and fingerprint; acceptance is unknown | CAS to `Dispatched`, `ZoneResolved`, or `Abandoning`; return to `Prepared` only with definitive no-dispatch proof. |
| Dispatched | Accepted operation or authoritative matching resource plus ownership metadata | CAS to `ZoneResolved` or `Abandoning`. |
| ZoneResolved | Placement, fingerprint, and one validated raw zone | CAS to `Published` or `Abandoning`. |
| Published | Provider ID and canonical zone on the NodeClaim | Normal reconciliation or CAS to `Abandoning`. |
| Abandoning | Deterministic resource retained under cleanup ownership | Release ownership only after deletion or absence is authoritatively confirmed. |

Every transition uses the current record revision through CAS or ETag. A reconciler must win the
`Prepared` to `DispatchPossible` transition before it can send, and that write records a new attempt
epoch and owner. Concurrent reconcilers that lose the CAS do not dispatch. If the winning reconciler
stops, one replacement claims a later attempt epoch through CAS while remaining in
`DispatchPossible`; other reconcilers continue to observe without sending.

An accepted response moves `DispatchPossible` to `Dispatched`; an authoritative read that already
contains one valid zone may move directly to `ZoneResolved`. A return to `Prepared` is allowed only
when the same attempt proves its outbound client was never invoked or the target returns a definitive
no-dispatch rejection. A crash, cancellation, timeout, transport error, or not-found response after
send may have started is not such proof.

Recovery from `DispatchPossible` is adoption-first:

1. Reuse the persisted deterministic identity, exact allowlist, complete fingerprint, and canonical
   immutable request. Do not rerun offering selection.
2. Perform authoritative GET/poll and inspect any durable operation result. Adopt only when the
   resource identity, fingerprint, and readable immutable fields match.
3. Apply the proposed transient not-found policy: retain ownership and repeat authoritative reads
   throughout a configurable `TdispatchNotFound` observation interval measured from the last
   possible send. A production value requires service-consistency guarantees and measured data; this
   design does not guess one.
4. Only after that policy establishes stable absence may the single CAS owner issue an idempotent
   exact replay to the same deterministic name with the same byte-equivalent canonical immutable
   payload, allowlist, fingerprint, and idempotency identity. The state remains `DispatchPossible` until acceptance is
   resolved. Replay never selects another zone set or creates a differently named resource.

Cancellation and garbage collection cannot discard ownership while a `DispatchPossible` acceptance
is ambiguous. If the NodeClaim is deleting, CAS moves the record to `Abandoning`, and cleanup targets
the same deterministic identity; absence is confirmed under the same not-found policy before
ownership is released. For header batch, every entry reaches `DispatchPossible` before inclusion in
the outbound batch, so a crash immediately after batch send is recovered independently per Machine.

At `Tresolve`, create returns the recoverable unresolved result without clearing ownership in either
`DispatchPossible` or `Dispatched`. Orphan collection must not race a live resolution attempt. Retry,
NodeClaim liveness processing, cancellation, and garbage collection all consume the same durable
state rather than using independent guesses.

### Raw and canonical zone conversion

The implementation uses distinct types:

```text
type RawZoneID string      // for example, "3"
type TopologyZone string   // for example, "westus2-3"
```

Conversion from `RawZoneID` to `TopologyZone` requires a known Azure location and a matching exact
offering. The inverse conversion validates against the same location and offering set. Conversion
rejects empty values, regional markers, sentinels, malformed topology labels, and zones not offered
for the fixed SKU and capacity semantics.

Code must never derive a raw zone by taking the final character, splitting on an unchecked suffix,
or comparing `"3"` directly with `"westus2-3"`. Multi-character zone identifiers and location
names containing punctuation must remain safe under the typed conversion.

The common success invariants for direct VM and Machine modes are:

```text
VM.RawZoneID in persisted includeZones
canonical(location, VM.RawZoneID) == NodeClaim.TopologyZone
NodeClaim.TopologyZone == independently verified Node.TopologyZone
```

Machine modes add this invariant:

```text
only(Machine.zones) == Machine.RawZoneID == VM.RawZoneID
```

Direct VM mode does not require a Machine object; the authoritative VM GET supplies its raw zone.
For Machine mode, the backing VM observation is persisted before success, and the provider reads the
projected `Machine.zones` value before returning.

### Error handling

Errors are classified by whether dispatch is provably impossible, possible, or confirmed:

- An eligibility or capability failure proven before dispatch may use the existing exact-placement
  path. The reason is observable and does not mutate an accepted automatic request.
- A stale generation, malformed V2 entry, or unsupported contract fails closed before dispatch.
- After dispatch may have occurred, the provider never falls back by creating a second resource. It
  adopts, continues resolution, or cleans up the deterministic original resource.
- Zero, multiple, malformed, out-of-allowlist, or offering-incompatible selected zones are contract
  failures. They are not silently rewritten.
- A pre-existing conflicting NodeClaim topology label is rejected rather than overwritten.
- A zone-agnostic capacity failure does not make one arbitrary exact-zone offering unavailable.
  Included zones are collectively marked unavailable only when the service error explicitly covers
  the full allowlist.
- A `Tresolve` expiry is recoverable and ownership-preserving. It is not classified as insufficient
  capacity or as an unowned resource.
- For a resource whose creation was already resolved, authoritative not-found during idempotent
  deletion is success. In `DispatchPossible`, not-found remains transient until the documented
  observation policy completes; ambiguous create results require authoritative GET-first adoption
  before an exact replay.
- Partial header batch outcomes are handled per Machine; one entry cannot turn another entry's
  accepted resource into a retry-created duplicate.

Every error records bounded reason codes and lifecycle state without logging bootstrap data,
credentials, or unbounded identifiers.

## Alternatives rejected

- **Use `zones: ["auto"]` or `zones: ["any"]`.** This conflates requested placement with observed
  topology, cannot carry an exact correlated allowlist, and risks publishing a sentinel as a
  Kubernetes topology value.
- **Use `Auto` for individual VMs.** `Auto` is the VM scale set policy. Individual VM placement uses
  `Any`.
- **Build `includeZones` from flattened NodeClaim zones.** Flattening loses correlations among SKU,
  capacity, price, reservation, disk, availability, and placement scope. It can admit a zone with no
  equivalent exact offering.
- **Return before the selected zone is known.** This can publish a provider ID without truthful
  topology and can violate volume, DRA, affinity, and scheduling constraints.
- **Trust the create response as authoritative.** The design requires an authoritative GET/poll
  barrier because accepted creation and selected-zone visibility are separate lifecycle facts.
- **Fall back after an ambiguous or accepted dispatch.** A second create can duplicate or abandon the
  first resource. Durable adoption and cleanup are required instead.
- **Add unresolved topology to upstream Karpenter.** The provider can resolve topology within
  `CloudProvider.Create`, so upstream immutable multi-valued requirements and launch semantics do
  not need to change.
- **Make Azure-selected placement the default.** Exact placement has established topology, pricing,
  reservation, drift, and error-accounting behavior and remains the compatibility baseline.

## Testing

Every behavior-changing implementation starts with a failing test that demonstrates the intended
contract. Tests must prove behavior, not merely exercise serialization.

Provider unit and acceptance coverage includes:

- equivalence-class construction across availability, effective price, capacity type, reservation,
  UltraSSD and disk constraints, regional scope, and fixed launch configuration;
- singleton topology, volume, and DRA constraints staying explicit, with multi-zone constraints
  narrowing `includeZones`;
- `Any` plus exact raw allowlists for eligible claims and exact placement for all fallback cases;
- typed `RawZoneID`/`TopologyZone` conversion, including malformed and multi-character cases;
- no sentinel value entering an offering, NodeClaim, or Node topology label;
- proposed `spec.zoneSelectionPolicy` enum validation and omission defaulting to `Explicit`;
- all four NodeClass-policy/deployment-gate combinations, proving only `AzureSelected` plus
  `AzureSelectedZonePlacement=true` can originate automatic placement;
- upgrades and downgrades preserving the exact-placement default and lifecycle compatibility, with
  policy and gate changes excluded from drift decisions;
- capability missing, stale, revoked, malformed, or unknown failing before dispatch;
- direct VM authoritative GET/poll before return, including delayed visibility and ambiguous create
  responses;
- complete launch-fingerprint adoption with one-at-a-time mutations of image, network, subnet,
  disks, security, identity, bootstrap digest, NodeClass hash, SKU, capacity, and placement;
- CAS/ETag single-writer behavior with concurrent reconcilers, including a crash immediately after
  send and before response-state persistence;
- transient not-found observations retaining `DispatchPossible` ownership, followed only by an
  exact replay to the same deterministic name and canonical payload after the observation policy;
- cancellation and garbage collection in `DispatchPossible`, after `Dispatched`, after zone
  resolution, and before publication without losing ambiguous ownership;
- `Tresolve`, retry, NodeClaim liveness, deletion, and orphan collection exercised together;
- zone-agnostic and allowlist-wide capacity error accounting.

Machine contract tests cover non-batch and header batch paths:

- omission of top-level request `Machine.zones` for automatic placement;
- rejection of `Auto`, sentinels, malformed allowlists, client-supplied observed zones, unknown V2
  fields, duplicates, stale generations, and placement conflicts before dispatch;
- persistence of exactly one raw selected zone before success and projection through
  `Machine.zones` on GET;
- per-Machine `DispatchPossible` persistence before non-batch send or header-batch inclusion, including
  recovery from a crash after send but before `Dispatched` persistence;
- idempotent exact replay, immutable placement, per-entry partial batch outcomes, and generation
  revocation;
- old-reader reconciliation and deletion of an existing automatic Machine;
- unchanged explicit and regional Machine behavior.

End-to-end coverage runs where each mode is supported:

- VM-based direct creation in `aksscriptless` and `bootstrappingclient`;
- delegated Machine creation in `aksmachineapi` and `aksmachineapiheaderbatch`, covering non-batch
  and header batch dispatch;
- NAP and self-hosted deployments;
- flexible multi-zone scheduling, exact selectors, affinity and anti-affinity, topology spread,
  bound volumes, delayed-binding volumes, and singleton and multi-zone DRA constraints;
- different allowlists within one batch, process restarts, ambiguous responses, cancellation, and
  NodeClaim deletion at each durable lifecycle state;
- direct-VM equality checks from backing VM raw zone through returned NodeClaim and registered Node,
  without requiring a Machine, plus persisted Machine raw-zone equality in Machine modes;
- drift, consolidation, replacement, deletion, rollback read/delete, and orphan cleanup.

A regression test must fail if `CloudProvider.Create` publishes a provider ID before exact observed
labels, if a selected zone lies outside `includeZones`, or if any provisioning path skips the
resolution barrier.

## Production readiness

The feature is ready to originate resources only after all of these conditions are met:

- direct VM and delegated Machine paths implement the same eligibility and selected-zone invariants;
- non-batch and strict header batch V2 pass unit, acceptance, integration, restart, and downgrade
  tests;
- NAP and self-hosted builds map the same proposed `AzureSelectedZonePlacement` gate, preserve its
  default-off behavior, and treat an omitted NodeClass policy as `Explicit`;
- all VM-based and Machine-based provisioning modes retain read, reconcile, adoption, and delete
  support across mixed-version upgrades;
- old readers can observe a concrete `Machine.zones` value and safely delete existing resources;
- policy toggles do not cause fleet-wide drift, and consolidation evaluates the concrete selected
  offering;
- rollback can immediately disable origination while leaving read/reconcile/delete and cleanup
  enabled;
- the rollout is staged: compatibility readers first, shadow eligibility next, test targets next,
  and opt-in origination only after correctness signals remain healthy.

`Tresolve`, capability-cache duration, and rollout thresholds require representative latency and
failure data before values are selected. This proposal intentionally defines the measurement and
failure semantics without guessing those values.

Telemetry is bounded and segmented by deployment and provisioning mode. Metrics include policy,
eligibility or fallback reason, capability result, header-batch schema, allowlist size bucket,
lifecycle state, placement-resolution latency, adoption and cleanup outcome, and selected-zone
contract failures. Customer-derived identifiers, full requirement sets, and raw launch payloads are
not metric dimensions.

Production success is measured by first-attempt allocation success, resolution latency, time to a
registered and independently verified Node, adoption rate, cleanup latency, and a zero selected-zone
contract-mismatch count. Zone-count balance is diagnostic only and is not a correctness target.

## FAQ

**Why not let Azure choose from every zone in the NodeClaim?**

A NodeClaim requirement is a set of permitted topology values, but it does not preserve correlations
with price, reservation, capacity type, disk support, availability, and other exact-offering facts.
Only the equivalent-offering intersection is safe for `includeZones`.

**Does `Any` balance nodes evenly?**

No. `Any` lets Azure Compute select one allowed zone at allocation time. Correctness is subset
membership and independent cross-object consistency, not even spread.

**Why does VM scale set `Auto` appear in the design?**

It documents the boundary. VM scale sets use `Auto`; this proposal concerns individual VMs, which
use `Any`. The implementation must not copy one policy into the other resource type.

**What happens for a hard single-zone volume or DRA constraint?**

The provider uses exact placement. A multi-zone constraint may narrow an otherwise eligible
allowlist but may not broaden it.

**Why block `CloudProvider.Create` until the zone is known?**

The provider must publish the provider ID and truthful concrete topology together. Waiting inside
Create preserves upstream Karpenter's existing immutable requirements and avoids a generic
unresolved-topology API.

**What happens when `Tresolve` expires?**

Create returns a recoverable, ownership-preserving result. Retry adopts the deterministic resource
using the complete launch fingerprint, or cleanup deletes it if the NodeClaim no longer owns it. A
new resource is not created merely because resolution exceeded the bound.

**Can the provider fall back to exact placement?**

Yes, but only before dispatch is proven to have occurred, such as an eligibility decision or a
strict target-capability rejection. Once dispatch may have occurred, adoption or cleanup protects
against duplicates.

**How is rollback immediate without losing compatibility?**

Origination and lifecycle support are separate controls. Disabling origination restores exact
placement for new launches while existing automatic resources remain readable, reconcilable,
adoptable, and deletable through their concrete observed zones.

**Does this behave the same in NAP and self-hosted deployments?**

The deployment mechanisms differ, but the eligibility, Azure request, selected-zone barrier,
fingerprint, topology, and lifecycle contracts are shared. Validation covers each deployment model
and every supported provisioning mode before that mode can originate Azure-selected placement.
