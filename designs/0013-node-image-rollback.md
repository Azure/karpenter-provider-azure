# Node Image Rollback for NAP

**Author:** @rakechill

**Last updated:** Jun 26, 2026

**Status:** Proposed

**Related issue:** [Azure/karpenter-provider-azure#1220](https://github.com/Azure/karpenter-provider-azure/issues/1220)

Note: The issue title says "Add support for setting ImageID for nodeClass", but the problem statement is primarily asking for rollback/pinning capability to recover from bad image releases. This rollback design addresses the bad-release recovery use case. It does not address the literal title request to set arbitrary ImageID; that is likely a future AKS workstream, and Karpenter should not set that standard prematurely.

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

	// nodeImageVersion is the AKS node image version string used for rollback,
	// e.g. "AKSUbuntu-2204gen2containerd-202601.15.0".
	NodeImageVersion string `json:"nodeImageVersion,omitempty"`

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

1. recentlyUsedVersions captures the immediate prior node image version and orchestrator version pair before status.images advances.
2. recentlyUsedVersions.timestampUsed is static and is used to enforce the 7-day rollback eligibility window.
3. Only one recently-used entry is retained, matching AKS RP semantics.
4. If Karpenter stores multiple previous image versions in the future, rollback UX must specify how Karpenter chooses which previous version to use.

### Customer experience options

AKS agent pool rollback requires customers to specify a concrete node image version. The only valid rollback targets are versions that appear in the agent pool's recentlyUsedVersions list with a matching orchestrator version and valid timestamp. A boolean rollback field would therefore introduce different semantics from the AKS RP API by asking Karpenter to choose the rollback target on the customer's behalf.

NAP has two possible UX shapes:

#### Option A: Explicit rollback target

Customers specify the exact node image version to roll back to:

```yaml
spec:
  imageVersion:
    rollbackTo: AKSUbuntu-2204gen2containerd-202601.15.0
```

Behavior:

1. Karpenter validates the requested version against status.recentlyUsedVersions.
2. Rollback is rejected if the requested version is not the single recently-used version, the orchestrator version does not match, or the timestamp is older than 7 days.

Pros:

1. Mirrors AKS RP semantics closely.
2. Makes customer intent explicit.
3. Avoids surprise rollbacks to an image the customer did not realize was the previously used version.

Cons:

1. Requires customers to inspect status and copy the exact rollback value.
2. Requires customers to specify portions of the image version that are already represented by existing AKSNodeClass image-family fields.
3. Slightly more cumbersome during incident response.

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

### Alternative considered: move imageFamily and fipsMode under an image section

Current AKSNodeClass image controls are split across top-level fields. One option is to eventually group image controls under a dedicated section.

This is not the primary choice for the rollback workstream.

For now, rollback should remain focused within `spec.imageVersion`; the exact rollback request field should be chosen from the customer experience options above.

Possible follow-up direction (discussion item only):

```go
type AKSNodeClassSpec struct {
	// existing fields...
	Image *ImageSpec `json:"image,omitempty"`

	// Existing compatibility fields (kept during transition):
	ImageFamily *string   `json:"imageFamily,omitempty"`
	FIPSMode    *FIPSMode `json:"fipsMode,omitempty"`
}

type ImageSpec struct {
	Family *string   `json:"family,omitempty"`
	FIPSMode *FIPSMode `json:"fipsMode,omitempty"`
	Version *ImageVersionSpec `json:"version,omitempty"`
}
```

If this is pursued later, it should be handled in a separate API-shape design with explicit compatibility and precedence rules.

## Reconciliation Design

### Snapshot point

NodeImageReconciler in images.go is the canonical place to capture rollback state.

Before status.images is overwritten due to a detected version update:

1. Snapshot the current node image version and orchestrator version into status.recentlyUsedVersions.
2. Set status.recentlyUsedVersions.timestampUsed to now.
3. Then write newly resolved images into status.images.

This location is authoritative because both old and new sets are simultaneously available only at this transition.

### Rollback path

When rollback is requested through the chosen `spec.imageVersion` UX:

1. Validate recentlyUsedVersions exists and has a nodeImageVersion.
2. Validate now - recentlyUsedVersions.timestampUsed is within TTL.
3. Validate recentlyUsedVersions.orchestratorVersion is compatible with the current rollback request.
4. If valid, set effective target image version to recentlyUsedVersions.nodeImageVersion.
5. Reconcile status.images to that target version.

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

1. Block rollback when the request transitions from false/unset to true and recentlyUsedVersions is expired.
2. Preserve recentlyUsedVersions for observability, but do not use it when expired.
3. Do not flag an expired recentlyUsedVersions entry as an error when rollbackToPrevious is false or unset. Expired rollback state should only matter when a user actively requests rollback.

## Validation and Conditions

Rollback request validation should cover three cases:

1. recentlyUsedVersions is missing or incomplete.
2. recentlyUsedVersions.timestampUsed is older than 7 days.
3. recentlyUsedVersions.orchestratorVersion is not valid for the current rollback request.

These validations should block setting rollbackToPrevious where possible. If admission-time validation cannot evaluate the state transition or status fields cleanly, the reconciler should reject the active rollback request via status and avoid applying it.

Important edge case:

1. If rollbackToPrevious is false or unset, an expired recentlyUsedVersions entry must not be erroneously flagged. The field is historical rollback eligibility state, not an error by itself.

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

Rollback should integrate with both existing drift behavior and new node creation by making status.images reflect rollback target images during active rollback.

Expected effects:

1. Existing nodes on post-upgrade images become drifted and are replaced according to disruption controls.
2. New scale-ups use rolled-back image IDs while rollback remains active and valid.
3. No special scale-up blocking is introduced.

This means rollback must be applied before both:

1. Drift compares existing NodeClaim image state against AKSNodeClass status.images.
2. Provisioning resolves an image for a newly-created node.

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

## Future Extensions for imageVersion

`spec.imageVersion` is intentionally minimal for v1 rollback and can be extended later.

Potential future fields:

1. `pin` (string): pin to a specific image version. If not specified, behavior defaults to latest.
2. `usePreparedImage` (bool): when true, use prepared image spec from the MC object.

### Interaction with future image controls

As future work introduces pinning and prepared image spec support, this rollback design will overlap with:

1. Decoupling control plane k8s upgrades from node k8s upgrades.
2. Defining image source/selection precedence when multiple controls are present.

Proposed baseline hierarchy for design consistency:

1. Exactly one of the following image intents may be set at a time.
2. Allowed intents: rollback, pinned image, prepared image spec.

Rollback behavior under mixed configuration:

1. If pinned image is specified, rollback is a no-op.
2. If prepared image spec is specified, rollback is a no-op.
3. Preferably, mixed intent should be blocked by AKSNodeClass validation rather than silently accepted.

Note: During detailed design of pinning and prepared image spec, semantics may evolve. Even if wording changes, this document calls out the expected one-of model and precedence intent to prevent ambiguous behavior.

TODO:

1. Confirm prepared image spec contract/docs and whether the prepared image reference is supplied at MC level or AP level.

## Observability Options

This rollback design does not introduce support-window warnings for stale images. Those warnings may become relevant in the context of future image selection support, including pinning and prepared image spec.

Options to evaluate later:

1. Metrics for rollback requests, applied rollbacks, expired rollback requests, missing recently-used state, and potentially image age.
2. Logs for rollback decision points, including missing/expired/mismatched recentlyUsedVersions.
3. AKSNodeClass warning status conditions for future stale-image or support-window signaling.

## Operational Scenarios

### Scenario A: Upgrade introduces regression

1. status.images moves from version N to N+1.
2. Reconciler snapshots N into recentlyUsedVersions at transition time.
3. Customer sets rollbackToPrevious=true.
4. Reconciler validates TTL and restores N set into status.images.
5. Drift and replacement bring fleet back to N under existing disruption controls.

### Scenario B: Rollback requested after TTL

1. Customer sets rollbackToPrevious=true after 7 days.
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
7. Ensure expired recentlyUsedVersions is not flagged when rollbackToPrevious is false or unset.
8. Condition reason coverage for success and failure paths.
9. Drift interaction test confirming rollback target drives drift decisions via status.images.

## Production Readiness

1. Decide which observability options to implement first: metrics, logs, and/or AKSNodeClass warning status conditions.
2. Add eventing/log entries for rollback decision points.
3. Document operator behavior when rollback is expired.
4. Ensure CRD schema and conversion behavior is backward compatible for existing AKSNodeClass objects.

## Open Questions

1. Should expired rollback requests be auto-cleared from spec, or only ignored with status signaling?
2. Should rollback include explicit guardrail checks against cluster auto-upgrade settings, similar to AKS RP constraints?
3. Do we need an admission-time validation webhook for rollbackToPrevious semantics, or is reconcile-time validation sufficient?
4. Should rollback support only the AKS Machine API path, or should it explicitly support both AKS Machine API and the node bootstrapping client/VM path? Current expectation is that it should work either way because both paths consume status.images, but this should be verified.
5. Does the existing node image cache require rollback-specific invalidation or cache-key changes so that rollback requests and roll-forward after rollback are reflected immediately?
6. When the maintenance window opens and Karpenter selects latest again, should rollbackToPrevious be reset to false, or should it remain set and become ignored/invalid once recentlyUsedVersions no longer applies?
