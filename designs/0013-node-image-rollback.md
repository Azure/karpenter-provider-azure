# Node Image Rollback for NAP

**Author:** @rakechill

**Last updated:** Jun 26, 2026

**Status:** Proposed

**Related issue:** [Azure/karpenter-provider-azure#1220](https://github.com/Azure/karpenter-provider-azure/issues/1220)

Note: The issue title says "Add support for setting ImageID for nodeClass", but the problem statement is primarily asking for rollback/pinning capability to recover from bad image releases. This rollback design addresses the bad-release recovery use case. It does not address the literal title request to set arbitrary ImageID; that is likely a future AKS workstream, and Karpenter should not set that standard prematurely.

## Table of Contents

1. [Overview](#overview)
2. [Background](#background)
3. [Goals](#goals)
4. [Non-Goals](#non-goals)
5. [API Changes](#api-changes)
6. [Reconciliation Design](#reconciliation-design)
7. [Validation and Conditions](#validation-and-conditions)
8. [Drift and Provisioning Behavior](#drift-and-provisioning-behavior)
9. [Decision Notes](#decision-notes)
10. [Out of Scope Follow-up Designs](#out-of-scope-follow-up-designs)
11. [Operational Scenarios](#operational-scenarios)
12. [Testing](#testing)
13. [Production Readiness](#production-readiness)
14. [References](#references)
15. [Open Questions](#open-questions)

## Overview

This document proposes a rollback-focused capability for AKS Node Auto Provisioning (NAP) in Karpenter, aligned with current AKS agent pool rollback semantics.

The intent is to let customers safely and quickly return to the last known-good node image when a newly released image causes regressions.

This design intentionally focuses on rollback only.

Related but separate workstreams are:

1. True node image pinning with long-duration control.
2. Prepared image spec support using a full resource ID that maps to an AKS API prepared image field.
3. Decoupling control plane k8s upgrades from node k8s upgrades.

These workstreams require independent design and are out of scope here.

## Background

AKS node images are VHD-based image versions that are updated frequently. Today, NAP users configure selector-style image controls in spec (for example image family), while resolved concrete images are surfaced in status.

Current NAP behavior has no rollback affordance:

1. No spec field lets customers request rollback.
2. No status field preserves the previous image set as a first-class rollback target.
3. Drift logic replaces nodes that are not on the current status image set, but does not model rollback intent.

AKS RP rollback semantics rely on a recently-used allowlist with time limits. NAP currently has no equivalent mechanism.

## Goals

1. Provide a clear, explicit customer surface to request rollback to the previously used node image version.
2. Preserve recently-used rollback eligibility state durably in AKSNodeClass status.
3. Enforce a rollback time window aligned with AKS RP behavior (7 days).
4. Keep rollback operationally safe and predictable without introducing long-term pinning semantics.

## Non-Goals

1. Implement long-duration image pinning.
2. Introduce prepared image spec by full resource ID.
3. Guarantee rollback to arbitrary historical versions.
4. Block provisioning scale-up while rollback state is being evaluated.

## API Changes

### AKSNodeClass status: recentlyUsedVersions

Add a new status section that mirrors the AKS RP recently-used rollback model:

```go
type RecentlyUsedVersions struct {
	// timestampUsed is the time this node image and orchestrator version pair
	// was last active before Karpenter moved status.images forward.
	TimestampUsed metav1.Time `json:"timestampUsed,omitempty"`

	// imageVersion is the AKS node image release version suffix used for rollback,
	// e.g. "202601.15.0".
	ImageVersion string `json:"imageVersion,omitempty"`

	// orchestratorVersion is the Kubernetes version paired with this image,
	// e.g. "1.32.5".
	OrchestratorVersion string `json:"orchestratorVersion,omitempty"`
}
```

```go
type AKSNodeClassStatus struct {
	// existing fields...
	Images               []NodeImage             `json:"images,omitempty"`
	RecentlyUsedVersions *RecentlyUsedVersions   `json:"recentlyUsedVersions,omitempty"`
}
```

Semantics:

1. recentlyUsedVersions captures the immediate prior node image release version suffix and orchestrator version pair before status.images advances.
2. recentlyUsedVersions.timestampUsed is static and is used to enforce the 7-day rollback eligibility window.
3. Only one recently-used entry is retained, matching AKS RP semantics.
4. If Karpenter stores multiple previous image versions in the future, rollback UX must specify how Karpenter chooses which previous version to use.

### Customer experience options

AKS agent pool rollback requires customers to specify a concrete node image version. The only valid rollback targets are versions that appear in the agent pool's recentlyUsedVersions list with a matching orchestrator version and valid timestamp. For NAP, the explicit rollback value can be the shared release version suffix rather than the full AKS node image string, because Karpenter already derives the image definition from AKSNodeClass and instance-type requirements. A boolean rollback field would therefore introduce different semantics from the AKS RP API by asking Karpenter to choose the rollback target on the customer's behalf.

NAP has two possible UX shapes:

#### Option A: Explicit rollback target

Customers specify the exact node image release version suffix to roll back to:

```yaml
spec: { imageVersion: { rollbackTo: "202601.15.0" } }
```

Karpenter still derives the image family, architecture, generation, and runtime-specific image definition from the AKSNodeClass and selected instance type. This matters because multiple NodePools can share the same AKSNodeClass while selecting different instance types, which may resolve to different image definitions such as Gen1, Gen2, or Arm64 variants.

Behavior:

1. Karpenter validates the requested version against status.recentlyUsedVersions.
2. Rollback is rejected if the requested version suffix is not the single recently-used version suffix, the orchestrator version does not match, or the timestamp is older than 7 days.
3. For each resolved image definition, Karpenter applies the requested release version suffix instead of requiring the customer to specify a full image string such as `AKSUbuntu-2204gen2containerd-202601.15.0`.

Pros:

1. Mirrors AKS RP semantics closely.
2. Makes customer intent explicit.
3. Avoids making customers specify image definition portions that Karpenter already derives.
4. Avoids surprise rollbacks to an image the customer did not realize was the previously used version.

Cons:

1. Requires customers to inspect status and copy the exact rollback value.
2. Slightly more cumbersome during incident response.

#### Option B: Boolean rollback request

Customers request rollback to the previous version without specifying the concrete version:

```yaml
spec:
  imageVersion:
    rollbackToPrevious: true
```

Behavior:

1. Karpenter uses status.recentlyUsedVersions as the rollback target.
2. Rollback is rejected if recentlyUsedVersions is missing, the orchestrator version is invalid for the request, or the timestamp is older than 7 days.

Pros:

1. Easier customer UX during a bad image incident.
2. Avoids requiring customers to copy/paste long image version strings.
3. Still uses the AKS-style recently-used allowlist under the hood.

Cons:

1. A customer may roll back to an image they did not realize was the previously used version.
2. Requires status and conditions to make the selected rollback target highly visible.
3. If Karpenter later stores multiple previous image versions, a boolean rollback request would become ambiguous unless the API also defines which previous entry is selected.

The final UX choice should be resolved before implementation. Both options rely on the same recentlyUsedVersions validation model.

## Reconciliation Design

### Snapshot point

NodeImageReconciler in images.go is the canonical place to capture rollback state.

Before status.images is overwritten due to a detected version update:

1. Snapshot the current node image release version suffix and orchestrator version into status.recentlyUsedVersions.
2. Set status.recentlyUsedVersions.timestampUsed to now.
3. Then write newly resolved images into status.images.

This location is authoritative because both old and new sets are simultaneously available only at this transition.

### Rollback path

When rollback is requested through the chosen `spec.imageVersion` UX:

1. Validate recentlyUsedVersions exists and has an imageVersion.
2. Validate now - recentlyUsedVersions.timestampUsed is within TTL.
3. Validate recentlyUsedVersions.orchestratorVersion is compatible with the current rollback request.
4. If valid, set effective target image release version suffix to recentlyUsedVersions.imageVersion.
5. Apply the rollback image version suffix using one of the implementation options below.

### Image selection during rollback

status.images is already a list because a single AKSNodeClass can expose multiple compatible image definitions. For example, the same NodeClass may publish Gen2 amd64, Gen1 amd64, and Gen2 arm64 images, each with requirements that determine which instance types can use it.

Rollback should preserve this model:

1. Karpenter resolves the normal goal image list from the AKSNodeClass, Kubernetes version, FIPS mode, SIG/CIG mode, and supported image definitions.
2. Each resolved image keeps its existing requirements. Node launch continues to choose the first image whose requirements are compatible with the selected instance type.
3. Rollback does not choose one image from status.images globally. Instead, it rewrites every resolved image ID to the requested release version suffix.
4. To rewrite an image ID, Karpenter strips the existing `/versions/<current>` suffix and appends `/versions/<rollback imageVersion>`.

Example:

```text
normal goal image:
/CommunityGalleries/.../images/2204gen2containerd/versions/202607.15.0

recentlyUsedVersions.imageVersion:
202606.08.1

rollback goal image:
/CommunityGalleries/.../images/2204gen2containerd/versions/202606.08.1
```

The same suffix rewrite is applied independently to each resolved image definition. This lets multiple NodePools share one AKSNodeClass while still rolling back to the image variant selected by each NodePool's instance type requirements.

Before using rolled-back images, Karpenter should verify that the reconstructed image version exists for each resolved image definition. If any required image definition does not have the requested release suffix, rollback should fail with a clear condition rather than using an invalid image ID.

### Implementation options for applying rollback

There are two viable places to apply the rollback image version suffix.

#### Option 1: Keep status.images as latest and convert at use sites

status.images continues to represent the normally resolved latest image set. Rollback is applied as a second layer by a helper such as `convertToRolledBackImage`, which rewrites the selected or candidate image ID from `/versions/<latest>` to `/versions/<rollback imageVersion>`.

Behavior:

1. NodeImageReconciler keeps publishing latest resolved images in status.images.
2. Consumers that need the effective image call `convertToRolledBackImage` after normal image selection.
3. The helper preserves the selected image's requirements and only replaces the version suffix.
4. The helper verifies the reconstructed image exists in SIG before returning it.

Pros:

1. status.images remains the source of latest goal-state images and is not overloaded with rollback state.
2. Rollback behavior is explicit at use sites, which makes it easier to reason about latest-vs-effective image selection.
3. Existing status update and maintenance-window behavior can remain mostly unchanged.

Cons:

1. Every consumer that launches nodes or compares drift must consistently use the effective-image helper.
2. A missed call site could accidentally use latest images while rollback is requested.
3. Conditions and observability need to clearly show that effective images differ from status.images.

#### Option 2: Write rolled-back images into status.images

NodeImageReconciler applies the rollback image version suffix whenever it resolves images while rollback is active. status.images then contains the effective rolled-back image IDs instead of latest IDs.

Behavior:

1. NodeImageReconciler resolves the normal latest image list.
2. If rollback is active and valid, it rewrites every resolved image ID to `/versions/<rollback imageVersion>` before writing status.images.
3. The reconciler preserves each image's requirements and only replaces the version suffix.
4. The reconciler verifies the reconstructed image exists in SIG before publishing it.

Pros:

1. Existing node launch and drift paths that already consume status.images automatically use the rolled-back images.
2. Fewer use-site changes are required because status.images remains the effective image source.
3. The current status surface directly shows the images Karpenter will use for new nodes.

Cons:

1. status.images no longer shows the latest normally resolved image set while rollback is active.
2. Roll-forward behavior must carefully distinguish latest resolution from rolled-back effective state.
3. Snapshotting recentlyUsedVersions must avoid treating the rollback rewrite itself as a new image update.

### TTL and expiry

Rollback TTL is 7 days from recentlyUsedVersions.timestampUsed.

If expired:

1. Rollback request is rejected as stale at reconciliation time.
2. Effective behavior remains subject to normal image update behavior.

Confirmed AKS RP behavior:

1. The 7-day TTL on recentlyUsedVersions is a validation gate only.
2. TTL expiry does not trigger an automatic roll-forward to latest.
3. There is no AKS RP background job watching recentlyUsedVersions expiry.
4. Once a pool is rolled back, it can remain on the rolled-back image after the rollback eligibility window expires.
5. Roll-forward is controlled separately by node image auto-upgrade behavior.

AKS auto-upgrade behavior after rollback:

| `nodeOSUpgradeChannel` | What happens when rollback TTL expires | What happens when next maintenance window opens |
|---|---|---|
| `None` | Nothing. Pool stays on rolled-back image. | Nothing. Auto-upgrade is disabled. |
| `NodeImage` | Nothing. Pool stays on rolled-back image. | Pool is bumped to latest when autoupgrader fires `upgradeNodeImageVersion`. |
| `SecurityPatch` | Nothing. Pool stays on rolled-back image. | Pool is bumped to the latest security patch VHD when autoupgrader fires. |
| `Unmanaged` | Nothing. Pool stays on rolled-back image. | Nothing. OS patching happens via unattended-upgrade, not VHD rollout. |

Recommended behavior for v1:

1. Block rollback when a rollback request becomes active and recentlyUsedVersions is expired.
2. Preserve recentlyUsedVersions for observability, but do not use it when expired.
3. Do not flag an expired recentlyUsedVersions entry as an error when rollback is not actively requested. Expired rollback state should only matter when a user actively requests rollback.

## Validation and Conditions

Rollback request validation should cover three cases:

1. recentlyUsedVersions is missing or incomplete.
2. recentlyUsedVersions.timestampUsed is older than 7 days.
3. recentlyUsedVersions.orchestratorVersion is not valid for the current rollback request.

These validations should block invalid rollback requests where possible. If admission-time validation cannot evaluate the state transition or status fields cleanly, the reconciler should reject the active rollback request via status and avoid applying it.

Important edge case:

1. If rollback is not actively requested, an expired recentlyUsedVersions entry must not be erroneously flagged. The field is historical rollback eligibility state, not an error by itself.

Add status conditions for operator clarity.

Proposed condition types:

1. ImageRollbackReady
2. ImageRollbackActive

Proposed reasons:

1. RecentlyUsedVersionsNotAvailable
2. RecentlyUsedVersionsExpired
3. OrchestratorVersionMismatch
4. RollbackApplied
5. RollbackIgnored

These conditions avoid silent behavior and explain why rollback did or did not occur.

## Drift and Provisioning Behavior

Rollback should integrate with both existing drift behavior and new node creation by ensuring both paths consume rollback-effective images while rollback is active and valid.

### Existing nodes

When a valid rollback request becomes active:

1. The AKSNodeClass change is observed by Karpenter core.
2. Karpenter core enqueues NodeClaims that reference the changed AKSNodeClass.
3. Karpenter core calls the Azure provider's `IsDrifted()` implementation for each affected NodeClaim.
4. Azure drift logic compares each NodeClaim image against the rollback-effective image set.
5. Existing nodes running the post-upgrade image become drifted and are replaced according to normal disruption controls.

Rollback should not introduce a separate replacement mechanism. It should rely on the existing drift and disruption flow once the effective desired image has changed.

### New scale-ups

When a new pod requires capacity while rollback is active:

1. Karpenter schedules the pod to a new NodeClaim using the matching NodePool and AKSNodeClass.
2. The Azure provider resolves the normal compatible image definition for the selected instance type.
3. Rollback applies the requested image release suffix to that selected image definition.
4. The newly-created machine uses the rollback-effective image ID.

New scale-ups should not be blocked while rollback state is evaluated. If rollback validation fails, provisioning should continue only with normal latest-image behavior after the AKSNodeClass condition clearly reports why rollback was not applied.

### Drift trigger choice

There are two acceptable ways for rollback to make existing nodes drift:

1. NodeClassDrift: if the chosen rollback request field is part of AKSNodeClass spec and participates in AKSNodeClass hashing, changing rollback intent changes the NodeClass hash and existing NodeClaims drift because their stored hash no longer matches.
2. ImageDrift: if rollback changes the effective desired image set, existing NodeClaims drift because their current image no longer matches the rollback-effective images.

The implementation does not need to depend on only one drift reason, but it must make both reasons internally consistent:

1. If rollback fields participate in AKSNodeClass hashing, the hash-driven NodeClassDrift path should be expected and tested.
2. If image drift is expected to identify rollback replacements, the image comparison must use rollback-effective images rather than the latest normally resolved images.
3. New node creation must use the same effective image calculation as drift, regardless of whether rollback is applied by rewriting status.images or by a helper at use sites.

The key invariant is that drift and provisioning must agree on the desired image while rollback is active.

## Decision Notes

### Decision 1: Snapshot storage in status

Conclusion: Use AKSNodeClass status.recentlyUsedVersions.

Rationale:

1. etcd-backed durability across operator restarts.
2. Closest coupling to current status.images lifecycle.
3. Mirrors the AKS RP rollback allowlist shape.
4. No new external state store required.

### Decision 2: Single-entry history

Conclusion: Keep one recently-used version entry only.

Rationale:

1. Aligns with AKS RP recently-used one-entry semantics.
2. Keeps behavior explicit and simple.
3. Reduces state complexity and ambiguity.

Future consideration: if Karpenter stores multiple previous image versions, the API must define whether rollback selects the most recent entry, requires an explicit target, or exposes another selection mechanism.

### Decision 3: 7-day rollback window

Conclusion: Use 7-day TTL from recentlyUsedVersions.timestampUsed.

Rationale:

1. Mirrors current AKS RP rollback window constraints.
2. Encourages rollback as short-term mitigation, not long-term pinning.

## Out of Scope Follow-up Designs

The following are intentionally deferred and must be designed separately:

1. True node image pinning, including k8s version interactions, SLA considerations, and reconciliation behavior for long-duration static versions.
2. Prepared image spec support that accepts full resource ID and maps to a dedicated AKS API field.
3. Decoupling control plane k8s upgrades from node k8s upgrades, including how image resolution should behave when nodes intentionally remain on an older orchestrator version.
4. A broader image API shape that groups image-related controls together. AKSNodeClass now has multiple top-level image-related fields, and a future design should decide whether controls like image family, FIPS mode, rollback, pinning, and prepared images remain separate top-level fields or move under a dedicated image section with explicit compatibility and precedence rules.
5. Future `spec.imageVersion` extensions such as pinned image versions or prepared-image selection. A follow-up design should define whether image intents are mutually exclusive and how rollback, pinning, and prepared images interact.
6. Support-window or stale-image warnings for future image selection support, including pinning and prepared image spec.

## Operational Scenarios

### Scenario A: Upgrade introduces regression

1. status.images moves from version N to N+1.
2. Reconciler snapshots N into recentlyUsedVersions at transition time.
3. Customer requests rollback through the chosen spec.imageVersion UX.
4. Reconciler validates TTL and restores N set into status.images.
5. Drift and replacement bring fleet back to N under existing disruption controls.

### Scenario B: Rollback requested after TTL

1. Customer requests rollback after 7 days.
2. Reconciler marks rollback expired.
3. status.images remains subject to normal image update behavior.
4. TTL expiry alone does not force roll-forward to latest.

## Testing

Minimum test coverage:

1. Snapshot behavior when shouldUpdate=true and image set changes.
2. No snapshot churn when shouldUpdate=false or set unchanged.
3. Rollback apply path with valid recentlyUsedVersions and TTL.
4. Rollback reject/ignore path for missing recentlyUsedVersions.
5. Rollback reject/ignore path for expired TTL.
6. Rollback reject/ignore path for orchestrator version mismatch.
7. Ensure expired recentlyUsedVersions is not flagged when rollback is not actively requested.
8. Condition reason coverage for success and failure paths.
9. Drift interaction test confirming rollback target drives drift decisions via status.images.

## Production Readiness

1. Add metrics for rollback requests, applied rollbacks, expired rollback requests, and missing recently-used state.
2. Add logs or events for rollback decision points, including missing, expired, or mismatched recentlyUsedVersions.
3. Document operator behavior when rollback is expired.
4. Ensure CRD schema and conversion behavior is backward compatible for existing AKSNodeClass objects.

## References

1. [Agent Pools - Get Upgrade Profile REST API](https://learn.microsoft.com/en-us/rest/api/aks/agent-pools/get-upgrade-profile): defines `properties.recentlyUsedVersions` as historical good versions for rollback operations, including `nodeImageVersion`, `orchestratorVersion`, and `timestamp`.
2. [Agent Pools - Create Or Update REST API](https://learn.microsoft.com/en-us/rest/api/aks/agent-pools/create-or-update): defines `properties.nodeImageVersion` and states that setting this value triggers an agent pool rollback and only values from `recentlyUsedVersions` are allowed.
3. [Roll Back Node Pool Versions in AKS](https://learn.microsoft.com/en-us/azure/aks/roll-back-node-pool-version): describes AKS node pool rollback behavior, the seven-day rollback window, auto-upgrade considerations, and the ability to roll back only the node image when only the node image changed.
4. [Upgrade Operating System Version in AKS](https://learn.microsoft.com/en-us/azure/aks/upgrade-os-version): documents OS SKU rollback guidance and OS-version rollback limits that are separate from node image version rollback.

## Open Questions

1. Should expired rollback requests be auto-cleared from spec, or only ignored with status signaling?
2. Should rollback include explicit guardrail checks against cluster auto-upgrade settings, similar to AKS RP constraints?
3. Do we need an admission-time validation webhook for rollback request semantics, or is reconcile-time validation sufficient?
4. Should rollback support only the AKS Machine API path, or should it explicitly support both AKS Machine API and the node bootstrapping client/VM path? Current expectation is that it should work either way because both paths consume status.images, but this should be verified.
5. Does the existing node image cache require rollback-specific invalidation or cache-key changes so that rollback requests and roll-forward after rollback are reflected immediately?
6. When the maintenance window opens and Karpenter selects latest again, should the rollback request be cleared, or should it remain set and become ignored/invalid once recentlyUsedVersions no longer applies?
