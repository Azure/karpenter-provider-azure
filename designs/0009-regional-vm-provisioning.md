# Regional (Non-Zonal) VM Provisioning Support

## Table of Contents

- [Summary](#summary)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Motivation](#motivation)
  - [Problem](#problem)
  - [Known Issues](#known-issues)
  - [Background: Azure Zonal vs Regional VMs](#background-azure-zonal-vs-regional-vms)
  - [Current State](#current-state)
- [Design Constraints](#design-constraints)
  - [Placement Envelope vs Allocation Strategy](#placement-envelope-vs-allocation-strategy)
  - [Zone as Placement Instruction and Topology Dimension](#zone-as-placement-instruction-and-topology-dimension)
  - [Backward Compatibility](#backward-compatibility)
- [Design Options](#design-options)
  - [Options Summary](#options-summary)
  - [Option A: Zone-Only Offering Model](#option-a-zone-only-offering-model)
  - [Option B: Explicit Placement Controls](#option-b-explicit-placement-controls)
  - [Option C: Provider-Owned Placement Policy](#option-c-provider-owned-placement-policy)
  - [Option D: Silent Fallback in the Provisioning Path](#option-d-silent-fallback-in-the-provisioning-path)
  - [Preference Pattern: Weighted NodePools](#preference-pattern-weighted-nodepools)
- [Recommendation](#recommendation)
  - [Option B Implementation Checklist](#option-b-implementation-checklist)
  - [Conceptual Code Change](#conceptual-code-change)
  - [Implementation Notes](#implementation-notes)
  - [Design Constraints on Future Strategy](#design-constraints-on-future-strategy)
  - [Validation Matrix](#validation-matrix)
  - [Rollout Guidance](#rollout-guidance)
- [Behavioral and Operational Considerations](#behavioral-and-operational-considerations)
  - [Schedulability](#schedulability)
  - [Observability](#observability)
  - [AKS Agent Pool Parity and Mapping](#aks-agent-pool-parity-and-mapping)
  - [Compatibility Modes and Regional-Only SKUs](#compatibility-modes-and-regional-only-skus)
  - [Storage and Zone Topology](#storage-and-zone-topology)
  - [Machine API and Provisioning Path Impact](#machine-api-and-provisioning-path-impact)
  - [Drift and Upgrade Implications](#drift-and-upgrade-implications)
- [Open Questions](#open-questions)
- [References](#references)
- [Appendix: Azure Zone and Placement Reference](#appendix-azure-zone-and-placement-reference)

## Summary

Support provisioning regional (non-zonal) VMs in Karpenter on Azure for zone-capable SKUs, and provide user-facing controls for regional placement. This enables fallback when zonal offerings are exhausted or restricted, and explicit regional or mixed-mode eligibility when the placement envelope permits it.

The recommended first version widens the default envelope so both zonal and regional offerings are eligible. It pairs that with launch-side selection of one concrete cheapest compatible offering, plus an internal price-first ranking that prefers zonal offerings on equal-price ties. This ranking is the **zonal-preferred tie-break**. The design also requires regional-aware unavailability handling so zonal failures do not accidentally suppress regional fallback.

> **TL;DR:** Add `karpenter.azure.com/zone-placement` as an offering label with values `zonal` and `regional` (Option B), widen the default envelope to include regional offerings for zone-capable SKUs, and apply zonal-preferred tie-breaking so the common case stays zonal-first. This is a behavioral compatibility change for existing unconstrained NodePools.

### Goals

- Enable provisioning of regional (non-zonal) VMs for zone-capable SKUs when all availability zones are exhausted, restricted, or otherwise unavailable
- Provide explicit user-facing controls for regional placement behavior for zone-capable SKUs, including regional-only and mixed-mode eligibility where appropriate
- Minimize unexpected behavior changes on upgrade; exact post-upgrade behavior may still change if the resulting placement remains predictable and acceptably close to today's zonal-first behavior
- Ensure regional offerings work correctly with the existing Machine API and VM provisioning paths
- Use zone `"0"` consistently to match AKS node labels set by cloud-provider-azure for non-zonal VMs

### Non-Goals

- VMSS-based placement — Karpenter creates standalone VMs only; VMSS fault domain spreading and related regional behaviors are out of scope
- Topology spread constraint automation — users who mix zonal and regional nodes are responsible for configuring topology spread constraints appropriately on their workloads
- Proactive zonal capacity or quota management — this design does not add quota-aware scheduling, capacity-aware zone choice, or other predictive handling of Azure zone-specific state; it continues to react to provisioning failures via the UnavailableOfferings cache
- Changing the zone label semantics for non-zonal VMs — the `topology.kubernetes.io/zone = "0"` value is determined by cloud-provider-azure, not Karpenter

## Motivation

### Problem

This design addresses two distinct problems:

1. **Zonal exhaustion with no regional fallback.** On Azure, a VM SKU that appears zonal (zones listed in the Resource SKUs API) can fail provisioning in every supported zone — due to quota exhaustion, capacity shortage, or subscription restrictions — yet succeed when provisioned without a zone (regional/non-zonal). Regional placement removes the zone constraint, giving Azure access to a larger pool of hardware — including racks not assigned to any availability zone — that zonal requests cannot reach. Today, Karpenter only generates **zonal offerings** for zone-capable SKUs. If all zones are marked unavailable (via the UnavailableOfferings cache), the SKU becomes entirely unprovisionable — even though Azure could place the VM regionally. This is distinct from zone *restrictions* in the Resource SKUs API: when all zones are restricted, skewer returns an empty zone set and the SKU falls through to a regional (`zone="0"`) offering. The gap is specifically for runtime capacity/quota failures where zonal offerings exist but are all marked unavailable.

2. **No user control over regional placement.** There is no mechanism for users to opt into regional placement for zone-capable SKUs — even when they explicitly prefer non-zonal VMs (e.g., for cost-insensitive batch workloads where availability matters more than zone pinning).

### Known Issues

- [Azure/karpenter-provider-azure#1002](https://github.com/Azure/karpenter-provider-azure/issues/1002) — Feature request: run VMs without redundancy options (no availability zone)
- [Azure/karpenter-provider-azure#1384](https://github.com/Azure/karpenter-provider-azure/issues/1384) — Non-zonal zone label uses `""` instead of `"0"`, breaking topology spread constraints (prerequisite fix, implemented in [PR #1615](https://github.com/Azure/karpenter-provider-azure/pull/1615))
- [Azure/AKS#5574](https://github.com/Azure/AKS/issues/5574) — NAP cannot assign regional (non-AZ) nodes. Users requesting `zone: 0` only get regional-only SKUs (e.g., `Standard_DS3`), because zone-capable SKUs don't generate regional offerings
- Reported KAITO request — a GPU SKU cannot currently be provisioned through Karpenter/NAP because zonal placement is blocked in the target environment even though regional placement remains viable

### Background: Azure Zonal vs Regional VMs

Azure VMs can be created **with a zone** (pinned to specific AZ hardware) or **without a zone** (regional — Azure chooses any available hardware in the region, including non-zonal racks). The regional capacity pool is a superset of zonal capacity. A SKU listing zones in the Resource SKUs API means it is zone-capable, not that capacity is guaranteed in those zones — and zone-scoped failures or restrictions do not imply regional restrictions.

Karpenter uses `"0"` as the zone value for non-zonal offerings because it must match the actual AKS node label: cloud-provider-azure reads the fault domain number from ARM (always `0` for non-zonal standalone VMs) and sets it as `topology.kubernetes.io/zone`. See [#1384](https://github.com/Azure/karpenter-provider-azure/issues/1384) for the prerequisite fix from `""` to `"0"`.

For detailed Azure zone mechanics, the Resource SKUs API, common failure modes, and zone label semantics, see [Appendix: Azure Zone and Placement Reference](#appendix-azure-zone-and-placement-reference).

### Current State

Today, for a zone-capable SKU (e.g., `Standard_NC24ads_A100_v4` in eastus with zones [1,2,3]), Karpenter generates only zonal offerings:

```
Offerings:
  (Standard_NC24ads_A100_v4, eastus-1, on-demand)
  (Standard_NC24ads_A100_v4, eastus-2, on-demand)
  (Standard_NC24ads_A100_v4, eastus-3, on-demand)
  (Standard_NC24ads_A100_v4, eastus-1, spot)
  (Standard_NC24ads_A100_v4, eastus-2, spot)
  (Standard_NC24ads_A100_v4, eastus-3, spot)
```

There is **no regional/non-zonal offering**. Zone `"0"` is only generated for SKUs where the SKU API returns empty zones (SKUs that don't support zones at all, or non-zonal regions).

The relevant code is in `pkg/providers/instancetype/instancetypes.go`:

```go
func (p *DefaultProvider) instanceTypeZones(sku *skewer.SKU) sets.Set[string] {
    skuZones := lo.Keys(sku.AvailabilityZones(p.region))
    if len(skuZones) > 0 {
        return sets.New(lo.Map(skuZones, func(zone string, _ int) string {
            return zones.MakeAKSLabelZoneFromARMZone(p.region, zone)
        })...)
    }
    return sets.New(zones.Regional)  // "0" — only for non-zonal SKUs
}
```

## Design Constraints

Three constraints shape the design space: the distinction between which offerings are eligible and which one wins, the dual role of zone values, and the backward compatibility impact of widening the placement envelope. Regional VMs also interact with zone-pinned storage; see [Storage and Zone Topology](#storage-and-zone-topology).

### Placement Envelope vs Allocation Strategy

This design separates two questions:

1. **Which offerings are eligible at all?** This is the **placement envelope** — defined by hard constraints such as `topology.kubernetes.io/zone`, `karpenter.azure.com/zone-placement`, or an AKSNodeClass placement policy.
2. **If multiple offerings are eligible, which one should win?** This is the **allocation strategy** — a ranking policy applied after the eligible set is determined.

The distinction matters because these look similar but represent different contracts:

- **Envelope-only zonal:** only zonal offerings are eligible
- **Envelope allowing any placement:** zonal and regional offerings are both eligible; the envelope itself does not prefer one over the other
- **Any-placement envelope plus ranking:** both are eligible, but zonal wins on equal-price ties. In Option B, this state corresponds to omitting the `zone-placement` requirement; in Option C, it becomes the explicit `Any` policy value.

An envelope that allows both is **not** the same as "zonal fallback to regional" — without additional ranking, regional could win over zonal (e.g., if it appears first in an arbitrary sort), so the envelope alone does not imply any preference order.

> **Zonal-preferred tie-break (definition):** The recommended internal default ranking for the first version. Within an any-placement envelope, keep normal price ordering but prefer a compatible zonal offering when zonal and regional offerings are price-equivalent; fall back to regional when the best available price tier has no compatible zonal offering.

Where different policy types belong:

- If only one placement mode is acceptable → express it in the **placement envelope** (`Zonal`, `Regional`, `zone NotIn [0]`, etc.)
- If multiple placement modes are acceptable and the question is which wins first → that is an **allocation strategy** problem
- **Weighted NodePools** are a coarse-grained, core-visible preference across separate envelopes; **allocation strategy** is fine-grained ranking within one envelope

This framework applies to Options **A**, **B**, and **C** below whenever the envelope can expose both zonal and regional offerings. It does not apply to **D**, because silent fallback is not represented in the offering model.

### Zone as Placement Instruction and Topology Dimension

The zone value participates in two independent mechanisms at different stages:

1. **Before VM creation — Karpenter's scheduling simulation** selects an offering, which includes a zone value (e.g., `eastus-1` or `"0"`). This zone determines where the VM will be placed: Karpenter translates it into the ARM `zones` property on the VM creation request. At this stage, the zone is a **placement instruction**.

2. **After VM creation — Kubernetes scheduling** uses the same zone value, now a label on the node (`topology.kubernetes.io/zone`), as a **topology dimension** for topology spread constraints, pod affinity, and other zone-aware scheduling decisions.

Regional VMs create a tension between these two roles: they have no real availability zone, but this provider path still needs a consistent zone label for post-launch topology semantics, and cloud-provider-azure sets that label to the fault domain number (always `"0"` for standalone VMs). This `"0"` value is semantically different from `eastus-1` — it represents a fault domain, not an availability zone, and provides no indication of where Azure physically placed the VM. Yet topology spread constraints treat it as just another zone domain.

### Backward Compatibility

Existing users expect:
- Zone-capable SKUs produce zonal nodes
- Topology spread on zone works correctly
- No surprise non-zonal nodes appearing

**User placement intent vs. current and target behavior:**

| User intent | Today's behavior | Target behavior | Compatibility impact |
|---|---|---|---|
| Implicitly zonal — no zone requirement, but expects zonal nodes | Zonal only (no regional offerings exist) | Zonal-first via tie-break; regional possible if all zonal offerings unavailable | Behavioral change — widened envelope, but common case unchanged |
| Explicitly zonal — `zone In [eastus-1, eastus-2, ...]` or equivalent | Zonal only in named zones | Unchanged — explicit zone constraints still exclude `0` | None |
| Open to regional fallback — would accept regional if zonal is exhausted | Zonal only; SKU becomes unprovisionable if all zones exhausted | Both eligible; zonal preferred, regional available as fallback | New capability — matches user intent better than today |
| Regional-only — explicitly wants non-zonal placement | Only possible for regional-only SKUs; zone-capable SKUs have no regional offering | `zone-placement In [regional]` or `zone In [0]` enables regional for all SKUs | New capability |

The key tension is the first row: users who never said "zonal only" but got it implicitly. The following compatibility spectrum addresses how to handle that case.

Most compatibility questions in this design are about the **placement envelope** for existing objects, not about ranking. If an upgrade widens the envelope from zonal-only to any-placement, then regional nodes become possible outcomes for launches, consolidation, and topology interactions. That is a real behavioral compatibility change, even if a zonal-preferred strategy keeps zonal as the common case. By contrast, changing ranking inside the same envelope is usually a smaller change: it affects which acceptable offering wins first, not which outcomes are allowed at all.

This gives a useful rule of thumb:

- If the question is **"can regional ever happen after upgrade?"**, that is an **envelope** question.
- If the question is **"when zonal and regional are both allowed, which should win first?"**, that is a **ranking** question.

Full preservation of existing behavior is one option, but not the only acceptable one. A smaller behavioral compatibility change may still be acceptable if it is predictable and still leans toward today's zonal-first intent.

The resulting compatibility spectrum:

- **Strict preservation:** existing objects keep an effectively zonal-only envelope unless users opt in to something broader
- **Softened preservation:** existing objects may include regional placement in the envelope, but the common case stays close to today's zonal-first behavior through internal ranking or similar safeguards
- **Explicit behavioral change:** omission or lack of requirements means any placement is eligible, with no implied zonal-only behavior beyond whatever ranking the implementation applies

The choice between those cases is a product decision. Strict preservation requires a provider-owned compatibility envelope (e.g., AKSNodeClass-owned filtering). Softened preservation makes options that widen eligibility but prefer zonal placement in common cases more viable. The working recommendation is to accept softened preservation (see [Recommendation](#recommendation)).

## Design Options

### Options Summary

The options below compare different ways to define the placement envelope. Throughout this section, "behavioral compatibility change" means a change in observable placement behavior, not an API/schema change. Within **Option B**, the remaining sub-choice is the placement vocabulary: two-valued (`zonal`, `regional`) or three-valued (adding `regional-only`). **Weighted NodePools** are a preference pattern that layers on top of **A**, **B**, or **C** (not **D**); see [Preference Pattern: Weighted NodePools](#preference-pattern-weighted-nodepools).

| Option | Approach | Backward compatible? | Default semantics | User control | Key tradeoff |
|---|---|---|---|---|---|
| **A** | Zone is the only placement control surface | No | Unconstrained NodePool = zonal and regional eligible | `zone` requirements only | Smallest implementation, but overloads topology with placement intent |
| **B** | Placement mode is modeled explicitly via `zone-placement` | No, though a price-first default that prefers zonal on equal-price ties softens the common-case impact | No `zone-placement` requirement = any placement eligible; recommended first-version behavior is price-first with zonal preference on equal-price ties | `zone-placement` plus `zone` requirements | Primary recommendation; clean explicit model, but unconstrained existing NodePools widen after upgrade |
| **C** | AKSNodeClass owns the placement envelope before scheduling | Yes, if omission or the defaulted policy preserves current behavior | Compatibility envelope when needed; otherwise a provider-owned default such as `Any` is separate | Optional AKSNodeClass policy plus `zone` requirements | Strongest alternative for stricter compatibility or provider-owned defaults, but adds a second control surface and defaulting complexity |
| **D** | Regional retry happens outside the offering model | At the API surface only | Automatic fallback after zonal failure | None | Smallest user-facing change, but hides placement changes from the scheduler and user |

Under the working recommendation, prefer **B** with the two-value variant; weighted NodePools remain an optional explicit preference pattern on top. Prefer **C** only if a provider-owned default envelope or stricter compatibility path is needed.

### Option A: Zone-Only Offering Model

Add a regional offering for zone-capable SKUs by inserting zone `"0"` alongside the existing zonal offerings, and rely entirely on `topology.kubernetes.io/zone` for selection.

For `Standard_NC24ads_A100_v4` in eastus:

```
Offerings:
  (Standard_NC24ads_A100_v4, eastus-1, on-demand)   ← zonal
  (Standard_NC24ads_A100_v4, eastus-2, on-demand)   ← zonal
  (Standard_NC24ads_A100_v4, eastus-3, on-demand)   ← zonal
  (Standard_NC24ads_A100_v4, 0, on-demand)           ← regional (NEW)
  ... × spot
```

**User controls via `topology.kubernetes.io/zone` requirement:**

| Intent | NodePool requirement |
|---|---|
| Only zonal | `zone NotIn [0]` or `zone In [eastus-1, eastus-2, eastus-3]` |
| Only regional | `zone In [0]` |
| Specific zonal zones | `zone In [eastus-1, eastus-2]` |
| Any | No zone requirement |

This is the smallest code change and composes naturally with existing exact-zone requirements. Like **Option B**, it widens the default envelope for unconstrained NodePools. The main drawback is semantic overload: zone becomes both topology and placement mode, making abstract intent such as "zonal anywhere in the region" less portable across clusters or regions.

**Verdict:** technically simple but weaker than **Option B** due to semantic overload of the zone label.

### Option B: Explicit Placement Controls

Introduce an Azure-specific offering label such as `karpenter.azure.com/zone-placement`, and treat placement mode as an orthogonal dimension from zone topology.

Example offerings:

```text
Offerings:
  (NC24ads, eastus-1, on-demand, zone-placement=zonal)
  (NC24ads, eastus-2, on-demand, zone-placement=zonal)
  (NC24ads, eastus-3, on-demand, zone-placement=zonal)
  (NC24ads, 0,        on-demand, zone-placement=regional)
```

The critical design rule is:

> If placement is controlled only through NodePool requirements, then **absence of a `zone-placement` requirement means any placement is eligible**, not "default zonal".

The recommended first version pairs that any-placement default with launch-side selection of a concrete compatible offering from the sorted set, plus an internal price-first ranking that prefers zonal offerings on equal-price ties, so the wider envelope still behaves zonal-first in the common case. Changing offering sort order alone is not sufficient.

This model composes cleanly with normal zone requirements because `zone-placement` filters the placement envelope, while `topology.kubernetes.io/zone` continues to select concrete topology domains within that envelope.

Users can still control placement via `zone` alone when the zone value already identifies the intended mode (e.g., `zone In [0]` effectively means regional only). However, `zone-placement` expresses abstract intent like "zonal anywhere in the region" more cleanly and portably across clusters and regions.

For brevity, the examples below use `zone-placement` and `zone` as shorthand for `karpenter.azure.com/zone-placement` and `topology.kubernetes.io/zone`.

**User controls:**

| Intent | Preferred requirement | Zone-only alternative | Note |
|---|---|---|---|
| Only zonal | `zone-placement In [zonal]` | `zone NotIn [0]`, or enumerate all concrete zonal values | `NotIn [0]` is preferred over enumerating values for portability across regions |
| Only regional | `zone-placement In [regional]` | `zone In [0]` | Clean zone-only form because `0` already identifies regional placement |
| Only regional, explicit zone | `zone-placement In [regional]` + `zone In [0]` | `zone In [0]` | Equivalent in practice; keeping both can make intent more obvious |
| Specific zonal zones | `zone-placement In [zonal]` + `zone In [eastus-1, eastus-2]` | `zone In [eastus-1, eastus-2]` | Natural when the user really wants named zones |
| Any | No `zone-placement` requirement, or `zone-placement In [zonal, regional]` | No `zone` requirement | A zone-only include list would require naming all currently eligible zones |

**Pros:**
- Clean separation of concerns — zone topology stays pure, placement mode is an orthogonal dimension
- Composes naturally with exact zone requirements
- Lets users say `zonal only`, `regional only`, or `any` without overloading zone semantics
- Makes NodePool intent more portable across clusters and regions by avoiding the need to name or enumerate concrete zones for common placement modes
- Fits Karpenter's existing offering and requirement model

**Cons:**
- If this is the only control surface, it is **not strictly backward compatible**: no `zone-placement` requirement means regional is eligible. The zonal-preferred tie-break softens the common-case impact, but the widened envelope is a real behavioral compatibility change
- A single NodePool still cannot express user-controlled ordered preference between zonal and regional; it can only inherit the provider default unless a future first-class strategy exists
- If the label should appear on launched nodes, explicit propagation of the selected offering value is needed

**Verdict:** the recommended primary model when an explicit, requirement-based experience is desired and some behavioral widening is acceptable.

#### Placement Vocabulary

Two variants for the `zone-placement` label values:

- **Two-value model (recommended):** `zonal` and `regional`. All regional offerings, including regional-only SKUs, are tagged `regional`. This is the simplest design and the recommended form for the first version.

- **Three-value model (optional):** Add `regional-only` to distinguish inherently regional SKUs from zone-capable SKUs placed regionally. Not required for correctness — only useful if the design wants finer-grained observability or future policy controls.

### Option C: Provider-Owned Placement Policy

Add an optional field on AKSNodeClass that filters offerings before they are exposed to scheduling, for example:

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: default
spec:
  zonePlacementPolicy: Zonal | Regional | Any
```

Under this model, AKSNodeClass owns the placement envelope and NodePool requirements only narrow further. Avoid `Default` as a policy value; use `Compatible` if a compatibility-preserving mode is needed.

If backward compatibility is required, omission should preserve current behavior (zone-capable SKUs zonal-only, regional-only SKUs remain eligible). This is the only clean way to make `no NodePool requirement` mean `preserve today's behavior`.

This model is also useful even without strict compatibility — it gives the provider a central control point for default placement envelopes, and lets managed products (NAP, AKS Automatic) share one AKSNodeClass-level policy across multiple NodePools.

Composes with zone requirements: `Zonal` + `zone In [eastus-1]` works; `Zonal` + `zone In [0]` yields no offerings.

**Pros:**
- Supports a real compatibility-preserving envelope when required
- Also supports provider-owned defaults and managed-product behavior when strict preservation is relaxed
- Keeps exact zone selection orthogonal and intact
- Makes the enforcement point explicit: the provider filters offerings before scheduling

**Cons:**
- Adds a second control surface
- If `Compatible` behavior is retained, it can codify current behavior even where current behavior is not desirable
- If omission means `Compatible`, newly created AKSNodeClasses in that API version inherit that same older envelope unless they set an explicit policy
- If compatibility is not a strong requirement, the extra control surface may not justify itself versus **Option B**
- Less convenient when multiple NodePools sharing one AKSNodeClass want different placement envelopes
- If NodePool-level overrides are later added, precedence rules must be defined carefully

**Verdict:** the strongest alternative when the design wants either a strict compatibility envelope or provider-owned defaults. If neither matters, **Option B** is cleaner.

#### Default Policy

What should an omitted `zonePlacementPolicy` mean? Assuming omission has one meaning per API version (no legacy/new split or per-object latching):

| Omitted-field choice | When it fits | Tradeoff |
|---|---|---|
| `Any` + zonal-preferred tie-break | Widening is acceptable; forward-looking default | Widens envelope for all AKSNodeClasses, but common case stays zonal-first |
| `Compatible` | Strict compatibility must remain the default | Preserves today's behavior, but also makes it the steady-state default for new AKSNodeClasses |
| Required (no default) | API should avoid silent defaults | Most explicit, but heavier UX burden |

The simplest **Option C** default is omission = `Any` with the zonal-preferred tie-break. If the project rejects that widening, the alternatives are `Compatible` for everyone in the version or requiring the field explicitly.

### Option D: Silent Fallback in the Provisioning Path

Do not model regional as an offering. Instead, when all zonal attempts fail for a SKU, retry without a zone as a last resort. This keeps the scheduler-visible envelope zonal-only and hides the regional choice from scheduling, which conflicts with the scheduler-decides model.

**Pros:**
- Minimal user reconfiguration
- Backward compatible at the API surface

**Cons:**
- No explicit user control
- Changes actual placement without the scheduler modeling that choice
- Makes topology behavior harder to reason about and debug
- Still results in nodes labeled `zone="0"`, so topology consequences are not avoided; they are only hidden from scheduling

**Verdict:** not recommended. It violates the scheduler-decides model and creates hidden behavior.

### Preference Pattern: Weighted NodePools

Weighted NodePools are a scheduling pattern — not a base option — that layers on top of any design where the scheduler can distinguish separate eligible envelopes. Unlike the zonal-preferred tie-break (implicit ranking within one envelope), weighted NodePools express an explicit, core-visible preference across separate NodePool templates.

Applies to Options **A**, **B**, and **C** (using separate NodePools with distinct placement constraints). Does not apply to **D** (fallback is hidden from the offering model).

Example under **Option B**:

```yaml
# NodePool 1: prefer zonal (weight: 100)
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: zonal
spec:
  weight: 100
  template:
    spec:
      requirements:
        - key: karpenter.azure.com/zone-placement
          operator: In
          values: [zonal]
---
# NodePool 2: fallback to regional (weight: 1)
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: regional-fallback
spec:
  weight: 1
  template:
    spec:
      requirements:
        - key: karpenter.azure.com/zone-placement
          operator: In
          values: [regional]
```

**Verdict:** recommended when users need explicit or stronger ordered preference than the provider default.

## Recommendation

We recommend **Option B**: add `karpenter.azure.com/zone-placement` as an offering label, widen the default envelope to include regional, and apply the zonal-preferred tie-break. This accepts a behavioral compatibility change for existing unconstrained NodePools — regional nodes become possible outcomes — but the tie-break keeps zonal placement as the common case. For the full rationale, see [Option B](#option-b-explicit-placement-controls). For behavioral implications such as storage interactions, consolidation, and topology spread, see [Behavioral and Operational Considerations](#behavioral-and-operational-considerations).

If the project requires a provider-owned compatibility envelope or provider-owned defaults, use **Option C** instead; see [Option C](#option-c-provider-owned-placement-policy).

### Option B Implementation Checklist

1. **Add `karpenter.azure.com/zone-placement`** with values `zonal` and `regional`
2. **Generate regional offerings for zone-capable SKUs** with `zone="0"` for capacity types that support regional placement
3. **Tag all regional offerings**, including regional-only SKUs, as `regional`
4. **Do not infer a default** from the absence of a NodePool requirement; no `zone-placement` requirement means any placement is eligible (i.e., equivalent to `zone-placement In [zonal, regional]`)
5. **Apply the zonal-preferred tie-break** within the any-placement envelope via the provider's allocation strategy (see `allocationstrategy` package). The implementation should stop deriving capacity type from the cheapest offering and then choosing an arbitrary zone; instead it should consume one concrete offering chosen from an already ordered candidate set. Whether the final `pick first` step remains in the launch helper or moves fully into `allocationstrategy` can be finalized later
6. **Make error handling and `UnavailableOfferings` marking regional-aware**, so zonal-scoped failures do not automatically mark the regional fallback unavailable

### Conceptual Code Change

#### Path 1: NodePool-Only Explicit Model

In `instanceTypeZones`:

```go
func (p *DefaultProvider) instanceTypeZones(sku *skewer.SKU) sets.Set[string] {
    skuZones := lo.Keys(sku.AvailabilityZones(p.region))
    if len(skuZones) > 0 {
        zoneSet := sets.New(lo.Map(skuZones, func(zone string, _ int) string {
            return zones.MakeAKSLabelZoneFromARMZone(p.region, zone)
        })...)
        // Also add a regional offering candidate for zone-capable SKUs.
        // The capacity-type loop in createOfferings can still skip
        // unsupported combinations such as regional spot.
        zoneSet.Insert(zones.Regional)
        return zoneSet
    }
    return sets.New(zones.Regional)
}
```

In `createOfferings`, attach the `zone-placement` label and skip regional offerings for unsupported capacity types if needed:

```go
zonePlacement := "zonal"
if zone == zones.Regional {
    zonePlacement = "regional"
}
offering := &cloudprovider.Offering{
    Requirements: scheduling.NewRequirements(
        scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType),
        scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zone),
        scheduling.NewRequirement("karpenter.azure.com/zone-placement", corev1.NodeSelectorOpIn, zonePlacement),
    ),
    Price:     price,
    Available: available,
}
```

In `computeRequirements`, derive the instance type's `zone-placement` requirement from available offerings so NodePool requirements can filter on it. Absence of a NodePool requirement does **not** imply zonal-only behavior.

In launch selection, derive both capacity type and zone from one concrete offering rather than from the cheapest capacity type plus an arbitrary zone within that capacity type. The key requirement is that selection consume an already ordered concrete-offering view. That can be implemented either by having `allocationstrategy` return ordered candidates and letting the launch helper pick the first one, or by letting `allocationstrategy` own the final pick as well. A conservative sketch of the first shape is:

```go
func PickInstanceTypeAndOffering(
  ctx context.Context,
  instanceOfferings []allocationstrategy.InstanceOffering,
) (*cloudprovider.InstanceType, *cloudprovider.Offering) {
  if len(instanceOfferings) == 0 {
    return nil, nil
  }
  best := instanceOfferings[0]
  if len(best.Offerings) == 0 {
    return nil, nil
  }
  // After compatibility filtering and allocation-strategy ordering,
  // consume the first concrete offering rather than re-deriving
  // capacity type and then picking an arbitrary zone.
  return best.InstanceType, best.Offerings[0]
}
```

The example above keeps the final `pick first` step in the launch helper only to show the data flow more clearly. The actual ownership boundary can remain open. In either shape, the caller derives `capacityType` and `zone` from one concrete offering rather than from `Offerings.Cheapest()` plus arbitrary same-priority zone selection. The exact equal-price precedence between `spot` vs `on-demand` and `zonal` vs `regional` can remain conservative and explicit in the first version.

In unavailability handling, keep the first version conservative by distinguishing zonal-scoped from regional-scoped failures instead of automatically marking every offering on the instance type unavailable:

```go
switch errorScope {
case ZonalOnly:
  markAttemptedZonalOfferingUnavailable(...)
case RegionalOnly:
  markRegionalOfferingUnavailable(...)
case RegionWideForCapacityType:
  markZonalAndRegionalOfferingsUnavailable(...)
}
```

The exact mapping from Azure error families to these scopes can be finalized later, but handlers that currently iterate all offerings for an instance type or capacity type need review before regional offerings are added.

### Implementation Notes

- **Dependency: zone `"0"` normalization first.** This design depends on [PR #1615](https://github.com/Azure/karpenter-provider-azure/pull/1615) (fixing [#1384](https://github.com/Azure/karpenter-provider-azure/issues/1384)), which establishes `"0"` as the canonical non-zonal zone value.
- **WellKnownLabels registration:** add `zone-placement` to `AzureWellKnownLabels` only if it is exposed as a schedulable requirement on offerings.
- **CRD validation allowlists:** if `zone-placement` is exposed as a NodePool-facing requirement or label, update the generated NodePool CRD allowlist for `karpenter.azure.com` keys at the same time.
- **`computeRequirements` update:** required only for designs that let NodePools filter on `zone-placement`.
- **Selected-offering label propagation:** if `zone-placement` should appear on NodeClaims or Nodes, explicit propagation of the selected offering value is needed. Adding the label to offerings and multi-valued instance type requirements is not sufficient on its own.
- **Empty-envelope diagnostics:** contradictory combinations that narrow the placement envelope to nothing, such as a zonal-only policy plus `zone In [0]`, should not fail silently. Prefer admission-time validation when practical; otherwise surface explicit status or event diagnostics rather than only returning "no instance types available."
- **Launch-side offering selection must change if zonal preference is desired:** existing code derives capacity type from the cheapest offering and then may choose an arbitrary zone among offerings with that capacity type. A zonal-preferred equal-price policy therefore requires consuming one concrete compatible offering after allocation-strategy ordering; changing sort order alone is insufficient. Whether `allocationstrategy` also owns the final pick can remain an implementation detail.
- **Regional-aware unavailability handling:** handlers that currently mark all offerings for an instance type or capacity type unavailable must distinguish zonal-scoped from regional-scoped failures once `zone="0"` offerings exist. Zone `"0"` works as a cache key, but the scope of which offerings get marked unavailable must change.

### Design Constraints on Future Strategy

- **No natural zonal preference in Azure pricing.** Any zonal-first default is an implementation choice, not a price signal from Azure. Weighted NodePools can express preference today; a future `allocationStrategy` could express configurable ranking.
- **Consolidation and allocation strategy interaction.** Equal-price tie-breaking between zonal and regional is safe because it does not create a cost-model mismatch with core. However, a future ranking strategy that prefers different-price or non-price outcomes without reflecting that in offering requirements or price would not automatically influence consolidation decisions. This constrains future `allocationStrategy` design.

### Validation Matrix

Keep the first validation pass intentionally small and behavior-oriented:

| Area | Minimal validation |
|---|---|
| Offering generation | Verify zone-capable SKUs expose zonal plus regional offerings, regional-only SKUs remain regional, and zonal-only constraints still exclude `0` |
| Launch selection | Verify the launch path picks one concrete offering deterministically; under equal-price mixed envelopes, zonal wins when a compatible zonal offering survives filtering; regional wins when zonal offerings are filtered or unavailable |
| Unavailability handling | Verify zonal-scoped failures block only the intended zonal offering(s); explicitly regional or region-wide failures block regional offerings only when appropriate |
| Storage and topology | Verify launched regional nodes label as `zone="0"`; LRS PVCs bound to concrete zonal labels do not attach to regional nodes; only `0`-bound legacy cases are treated as plausible standalone-VM migration candidates |
| Upgrade and consolidation | Verify enabling the feature does not drift existing nodes immediately; mixed envelopes may allow cross-mode replacements, while zonal-only or regional-only envelopes do not |

The exact suite split can be decided later. A conservative first pass can rely on unit coverage for offering generation, launch selection, and unavailability handling, plus one focused integration path for zonal failure to regional fallback.

### Rollout Guidance

- **Self-hosted or existing clusters:** if omission means any placement is eligible, users who want to preserve today's zonal-only behavior should add an explicit zonal-only constraint before or during upgrade, using either `zone-placement In [zonal]` or the zone-only equivalent if that is the final API shape.
- **Managed defaults:** managed products may stamp zonal-only defaults onto managed default NodePools or equivalent provider-owned envelopes, but that should be documented as a platform policy rather than implied as a provider-wide default.
- **Conflicting controls:** if NodeClass policy and NodePool requirements narrow to an empty envelope, prefer validation or explicit status over leaving operators to infer the problem from a generic scheduling failure.

## Behavioral and Operational Considerations

### Schedulability

This design uses two different kinds of scheduling labels, and they should not be treated as interchangeable.

| Label | Present on offerings? | Present on launched nodes? | Workload-schedulable? |
|---|---|---|---|
| `topology.kubernetes.io/zone` | Yes | Yes | Yes |
| `karpenter.azure.com/zone-placement` | Yes | Yes — propagated from the selected offering | Yes, once propagated |

- **`topology.kubernetes.io/zone` is fully workload-facing.** Pods may reference it in `nodeSelector`, `nodeAffinity`, and `topologySpreadConstraints`. For regional nodes, the value is `"0"`.
- **`karpenter.azure.com/zone-placement` is propagated to nodes** from the selected offering. NodePools use it to control placement; workloads can also reference it in `nodeSelector` or `nodeAffinity` if needed.

Zone `"0"` is not new to AKS — non-zonal agent pools already produce nodes with this value. Regional Karpenter nodes join an existing topology label space. What changes is the frequency of `"0"` nodes, especially for topology spread. See [Appendix: Azure Zone and Placement Reference](#appendix-azure-zone-and-placement-reference).

### Observability

- **Propagate `zone-placement` to launched nodes.** The selected offering's `zone-placement` value (`zonal` or `regional`) should be set as a label on the node, so operators can inspect placement outcome directly via `kubectl get nodes --show-labels`.
- **Minimum useful signal:** operators should be able to tell whether a launched node was effectively zonal or regional.
- **Useful additional signal when practical:** distinguish between explicitly requested regional placement and regional placement reached because compatible zonal offerings were unavailable.

### AKS Agent Pool Parity and Mapping

AKS agent pools support two placement modes — **zonal** (zone-spanning or zone-aligned) and **regional** (no zone assignment). Karpenter and NAP do not need a strict 1:1 resource mapping, but should be able to represent the same placement modes.

| AKS agent-pool shape | Placement semantics | Possible Karpenter / NAP mapping |
|---|---|---|
| Zone-spanning | Zonal, across selected zones | NodePool with zonal placement + `zone In [...]` |
| Zone-aligned | Zonal, pinned to one zone | One or more NodePools with explicit zone constraints |
| Regional | No zone assignment | NodePool with regional placement, or `zone In [0]` |

Key implications:

- Regional placement for zone-capable SKUs should be an explicit capability, not a hidden retry — AKS already exposes this as a deliberate choice
- The placement vocabulary should stay simple (`zonal` vs `regional`) — AKS does not distinguish `regional-only` from `regional-by-choice`
- Exact 1:1 pool mapping is unnecessary; Karpenter need not create one pool per zone just because AKS often does
- NAP and AKS Automatic can intentionally mimic agent-pool defaults on managed NodePools

#### Azure-Selected Zone (`zone=any`)

Azure Compute now has a preview capability for zonal VMs where Azure selects the zone. This is conceptually a third placement mode (explicit zonal, Azure-selected zonal, regional). Karpenter already covers much of the user intent — an unconstrained multi-zone NodePool lets the provider pick any compatible zone — but the implementation model differs, since Karpenter commits to a concrete zone before VM creation while Azure-selected zone defers that choice.

This design does not need a `zonal-any` value. If Azure-selected zone parity becomes important, it should be a follow-on enhancement with separate scheduler and API analysis.

### Compatibility Modes and Regional-Only SKUs

Regional-only SKUs remain functional under all options without special handling. Under **Option A**, they appear as `zone="0"` offerings. Under **Option B**, they are tagged `zone-placement=regional`. Under **Option C**, the AKSNodeClass policy controls eligibility. A distinct `regional-only` value is not required for correctness; it only adds extra policy or observability semantics.

Managed environments can stamp placement constraints onto managed default NodePools (e.g., `zone-placement In [zonal]`). This helps the backward-compatibility story for managed pools on new clusters, but does not create a provider-wide default — user-created NodePools and self-hosted deployments follow the generic design semantics.

### Storage and Zone Topology

Azure Disk CSI driver uses `topology.kubernetes.io/zone` for volume topology-aware scheduling. This creates important interactions with regional VMs:

- **LRS (Locally Redundant Storage) disks** are zone-pinned. An LRS disk created in `eastus-1` can only be attached to a VM in `eastus-1`. A pod with an LRS PVC bound to `eastus-1` **cannot** be scheduled on a regional node (`zone="0"`) — the zone labels don't match.
- **ZRS (Zone-Redundant Storage) disks** are replicated across zones and can be attached to VMs in any zone, including regional nodes. No topology constraint.
- **LRS disks on regional nodes**: An LRS PVC created on a regional node is bound to `zone="0"`. It can only be re-attached to another `zone="0"` node. This effectively locks the workload to regional placement.

**Legacy cluster migration (see [Azure/AKS#5574](https://github.com/Azure/AKS/issues/5574)):** Clusters with non-zonal node pools that use LRS storage may have PVCs bound to FD-based zone labels (e.g., `"0"`, `"1"`, `"2"`). Regional standalone-VM-backed Karpenter nodes clearly help only the `"0"` case, because those nodes always label as `"0"`; PVCs bound to `"1"` or `"2"` still do not match standalone VMs. The exact behavior of the Azure Disk CSI driver with FD-based zone values still needs verification, so this should be treated as a narrow potential migration aid rather than a general legacy-storage migration story.

### Machine API and Provisioning Path Impact

The existing Machine API template-building and ARM translation paths mostly require no changes once a concrete offering has already been selected. The main additional work is in offering generation, launch selection, and regional-aware unavailability handling. The zone string itself still flows through the system transparently:

| Layer | Change needed? | Details |
|---|---|---|
| Instance type provider (`instanceTypeZones`) | **Yes** | Add `"0"` to zones for zone-capable SKUs |
| Offering creation (`createOfferings`) | **Yes** | Tag each offering as `zonal` or `regional`; add regional offerings |
| Launch-side offering selection | **Yes** | Select one concrete compatible offering instead of cheapest-capacity-type + arbitrary zone |
| Machine API template builder | No | Already handles `zone="0"` → empty ARM zones via `MakeARMZonesFromAKSLabelZone` |
| Machine API creation | No | Passes the template through |
| Machine API zone readback | No | Already maps zoneless machines to `"0"` via `GetAKSLabelZoneFromAKSMachine` |
| VM API provisioning path | No | Same `MakeARMZonesFromAKSLabelZone` translation |
| Error handling / UnavailableOfferings cache | **Yes** | Handlers must distinguish zonal-scoped from regional-scoped failures |
| Machine reuse path | No | Already handles empty ARM zones → `RegionalZone` (`"0"`) via `MakeAKSLabelZoneFromARMZones` |

The key translation point is `MakeARMZonesFromAKSLabelZone(zone)`, which maps `"0"` → empty ARM zones slice (note: it does **not** handle `""` — only `RegionalZone`/`"0"` is recognized). This means a regional offering with `zone="0"` will correctly produce a VM/Machine creation request with no zone specified.

### Drift and Upgrade Implications

**No option causes automatic drift on upgrade.** Introducing new offerings or labels does not force existing nodes to drift. However, options that widen the placement envelope (A, B) may change future launch and consolidation behavior for unconstrained NodePools.

**User-initiated drift** occurs when requirements are narrowed after upgrade — for example, adding `zone NotIn [0]` would drift existing regional nodes. This is intentional behavior.

**Node label projection (Option B):** If `zone-placement` should appear on NodeClaims or Nodes, explicit propagation of the selected offering value is needed. Pre-existing nodes would lack the label unless rotated or backfilled.

**Consolidation:** When the envelope allows both zonal and regional offerings, consolidation may propose cross-mode replacements. Equal-price tie-breaking does not create a cost-model mismatch with core, but a ranking strategy with different-price preferences would.

| Option | Drift on upgrade? | Drift from user action? | Mixed label state? | Consolidation risk? |
|---|---|---|---|---|
| **A** | No | Yes — `zone NotIn [0]` drifts regional nodes | No | Yes — may replace zonal with regional |
| **B** | No | Depends on control surface and label projection | Depends on whether `zone-placement` is projected | Depends on which modes the envelope allows |
| **C** | No | Yes — if the AKSNodeClass policy changes | No by default | Depends on which envelopes the policy exposes |
| **D** | No | N/A | No | No |

## Open Questions

1. **[Blocking] Compatibility default:** Should omitted placement mean `Any` (with zonal-preferred tie-break), `Compatible`, or be required explicitly?

2. **[Informational] Topology-spread warnings:** Is documentation enough, or should Karpenter emit a warning event when a configuration allows both zonal and regional offerings with `topologySpreadConstraints` on zone?

3. **[Informational] Legacy LRS migration:** Do we need to verify Azure Disk CSI behavior for FD-based zone labels before calling legacy non-zonal LRS migration a supported scenario?

4. **[Deferred] NodePool overrides with AKSNodeClass policy:** If both control surfaces exist, should NodePool requirements only narrow from the AKSNodeClass envelope?

5. **[Blocking] First-version offering ranking:** Given the launch-side "pick one concrete compatible offering" approach, what exact equal-price precedence should the provider use when multiple compatible offerings remain, especially for `spot` vs `on-demand` and zonal vs regional candidates within the same effective price tier?

6. **[Blocking] First-version error scoping:** Which Azure error families should be treated as zonal-only, regional-only, or region-wide for a given capacity type? Conservative default: do not block regional on a zonal-scoped failure.

7. **[Deferred] Azure-selected zone parity:** Is Karpenter's existing "choose one explicit eligible zone" behavior sufficient, or do we need API-level parity with Azure-selected zone?

8. **[Blocking] Spot support for regional VMs:** Does Azure support spot pricing for regional placement? Should `createOfferings` skip spot+regional offerings if not?

## References

- [Azure Availability Zones Overview](https://learn.microsoft.com/en-us/azure/reliability/availability-zones-overview)
- [What is Azure Kubernetes Service (AKS) Automatic?](https://learn.microsoft.com/en-us/azure/aks/intro-aks-automatic)
- [VM Availability Options](https://learn.microsoft.com/en-us/azure/virtual-machines/availability)
- [Allocation Failure Troubleshooting](https://learn.microsoft.com/en-us/troubleshoot/azure/virtual-machines/windows/allocation-failure)
- [Resolve SKU Restriction Errors](https://learn.microsoft.com/en-us/azure/azure-resource-manager/troubleshooting/error-sku-not-available)
- [Resource SKUs REST API](https://learn.microsoft.com/en-us/rest/api/compute/resource-skus/list)
- [Issue #1384: Non-zonal zone label fix](https://github.com/Azure/karpenter-provider-azure/issues/1384)
- [PR #1615: fix: use zone "0" for regional VMs to match AKS node labels](https://github.com/Azure/karpenter-provider-azure/pull/1615) (implements the prerequisite fix)

## Appendix: Azure Zone and Placement Reference

> This section is reference material. The key points are summarized in [Background: Azure Zonal vs Regional VMs](#background-azure-zonal-vs-regional-vms).

### How Azure Zones Work

When you create a VM **with a zone specified**, Azure must place it on hardware **physically located in that specific zone**. When you create a VM **without a zone** (regional/non-zonal), Azure can place it on **any available hardware across the entire region**, including hardware that isn't mapped to any availability zone.

Azure regions often have older or overflow racks that aren't assigned to a zone. The compute resource pool for regional placement is a **superset** that includes non-zonal hardware plus potentially zonal hardware (depending on the error type).

This is different from AWS, where every EC2 instance is always placed in an availability zone (via subnet mapping). Azure's regional tier exists because availability zones were added later to an existing regional infrastructure.

### Resource SKUs API

The Resource SKUs API (`GET /subscriptions/{sub}/providers/Microsoft.Compute/skus`) reports zone capability via `LocationInfo.Zones`:

| `zones` field | `restrictions` | Zonal deploy? | Regional deploy? |
|---|---|---|---|
| `["1","2","3"]` | none | Yes | Yes |
| `[]` | none | No | Yes |
| `["1","2","3"]` | all zones restricted | No (blocked) | **Yes (may work!)** |
| `["1","2","3"]` | zones `["1"]` restricted | Yes (zones 2,3) | Yes |
| any | location restricted | No | No |

Key insight: **A SKU listing zones means it is zone-capable, not that capacity is guaranteed in those zones.** And zone-scoped restrictions do not imply regional restrictions — the restriction types are orthogonal.

### Common Failure Modes

- **`SubscriptionQuotaReached`** — Subscription vCPU quota exhausted for that SKU family in a specific zone. Zonal quotas can differ from regional quotas.
- **`SkuNotAvailable` / `NotAvailableForSubscription`** — SKU restricted for the subscription in that zone (policy, capacity reservation, or enrollment restriction). Can be zone-scoped while the SKU remains available regionally.
- **`ZonalAllocationFailed`** — Insufficient capacity in the requested zone.
- **`OverconstrainedZonalAllocationRequest`** — Zone cannot accommodate the selected size and capacity combination.

All of these can be **zone-specific** and do not necessarily apply to non-zonal (regional) placement.

### Zonal vs Regional Comparison

| Aspect | Zonal VM | Regional (Non-Zonal) VM |
|---|---|---|
| Placement | Pinned to a specific AZ | Azure chooses any rack in the region |
| `topology.kubernetes.io/zone` label (AKS) | `<region>-<zone>` (e.g., `eastus-1`) | `<fault-domain>` (always `0` for standalone VMs) |
| SLA | Higher (99.99% with 2+ zones) | Lower (99.95% single VM with premium disks) |
| Capacity pool | Only hardware in that zone | All hardware in the region (zonal + non-zonal) |
| ARM `zones` property | `["1"]`, `["2"]`, etc. | `null` or empty |
| Quota enforcement | Per-zone quotas may apply | Regional quota only |

### The Zone Label, `"0"`, and Topology Spread

The `topology.kubernetes.io/zone` label on AKS nodes has different semantics depending on whether the VM is zonal or non-zonal:

- **Zonal VMs:** cloud-provider-azure formats the label as `<region>-<zone>` (e.g., `eastus-1`).
- **Non-zonal VMs:** cloud-provider-azure reads the **fault domain (FD) number** from the ARM API and uses it as the zone label. For standalone VMs, the FD is always `0`.

Karpenter uses `"0"` as the zone value for non-zonal offerings because it must match what the node will actually report. If Karpenter used a different value (e.g., `"regional"` or `"eastus-0"`), it would not match the actual node label, causing drift detection, topology spread miscalculations, and inconsistency with non-Karpenter nodes.

Previously, Karpenter used an empty string (`""`) internally for non-zonal offerings. This was changed to `"0"` (see [#1384](https://github.com/Azure/karpenter-provider-azure/issues/1384)) because Karpenter core uses `""` as a sentinel for "no domain found" in topology spread calculations.

There is no collision risk between FD numbers and zonal zone values: zonal labels use `<region>-<N>` format (e.g., `eastus-1`), while FD labels are bare numbers (`0`, `1`, `2`).

#### Zone label sources by backing type

| Backing type | Non-zonal zone label | Values |
|---|---|---|
| Standalone VM-backed node | FD number | Always `0` |
| VMSS-backed node pool | FD number | `0`, `1`, `2`, `3`, `4` (depends on region FD count) |
| Zonal node (VM or VMSS-backed) | AZ number with region prefix | `eastus-1`, `eastus-2`, `eastus-3` |

**Example mixed cluster** with a non-zonal VMSS-backed system pool and standalone-VM-backed Karpenter nodes:

```
System pool nodes (VMSS-backed, non-zonal, 3-FD region):
  node-1: topology.kubernetes.io/zone = 0    ← FD 0
  node-2: topology.kubernetes.io/zone = 1    ← FD 1
  node-3: topology.kubernetes.io/zone = 2    ← FD 2

Standalone-VM-backed nodes (non-zonal):
  node-4: topology.kubernetes.io/zone = 0    ← always FD 0
  node-5: topology.kubernetes.io/zone = 0    ← always FD 0
```

Standalone-VM-backed nodes always land in topology domain `"0"`, potentially creating skew against VMSS-backed nodes in domains `"1"` and `"2"`. This is an inherent limitation of standalone VMs — the FD assignment comes from Azure, not from Karpenter.

#### Topology spread assumptions and zone `"0"`

Topology spread constraints assume that zone domains represent non-overlapping failure boundaries: pods in different domains are isolated from each other, and pods in the same domain share a failure boundary. Zone `"0"` for standalone VMs violates both assumptions.

**Standalone VM FD 0 is not a real failure domain.** For VMSS-based non-zonal pools, FD 0, FD 1, FD 2 represent genuinely separate racks with independent failure boundaries. For standalone VMs, FD 0 is a placeholder — two standalone VMs both labeled `zone="0"` could be on completely different racks in different datacenters.

**Zone `"0"` may overlap with real zones.** A regional VM can be placed on hardware physically inside a zone's datacenter. A VM with `zone="0"` might sit on the same rack as a VM with `zone="eastus-1"`. Topology spread treats them as different domains, but they may share the same failure boundary. Conversely, two `zone="0"` VMs could be in completely different physical zones despite being "same domain."

**Live migration.** Azure can live-migrate regional VMs to hardware anywhere in the region. The zone label is not updated after creation, so `zone="0"` persists regardless of where the VM physically moves.

#### VMSS FD values vs real zones in mixed clusters

| Label | Source | Physical meaning |
|---|---|---|
| `0` | VMSS FD 0 | Specific rack (real isolation from FD 1, FD 2) |
| `0` | Standalone VM-backed node (regional) | Unknown rack — could be anywhere in the region |
| `1` | VMSS FD 1 | Different specific rack |
| `2` | VMSS FD 2 | Different specific rack |
| `eastus-1` | Standalone VM-backed node (zonal) | Zone 1 datacenter |
| `eastus-2` | Standalone VM-backed node (zonal) | Zone 2 datacenter |

Topology spread treats `"0"`, `"1"`, `"2"`, `"eastus-1"`, `"eastus-2"` as 5 non-overlapping domains. In reality:
- FD 0–2 are non-overlapping with each other (real rack isolation), but may physically overlap with `eastus-1`/`eastus-2`/`eastus-3`
- The two sources of `zone="0"` (VMSS-backed and standalone-VM-backed) have completely different semantics — one is a real rack, the other is "Azure picked, we don't know where"
- FD values and zone values can never collide syntactically (bare numbers vs `<region>-<N>` format), but can overlap physically

#### Topology spread summary

| Assumption topology spread makes | Holds for real zones? | Holds for VMSS FDs? | Holds for standalone VM FD 0? |
|---|---|---|---|
| Same domain = same failure boundary | Yes | Yes | **No** — standalone VMs in FD 0 can be anywhere |
| Different domains = isolated | Yes (zone ↔ zone) | Yes (FD ↔ FD) | **No** — FD 0 could overlap with any zone |
| Domain is a meaningful HA primitive | Yes | Yes (rack-level) | **No** — it's a label artifact, not a real domain |

This is an inherent limitation of Azure standalone VMs and cannot be solved by Karpenter. Users who mix regional Karpenter nodes or other standalone-VM-backed nodes with zone-based topology spread should understand that `zone="0"` does not provide the same isolation guarantees as real availability zones.
