# Karpenter fungibility improvements

**Author:** @matthchr

**Last updated:** June 24, 2026

**Status:** Proposed

## Overview

This document discusses improvements to Karpenter's "allocation strategy" and VM size selection logic - the mechanism by which we pick the best size to allocate according to `NodePool`/workload constraints.

Related issues

- [#1323](https://github.com/Azure/karpenter-provider-azure/issues/1323) — Improve handling on quota failures (or avoid them altogether)
- [#1733](https://github.com/Azure/karpenter-provider-azure/issues/1733) — Prefer availability over lowest-price for on-demand instance selection
- [#1727 (traceability)](https://github.com/Azure/karpenter-provider-azure/issues/1727) — Surface specific failure reason on the NodeClaim Launched condition (capacity vs quota vs allocation)
- [#943 (traceability)](https://github.com/Azure/karpenter-provider-azure/issues/943) — AMD64 v6 VM's never provisioned

**The current behavior is:**

- Karpenter is entirely reactive to errors that it gets from the compute API (quota, capacity, etc), and only adjusts its provisioning based on errors it has seen (with some cache expiry so errors don't prevent it from ever trying a given size again).
- Karpenter always tries the cheapest sizes first. If those sizes are under heavy regional contention or the user has low quota for them, they are tried anyway unless they are excluded at the `NodePool` level. Only advanced users even know to do that.
- Karpenter allocates the same way all the time (price-aware only). There is no way for Azure to perform capacity shaping among "similar" sizes. Note that capacity shaping does not exist anywhere today, and when it exists will likely only be enabled with explicit user opt-in, but the desire to eventually support the capability is there.

> [!NOTE]
> Some APIs referenced by this document are Microsoft internal-only. We've kept what we can public, but there are some links in this document that will not work for the general public.

### Current statistics

See [methodology](https://github.com/Azure/karpenter-poc/issues/1529) for more details about how these numbers were obtained. Note that these metrics are primarily focused on individual allocation attempts and NOT overall NodePool/NodeClaim allocation success. It's possible that an individual attempt for a given size fails, while the NodeClaim as a whole eventually allocates successfully. That initial failure results in increased latency for the user as well as increased Azure API throttling load, which is why we want to drive failed attempts down.

- Global success rate of individual allocation attempts, measured as "successes allocating a VM" / ("successes allocating a VM" + "failures allocating a VM") is **~27%** over the last 30d. Many of those errors are intermittent (throttling, network blips, etc). Excluding transient errors and errors unrelated to quota/capacity the success rate is **~58%**.
- The vast majority of observed failures are due to `SKUFamilyQuotaExceeded`.

| ErrorCategory                         | Stage | Pct   |
| ------------------------------------- | ----- | ----- |
| SKUFamilyQuotaExceeded                | Sync  | 94.52 |
| ZonalAllocationFailure                | Async | 2.73  |
| AllocationFailure                     | Async | 1.23  |
| SKUNotAvailable                       | Sync  | 0.81  |
| OverconstrainedZonalAllocationFailure | Async | 0.32  |
| Other                                 | Sync  | 0.21  |
| OverconstrainedAllocationRequest      | Async | 0.1   |
| SKUFamilyQuotaExceeded                | Async | 0.04  |
| Other                                 | Async | 0.02  |

We will use the queries defined in [methodology](https://github.com/Azure/karpenter-poc/issues/1529) to track the success rate of the "Improve reaction speed and responsiveness" goal defined below.

### Goals

- **Improve reaction speed and responsiveness** by knowing more before initiating a costly provisioning attempt. Karpenter already picks a working size (eventually) today.
  - **Increase "first allocation attempt" success rate** by taking more signals into account.
  - **Improve handling of quota issues** by choosing the size we attempt to allocate more intelligently.
  - **Improve handling of capacity issues** by choosing the size we attempt to allocate more intelligently.
  - **Improve `unavailableofferings` cache** to react more intelligently to errors.
- **Integrate with Azure Capacity Recommendation Service (CRS)** to enable capacity shaping.
- **Improve traceability** to help users answer the question why a particular size was chosen.

### Non-Goals

- Changing how we provision VMs (PUT virtualMachine or PUT machine) - we've got ongoing investment in the Machine path so doing something other than that doesn't meet our short-term goals.
- Improving "eventual allocation" success rate. Karpenter already tries every possible size allowed by the users configuration until it hits one that works.

## Decisions

### Decision 1: How can we improve our reactivity to errors from CRP?

Right now we manage our available set of VMs using the [unavailableofferings cache](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/cache/unavailableofferings.go). This cache is reactive and populated when we get errors from CRP.

Problems to consider here:

1. Hitting a capacity error for D64 doesn't guarantee that D32 isn't available, but on the other hand it _strongly suggests_ it. Today, we mark D64 and anything larger unavailable, but do not block smaller sizes. This often results in Karpenter just trying 2x D32 right after 1x D64 fails. Right now, we have two options we either exclude D32 or we don't. If we exclude D32 as well (basically marking that whole family as unavailable), that's great if they're actually out of capacity, but bad if they're just low on capacity but it's the only size in the allocation list.
2. Size filtering only at NodeClaim creation time isn't enough, because if multiple NodeClaims are created at the same time for the same SKUs, one hitting capacity issues means the others will too.

#### Conclusion: Integrate unavailableofferings into allocation strategy alongside price

This allows us to de-prioritize VMs whose families have hit capacity errors without excluding them entirely until we know for sure they won't work.

For item 2: We already filter at allocation time as well, during `resolveInstanceTypes()` we consider if a VM is unavailable and exclude from attempting it when provisioning the node.

> [!NOTE]
> We will hold off on actually implementing this until we've integrated [near term quota API usage](#decision-3-how-can-we-improve-our-allocation-success-rate-in-the-near-term), as it may be an optimization we don't need once we have quota signals integrated.

### Decision 2: Which API should we use to get capacity recommendations from Azure?

See [API comparison](#api-comparison) for a detailed breakdown of APIs considered and what they do for us.

At this stage, the ideal API would:

1. **Be recommendation only**: leaving final decision making to Karpenter. This enables integration with the Machine API as well as improving diagnosability.
2. **Include reasoning for the recommendations**: This improves traceability.
3. **Support detailed zone control**: There are times when Karpenter has very specific requirements about zonal placements due to pod topology constraints, we cannot rely on the platform to perform zonal balancing for us, we must control which VMs will be allocated in which zones.
4. **Be supported in all regions/clouds**: Karpenter is everywhere, the APIs we rely on need to be too.
5. **Be a one-stop-shop**: Considering quota, capacity, and CRS recommendations. This makes our life easier than having to join + score data from multiple sources.
6. **Be public**: So that we can use the same codepath in self-hosted Karpenter as well as NAP. Also so that we can leverage shared investment for upcoming `AzureNodeClass` and use the same API there even though that experience will be self-hosted only.
7. **Have a high throttling limit, be easily cacheable, or both**: Karpenter makes a lot of scheduling decisions. A non-cacheable low-limit API will negatively impact user experience. If the API is called in the hot path we must also consider API latency.
8. **Support 1 VM and N VM cases, including partial success**: While we currently only create 1 VM at a time, batching is coming with Machine API. We may create up to 50 VMs at a time. 40/50 of the preferred size is fine, we can allocate 10/50 of the next best size.

#### API Comparison

| Dimension                                  | On-Demand Placement Score          | Spot Placement Score               | Attribute-Based Recommendations   | Quota API     | Compute Fleet               | SKU Mix Placement                        | Capacity APIs (svc underlay) |
| ------------------------------------------ | ---------------------------------- | ---------------------------------- | --------------------------------- | ------------- | --------------------------- | ---------------------------------------- | ---------------------------- |
| **1. Recommendation-only?**                | ✅                                 | ✅                                 | ✅                                | ✅            | ❌ Changes VM creation path | ✅                                       | ❓                           |
| **2. Traceability?**                       | ⚠️ High/Medium/Low per SKU+Zone    | ⚠️ High/Medium/Low per SKU+Zone    | ⚠️ Ordered list, but no reasoning | ✅            | ❌                          | ⚠️ Score 1-10 + partialFulfillmentReason | ❓                           |
| **3. Zone control?**                       | ✅                                 | ✅                                 | ❌                                | N/A           | ✅                          | ✅ Explicit zone in skuSplit             | ❓                           |
| **4. All regions/clouds?**                 | ✅                                 | ✅                                 | ✅                                | ✅            | ✅                          | ⚠️ Public cloud only for now             | ✅                           |
| **5. One-stop-shop (quota+capacity+CRS)?** | ⚠️ Quota/capacity assume per-zone  | ⚠️ Quota/capacity assume per-zone  | ❓                                | ❌ Quota only | ✅                          | ✅                                       | ❓                           |
| **6. Public API?**                         | ❌                                 | ✅                                 | ✅                                | ✅            | ✅                          | ⚠️ Preview, public cloud only            | ❌ AKS-internal only         |
| **7. Cacheable / high throttle limit?**    | ⚠️ Score depends on `desiredCount` | ⚠️ Score depends on `desiredCount` | ❓                                | ✅            | N/A                         | ⚠️ Score depends on count/sizes          | ❓                           |
| **8. Supports 1-N VMs + partial success?** | ⚠️ Scores based on full success    | ⚠️ Scores based on full success    | ❌                                | N/A           | ✅                          | ✅                                       | ❓                           |

Legend: ✅ = good / yes, ⚠️ = partial / caveats, ❌ = no / not supported, ❓ = unknown

#### Conclusion: Use the SKU Mix Placement API

Based on our [goals](#goals), it's between **On-Demand/Spot Placement score** and **SKU Mix Placement**.

**Quoting Wayne Kuo (owner of placement scores API):**

> The main way that we differentiate between the two APIs is that Placement Scores is for homogenous SKU workloads (i.e. same SKU for 100 instances) and SKU Split is for heterogenous SKU workloads (i.e. 100 instances split across several SKUs)

**Quoting Mamadou Kane (owner of SKU Split API):**

> If you are only calling with `capacity: 1`, placement scores API is probably better as it's more cacheable and you can anyway just pick the highest zone. SKU split is more useful for heterogeneous workloads when `capacity: >1`.

SKU Split also has the advantage that it's preview and will be public eventually, whereas on-demand placement scores are internal-only. The SKU Split API has some [gotchas still](#gotchas-5).

Choosing the SKU split API instead of placement scores as our target recommendation API makes sense because:

1. It is public (preview currently)
2. It scales to N heterogeneous VMs, which is something we may soon want with Machine batch support.

##### Rollout

In terms of actually using this API, since it is in preview now it makes sense to take a two-phase approach. SKU Mix Placement should be introduced behind a feature gate and should not become a hard dependency until the API is GA, sovereign cloud support is available, throttling behavior is well understood, and we have proven timeout and fail-open behavior. When the feature gate is disabled, the API is unsupported in the current cloud, the API is throttled, or the API times out/errors, Karpenter should fall back to local allocation ranking using the quota provider and existing `unavailableofferings` behavior.

Phase 1: Call the API (+ cache), but do not actually use its results to provision anything. Instead just log what we _would_ provision. We can use this to build confidence: Is the API picking the same thing we would have? We can build reporting/monitoring on this. Note that Karpenter will still have to filter the large set of possible VM sizes down to a smaller set before calling the API.

Phase 2: Start using the API as the main source of size picking.

### Decision 3: How can we improve our allocation success rate in the near term?

#### Conclusion: New quota provider (short term), to be replaced by SKU Split provider in the medium term

Create a `quota` provider located at `pkg/providers/quota/quota.go`. This provider should call the [Azure Quota SDK](https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/quota/armquota/v2) periodically (every 10m by default).

Reasons to do this now:

- The quota and usage APIs are publicly available now in all regions/clouds.
- The biggest cause of errors seems to be `SKUFamilyQuotaExceeded`, which can be checked with just the quota APIs.

Key design decisions for the quota provider:

- **Family quota only.** Total regional quota is not useful as a size-selection input — if it is exhausted, no family or size choice will help and all we can do is retry until a node can be created.
- **Fail open.** If quota data is unavailable, stale, or cannot be parsed, assume quota is available for every family and fall back to today's behavior (price + `unavailableofferings`).
- **No local usage tracking between polls.** Counting in-flight vCPUs across concurrent NodeClaims, external VM creation, and controller restarts is error-prone and not worth the complexity. The quota provider supplies pre-knowledge only; observed failures still flow through `unavailableofferings` to block or de-prioritize offerings based on real errors. Quota provider will catch up at next poll interval.

We can use this data either as a **hard filter**, or as a **ranking signal**.

##### Hard filter

* ✅ Easier to understand.
* ✅ Preserves price-first with filters approach we've had historically.
* ✅ Avoids wasting a VM creation attempt (and its latency/throttling cost) on a family we already know will fail.
* ❌ Quota data is polled every 10m and stale between polls. If quota is freed (node deleted, external change), we won't know until next poll and will incorrectly exclude the family. This is very similar to the existing unavailableofferings cache, so users are (likely) already experiencing this latency today.
* ❌ Quota is per-family, not per-size. A family with 8 remaining cores can't fit a D64 but can fit a D4. We will need to be core-aware which adds some complexity.

##### Ranking signal

* ✅ More resilient to stale data — families with freed quota still get attempted, just later in the order.
* ❌ Still wastes one attempt if the top-ranked candidate ends up being quota-exhausted (though less likely since de-prioritized).
* ❌ More complex ranking logic — need to define interaction between quota score and price (is a $0.30/hr family with plenty of quota better than the $0.20/hr family with almost none?).
* ❌ Harder for users to reason about: "why did it pick a more expensive size?" vs the simpler "excluded due to quota."

Given our objective is as a stopgap measure while we wait for the SKU Split API, going for the simpler hard filter seems the correct approach.

### Decision 4: How can we improve traceability for VM size selection?

Existing issues:

- [kubernetes-sigs/karpenter#611](https://github.com/kubernetes-sigs/karpenter/issues/611) — Resolve Instance Types into the NodePool Status (closed, not planned — stale bot)
- [kubernetes-sigs/karpenter#1051](https://github.com/kubernetes-sigs/karpenter/issues/1051) — Mega Issue: Karpenter Observability (metrics, logs, eventing, etc.) (closed, completed)
- [kubernetes-sigs/karpenter#1042](https://github.com/kubernetes-sigs/karpenter/issues/1042) — Karpenter logging and metric suggestion (closed, not planned — stale bot)
- [kubernetes-sigs/karpenter#1904](https://github.com/kubernetes-sigs/karpenter/issues/1904) — Karpenter runtime logs showing tons of "incompatible with nodepool, daemonset overhead" (open)
- [kubernetes-sigs/karpenter#1442](https://github.com/kubernetes-sigs/karpenter/issues/1442) — Consolidation: Unconsolidatable / Can't remove without creating 2 candidates (open)

The challenge is that users may see a particular VM size chosen and want to answer the question: "Why was this other size not chosen"? While it is relatively easy to understand why a given VM _is_ compatible with the requirements, it's more difficult to expose why a given is _not_ compatible, or if it is compatible why it was not chosen.

**The current state is:**

- When a pod cannot schedule, Karpenter core gives relatively good error messages and pod events that explain why. See [Stages of VM size filtering](#stages-of-vm-size-filtering) for examples of these errors.
- When a pod can schedule, but it ends up on what the user feels is the "wrong" VM size, it is much harder to diagnose.

This means traceability improvements should focus on the second problem: The pod was scheduled but the size chosen wasn't what the user expected.

#### Option A: Add logs

[Stages of VM size filtering](#stages-of-vm-size-filtering) outlines the various levels at which sizes are excluded from contention. At many of these levels, hundreds or thousands of sizes are checked against many properties such as memory capacity, cpus, various labels, etc. This means that "just log it all" is not feasible. The volume of logs would be huge for every scheduling decision, and Karpenter makes hundreds of scheduling decisions in a short period of time.

#### Option B: Client tool

We could add a client tool that performs the same computations as Karpenter but without allocating any VMs. This tool could support options like `--why <size>` that answered why a particular size was not chosen.

The main problem with this is that the critical filtering functions in Karpenter core are unexported and tightly coupled to live cluster state (particularly topology/affinity logic). A client tool could reimplement the simpler requirement-matching and resource-fitting stages, but could not invoke the exact same code paths without upstream changes to export key functions or provide a dry-run API.

#### Option C: Annotation on the NodeClaim

We could add support for an annotation `karpenter.azure.com/why=<size>` (or similar), which our controller used as a signal to log extra information for that particular VM size or emit extra events on that particular NodeClaim. This dodges some of the issues that [Option A](#option-a-add-logs) has, but could only work on the parts of provisioning that we control in our provider. We could eventually upstream something like this (annotation or as part of `Spec`?) as well.

#### Conclusion: Defer for now

There isn't a single easy solution and we're currently at tracing parity with other Karpenter providers. It would be nice to make some improvements, but improvements will likely requires upstream changes and will need to be designed. If we end up needing something on our side we could prototype [Option C](#option-c-annotation-on-the-nodeclaim) and turn that into an upstream proposal at some point in the future.

# Appendix

## Azure APIs

### On-Demand compute placement score API

| Dimension                                  |                                                |
| ------------------------------------------ | ---------------------------------------------- |
| **1. Recommendation-only?**                | ✅                                             |
| **2. Traceability?**                       | ⚠️ High/Medium/Low per SKU+Zone                |
| **3. Zone control?**                       | ✅                                             |
| **4. All regions/clouds?**                 | ✅                                             |
| **5. One-stop-shop (quota+capacity+CRS)?** | ⚠️ Quota/capacity checks assume per-zone count |
| **6. Public API?**                         | ❌                                             |
| **7. Cacheable / high throttle limit?**    | ⚠️ Score depends on `desiredCount`             |
| **8. Supports 1-N VMs + partial success?** | ⚠️ Scores based on full success                |

#### API reference

This API is internal only. Read more about it [here (MSFT only)](https://github.com/Azure/karpenter-poc/issues/1875).

### Spot placement score

| Dimension                                  |                                                |
| ------------------------------------------ | ---------------------------------------------- |
| **1. Recommendation-only?**                | ✅                                             |
| **2. Traceability?**                       | ⚠️ High/Medium/Low per SKU+Zone                |
| **3. Zone control?**                       | ✅                                             |
| **4. All regions/clouds?**                 | ✅                                             |
| **5. One-stop-shop (quota+capacity+CRS)?** | ⚠️ Quota/capacity checks assume per-zone count |
| **6. Public API?**                         | ✅                                             |
| **7. Cacheable / high throttle limit?**    | ⚠️ Score depends on `desiredCount`             |
| **8. Supports 1-N VMs + partial success?** | ⚠️ Scores based on full success                |

#### API reference

**URL:** `POST /subscriptions/{subscriptionId}/providers/Microsoft.Compute/locations/{location}/placementScores/spot/generate?api-version=2025-06-05`

**Swagger/TypeSpec:** [RecommenderRP.json](https://github.com/Azure/azure-rest-api-specs/blob/main/specification/compute/resource-manager/Microsoft.Compute/Recommender/preview/2026-05-05-preview/RecommenderRP.json#L131) — Note that there is an earlier 2025 API version of this too that isn't preview

**Docs:**

- [About spot placement score](https://learn.microsoft.com/en-us/azure/virtual-machine-scale-sets/spot-placement-score?tabs=portal)
- [Spot placement score API](https://learn.microsoft.com/en-us/rest/api/recommenderrp/spot-placement-scores/post?view=rest-recommenderrp-2025-06-05&tabs=HTTP)
- [Spot placement score example](https://github.com/iamvighnesh/intelligent-spot-scaling-on-aks)

Note that this is purely just about capacity currently, it doesn't take spot evictions into account.

#### Samples

```
az compute-recommender spot-placement-score \
        --location "$API_LOCATION" \
        --subscription "$SUBSCRIPTION_ID" \
        --availability-zones true \
        --desired-locations "$REGIONS" \
        --desired-count "$VM_COUNT" \
        --desired-sizes "$SKU_SIZES" 2>&1
```

Publicly callable

#### Gotchas

- Purely just about capacity currently, it doesn't take spot evictions into account.
- `isQuotaAvailable` and score currently assume `desiredCount` nodes _per zone_, which means quota/capacity checks are not accurate for our use-case where we may only want N total across all zones. The capacity team has indicated willingness to fix this.

### Attribute-based recommendations API

| Dimension                                  |                                   |
| ------------------------------------------ | --------------------------------- |
| **1. Recommendation-only?**                | ✅                                |
| **2. Traceability?**                       | ⚠️ Ordered list, but no reasoning |
| **3. Zone control?**                       | ❌                                |
| **4. All regions/clouds?**                 | ✅                                |
| **5. One-stop-shop (quota+capacity+CRS)?** | ❓                                |
| **6. Public API?**                         | ✅                                |
| **7. Cacheable / high throttle limit?**    | ❓                                |
| **8. Supports 1-N VMs + partial success?** | ❌                                |

#### API reference

**URL:** `POST /subscriptions/{subscriptionId}/providers/Microsoft.Compute/locations/{location}/vmSizeRecommendations/vmAttributeBased/generate`

**Swagger/TypeSpec:** [diagnostic.json](https://github.com/Azure/azure-rest-api-specs/blob/68a4ca6e638fb9a0de4f045517a9307f4624250e/specification/compute/resource-manager/Microsoft.Compute/Diagnostic/preview/2025-02-01-preview/diagnostic.json#L492)

**Docs:**

- Used internally by AKS RP in some places. See [here (MSFT only)](https://github.com/Azure/karpenter-poc/issues/1875) for more details.

#### Gotchas

- Output SKUs are simply ordered in capacity availability, but it doesn't offer the delineation of High/Medium/Low scores.
- Designed for Portal/Copilot scenarios, not allocation optimization.

### Quota API

| Dimension                                  |               |
| ------------------------------------------ | ------------- |
| **1. Recommendation-only?**                | ✅            |
| **2. Traceability?**                       | ✅            |
| **3. Zone control?**                       | N/A           |
| **4. All regions/clouds?**                 | ✅            |
| **5. One-stop-shop (quota+capacity+CRS)?** | ❌ Quota only |
| **6. Public API?**                         | ✅            |
| **7. Cacheable / high throttle limit?**    | ✅            |
| **8. Supports 1-N VMs + partial success?** | N/A           |

#### API reference

**URL (usages):** `GET https://management.azure.com/{scope}/providers/Microsoft.Quota/usages?api-version=2025-09-01`

**URL (quota):** `GET https://management.azure.com/{scope}/providers/Microsoft.Quota/quotas?api-version=2025-09-01`

**Docs:**

- [Usages/get](https://learn.microsoft.com/en-us/rest/api/quota/usages/get?view=rest-quota-2025-09-01&tabs=HTTP)
- [Usages/list](https://learn.microsoft.com/en-us/rest/api/quota/usages/list?view=rest-quota-2025-09-01&tabs=HTTP)
- [Quota/list](https://learn.microsoft.com/en-us/rest/api/quota/quota/list?view=rest-quota-2025-09-01&tabs=HTTP)

#### Gotchas

- Quota data is already taken care of by other APIs such as Placement Score and SKU Mix Placement, so this may be redundant if using those.
-

### Compute fleet

| Dimension                                  |                             |
| ------------------------------------------ | --------------------------- |
| **1. Recommendation-only?**                | ❌ Changes VM creation path |
| **2. Traceability?**                       | ❌                          |
| **3. Zone control?**                       | ✅                          |
| **4. All regions/clouds?**                 | ✅                          |
| **5. One-stop-shop (quota+capacity+CRS)?** | ✅                          |
| **6. Public API?**                         | ✅                          |
| **7. Cacheable / high throttle limit?**    | N/A                         |
| **8. Supports 1-N VMs + partial success?** | ✅                          |

#### API reference

**URL:** `PUT /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.AzureFleet/fleets/{fleetName}?api-version=2024-11-01`

**Docs:**

- [Fleet BICEP sample](https://learn.microsoft.com/en-us/azure/templates/microsoft.azurefleet/fleets?pivots=deployment-language-bicep)
- [Fleet REST API documentation](https://learn.microsoft.com/en-us/rest/api/computefleet/fleets?view=rest-computefleet-2024-11-01)
- [Fleet capabilities overview](https://learn.microsoft.com/en-us/azure/azure-compute-fleet/overview)

#### Gotchas

- This changes our allocation mechanism (no longer PUT Machine or PUT VM).
- Not recommendation-only; requires architectural change to VM creation path.

### Capacity APIs via internal AKS route (AKS only)

| Dimension                                  |                      |
| ------------------------------------------ | -------------------- |
| **1. Recommendation-only?**                | ❓                   |
| **2. Traceability?**                       | ❓                   |
| **3. Zone control?**                       | ❓                   |
| **4. All regions/clouds?**                 | ✅                   |
| **5. One-stop-shop (quota+capacity+CRS)?** | ❓                   |
| **6. Public API?**                         | ❌ AKS-internal only |
| **7. Cacheable / high throttle limit?**    | ❓                   |
| **8. Supports 1-N VMs + partial success?** | ❓                   |

#### API reference

**URL:** ???

**Docs:**

- Offered (or will be offered?) internally by AKS at some point. See [here (MSFT only)](https://github.com/Azure/karpenter-poc/issues/1875) for more details.

#### Gotchas

- It's not clear to me that this even exists right now.
- Not publicly callable — AKS-internal only.
- No clear advantage over calling APIs ourselves.

### SKU Mix Placement API

| Dimension                                  |                                          |
| ------------------------------------------ | ---------------------------------------- |
| **1. Recommendation-only?**                | ✅                                       |
| **2. Traceability?**                       | ⚠️ Score 1-10 + partialFulfillmentReason |
| **3. Zone control?**                       | ✅ Explicit zone in skuSplit             |
| **4. All regions/clouds?**                 | ⚠️ Public cloud only for now             |
| **5. One-stop-shop (quota+capacity+CRS)?** | ✅                                       |
| **6. Public API?**                         | ⚠️ Preview, public cloud only            |
| **7. Cacheable / high throttle limit?**    | ⚠️ Score depends on count/sizes          |
| **8. Supports 1-N VMs + partial success?** | ✅                                       |

#### API reference

**URL:** `POST /subscriptions/{subscriptionId}/providers/Microsoft.Compute/locations/{location}/skuMixPlacementScores/recommendations/generate?api-version=2026-05-05-preview`

**Swagger/TypeSpec:** [GenerateSkuMixPlacementScores.json](https://github.com/Azure/azure-rest-api-specs/blob/main/specification/compute/resource-manager/Microsoft.Compute/Recommender/preview/2026-05-05-preview/examples/GenerateSkuMixPlacementScores.json) — check the examples there too, they are useful.

**Docs:**

- [Internal docs (MSFT only)](https://github.com/Azure/karpenter-poc/issues/1875)

Contact: Bartosz Paliswiat, Yash Khandelwal

#### Samples

Sample request (zonal)

```json
{
  "zones": ["1", "2", "3"],
  "capacityProfile": {
    "capacity": 5,
    "capacityType": "VM",
    "priority": "Regular",
    "allocationStrategy": "Prioritized",
    "osType": "Linux"
  },
  "instanceDescription": {
    "type": "VMSizes",
    "vmSizes": [
      {
        "name": "Standard_D8s_v5",
        "rank": 0
      },
      {
        "name": "Standard_D2s_v3",
        "rank": 1
      },
      {
        "name": "Standard_E2s_v3",
        "rank": 2
      }
    ]
  }
}

$ az rest --method post --uri "https://management.azure.com/subscriptions/{subscriptionId}/providers/Microsoft.Compute/locations/westus2/skuMixPlacementScores/recommendations/generate?api-version=2026-05-05-preview" --body @/tmp/skuMixPlacement.json
```

Sample response

```json
{
  "partialFulfillmentReason": "None",
  "placementChoices": [
    {
      "id": "498f1a1f-b85c-476e-b8be-f37e5721c3f2",
      "score": 9,
      "skuSplit": [
        {
          "capacity": 5,
          "name": "Standard_D2s_v3",
          "priority": "Regular",
          "zone": "1"
        }
      ]
    }
  ]
}
```

Sample request (regional):

```json
{
  "zones": [], // No zones
  "capacityProfile": {
    "capacity": 5,
    "capacityType": "VM",
    "priority": "Regular",
    "allocationStrategy": "Prioritized",
    "osType": "Linux"
  },
  "instanceDescription": {
    "type": "VMSizes",
    "vmSizes": [
      {
        "name": "Standard_D8s_v5",
        "rank": 0
      },
      {
        "name": "Standard_D2s_v3",
        "rank": 1
      },
      {
        "name": "Standard_E2s_v3",
        "rank": 2
      }
    ]
  }
}


$ az rest --method post --uri "https://management.azure.com/subscriptions/{subscriptionId}/providers/Microsoft.Compute/locations/westus2/skuMixPlacementScores/recommendations/generate?api-version=2026-05-05-preview" --body @/tmp/skuMixPlacement.json
```

Sample response:

```json
{
  "partialFulfillmentReason": "None",
  "placementChoices": [
    {
      "id": "eafaaf5a-0243-49ac-92cc-788f7190cde2",
      "score": 9,
      "skuSplit": [
        {
          "capacity": 5,
          "name": "Standard_D2s_v3",
          "priority": "Regular"
          // No zones
        }
      ]
    }
  ]
}
```

#### FAQ

Q: What's the relationship between this API and compute placement score?

A: Owned by sister teams. Placement scores is more for homogeneous allocations, SKU split is more for heterogeneous allocations.

Q: Do Fleet and SKUSplit use the same underlying datasource?

A: Yes. SKUSplit is basically "part of Fleet" - fleet calls Sku Split v2. The public API (SKU Mix Placement v3) has more hardware knowledge, better. Presumably eventually Fleet will move to use it internally too?

Q: How does CRS capacity shaping work?

A: Doesn't exist yet. 1P only? Seems very early days, not clear what the API shape is gonna look like or exactly what the behavior will be.

Q: Can we cache this response?

A: Yes, `validUntil` is coming soon in the API response. In the meantime we can probably cache for a short duration (30s?) safely.

#### Gotchas

- Response is an allocation plan ("put 15 here, 20 there") — awkward for Karpenter's 1-VM-at-a-time model, but calling with count=1 works.
- Throttling limits: 150 calls / min / sub for preview, targeting 600 for GA
- Only public cloud is supported for now. Other clouds TBD.
- Only preview currently, can't be called without manual subscription registration. ETA for public preview unclear, they will know more on **July 15th**.
- Score depends on count+sizes, so caching requires a composite key (priority, zone, osType, vmSizes) with short TTL (~1m or less).
- Price consideration comes from commerce team exposes a Rate card API through which we get the prices. They should be in-sync with public prices but we should learn more.
- Given 10 size inputs, _does not_ return them all. Only returns a few recommended placement combinations. This makes traceability slightly harder because we don't know why other sizes were excluded.
- CRS capacity shaping doesn't exist yet. When it does, probably only for 1Ps.

## Stages of VM Size Filtering

How instance types are filtered from initial SKU list to final selection for a NodeClaim.

## Stage 1: Static Allow-List _(Azure Provider)_

**[`GetKarpenterWorkingSKUs()`](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/instancetype/skus.go#L63)** starts from [`known_skus.yaml`](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/instancetype/known_skus.yaml) and removes:

- **AKS-restricted**: `Standard_A0`, `A1`, `A1_v2`, `B1s`, `B1ms`, `F1`, `F1s`, `Basic_A0–A4`
- **Karpenter-restricted**: `Standard_E64i_v3`, `Standard_E64is_v3`

**Observable errors:** None — these SKUs are never exposed. Users cannot trigger failures at this stage.

## Stage 2: SKU Support Checks _(Azure Provider)_

**[`UpdateInstanceTypes()`](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/instancetype/instancetypes.go#L396)** → [`isSupported()`](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/instancetype/instancetypes.go#L439):

| Filter                     | Criterion                        |
| -------------------------- | -------------------------------- |
| `HasLocationRestriction()` | SKU not restricted in region     |
| `hasMinimumCPU()`          | ≥ 2 vCPUs                        |
| `hasMinimumMemory()`       | ≥ 3.5 GiB RAM                    |
| `isUnsupportedGPU()`       | GPU SKUs must be in GPU registry |
| `hasConstrainedCPUs()`     | No constrained-vCPU variants     |
| `isConfidential()`         | DC/EC series excluded            |

**Observable errors:** None directly. These silently reduce the available instance type pool. If all remaining types are removed by later stages, the downstream errors will surface (Stages 5–7).

## Stage 3: Offering Availability _(Azure Provider)_

**[`createOfferings()`](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/instancetype/instancetypes.go#L241)** per zone/capacity-type:

| Filter                                                                                                                                      | Effect                                                                     |
| ------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| [`UnavailableOfferings.IsUnavailable()`](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/cache/unavailableofferings.go#L81) | ICE errors block offering for TTL (also family-level blocks by vCPU count) |
| `sku.IsLowPriorityCapable()`                                                                                                                | Spot offering unavailable if false                                         |
| No pricing data                                                                                                                             | `MissingPrice` (999.0) assigned — used for ranking, not hard exclusion     |

Instance types with **zero available offerings** are dropped (`len(Offerings) == 0`).

**Observable errors:** None directly from this stage. But if a pod requires `spot` and no offerings exist, this manifests as a Stage 5/6 error:

| Pod Event                                                                                                                                    | Controller Log                                                                                                                                   |
| -------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| `Warning FailedScheduling karpenter Failed to schedule pod, no instance type has the required offering, requirements={...}, resources={...}` | `{"level":"ERROR","message":"could not schedule pod","error":"no instance type has the required offering, requirements={...}, resources={...}"}` |

## Stage 4: NodeClass Parameter Filters _(Azure Provider)_

**[`isInstanceTypeSupportedByFilters()`](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/instancetype/instancetypes.go#L307)** in `List()`:

| Filter                                         | Criterion                                            |
| ---------------------------------------------- | ---------------------------------------------------- |
| `isInstanceTypeSupportedByImageFamily()`       | GPU SKU must support selected OS                     |
| `isInstanceTypeSupportedByEncryptionAtHost()`  | SKU must have `EncryptionAtHostSupported=True`       |
| `isInstanceTypeSupportedByLocalDNS()`          | ≥ 4 vCPU, ≥ 244 MiB when LocalDNS enabled            |
| `isInstanceTypeSupportedByGPUDriverMode()`     | GPU SKU must support driver install in "Driver" mode |
| `isInstanceTypeSupportedByArtifactStreaming()` | ARM64 excluded when artifact streaming enabled       |

**Observable errors:** None directly. This further reduces the instance type pool before Karpenter Core scheduling. If all types are removed, the errors manifest in Stage 5.

## Stage 5: NodePool Requirements Pre-Filter _(Karpenter Core)_

[`NewScheduler()`](https://github.com/kubernetes-sigs/karpenter/blob/v1.13.0/pkg/controllers/provisioning/scheduling/scheduler.go#L117) calls [`filterInstanceTypesByRequirements()`](https://github.com/kubernetes-sigs/karpenter/blob/v1.13.0/pkg/controllers/provisioning/scheduling/nodeclaim.go#L412) per NodePool. Each instance type must pass **all three**:

1. **`compatible()`** — `it.Requirements.Intersects(requirements)` — NodePool labels (arch, zone, capacity-type, instance-type) must overlap
2. **`fits()`** — `resources.Fits(totalRequests, it.Allocatable())` — accumulated requests fit allocatable capacity
3. **Available offering** — at least one offering is `Available && requirements.IsCompatible(of.Requirements)`

**Observable errors when ALL instance types are filtered at this stage:**

| Trigger                                                                   | NodePool Event                                                                                                           | Pod Event                                                                                                                                                                                              | Controller Log                                                                                                                              |
| ------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------- |
| NodePool requires non-existent instance type (`Standard_NONEXISTENT_v99`) | `Warning NoCompatibleInstanceTypes karpenter NodePool requirements filtered out all compatible available instance types` | `Warning FailedScheduling karpenter Failed to schedule pod, no instance type met all requirements, requirements={node.kubernetes.io/instance-type In [Standard_NONEXISTENT_v99] ...}, resources={...}` | `{"level":"ERROR","message":"could not schedule pod","error":"no instance type met all requirements, requirements={...}, resources={...}"}` |

## Stage 6: Per-Pod Scheduling _(Karpenter Core)_

[`NodeClaim.CanAdd()`](https://github.com/kubernetes-sigs/karpenter/blob/v1.13.0/pkg/controllers/provisioning/scheduling/nodeclaim.go#L114) applies when adding a pod to a NodeClaim:

- **Taint toleration** — pod must tolerate NodeClaim taints
- **Host port conflicts** — no clashes with already-scheduled pods
- **Pod affinity** — `Compatible()` with combined requirements
- **Topology spread** — may add zone constraints
- **Volume topology** — PV zone requirements must match
- **Re-runs `filterInstanceTypesByRequirements()`** with pod's requirements merged in

### Observable errors at this stage:

#### 6a. Taint Toleration Failure

| Trigger                                        | Pod Event                                                                                                                       | Controller Log                                                                                                                                                                    |
| ---------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| NodePool taint, pod has no matching toleration | `Warning FailedScheduling karpenter Failed to schedule pod, did not tolerate taint (taint=example.com/special=true:NoSchedule)` | `{"level":"ERROR","message":"could not schedule pod","taint":"example.com/special=true:NoSchedule","error":"did not tolerate taint (taint=example.com/special=true:NoSchedule)"}` |

#### 6b. Incompatible Requirements (label/selector mismatch)

| Trigger                                                            | Pod Event                                                                                                                                                                    | Controller Log                                                                                                                                                                   |
| ------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Pod `nodeSelector: arch=arm64`, pool only allows amd64             | `Warning FailedScheduling karpenter Failed to schedule pod, incompatible requirements, key kubernetes.io/arch, [arm64] not in [amd64]`                                       | `{"level":"ERROR","message":"could not schedule pod","error":"incompatible requirements, key kubernetes.io/arch, [arm64] not in [amd64]"}`                                       |
| Pod `nodeSelector: capacity-type=spot`, pool only allows on-demand | `Warning FailedScheduling karpenter Failed to schedule pod, incompatible requirements, key karpenter.sh/capacity-type, [spot] not in [on-demand]`                            | `{"level":"ERROR","message":"could not schedule pod","error":"incompatible requirements, key karpenter.sh/capacity-type, [spot] not in [on-demand]"}`                            |
| Pod `nodeSelector: zone=westus2-99`, zone doesn't exist            | `Warning FailedScheduling karpenter Failed to schedule pod, incompatible requirements, key topology.kubernetes.io/zone, [westus2-99] not in [westus2-1 westus2-2 westus2-3]` | `{"level":"ERROR","message":"could not schedule pod","error":"incompatible requirements, key topology.kubernetes.io/zone, [westus2-99] not in [westus2-1 westus2-2 westus2-3]"}` |

#### 6c. No Instance Type Has Enough Resources

| Trigger                                    | Pod Event                                                                                                                                              | Controller Log                                                                                                                                             |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Pod requests `cpu: 512` (exceeds all SKUs) | `Warning FailedScheduling karpenter Failed to schedule pod, no instance type has enough resources, requirements={...}, resources={cpu=512 memory=...}` | `{"level":"ERROR","message":"could not schedule pod","error":"no instance type has enough resources, requirements={...}, resources={cpu=512 memory=...}"}` |

#### 6d. Host Port Conflict

| Trigger                                                      | Pod Event                                                                                                                                                                                                                            | Controller Log                                                                                                                                               |
| ------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Two pods with same `hostPort: 9999`, constrained to one node | `Warning FailedScheduling karpenter Failed to schedule pod, checking host port usage, pod host port conflicts with existing hostport configuration, pod-hostport-ip=0.0.0.0, pod-hostport-port=9999, pod-hostport-protocol=TCP, ...` | `{"level":"ERROR","message":"could not schedule pod","error":"checking host port usage, pod host port conflicts with existing hostport configuration, ..."}` |

#### 6e. Volume Topology Conflict

| Trigger                                     | Pod Event                                                                                                                                                                           | Controller Log                                                                                                                                                                          |
| ------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| PV pinned to non-existent zone `westus2-99` | `Warning FailedScheduling karpenter Failed to schedule pod, incompatible volume requirements, key topology.kubernetes.io/zone, [westus2-99] not in [westus2-1 westus2-2 westus2-3]` | `{"level":"ERROR","message":"could not schedule pod","error":"incompatible volume requirements, key topology.kubernetes.io/zone, [westus2-99] not in [westus2-1 westus2-2 westus2-3]"}` |

#### 6f. Topology Spread Unsatisfiable

| Trigger                                                              | Pod Event                                                                                                                                                                                         | Controller Log                                                                                                                                              |
| -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `topologySpreadConstraints` with `DoNotSchedule` + impossible domain | `Warning FailedScheduling karpenter Failed to schedule pod, unsatisfiable topology constraint for topology spread, key=topology.kubernetes.io/zone (counts=..., podDomains=..., nodeDomains=...)` | `{"level":"ERROR","message":"could not schedule pod","error":"unsatisfiable topology constraint for topology spread, key=topology.kubernetes.io/zone ..."}` |

## Stage 7: NodePool Limits _(Karpenter Core)_

[`addToNewNodeClaim()`](https://github.com/kubernetes-sigs/karpenter/blob/v1.13.0/pkg/controllers/provisioning/scheduling/scheduler.go#L608) in the scheduler:

- **Node count** — `remaining[resources.Node].IsZero()` → pool exhausted
- **Resource limits** — [`filterByRemainingResources()`](https://github.com/kubernetes-sigs/karpenter/blob/v1.13.0/pkg/controllers/provisioning/scheduling/scheduler.go#L951) removes instance types exceeding the pool's remaining CPU/memory budget

**Observable errors:**

| Trigger                                           | Pod Event                                                                                                                                      | Controller Log                                                                                                                                     |
| ------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| NodePool `limits.cpu: 2`, workload exceeds budget | `Warning FailedScheduling karpenter Failed to schedule pod, all available instance types exceed limits for nodepool, NodePool=general-purpose` | `{"level":"ERROR","message":"could not schedule pod","error":"all available instance types exceed limits for nodepool, NodePool=general-purpose"}` |
| NodePool node count exhausted                     | `Warning FailedScheduling karpenter Failed to schedule pod, node limits have been exhausted for nodepool, NodePool=general-purpose`            | `{"level":"ERROR","message":"could not schedule pod","error":"node limits have been exhausted for nodepool, NodePool=general-purpose"}`            |

## Stage 8: Preference Relaxation _(Karpenter Core)_

If all above fails, [`preferences.Relax()`](https://github.com/kubernetes-sigs/karpenter/blob/v1.13.0/pkg/controllers/provisioning/scheduling/preferences.go#L38) progressively drops soft constraints and retries:

1. Required node affinity terms (if multiple OR terms)
2. Preferred pod affinity/anti-affinity
3. Preferred node affinity
4. TopologySpread `ScheduleAnyway`
5. PreferNoSchedule tolerations

**Observable errors:** None from relaxation itself — this is a recovery mechanism. If relaxation succeeds, the pod schedules. If all relaxations are exhausted and scheduling still fails, the final error from Stages 5–7 is emitted.

### Cross-Reference: Stage → Error → Test Command

| Stage | Error Class                         |                                                                           Key Phrase in Error | Quick Repro                                                                                                                                          |
| ----- | ----------------------------------- | --------------------------------------------------------------------------------------------: | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| 3     | No offering available               |                                                  `no instance type has the required offering` | Pod requires spot, no SKUs are spot-capable                                                                                                          |
| 5     | All instance types filtered at init | `NodePool requirements filtered out all compatible available instance types` (NodePool event) | NodePool requires `Standard_NONEXISTENT_v99`                                                                                                         |
| 6a    | Taint                               |                                                                      `did not tolerate taint` | `kubectl patch nodepool general-purpose --type=merge -p '{"spec":{"template":{"spec":{"taints":[{"key":"x","effect":"NoSchedule","value":"y"}]}}}}'` |
| 6b    | Requirements                        |                                                        `incompatible requirements, key <KEY>` | Pod nodeSelector contradicts NodePool                                                                                                                |
| 6c    | Resources                           |                                                       `no instance type has enough resources` | Pod requests `cpu: 512`                                                                                                                              |
| 6d    | Host port                           |                                                                     `pod host port conflicts` | Two pods, same hostPort, node limit=1                                                                                                                |
| 6e    | Volume topology                     |                                                            `incompatible volume requirements` | PV zone doesn't exist                                                                                                                                |
| 6f    | Topology spread                     |                                                           `unsatisfiable topology constraint` | DoNotSchedule + impossible zone                                                                                                                      |
| 7     | Limits                              |                                                                  `exceed limits for nodepool` | `limits.cpu: 2` on NodePool                                                                                                                          |

### Notes

- **All pod events** use reason `FailedScheduling` with format: `Failed to schedule pod, <error>`
- **All controller logs** use `level=ERROR`, `message="could not schedule pod"`, `error=<same string>`
- Stages 1–4 (Azure provider) silently reduce the instance type pool; they produce no direct user-visible errors unless the pool becomes empty, at which point Stage 5+ errors surface
- The log JSON includes extra structured fields not in the event (e.g., `"taint":"..."` for 6a, `"NodePool":"..."` for Stage 7)
- Stages 1–4 errors are only observable via Karpenter **debug logs** showing SKU filtering decisions during instance type refresh

### Other

- [AWS Karpenter allocation-strategy](https://karpenter.sh/docs/faq/#how-does-karpenter-dynamically-select-instance-types)
