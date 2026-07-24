# Node Image and Kubernetes Version Controls for NAP

**Author:** @rakechill

**Last updated:** Jun 26, 2026

**Status:** Proposed

**Related issues:** [Azure/karpenter-provider-azure#1220](https://github.com/Azure/karpenter-provider-azure/issues/1220), [Azure/karpenter-provider-azure#1355](https://github.com/Azure/karpenter-provider-azure/issues/1355)

Note: The issue title says "Add support for setting ImageID for nodeClass", but the problem statement is primarily asking for rollback/pinning capability to recover from bad image releases. This rollback design addresses the bad-release recovery use case. It does not address the literal title request to set arbitrary ImageID; that is likely a future AKS workstream, and Karpenter should not set that standard prematurely.

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

When a new node image version becomes available, Karpenter automatically picks it up and surfaces it in `status.images`. Existing nodes running the prior image are then considered drifted and replaced according to normal disruption controls, subject to maintenance windows and disruption budgets. Customers have no mechanism today to opt out of this process, defer it, or manually trigger it at a time of their choosing: the upgrade happens whenever the disruption controller determines it is safe to act.

### Kubernetes version upgrades

NAP node Kubernetes version upgrades behave differently from AKS managed agent pools. When the cluster control plane is upgraded to a new Kubernetes version, Karpenter immediately recognizes the version delta and marks affected nodes as drifted. Replacement is then driven by the standard disruption flow, respecting maintenance windows and disruption budgets, but customers cannot separately stage or defer the node k8s version upgrade the way they can with AKS agent pool upgrade controls.

Additionally, a Kubernetes version upgrade also triggers a node image version refresh: nodes are replaced with the latest node image compatible with the new Kubernetes version. This means a control plane upgrade causes both a k8s version change and a node image change on NAP nodes simultaneously, neither of which is individually controllable today.

### Gap this design addresses

Current NAP behavior has no rollback affordance and no version control surface:

1. No spec field lets customers request rollback or pin to a specific node image version.
2. No spec field lets customers decouple their NAP nodes' Kubernetes version from the control plane's current version.
3. No status field preserves the previous image set as a first-class rollback target.
4. Drift logic replaces nodes that are not on the current status image set, but does not model rollback intent.

AKS RP rollback semantics rely on a recently-used allowlist. NAP currently has no equivalent mechanism.

## Goals

1. Provide a `nodeImageVersion` spec field that allows customers to pin to their current node image version or roll back to the previously used version.
2. Preserve recently-used version state durably in AKSNodeClass status, including the Kubernetes version paired with the previously used node image.
3. Keep rollback and pinning operationally safe and predictable. Pinning is limited to the current and previously used versions only.
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
  <wrapper>:
    kubernetesVersion: "1.32"
    nodeImageVersion: "202601.15.0"
    # future: selectors: [...]
```

The open question is the name of the wrapper. Three candidates:

| Name | Pros | Cons |
|---|---|---|
| `manualUpgrade` | Short. Clearly signals opt-out of automatic version management. "Manual" is the natural opposite of "auto". | "Upgrade" implies forward movement — rolling back or pinning at current is not strictly an upgrade. |
| `upgradeControl` | Directionally neutral — covers upgrading, pinning, and rolling back without implying movement. Reads naturally in a Kubernetes API context. | Slightly more abstract. Does not immediately convey the opt-out-of-auto framing. |
| `manualVersionControl` | Accurate description of what the fields do. | "Version control" carries strong source-control connotations (git, svn). Longest of the three. |

**Recommendation:** `upgradeControl` — semantically precise, direction-neutral, and fits the Kubernetes API style. `manualUpgrade` is a strong second if the opt-out-of-auto signal is prioritized over directional neutrality.

### kubernetesVersion Spec Field

The `kubernetesVersion` spec field allows customers to specify a desired Kubernetes version for the NodeClass. When set, Karpenter uses this value for k8s version drift detection instead of the cluster control plane's current version.

**Semantics:**

1. **Unset (default):** Karpenter uses the cluster control plane's current Kubernetes version to determine whether an existing node has a k8s version mismatch. This is the existing behavior.
2. **Set:** Karpenter uses the specified version as the desired k8s version for nodes referencing this NodeClass. Nodes running a different Kubernetes version are treated as drifted and replaced via normal disruption controls.
3. **Changing `kubernetesVersion`:** Follows AKS semantics. When the k8s version changes, the node image version is automatically refreshed to the latest compatible image for the new Kubernetes version. To prevent conflicts between an explicit `nodeImageVersion` and the new Kubernetes version, CEL admission validation requires `nodeImageVersion` to be unset before `kubernetesVersion` can be changed to a new value.

**AKS version skew constraint:**

AKS enforces a version skew policy between the cluster control plane and node pools. For clusters running Kubernetes 1.28 and later, the node pool Kubernetes version must be within **three minor versions** of the control plane (N-3). The node pool version also cannot be greater than the control plane version. This is documented in the [AKS supported Kubernetes versions FAQ](https://learn.microsoft.com/en-us/azure/aks/supported-kubernetes-versions#what-is-the-allowed-difference-in-versions-between-the-control-plane-and-node-pools).

When `spec.upgradeControl.kubernetesVersion` is set, Karpenter must validate that the specified version satisfies this constraint relative to the cluster control plane version before provisioning or during drift evaluation:

1. The specified version must not be greater than the control plane version.
2. The specified version must not be more than three minor versions behind the control plane version (for Kubernetes 1.28+).

Violations should be surfaced as a condition on the NodeClass rather than silently accepted, since AKS will reject the machine creation at the RP level if the skew constraint is violated.

**Version granularity:** Whether this field accepts patch versions (e.g. `1.32.5`) or only minor versions (e.g. `1.32`) is an open question. AKS agent pool behavior needs to be researched before finalizing accepted values. See Open Questions.

### nodeImageVersion Spec Field

The `nodeImageVersion` spec field is the unified customer surface for both node image pinning and rollback. It determines which node image release version Karpenter targets for new provisioning and drift evaluation.

**Semantics:**

1. **Unset (default):** Karpenter always resolves and uses the latest available node image version. This is the existing NAP behavior.
2. **Set to `status.latestImageVersion`:** Karpenter pins to the latest resolved version. New nodes are provisioned on that version. Automatic node image upgrades are paused — if a newer version becomes available, nodes will not drift until the customer updates or clears the pin.
3. **Set to `status.recentlyUsedVersions.imageVersion`:** Karpenter rolls back to the previously used version. The rollback validation rules from `status.recentlyUsedVersions` apply: the requested version must match `status.recentlyUsedVersions.imageVersion` and the Kubernetes version must be compatible.
4. **Set to any other value:** CEL admission validation rejects the request. The only valid values are `status.latestImageVersion` (pinning at latest) or `status.recentlyUsedVersions.imageVersion` (rollback to previous).

The latest image version is always visible through `status.latestImageVersion`. The previously used version is visible through `status.recentlyUsedVersions`.

**Decision: explicit version string.**

Customers set `nodeImageVersion` to the exact node image release version suffix they want Karpenter to target:

```yaml
spec:
  upgradeControl:
    nodeImageVersion: "202601.15.0"
```

Karpenter still derives the image family, architecture, generation, and runtime-specific image definition from the AKSNodeClass and selected instance type. This matters because multiple NodePools can share the same AKSNodeClass while selecting different instance types, which may resolve to different image definitions such as Gen1, Gen2, or Arm64 variants. Customers read the valid values from `status.recentlyUsedVersions.imageVersion` (for rollback) or from `status.latestImageVersion` (for pinning at current).

Behavior:

1. Karpenter validates the requested version against `status.recentlyUsedVersions`.
2. Rollback is rejected if the requested version suffix is not the single recently-used version suffix or the Kubernetes version does not match.
3. For each resolved image definition, Karpenter applies the requested release version suffix instead of requiring the customer to specify a full image string such as `AKSUbuntu-2204gen2containerd-202601.15.0`.

**Alternative considered: boolean rollback flag**

A boolean field (`rollbackToPrevious: true`) was considered, which would let Karpenter automatically select `status.recentlyUsedVersions.imageVersion` without the customer specifying it. This was rejected because:

1. It does not extend to pinning-at-current, where there is no "previous" to roll back to.
2. A customer could silently roll back to a version they did not intend.
3. If Karpenter later stores multiple previous image versions, a boolean becomes ambiguous without additional API to specify which entry to use.
4. The explicit version string approach mirrors AKS RP semantics, where `nodeImageVersion` is always a concrete version value.

### AKSNodeClass status: latestImageVersion and recentlyUsedVersions

Add a new status section that mirrors the AKS RP recently-used rollback model:

```go
type RecentlyUsedVersions struct {
	// timestampUsed is the time this node image version was last active,
	// recorded for observability when Karpenter moves status.images forward.
	TimestampUsed metav1.Time `json:"timestampUsed,omitempty"`

	// imageVersion is the AKS node image release version suffix used for rollback,
	// e.g. "202601.15.0".
	ImageVersion string `json:"imageVersion,omitempty"`

	// kubernetesVersion is the Kubernetes version paired with this image,
	// e.g. "1.32.5".
	KubernetesVersion string `json:"kubernetesVersion,omitempty"`
}
```

```go
type AKSNodeClassStatus struct {
	// existing fields...
	Images               []NodeImage             `json:"images,omitempty"`
	LatestImageVersion   string                  `json:"latestImageVersion,omitempty"`
	RecentlyUsedVersions *RecentlyUsedVersions   `json:"recentlyUsedVersions,omitempty"`
}
```

Semantics:

1. recentlyUsedVersions captures the immediate prior node image release version suffix and Kubernetes version pair before status.images advances.
2. recentlyUsedVersions.timestampUsed records when the previous version was last active, for observability.
3. latestImageVersion always reflects the latest resolved image version suffix, regardless of whether rollback or pinning is active. It is updated on every reconcile pass, even when status.images is overwritten with a rolled-back or pinned version.
4. Only one recently-used entry is retained, matching AKS RP semantics.
5. If Karpenter stores multiple previous image versions in the future, rollback UX must specify how Karpenter chooses which previous version to use.

## Reconciliation Design

### Snapshot point

NodeImageReconciler in images.go updates `status.latestImageVersion` on every reconcile pass. A snapshot into `status.recentlyUsedVersions` is taken whenever the **effective image version changes**. The three triggers are:

- **Gallery advance:** `status.latestImageVersion` moves to a newer version while `nodeImageVersion` is unset.
- **Customer sets `nodeImageVersion`:** effective version changes from what was in `status.images` to the newly requested value.
- **Customer unsets `nodeImageVersion`:** effective version changes from the pinned version back to `status.latestImageVersion`.

In all cases the snapshot captures the version being left, so `status.recentlyUsedVersions.imageVersion` always represents "the previous effective version before the current one." `status.recentlyUsedVersions.kubernetesVersion` reflects `spec.upgradeControl.kubernetesVersion` if set, otherwise the cluster control plane version.

### Rollback path

When `spec.upgradeControl.nodeImageVersion` is set to the previously used version:

1. Validate `status.recentlyUsedVersions` exists and has an `imageVersion`.
2. Validate the requested `nodeImageVersion` matches `status.recentlyUsedVersions.imageVersion`.
3. Validate the currently desired Kubernetes version is compatible with `status.recentlyUsedVersions.kubernetesVersion`. The desired Kubernetes version is `spec.upgradeControl.kubernetesVersion` if set, otherwise the cluster control plane version.
4. If valid, set the effective target image release version suffix to `status.recentlyUsedVersions.imageVersion`.
5. Apply the rollback image version suffix to `status.images` per the implementation decision (Option 2).

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

**Decision: write rolled-back images into `status.images` (Option 2).**

`status.images` always contains the effective image IDs Karpenter will use for provisioning and drift. When `spec.upgradeControl.nodeImageVersion` is set, the reconciler rewrites every resolved image ID to `/versions/<nodeImageVersion>` before publishing. When unset, `status.images` contains the latest resolved images. All existing consumers of `status.images` automatically pick up the correct images without call-site changes.

`status.latestImageVersion` is always updated to the latest gallery version regardless of the active pin, serving two purposes: (1) it is the valid "pin at current" target for CEL validation, and (2) it lets operators see whether a newer version is available while the cluster is pinned.

**Alternative considered:** keeping `status.images` as latest and converting at each consumer call site was rejected because a missed call site would silently provision the wrong image.

## Validation and Conditions

Rollback validation rejects a request when `recentlyUsedVersions` is missing or incomplete, or when the desired Kubernetes version is incompatible with `recentlyUsedVersions.kubernetesVersion`. If admission-time validation cannot cleanly evaluate status fields, the reconciler rejects the request via condition.

**Proposed conditions:**

| Condition type | Reason | Meaning |
|---|---|---|
| `ImageRollbackReady` | `RecentlyUsedVersionsNotAvailable` | No valid rollback target exists |
| `ImageRollbackActive` | `RollbackApplied` | Rollback is active and applied |
| `ImageRollbackActive` | `RollbackIgnored` | Rollback was requested but not applied |
| `ImageRollbackActive` | `KubernetesVersionMismatch` | k8s version incompatible with rollback target |
| `NodeImageVersionPinned` | `ImageVersionPinnedAtCurrent` | Pinned to latest; auto-upgrades paused |
| `NodeImageVersionPinned` | `ImageVersionPinnedAtPrevious` | Pinned to previous version; rollback active |
| `KubernetesVersionControlled` | `KubernetesVersionMismatch` | Node k8s version differs from `spec.upgradeControl.kubernetesVersion` |

### nodeImageVersion and kubernetesVersion Validation

In addition to rollback-specific validation, the following rules apply to `nodeImageVersion` and `kubernetesVersion`:

**CEL admission validation for `nodeImageVersion`:**

`spec.upgradeControl.nodeImageVersion` must equal `status.latestImageVersion` (pin at current) or `status.recentlyUsedVersions.imageVersion` (rollback). All other values are rejected. Note: status-dependent CEL rules may require a webhook validator; reconcile-time validation with a clear condition is acceptable as a fallback.

**CEL admission validation for `kubernetesVersion`:**

1. Changing `kubernetesVersion` requires `nodeImageVersion` to be unset first — prevents conflicts between a pinned image and a new k8s version.
2. The specified version must satisfy the AKS skew constraint: not greater than the control plane, and not more than three minor versions behind (Kubernetes 1.28+). Violations surface as a NodeClass condition.

## Drift and Provisioning Behavior

Because `status.images` always contains the effective image IDs (pinned or latest), existing drift and provisioning paths consume rollback-effective images automatically.

### Existing nodes

When `spec.upgradeControl.nodeImageVersion` changes, it changes the AKSNodeClass hash, enqueuing affected NodeClaims. The drift logic compares each NodeClaim's current image against `status.images`. Nodes not on the effective version are drifted and replaced via normal disruption controls — no separate replacement mechanism is needed.

### New scale-ups

New NodeClaims are provisioned using `status.images` directly, which already contains the effective version. Scale-up is never blocked by rollback state; if validation has failed, provisioning falls back to the latest image and the NodeClass condition explains why.

### Drift trigger choice

`spec.upgradeControl` fields participate in AKSNodeClass hashing, so any change to `nodeImageVersion` or `kubernetesVersion` triggers NodeClassDrift on affected NodeClaims. Image-level drift (comparing a node's current image against `status.images`) also fires if the effective image set changes. Both paths must agree on the desired image — the invariant is that drift comparison and new node provisioning always use the same `status.images`.

### Kubernetes version drift

When `spec.upgradeControl.kubernetesVersion` is set, Karpenter uses it as the desired k8s version for drift detection instead of the cluster control plane version. Nodes running a different version are drifted and replaced. New nodes are provisioned with the image compatible with the specified version. If unset, drift falls back to comparing against the control plane's current version. `kubernetesVersion` participates in AKSNodeClass hashing, so changing it triggers NodeClassDrift.

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

Future consideration: if Karpenter stores multiple previous image versions, the API must define whether rollback selects the most recent entry, requires an explicit target, or exposes another selection mechanism. See Out of Scope Follow-up Design item 5.

## Open Questions

1. Should rollback include explicit guardrail checks against cluster auto-upgrade settings, similar to AKS RP constraints?
2. Do we need an admission-time validation webhook for rollback request semantics, or is reconcile-time validation sufficient? This is particularly relevant for `nodeImageVersion` validation against status fields, which may not be accessible to standard CRD CEL validators at admission time.
3. Should rollback support only the AKS Machine API path, or should it explicitly support both AKS Machine API and the node bootstrapping client/VM path? Current expectation is that it should work either way because both paths consume status.images, but this should be verified.
4. Does the existing node image cache require rollback-specific invalidation or cache-key changes so that rollback requests and roll-forward after rollback are reflected immediately?
5. When auto-upgrade or a future image policy moves the pool forward after rollback, should the rollback request be cleared, or should it remain set and become ignored/invalid?
6. Should `kubernetesVersion` accept full patch versions (e.g. `1.32.5`) or only minor versions (e.g. `1.32`)? AKS agent pool behavior for Kubernetes version handling should be researched before finalizing accepted values and drift comparison semantics.
7. What should the wrapper field grouping `nodeImageVersion` and `kubernetesVersion` be named? The leading candidates are `upgradeControl` and `manualUpgrade`; see API Field Grouping and Wrapper Name.
8. As future image version selectors are introduced, how should they coexist with `nodeImageVersion`? Should `nodeImageVersion` take precedence over selectors, or should they be mutually exclusive? Should selectors live in the same grouping as `nodeImageVersion` and `kubernetesVersion`?
9. Should a dedicated `status.currentImageVersion` field be added for operator clarity, or is the current node image version sufficiently visible through `status.images`?

   **Resolved:** `status.latestImageVersion` is added to always reflect the latest resolved version suffix regardless of rollback/pinning state. This is sufficient for both observability and CEL validation; no separate `currentImageVersion` field is needed.

## Out of Scope Follow-up Designs

The following are intentionally deferred and must be designed separately:

1. Long-duration arbitrary node image pinning beyond the current and previously used versions. The `nodeImageVersion` spec field in this design supports pinning to the current or previously used version only. Pinning to arbitrary historical versions, SLA considerations for long-duration pinned clusters, and full image lifecycle management are not covered here.
2. Prepared image spec support that accepts full resource ID and maps to a dedicated AKS API field.
3. Image version selectors for filtering and ranking available node image versions. This design leaves room for selectors alongside `nodeImageVersion` and `kubernetesVersion` but does not specify their structure. A follow-up design should define selector semantics, how they interact with `nodeImageVersion`, and whether they are mutually exclusive or composable.
4. Support-window or stale-image warnings for long-duration pinned images.
5. **Multi-environment staged rollout across clusters.** The current design stores only one `recentlyUsedVersions` entry per NodeClass. This means a cluster can only target the current latest or the immediately preceding image version. For staged rollout pipelines where validation takes longer than one image release cycle — for example, dev validates `202607.15.0` while `202608.15.0` and `202609.15.0` have already shipped — downstream clusters can no longer pin to the validated version because it has been evicted from the single-entry history. Supporting reliable multi-cluster staged rollout will require storing multiple `recentlyUsedVersions` entries (a list of the last N versions) rather than a single pointer, so that a validated version remains reachable for pinning even if several newer versions have since been released.

## Production Readiness
