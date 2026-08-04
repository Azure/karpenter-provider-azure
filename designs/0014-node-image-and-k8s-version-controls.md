# Node Image and Kubernetes Version Controls for NAP

**Author:** @rakechill

**Last updated:** Jun 26, 2026

**Status:** Proposed

**Related issues:** [Azure/karpenter-provider-azure#1220](https://github.com/Azure/karpenter-provider-azure/issues/1220), [Azure/karpenter-provider-azure#1355](https://github.com/Azure/karpenter-provider-azure/issues/1355)

Note: The issue title says "Add support for setting ImageID for nodeClass", but the problem statement is primarily asking for rollback/pinning capability to recover from bad image releases and the ability to control when new images will be picked up by NAP nodes. This rollback design addresses the bad-release recovery and version control use cases. It does not address the literal title request to set arbitrary ImageID; that is likely a future AKS workstream, and Karpenter should not set that standard prematurely.

## Table of Contents

1. [Overview](#overview)
2. [Background](#background)
   - [Node image version updates](#node-image-version-updates)
   - [Kubernetes version upgrades](#kubernetes-version-upgrades)
   - [Gap this design addresses](#gap-this-design-addresses)
3. [Goals](#goals)
4. [Non-Goals](#non-goals)
5. [API Changes](#api-changes)
   - [API Field Grouping and Wrapper Name](#api-field-grouping-and-wrapper-name)
   - [kubernetesVersion Spec Field](#kubernetesversion-spec-field)
   - [nodeImageVersion Spec Field](#nodeimageversion-spec-field)
   - [AKSNodeClass status: latestImageVersion and recentlyUsedVersions](#aksnodeclass-status-latestimageversion-and-recentlyusedversions)
6. [Reconciliation Design](#reconciliation-design)
   - [Snapshot point](#snapshot-point)
   - [Rollback path](#rollback-path)
   - [Image selection during rollback](#image-selection-during-rollback)
   - [Implementation options for applying rollback](#implementation-options-for-applying-rollback)
7. [Validation and Conditions](#validation-and-conditions)
   - [nodeImageVersion and kubernetesVersion Validation](#nodeimageversion-and-kubernetesversion-validation)
8. [Drift and Provisioning Behavior](#drift-and-provisioning-behavior)
   - [Existing nodes](#existing-nodes)
   - [New scale-ups](#new-scale-ups)
   - [Drift trigger choice](#drift-trigger-choice)
   - [Kubernetes version drift](#kubernetes-version-drift)
9. [Decision Notes](#decision-notes)
10. [Open Questions](#open-questions)
11. [Out of Scope Follow-up Designs](#out-of-scope-follow-up-designs)
12. [Production Readiness](#production-readiness)
13. [References](#references)

## Overview

This document proposes version control capabilities for AKS Node Auto Provisioning (NAP) in Karpenter, including node image version pinning, rollback, and Kubernetes version drift control. These capabilities are aligned with AKS agent pool rollback and upgrade semantics.

The intent is to let customers control which node image version their NAP nodes run on: either by pinning to the current version to prevent automatic upgrades, or by rolling back to the previously used version to recover from a bad image release. Additionally, customers can specify a target Kubernetes version on the NodeClass to decouple node Kubernetes version drift detection from the control plane's current version.

Related but separate workstreams are:

1. Long-duration arbitrary node image pinning beyond current and previously used versions.
2. Prepared image spec support using a full resource ID that maps to an AKS API prepared image field.
3. Future image version selectors for filtering and ranking available image versions.

These workstreams require independent design and are out of scope here.

## Background

AKS node images are VHD-based image versions that are updated frequently. Today, NAP users configure selector-style image controls in spec (for example image family), while resolved concrete images are surfaced in status.

### Node image version updates

Today, the NodeImageReconciler resolves the latest gallery images on every AKSNodeClass event and on a 5-minute requeue. However, it does not always immediately publish those latest versions into `status.images`. If `ImagesReady` is false, it applies latest immediately. Otherwise, it only moves existing image definitions forward when the maintenance window is open; outside that window it preserves the existing versions and only adds newly available SKUs. If no maintenance window is configured for the node OS upgrade schedule, the current behavior fails open and applies latest.

Existing nodes are considered drifted and replaced according to normal disruption controls once the effective image set in `status.images` moves forward. Customers have no suggested mechanism to opt out of this process, defer it beyond maintenance window shaping, or manually trigger it at a time of their choosing.

### Kubernetes version upgrades

NAP node Kubernetes version upgrades behave differently from AKS managed agent pools. When the cluster control plane is upgraded to a new Kubernetes version, Karpenter recognizes the version delta soon after the update (subject to a polling interval) and marks affected nodes as drifted. Replacement is then driven by the standard disruption flow, respecting maintenance windows and disruption budgets, but customers cannot separately stage or defer the node k8s version upgrade the way they can with AKS agent pool upgrade controls.

Additionally, a Kubernetes version upgrade also triggers a node image version refresh: nodes are replaced with the effective node image set resolved for the new Kubernetes version. In practice this typically means moving to the latest compatible node image, subject to the same image-resolution and maintenance-window behavior described above. This means a control plane upgrade causes both a k8s version change and a node image change on NAP nodes simultaneously, neither of which is individually controllable today.

### Gap this design addresses

Current NAP behavior has no rollback affordance and no version control surface:

1. No spec field lets customers request rollback or control node image version rollout manually.
2. No spec field lets customers decouple their NAP nodes' Kubernetes version from the control plane's current version.
3. No status field preserves the previous image set as a first-class rollback target.

## Goals

1. Provide a `nodeImageVersion` spec field that allows customers to pin to their current node image version or roll back to the previously used version.
2. Preserve recently-used version state durably in AKSNodeClass status, including the Kubernetes version paired with the previously used node image.
3. Keep rollback and pinning operationally safe and predictable. Pinning is limited to the current and previously used versions only, following AKS's current policy.
4. Provide a `kubernetesVersion` spec field that allows customers to specify the desired Kubernetes version for drift detection, decoupling node k8s version drift from the cluster control plane version.
5. Surface both the current node image version and the recently used node image version (with its paired Kubernetes version) in AKSNodeClass status.
6. Keep the API design extensible for future image version selectors.

## Non-Goals

1. Implement long-duration or arbitrary image pinning beyond the current and previously used versions.
2. Introduce prepared image spec by full resource ID.
3. Guarantee rollback to arbitrary historical versions.
4. Block provisioning scale-up while rollback state is being evaluated.

## API Changes

### API Field Grouping and Wrapper Name

`nodeImageVersion` and `kubernetesVersion` should be grouped under a dedicated sub-section rather than placed at the top level of spec. Top-level placement would crowd the spec namespace and leave no clean home for future image version selectors.

The proposed shape is:

```yaml
spec:
  versionSelection:
    kubernetesVersion: "1.32"
    nodeImageVersion: "202601.15.0"
    # future:
    selectors:
      validated: "true"
      ring: "stable"
```

**Decision:** use `versionSelection`.

This name keeps the v1 fields narrow and readable while leaving an obvious home for future selection-based inputs. It also avoids overloading terms like "upgrade" or "policy" for behavior that includes pinning and rollback, not just forward movement.

The future `selectors` field should be a map of string key/value pairs rather than a closed set of predefined NodeClass schema fields. The intent is to let customers supply additional image-filtering hints without forcing each selector key to become a first-class CRD field. Selector interpretation remains a future design topic.

### kubernetesVersion Spec Field

The `kubernetesVersion` spec field allows customers to specify a desired Kubernetes version for the NodeClass. When set, Karpenter uses this value for k8s version drift detection instead of the cluster control plane's current version.

**Semantics:**

1. **Unset (default):** Karpenter uses the cluster control plane's current Kubernetes version to determine whether an existing node has a k8s version mismatch. This is the existing behavior.
2. **Set:** Karpenter uses the specified version as the desired k8s version for nodes referencing this NodeClass. Nodes running a different Kubernetes version are treated as drifted and replaced via normal disruption controls.
3. **Changing `kubernetesVersion`:** Follows AKS semantics. When the k8s version changes, the node image resolution flow refreshes to the effective image set for the new Kubernetes version, typically moving toward the latest compatible image subject to the existing maintenance-window behavior. To prevent conflicts between an explicit `nodeImageVersion` and the new Kubernetes version, CEL admission validation requires `nodeImageVersion` to be unset before `kubernetesVersion` can be changed to a new value.

**AKS version skew constraint:**

AKS enforces a version skew policy between the cluster control plane and node pools. Public AKS documentation is not perfectly internally consistent on the exact window: the supported versions FAQ says Kubernetes 1.28+ follows **three minor versions** of skew (N-3), while older agent pool REST descriptions still mention a tighter window. The design assumption here is that Karpenter must satisfy the AKS-supported skew window enforced by the platform and must not allow a node pool version greater than the control plane version.

When `spec.versionSelection.kubernetesVersion` is set, Karpenter must validate that the specified version satisfies this constraint relative to the cluster control plane version before provisioning or during drift evaluation:

1. The specified version must not be greater than the control plane version.
2. The specified version must remain within the AKS-supported minor-version skew window relative to the control plane version.

Violations should be surfaced as a condition on the NodeClass rather than silently accepted, since AKS is expected to reject machine creation once the requested version falls outside the supported skew window.

**Version granularity:** v1 should accept **minor versions only** (for example `1.32`). This matches the simplest interpretation of AKS alias minor version behavior and avoids introducing patch-level drift semantics in the first iteration. When a customer specifies a minor version, Karpenter should treat that minor as the desired version and should not drift nodes solely because a newer patch within the same minor becomes available. Explicit patch-version support can be a follow-up design if customers need it.

### nodeImageVersion Spec Field

The `nodeImageVersion` spec field is the unified customer surface for both node image pinning and rollback. It determines which node image release version Karpenter targets for new provisioning and drift evaluation.

**Semantics:**

1. **Unset (default):** Karpenter preserves the existing NAP image-resolution behavior. It resolves the latest gallery image version on every reconcile, updates `status.latestImageVersion`, and publishes the effective image set into `status.images` according to the current maintenance-window logic.
2. **Set to `status.latestImageVersion`:** Karpenter pins to the latest resolved version. New nodes are provisioned on that version. Automatic node image upgrades are paused — if a newer version becomes available, nodes will not drift until the customer updates or clears the pin.
3. **Set to a value in `status.recentlyUsedVersions[*].imageVersion`:** Karpenter rolls back to that previously used version. The rollback validation rules from `status.recentlyUsedVersions` apply: the requested version must match an entry in the array and the Kubernetes version of that entry must be compatible.
4. **Set to any other value:** CEL admission validation rejects the request. The only valid values are `status.latestImageVersion` (pinning at latest) or a value present in `status.recentlyUsedVersions[*].imageVersion` (rollback to a previous version).

The latest image version is always visible through `status.latestImageVersion`. The previously used version is visible through `status.recentlyUsedVersions`.

**Decision: explicit version string.**

Customers set `nodeImageVersion` to the exact node image release version suffix they want Karpenter to target:

```yaml
spec:
	versionSelection:
    nodeImageVersion: "202601.15.0"
```

Karpenter still derives the image family, architecture, generation, and runtime-specific image definition from the AKSNodeClass and selected instance type. This matters because multiple NodePools can share the same AKSNodeClass while selecting different instance types, which may resolve to different image definitions such as Gen1, Gen2, or Arm64 variants. Customers read the valid values from `status.recentlyUsedVersions[*].imageVersion` (for rollback) or from `status.latestImageVersion` (for pinning at current).

For maintained image families, these resolved Gen1, Gen2, and Arm64 variants are expected to move forward together on the same release version suffix. That makes a single customer-facing `nodeImageVersion` suffix a reasonable v1 API. The design must still tolerate exceptional cases where resolved image definitions for one NodeClass do not share a single suffix, such as frozen variants or other special rollout paths.

This behavior should stay internal to image resolution rather than becoming a customer-facing spec field. If a requested suffix is available for only a subset of resolved image definitions, Karpenter should publish the matching subset into `status.images`, omit the unavailable definitions, and surface a warning condition. If no resolved image definitions have the requested suffix, Karpenter should surface a failure condition and avoid publishing rollback/pin-effective `status.images` for that request.

Behavior:

1. Karpenter validates the requested version against `status.recentlyUsedVersions`.
2. Rollback is rejected if the requested version suffix is not the single recently-used version suffix or the Kubernetes version does not match.
3. For each resolved image definition, Karpenter applies the requested release version suffix instead of requiring the customer to specify a full image string such as `AKSUbuntu-2204gen2containerd-202601.15.0`.

**Alternative considered: boolean rollback flag**

A boolean field (`rollbackToPrevious: true`) was considered, which would let Karpenter automatically select the most recent entry in `status.recentlyUsedVersions` without the customer specifying it. This was rejected because:

1. It does not extend to pinning-at-current, where there is no "previous" to roll back to.
2. A customer could silently roll back to a version they did not intend.
3. If Karpenter later stores multiple previous image versions, a boolean becomes ambiguous without additional API to specify which entry to use.
4. The explicit version string approach mirrors AKS RP semantics, where `nodeImageVersion` is always a concrete version value.

### AKSNodeClass status: latestImageVersion and recentlyUsedVersions

Add a new status section that mirrors the AKS RP recently-used rollback model:

```go
// RecentlyUsedVersion records a node image version that was previously active
// on this NodeClass, used to validate rollback targets.
type RecentlyUsedVersion struct {
	// timestampUsed is the time this node image version was last active,
	// recorded when Karpenter moves status.images forward.
	// +optional
	TimestampUsed metav1.Time `json:"timestampUsed,omitempty"`

	// imageVersion is the AKS node image release version suffix,
	// e.g. "202601.15.0".
	// +optional
	ImageVersion string `json:"imageVersion,omitempty"`

	// kubernetesVersion is the Kubernetes version paired with this image,
	// e.g. "1.32.5".
	// +optional
	KubernetesVersion string `json:"kubernetesVersion,omitempty"`
}
```

```go
type AKSNodeClassStatus struct {
	// images contains the current set of images available to use for the NodeClass.
	// +optional
	Images []NodeImage `json:"images,omitempty"`
	// latestImageVersion is the latest resolved image version suffix from the gallery,
	// updated on every reconcile pass regardless of any active pin or rollback.
	// +optional
	LatestImageVersion string `json:"latestImageVersion,omitempty"`
	// recentlyUsedVersions contains the previously active node image versions
	// in reverse chronological order, used to validate rollback targets.
	// +optional
	RecentlyUsedVersions []RecentlyUsedVersion `json:"recentlyUsedVersions,omitempty"`
}
```

Semantics:

1. recentlyUsedVersions is an array of previously active node image versions in reverse chronological order. Each entry captures the version suffix and Kubernetes version that were in use before status.images advanced.
2. recentlyUsedVersions[*].timestampUsed records when that version was last active, for observability.
3. latestImageVersion always reflects the latest resolved image version suffix from the gallery, regardless of whether rollback or pinning is active. It is updated on every reconcile pass, even when status.images is overwritten with a rolled-back or pinned version. It does not, by itself, indicate whether that version is currently effective in `status.images`.
4. The maximum number of entries retained in recentlyUsedVersions is an open question; retaining more entries extends how far back a rollback target can be pinned (see Out of Scope item 5 on multi-environment staged rollout).
5. If Karpenter stores multiple previous image versions, rollback UX must specify how Karpenter chooses which entry to use.

Assumption for v1: a single AKSNodeClass usually resolves image definitions that share one release version suffix, even when multiple definitions exist for different generations or architectures. If reconcile discovers that a requested or latest suffix is only available for a subset of resolved image definitions, `status.images` contains only that matching subset and Karpenter surfaces a warning condition explaining that the suffix is only partially available for this NodeClass. If no resolved image definitions have the requested suffix, Karpenter surfaces a failure condition and does not publish rollback/pin-effective `status.images` for that request.

## Reconciliation Design

### Snapshot point

NodeImageReconciler in images.go updates `status.latestImageVersion` on every reconcile pass. A snapshot into `status.recentlyUsedVersions` is taken whenever the **effective image version changes**. The three triggers are:

- **Gallery advance becomes effective:** a newer gallery version is published into `status.images` while `nodeImageVersion` is unset, according to the existing maintenance-window and `ImagesReady` behavior.
- **Customer sets `nodeImageVersion`:** effective version changes from what was in `status.images` to the newly requested value.
- **Customer unsets `nodeImageVersion`:** effective version changes from the pinned version back to `status.latestImageVersion`.

In all cases the snapshot captures the version being left, so each entry in `status.recentlyUsedVersions` represents a previously effective version. `status.recentlyUsedVersions[0].kubernetesVersion` (the most recent entry) reflects `spec.versionSelection.kubernetesVersion` if it was set at snapshot time, otherwise the cluster control plane version.

### Rollback path

When `spec.versionSelection.nodeImageVersion` is set to the previously used version:

1. Validate `status.recentlyUsedVersions` is non-empty.
2. Validate the requested `nodeImageVersion` matches an entry in `status.recentlyUsedVersions[*].imageVersion`.
3. Validate the currently desired Kubernetes version is compatible with that entry's `kubernetesVersion`. The desired Kubernetes version is `spec.versionSelection.kubernetesVersion` if set, otherwise the cluster control plane version.
4. If valid, set the effective target image release version suffix to the matched `recentlyUsedVersions` entry's `imageVersion`.
5. Apply the rollback image version suffix across the resolved image definitions, publish any matching subset into `status.images`, and surface a warning if some compatible definitions do not have that suffix.

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

recentlyUsedVersions[0].imageVersion:
202606.08.1

rollback goal image:
/CommunityGalleries/.../images/2204gen2containerd/versions/202606.08.1
```

The same suffix rewrite is applied independently to each resolved image definition. This lets multiple NodePools share one AKSNodeClass while still rolling back to the image variant selected by each NodePool's instance type requirements.

Before using rolled-back images, Karpenter should verify which resolved image definitions actually have the requested release suffix.

1. Definitions that do not have the requested suffix are omitted from `status.images`, while definitions that do have it remain publishable.
2. Karpenter surfaces a warning condition so operators know that some compatible variants are temporarily unavailable for provisioning.
3. If no resolved image definitions have the requested suffix, Karpenter surfaces a failure condition and does not publish rollback-effective `status.images`.

### Implementation options for applying rollback

Two options exist for where to apply the version suffix rewrite:

- **Option 1 — Keep `status.images` as latest, convert at use sites:** `status.images` always holds the latest resolved images. A helper (e.g. `convertToRolledBackImage`) rewrites the version suffix at each consumer call site (provisioning, drift).
- **Option 2 — Write effective images into `status.images`:** The reconciler applies the version suffix rewrite before publishing `status.images`. All consumers automatically receive the correct images with no call-site changes. A separate `status.latestImageVersion` field always tracks the latest gallery version regardless of the active pin.

**Decision: Option 2.**

`status.images` always contains the effective image IDs Karpenter will use for provisioning and drift. When `spec.versionSelection.nodeImageVersion` is set, the reconciler rewrites every resolved image ID to `/versions/<nodeImageVersion>` before publishing. When unset, `status.images` continues to follow the existing image-resolution behavior, which may lag the latest gallery version until maintenance-window logic allows the new version to become effective.

`status.latestImageVersion` is always updated to the latest gallery version regardless of the active pin, serving two purposes: (1) it is the valid "pin at current" target for CEL validation, and (2) it lets operators see whether a newer version is available while the cluster is pinned. It is intentionally a gallery-view field, not a guarantee that the same version is currently effective in `status.images`.

Option 1 was rejected because a missed call site would silently provision the wrong image — Option 2 is safer by default.

## Validation and Conditions

Rollback validation rejects a request when `recentlyUsedVersions` is empty, or when the desired Kubernetes version is incompatible with the matched entry's `kubernetesVersion`. If admission-time validation cannot cleanly evaluate status fields, the reconciler rejects the request via condition.

**Proposed conditions:**

| Condition type | Reason | Meaning |
|---|---|---|
| `ImageRollbackReady` | `RecentlyUsedVersionsNotAvailable` | No valid rollback target exists |
| `ImageRollbackActive` | `RollbackApplied` | Rollback is active and applied |
| `ImageRollbackActive` | `RollbackIgnored` | Rollback was requested but not applied |
| `ImageRollbackActive` | `KubernetesVersionMismatch` | k8s version incompatible with rollback target |
| `NodeImageVersionPinned` | `ImageVersionPartiallyAvailable` | Requested image version exists for only a subset of resolved image definitions; provisioning continues using the matching subset |
| `NodeImageVersionPinned` | `ImageVersionUnavailable` | None of the resolved image definitions have the requested suffix, so no effective image set can be published |
| `NodeImageVersionPinned` | `ImageVersionPinnedAtCurrent` | Pinned to latest; auto-upgrades paused |
| `NodeImageVersionPinned` | `ImageVersionPinnedAtPrevious` | Pinned to previous version; rollback active |
| `KubernetesVersionControlled` | `KubernetesVersionMismatch` | Node k8s version differs from `spec.versionSelection.kubernetesVersion` |

### nodeImageVersion and kubernetesVersion Validation

In addition to rollback-specific validation, the following rules apply to `nodeImageVersion` and `kubernetesVersion`:

**CEL admission validation for `nodeImageVersion`:**

`spec.versionSelection.nodeImageVersion` must equal `status.latestImageVersion` (pin at current) or one of the `imageVersion` values in `status.recentlyUsedVersions` (rollback). All other values are rejected. Note: status-dependent CEL rules may require a webhook validator; reconcile-time validation with a clear condition is acceptable as a fallback.

If the resolved image definitions for one NodeClass do not all have the requested suffix available, Karpenter accepts the request, publishes the subset of matching image definitions into `status.images`, omits the non-matching definitions, and surfaces a warning condition. If no matching image definitions remain, Karpenter surfaces a failure condition and does not publish rollback/pin-effective `status.images` for that request.

**CEL admission validation for `kubernetesVersion`:**

1. Changing `kubernetesVersion` requires `nodeImageVersion` to be unset first — prevents conflicts between a pinned image and a new k8s version.
2. The specified version must satisfy the AKS-supported skew constraint: not greater than the control plane, and within the platform-supported minor-version skew window. Violations surface as a NodeClass condition.

## Drift and Provisioning Behavior

Because `status.images` always contains the effective image IDs (pinned or latest), existing drift and provisioning paths consume rollback-effective images automatically.

### Existing nodes

When `spec.versionSelection.nodeImageVersion` changes, it changes the AKSNodeClass hash, enqueuing affected NodeClaims. The drift logic compares each NodeClaim's current image against `status.images`. Nodes not on the effective version are drifted and replaced via normal disruption controls — no separate replacement mechanism is needed.

### New scale-ups

New NodeClaims are provisioned using `status.images` directly, which already contains the effective version.

If a requested suffix is only partially available, provisioning continues for the matching subset in `status.images` and only the omitted image definitions become temporarily unavailable. If resolution produces no matching images, scale-up for that pin or rollback request is blocked and the NodeClass condition explains that the requested image version could not be resolved for any compatible definition.

### Drift trigger choice

`spec.versionSelection` fields participate in AKSNodeClass hashing, so any change to `nodeImageVersion` or `kubernetesVersion` triggers NodeClassDrift on affected NodeClaims. Image-level drift (comparing a node's current image against `status.images`) also fires if the effective image set changes. Both paths must agree on the desired image — the invariant is that drift comparison and new node provisioning always use the same `status.images`.

### Kubernetes version drift

When `spec.versionSelection.kubernetesVersion` is set, Karpenter uses it as the desired k8s version for drift detection instead of the cluster control plane version. Nodes running a different version are drifted and replaced. New nodes are provisioned with the image compatible with the specified version. If unset, drift falls back to comparing against the control plane's current version. `kubernetesVersion` participates in AKSNodeClass hashing, so changing it triggers NodeClassDrift.

## Decision Notes

### Decision 1: Snapshot storage in status

Conclusion: Use AKSNodeClass status.recentlyUsedVersions.

Rationale:

1. etcd-backed durability across operator restarts.
2. Closest coupling to current status.images lifecycle.
3. Mirrors the AKS RP rollback allowlist shape.
4. No new external state store required.

### Decision 2: Array-based version history

Conclusion: Use `[]RecentlyUsedVersion` (array) rather than a single pointer.

Rationale:

1. Matches the AKS RP preview API shape (`recentlyUsedVersions` is an array there too).
2. Enables multi-environment staged rollout scenarios by retaining more than one historical version.
3. The maximum number of entries to retain is an open question; a single entry would match current AKS RP behavior, while more entries extend rollback reach.

Future consideration: the API must define how rollback selects the target entry when multiple are present — most recent match, explicit index, or another mechanism. See Out of Scope Follow-up Design item 5.

## Open Questions

1. Do we need an admission-time validation webhook for rollback request semantics, or is reconcile-time validation sufficient? This is particularly relevant for `nodeImageVersion` validation against status fields, which may not be accessible to standard CRD CEL validators at admission time.
2. Should rollback support only the AKS Machine API path, or should it explicitly support both AKS Machine API and the node bootstrapping client/VM path? Current expectation is that it should work either way because both paths consume status.images, but this should be verified.
3. Does the existing node image cache require rollback-specific invalidation or cache-key changes so that rollback requests and roll-forward after rollback are reflected immediately?
4. When auto-upgrade or a future image policy moves the pool forward after rollback, should the rollback request be cleared, or should it remain set and become ignored/invalid?
5. In a future follow-up, should `kubernetesVersion` also accept full patch versions (e.g. `1.32.5`), and if so should that mean exact patch pinning or another semantics?
6. If a NodeClass ever resolves image definitions with different latest version suffixes, is a single `latestImageVersion` plus partial-availability warning sufficient, or do we eventually need a more precise per-definition status model?

## Out of Scope Follow-up Designs

The following are intentionally deferred and must be designed separately:

1. Long-duration arbitrary node image pinning beyond the current and previously used versions. The `nodeImageVersion` spec field in this design supports pinning to the current or previously used version only. Pinning to arbitrary historical versions, SLA considerations for long-duration pinned clusters, and full image lifecycle management are not covered here.
2. Prepared image spec support that accepts full resource ID and maps to a dedicated AKS API field.
3. Image version selectors for filtering and ranking available node image versions. This design leaves room for a future `spec.versionSelection.selectors` map of string key/value pairs alongside `nodeImageVersion` and `kubernetesVersion`, but does not specify selector semantics, supported keys, precedence, or whether selectors are mutually exclusive or composable with explicit version fields.
4. Support-window or stale-image warnings for long-duration pinned images.
5. **Multi-environment staged rollout across clusters.** The current design stores only one `recentlyUsedVersions` entry per NodeClass. This means a cluster can only target the current latest or the immediately preceding image version. For staged rollout pipelines where validation takes longer than one image release cycle — for example, dev validates `202607.15.0` while `202608.15.0` and `202609.15.0` have already shipped — downstream clusters can no longer pin to the validated version because it has been evicted from the single-entry history. Supporting reliable multi-cluster staged rollout will require storing multiple `recentlyUsedVersions` entries (a list of the last N versions) rather than a single pointer, so that a validated version remains reachable for pinning even if several newer versions have since been released.

## Production Readiness
