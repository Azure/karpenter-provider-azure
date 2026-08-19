# Node Image and Kubernetes Version Controls for NAP

**Author:** @rakechill

**Last updated:** Aug 19, 2026

**Status:** Proposed

**Related issues:** [Azure/karpenter-provider-azure#1220](https://github.com/Azure/karpenter-provider-azure/issues/1220), [Azure/karpenter-provider-azure#1355](https://github.com/Azure/karpenter-provider-azure/issues/1355)

Note: The issue title says "Add support for setting ImageID for nodeClass", but the problem statement is primarily asking for rollback/pinning capability to recover from bad image releases and the ability to control when new images will be picked up by NAP nodes. This rollback design addresses the bad-release recovery and version control use cases. It does not address the literal title request to set arbitrary ImageID; that is likely a future AKS workstream, and Karpenter should not set that standard prematurely.

## Overview

This design adds two user controls to NAP NodeClasses:

1. `spec.versions.nodeImageVersion` lets a customer pin or roll back the node image version.
2. `spec.versions.kubernetesVersion` lets a customer control the node Kubernetes version used for drift and provisioning new nodes.

The main use cases are:

- pause automatic node image roll-forward
- roll back to a previously used image version after a bad image release
- decouple node Kubernetes version drift from the control plane version

The design does **not** introduce arbitrary image IDs or arbitrary historical rollback. It stays within status-backed values already visible on the NodeClass.

## Current Problem

Today, NAP continuously resolves the latest compatible gallery images and publishes them into `status.images` using existing maintenance-window behavior.

That creates two gaps:

1. Customers cannot explicitly hold the current image version or roll back to a previously used one.
2. Customers cannot independently choose the node Kubernetes version used for drift; upgrades effectively track control-plane changes.

The result is that a control-plane upgrade can drive both a Kubernetes version move and a node image move at the same time, with little direct operator control.

## Goals

1. Allow pinning and rollback through a single `nodeImageVersion` field.
2. Preserve last effective image/Kubernetes version pairs in NodeClass status.
3. Keep the API narrow by validating only status-backed version choices.
4. Allow an explicit `kubernetesVersion` for node drift and replacement decisions.
5. Leave room for future image selector-based controls.

## Non-Goals

1. Arbitrary long-term image pinning to any historical version.
2. Prepared image support by full resource ID.
3. Kubernetes version rollback.
4. Global scale-up blocking beyond requests that resolve to an invalid target.

## Operating Model

There are three version concepts in this design.

| Concept | Source | Meaning |
|---|---|---|
| Desired Kubernetes version | `spec.versions.kubernetesVersion` when set; otherwise `status.controlPlaneKubernetesVersion` | Version the NodeClass is trying to use for nodes |
| Effective node Kubernetes version | `status.kubernetesVersion` | Last accepted node Kubernetes version used for provisioning, image resolution, and drift |
| Observed control plane Kubernetes version | `status.controlPlaneKubernetesVersion` | Latest control-plane version observed by the reconciler |

For Kubernetes versions, the design uses two status fields.

| Field | Meaning |
|---|---|
| `status.kubernetesVersion` | Effective node Kubernetes version currently used by provisioning, image resolution, and drift |
| `status.controlPlaneKubernetesVersion` | Latest observed control-plane version used for compatibility checks and fallback behavior |

For node images, the design uses three status views.

| Field | Meaning |
|---|---|
| `status.latestImageVersion` | Latest gallery version known to the reconciler |
| `status.images` | Effective image set currently used for provisioning and drift |
| `status.recentlyUsedVersions` | Reverse-chronological history of last effective image/Kubernetes version pairs |

The key invariant is:

- provisioning and drift always use the published effective status fields, not a merely requested target

Supported spec combinations:

| Spec state | Meaning |
|---|---|
| neither field set | fully automatic behavior |
| only `kubernetesVersion` set | explicit node Kubernetes version, with latest compatible image version resolution |
| only `nodeImageVersion` set | invalid |
| both fields set | explicit image/Kubernetes pair |

## API Shape

### Spec

```go
// Versions controls the Kubernetes and node image versions used by the NodeClass.
// If omitted, nodes follow the observed control plane version and automatic latest node image selection.
type Versions struct {
    // kubernetesVersion is the Kubernetes version to use for nodes provisioned for the NodeClass.
    // If omitted, the observed control plane version is used.
    // +optional
    KubernetesVersion *string `json:"kubernetesVersion,omitempty"`
    // nodeImageVersion is the status-backed node image version to use for the NodeClass.
    // If omitted, the latest compatible image is selected automatically, subject to maintenance windows.
    // +optional
    NodeImageVersion *string `json:"nodeImageVersion,omitempty"`
}

type AKSNodeClassSpec struct {
    // versions controls the Kubernetes and node image versions for the NodeClass.
    // If omitted, both versions follow their automatic defaults.
    // +optional
    Versions *Versions `json:"versions,omitempty"`
}
```

The `versions` wrapper keeps the v1 surface narrow and leaves a natural home for future selectors such as `spec.versions.nodeImageSelectors`.

### Status

```go
type RecentlyUsedVersion struct {
    // timestampUsed is when this image version was last effective.
    // +optional
    TimestampUsed metav1.Time `json:"timestampUsed,omitempty"`
    // imageVersion is the node image version suffix.
    // +optional
    ImageVersion string `json:"imageVersion,omitempty"`
    // kubernetesVersion is the Kubernetes version paired with the image version.
    // +optional
    KubernetesVersion string `json:"kubernetesVersion,omitempty"`
}

type AKSNodeClassStatus struct {
    // images contains the current set of images available to use for the NodeClass.
    // +optional
    Images []NodeImage `json:"images,omitempty"`
    // kubernetesVersion contains the current kubernetes version which should be used for nodes provisioned for the NodeClass.
    // +optional
    KubernetesVersion *string `json:"kubernetesVersion,omitempty"`
    // controlPlaneKubernetesVersion is the latest observed control plane version.
    // +optional
    ControlPlaneKubernetesVersion *string `json:"controlPlaneKubernetesVersion,omitempty"`
    // latestImageVersion is the latest node image version resolved from the gallery.
    // +optional
    LatestImageVersion string `json:"latestImageVersion,omitempty"`
    // recentlyUsedVersions contains previously effective node image versions.
    // +optional
    RecentlyUsedVersions []RecentlyUsedVersion `json:"recentlyUsedVersions,omitempty"`
}
```

## Kubernetes Version Semantics

### Default behavior

If `spec.versions.kubernetesVersion` is unset:

1. reconcile observes the control plane version
2. reconcile validates it
3. reconcile publishes it into `status.kubernetesVersion`
4. drift and provisioning use `status.kubernetesVersion`

This preserves the existing behavior while making the effective node version explicit in status.

### Explicit behavior

If only `spec.versions.kubernetesVersion` is set:

1. reconcile validates the requested version
2. reconcile validates latest compatible image version resolution for that version
3. reconcile publishes the accepted version into `status.kubernetesVersion`
4. drift and provisioning use that accepted effective version

If validation fails, reconcile leaves the previously effective value in place and marks the NodeClass unready.

If both `spec.versions.kubernetesVersion` and `spec.versions.nodeImageVersion` are set, reconcile validates them as an explicit pair and publishes the new effective state only if both remain valid.

### AKS skew rules

The requested version must satisfy AKS node/control-plane skew rules.

Notation:

- `C = cMajor.cMinor.cPatch` is the control-plane version
- `N = nMajor.nMinor.nPatch` is the requested node version

1. major versions must match
    (`nMajor == cMajor`)
2. node minor must not be greater than control-plane minor
    (`nMinor <= cMinor`)
3. node minor must be at most three minors behind control-plane minor
    (`cMinor - nMinor <= 3`)
4. when both are on the same minor, node patch must not be greater than control-plane patch
    (`nMinor == cMinor -> nPatch <= cPatch`)
5. the requested version must not be lower than the current effective node Kubernetes version
    (if `E = eMajor.eMinor.ePatch` is the current effective node version, then `N >= E`)

This design does not support Kubernetes version rollback.

## Node Image Version Semantics

`spec.versions.nodeImageVersion` is the single customer control for pinning and rollback.

Valid values are only:

1. the current effective image version from `status.images[]`
2. the latest resolved image version from `status.latestImageVersion`
3. a previously effective version from `status.recentlyUsedVersions[*].imageVersion`

`nodeImageVersion` is only valid when `spec.versions.kubernetesVersion` is also set. Anything else is invalid.

### Default behavior

If `spec.versions.nodeImageVersion` is unset:

- NAP continues its existing automatic image-selection behavior
- `status.latestImageVersion` keeps tracking the newest gallery version
- `status.images` remains governed by existing maintenance-window behavior for automatic gallery updates

### Pinning behavior

If `spec.versions.nodeImageVersion` equals:

- the current effective image version: pin to current
- the current `status.latestImageVersion`: pin to that specific latest value at update time

In both cases, automatic image roll-forward pauses until the customer changes or clears the pin.

### Rollback behavior

If `spec.versions.nodeImageVersion` matches an entry in `status.recentlyUsedVersions[*].imageVersion`, the request is a rollback request.

Rollback is allowed only when the selected historical entry is **rollback-compatible**.

**Rollback-compatible Kubernetes version** means:

- the current desired Kubernetes version exactly equals the selected `status.recentlyUsedVersions[*].kubernetesVersion`

This is exact equality, not a looser skew-compatible rule.

## Status Semantics

### Kubernetes status fields

| Field | Meaning |
|---|---|
| `status.kubernetesVersion` | Effective node Kubernetes version |
| `status.controlPlaneKubernetesVersion` | Latest observed control-plane version |

Rules:

1. `status.kubernetesVersion` is the source of truth for provisioning, image resolution, and drift.
2. `status.controlPlaneKubernetesVersion` is used for skew validation and observability.
3. A control-plane refresh always updates `status.controlPlaneKubernetesVersion`, even if the effective node version remains pinned or becomes incompatible.

### Image status fields

| Field | Meaning |
|---|---|
| `status.images` | Effective image set |
| `status.latestImageVersion` | Latest resolved gallery version |
| `status.recentlyUsedVersions` | Reverse-chronological history of previously effective image/Kubernetes version pairs |

Rules:

1. `status.latestImageVersion` is always a gallery-view field, not a guarantee of what is currently effective.
2. `status.images` is the only image source used by provisioning and drift.
3. `status.recentlyUsedVersions` stores historical effective pairs, not just image versions.

## Reconciliation Model

### Snapshot trigger

A snapshot into `status.recentlyUsedVersions` is taken whenever the effective image set moves to a different image version suffix.

Triggers:

1. automatic gallery advance becomes effective
2. customer sets `nodeImageVersion`
3. customer unsets `nodeImageVersion`
4. effective Kubernetes version change produces a different image set

### Publication ordering

When reconcile is about to move the effective node Kubernetes version and/or effective image set, it must:

1. validate the request
2. resolve the goal image set
3. snapshot the old effective pair from `status.kubernetesVersion` and `status.images`
4. publish the new effective pair

This ensures `recentlyUsedVersions[*].kubernetesVersion` records the Kubernetes version with which the historical image was actually used.

## Explicit Image Version Changes

This flow applies to both rollback and pinning, and only when `spec.versions.kubernetesVersion` is also set.

1. validate that `spec.versions.kubernetesVersion` is set
2. validate the requested `nodeImageVersion` against status-backed choices
3. if it is a rollback target, validate rollback-compatible Kubernetes version equality
4. resolve concrete images through the normal `List()` flow
5. filter the concrete images to the requested `nodeImageVersion`
6. if at least one image remains, publish that set into `status.images`
7. if zero images remain, set the NodeClass unready and do not publish

Important behavior:

- explicit user-initiated `spec.versions` changes publish immediately after validation and resolution
- maintenance windows do **not** delay those explicit changes
- maintenance windows continue to govern only automatic gallery-driven updates

## Automatic Gallery Updates

Automatic updates keep the existing maintenance-window behavior.

That means:

1. reconcile may discover a newer gallery version and publish it into `status.latestImageVersion`
2. `status.images` may remain on the previous effective set while the maintenance window is closed
3. once the maintenance window opens, reconcile may publish the newer effective set into `status.images`

This preserves the current separation between “latest known” and “currently effective.”

## Validation Strategy

### Summary by phase

| Phase | Purpose |
|---|---|
| Admission | Best-effort format, transition, and persisted-state validation |
| Reconcile on spec change | Fresh lookups, authoritative compatibility checks, image-resolution validation |
| Periodic reconcile | Refresh observed control plane version and re-check accepted effective state |

### `nodeImageVersion` checks

| Rule | CEL admission | Reconcile failure |
|---|---|---|
| `spec.versions.kubernetesVersion` is set when `spec.versions.nodeImageVersion` is set | Reject | `ValidationSucceeded=False`, `NodeImageVersionRequiresKubernetesVersion` |
| Requested value must match a version already exposed in NodeClass status | Best-effort reject | `ValidationSucceeded=False`, `NodeImageVersionInvalid` |
| Rollback target is rollback-compatible | Best-effort when persisted state is sufficient | `ValidationSucceeded=False`, `RollbackTargetKubernetesVersionMismatch` |
| Requested version resolves at least one concrete image | Not checked | `ImagesReady=False`, `RequestedNodeImageVersionUnavailable` |

### `kubernetesVersion` checks

| Rule | CEL admission | Reconcile failure |
|---|---|---|
| Value is full `major.minor.patch` | Reject | `ValidationSucceeded=False`, `KubernetesVersionInvalidFormat` |
| Request is not a downgrade | Reject using `oldSelf.spec.versions.kubernetesVersion` when previously set, otherwise `status.kubernetesVersion` | `ValidationSucceeded=False`, `KubernetesVersionDowngradeNotSupported` |
| Request satisfies AKS skew relative to `status.controlPlaneKubernetesVersion` | Best-effort reject | `KubernetesVersionReady=False`, `KubernetesVersionControlPlaneIncompatible` |
| Requested patch exists in AKS-supported metadata | Not checked | `KubernetesVersionReady=False`, `KubernetesVersionUnsupported` |

Notes:

- CEL is best-effort because it only sees persisted state.
- Reconcile is authoritative because it performs fresh control-plane and supported-version lookups.
- Transient lookup or provider failures should return an error for retry rather than set a sticky `False` condition.

## Conditions and Reasons

The design reuses existing readiness-bearing conditions.

| Condition | Meaning when false |
|---|---|
| `ValidationSucceeded` | Requested spec is invalid for current object state |
| `KubernetesVersionReady` | Effective Kubernetes version is unsupported or incompatible |
| `ImagesReady` | No concrete images can be resolved for the effective target |

Recommended new reasons:

| Condition | Reason | Meaning |
|---|---|---|
| `ValidationSucceeded=False` | `NodeImageVersionInvalid` | Requested `nodeImageVersion` does not match a version already exposed in NodeClass status |
| `ValidationSucceeded=False` | `NodeImageVersionRequiresKubernetesVersion` | `nodeImageVersion` is set while `kubernetesVersion` is unset |
| `ValidationSucceeded=False` | `RollbackTargetKubernetesVersionMismatch` | Requested rollback target is not rollback-compatible |
| `ValidationSucceeded=False` | `KubernetesVersionDowngradeNotSupported` | Requested Kubernetes version is a downgrade |
| `ValidationSucceeded=False` | `KubernetesVersionInvalidFormat` | Requested version is not full `major.minor.patch` |
| `KubernetesVersionReady=False` | `KubernetesVersionUnsupported` | Requested patch is not supported by AKS metadata |
| `KubernetesVersionReady=False` | `KubernetesVersionControlPlaneIncompatible` | Requested version violates control-plane compatibility |
| `ImagesReady=False` | `RequestedNodeImageVersionUnavailable` | Requested image version resolved zero concrete images |

## Drift and Provisioning

The governing rule is simple:

- drift and provisioning always use the published effective status fields

### Provisioning

New NodeClaims use:

- `status.kubernetesVersion` for the effective node Kubernetes version
- `status.images` for the effective image set

New NodeClaims are only launched after the NodeClass is `Ready` for the current generation. This ensures launch does not consume stale effective status while reconcile is still validating or publishing a spec change.

If filtering resolved images to the requested `nodeImageVersion` leaves no concrete images, Karpenter cannot provision new NodeClaims from that NodeClass.

### Drift

A spec change can make an existing NodeClaim drifted immediately, but that does not by itself mean replacement is actionable.

1. `spec.versions` fields participate in the AKSNodeClass hash, so their changes enqueue affected NodeClaims.
2. User-initiated spec changes publish new effective status immediately, so drift can proceed against those values.
3. Automatic gallery updates do not create effective image drift until the new image set is actually published into `status.images`.

For Kubernetes version drift:

- when only `spec.versions.kubernetesVersion` is set, drift follows the accepted effective version in `status.kubernetesVersion`, with latest compatible image version resolution for that Kubernetes version
- when both `spec.versions.kubernetesVersion` and `spec.versions.nodeImageVersion` are set, drift follows the accepted explicit pair
- when it is unset, drift follows the effective version published from `status.controlPlaneKubernetesVersion`

Replacement must not proceed until the NodeClass is `Ready` for the current generation and the effective status for that generation has been published.

If the latest observed control plane version and the effective node Kubernetes version become incompatible, drift should not try to converge toward an invalid target. The NodeClass should remain unready until the compatibility issue is resolved.

## Decisions

### Decision 1: Use status for rollback history

Store rollback history on the AKSNodeClass itself.

Rationale:

1. durable across operator restarts
2. tightly coupled to the effective status lifecycle
3. mirrors the AKS RP recently-used rollback shape
4. avoids introducing another state store

### Decision 2: Use an array for history

Store `recentlyUsedVersions` as an array, not a single entry.

Rationale:

1. matches the AKS RP preview shape
2. preserves more than one rollback candidate
3. supports staged environments that intentionally lag behind newer releases

### Decision 3: Use full `major.minor.patch` for Kubernetes versions

Represent `kubernetesVersion` as a full Kubernetes version string such as `1.32.5`.

Rationale:

1. matches AKS-supported version metadata and upgrade semantics
2. avoids ambiguity around patch-level compatibility checks
3. makes downgrade and skew validation precise

### Decision 4: Use image version suffixes for node image versions

Represent `nodeImageVersion` as the image version suffix exposed in status, such as `202601.15.0`.

Rationale:

1. matches the user-visible values already surfaced in NodeClass status
2. avoids exposing full image IDs as the v1 control surface
3. preserves compatibility across multiple concrete image definitions that share the same release suffix

## Out of Scope

1. Arbitrary long-term image pinning beyond status-backed choices.
2. Prepared image support by full resource ID.
3. Future selector semantics under `spec.versions.nodeImageSelectors`.
4. Stale-image or support-window warnings for long-lived pins.
5. Strong retention guarantees for multi-environment staged rollouts.

## Resources

- [Upgrade Node Images in Azure Kubernetes Service (AKS)](https://learn.microsoft.com/en-us/azure/aks/upgrade-node-image)
- [Autoupgrade Node OS Images in Azure Kubernetes Service (AKS)](https://learn.microsoft.com/en-us/azure/aks/auto-upgrade-node-os-image)
- [Use Planned Maintenance to Schedule and Control Upgrades for Azure Kubernetes Service (AKS) Clusters](https://learn.microsoft.com/en-us/azure/aks/planned-maintenance)
- [Supported Kubernetes Versions in Azure Kubernetes Service (AKS)](https://learn.microsoft.com/en-us/azure/aks/supported-kubernetes-versions)
- [Upgrade an Azure Kubernetes Service (AKS) cluster](https://learn.microsoft.com/en-us/azure/aks/upgrade-aks-cluster)
