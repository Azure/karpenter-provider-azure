# Node Image and Kubernetes Version Controls for NAP

**Author:** @rakechill

**Last updated:** Aug 13, 2026

**Status:** Proposed

**Related issues:** [Azure/karpenter-provider-azure#1220](https://github.com/Azure/karpenter-provider-azure/issues/1220), [Azure/karpenter-provider-azure#1355](https://github.com/Azure/karpenter-provider-azure/issues/1355)

Note: The issue title says "Add support for setting ImageID for nodeClass", but the problem statement is primarily asking for rollback/pinning capability to recover from bad image releases and the ability to control when new images will be picked up by NAP nodes. This rollback design addresses the bad-release recovery and version control use cases. It does not address the literal title request to set arbitrary ImageID; that is likely a future AKS workstream, and Karpenter should not set that standard prematurely.

## Table of Contents

1. [Overview](#overview)
2. [Background](#background)
   - [Node image version updates](#node-image-version-updates)
   - [Kubernetes version upgrades](#kubernetes-version-upgrades)
3. [Goals](#goals)
4. [Non-Goals](#non-goals)
5. [API Changes](#api-changes)
   - [Definitions](#definitions)
   - [API Field Grouping and Wrapper Name](#api-field-grouping-and-wrapper-name)
   - [kubernetesVersion Spec Field](#kubernetesversion-spec-field)
   - [nodeImageVersion Spec Field](#nodeimageversion-spec-field)
   - [AKSNodeClass status: latestImageVersion and recentlyUsedVersions](#aksnodeclass-status-latestimageversion-and-recentlyusedversions)
6. [Reconciliation Design](#reconciliation-design)
   - [Snapshot point](#snapshot-point)
   - [Rollback path](#rollback-path)
   - [Image selection during rollback](#image-selection-during-rollback)
   - [Applying rollback during reconcile](#applying-rollback-during-reconcile)
7. [Validation and Conditions](#validation-and-conditions)
   - [End-to-end validation flow](#end-to-end-validation-flow)
   - [nodeImageVersion and kubernetesVersion Validation](#nodeimageversion-and-kubernetesversion-validation)
8. [Drift and Provisioning Behavior](#drift-and-provisioning-behavior)
   - [Existing nodes](#existing-nodes)
   - [New scale-ups](#new-scale-ups)
   - [Drift trigger choice](#drift-trigger-choice)
   - [Kubernetes version drift](#kubernetes-version-drift)
9. [Decision Notes](#decision-notes)
10. [Out of Scope Follow-up Designs](#out-of-scope-follow-up-designs)

## Overview

This document proposes version control capabilities for AKS Node Auto Provisioning (NAP) in Karpenter, including node image version pinning, rollback, and Kubernetes version drift control. These capabilities are aligned with AKS agent pool rollback and upgrade semantics.

The intent is to let customers control which node image version their NAP nodes run on: either by pinning to the current version to prevent automatic upgrades, or by rolling back to the previously used version to recover from a bad image release. Additionally, customers can specify a target Kubernetes version on the NodeClass to decouple node Kubernetes version drift detection from the control plane's current version.

## Background

AKS node images are VHD-based image versions that are updated frequently. Today, NAP users configure selector-style image controls in spec (for example image family), while resolved concrete images are surfaced in status.

### Node image version updates

Today, the NodeImageReconciler resolves the latest gallery images on every AKSNodeClass event and on a 5-minute requeue. However, it does not always immediately publish those latest versions into `status.images`. If `ImagesReady` is false, it applies latest immediately. Otherwise, it only moves existing image definitions forward when the maintenance window is open; outside that window it preserves the existing versions and only adds newly available SKUs. If no maintenance window is configured for the node OS upgrade schedule, the current behavior fails open and applies latest.

Existing nodes are considered drifted and replaced according to normal disruption controls once the effective image set in `status.images` changes. Customers have no suggested mechanism to opt out of this process, defer it beyond maintenance window shaping, or manually trigger it at a time of their choosing.

### Kubernetes version upgrades

NAP node Kubernetes version upgrades behave differently from AKS managed agent pools. When the cluster control plane is upgraded to a new Kubernetes version, Karpenter recognizes the version delta soon after the update (subject to a polling interval) and marks affected nodes as drifted. Replacement is then driven by the standard disruption flow, respecting maintenance windows and disruption budgets, but customers cannot separately stage or defer the node k8s version upgrade the way they can with AKS agent pool upgrade controls.

Additionally, a Kubernetes version upgrade also triggers a node image version refresh: nodes are replaced with the effective node image set resolved for the new Kubernetes version. In practice this typically means moving to the latest compatible node image, subject to the same image-resolution and maintenance-window behavior described above. This means a control plane upgrade causes both a k8s version change and a node image change on NAP nodes simultaneously, neither of which is individually controllable today.

## Goals

1. Provide a `nodeImageVersion` spec field that allows customers to pin to their current node image version or roll back to the previously used version.
2. Preserve recently-used version state durably in AKSNodeClass status, including the Kubernetes version paired with the previously used node image.
3. Keep rollback and pinning operationally safe and predictable. Pinning is limited to the current and previously used versions only, following AKS's current policy.
4. Provide a `kubernetesVersion` spec field that allows customers to specify the desired Kubernetes version for drift detection, decoupling node k8s version drift from the cluster control plane version.
5. Keep the API design extensible for future image version selectors.

## Non-Goals

1. Implement long-duration or arbitrary image pinning beyond the current and previously used versions.
2. Introduce prepared image spec by full resource ID.
3. Guarantee rollback to arbitrary historical versions.
4. Block all provisioning scale-up globally while rollback or pin validation is being evaluated. This design may still block scale-up for requests that resolve to an invalid or unusable target version.

## API Changes

### Definitions

- **Desired Kubernetes version:** `spec.versions.kubernetesVersion` when set; otherwise `status.kubernetesVersion`.
- **Current effective node Kubernetes version:** the last accepted desired node Kubernetes version for the NodeClass, falling back to `status.kubernetesVersion` when no explicit desired version has been set.
- **Effective image set:** the concrete `status.images` entries currently used for provisioning and drift.
- **Usable effective image set:** an effective image set with at least one concrete resolved image for the current desired version inputs.

### API Field Grouping and Wrapper Name

`nodeImageVersion` and `kubernetesVersion` should be grouped under a dedicated sub-section rather than placed at the top level of spec. Top-level placement would crowd the spec namespace and leave no clean home for future image version selectors.

The proposed shape is:

```yaml
spec:
  versions:
    kubernetesVersion: "1.32.5"
    nodeImageVersion: "202601.15.0"
```

**Decision:** use `versions`.

This name keeps the v1 fields narrow and readable while leaving an obvious home for future selection-based inputs. It also avoids overloading terms like "upgrade" or "policy" for behavior that includes pinning and rollback, not just forward movement.

If image selectors are added later, they should live under `spec.versions.nodeImageSelectors`. That future field should be a map of string key/value pairs rather than a closed set of predefined NodeClass schema fields. The intent is to let customers supply additional image-filtering hints without forcing each selector key to become a first-class CRD field. Selector interpretation remains a future design topic.

### kubernetesVersion Spec Field

The `kubernetesVersion` spec field allows customers to specify a desired Kubernetes version for the NodeClass. When set, Karpenter uses this value for k8s version drift detection instead of the cluster control plane's current version.

**Semantics:**

1. **Unset (default):** Karpenter uses the observed control plane Kubernetes version from `status.kubernetesVersion` to determine whether an existing node has a k8s version mismatch. This is the existing behavior.
2. **Set:** Karpenter uses the specified version as the desired k8s version for nodes referencing this NodeClass. Nodes running a different Kubernetes version are treated as drifted and replaced via normal disruption controls.
3. **Changing `kubernetesVersion`:** Follows AKS semantics. When the k8s version changes, the node image resolution flow refreshes to the effective image set for the new Kubernetes version, typically moving toward the latest compatible image subject to the existing maintenance-window behavior. To prevent conflicts between an explicit `nodeImageVersion` and the new Kubernetes version, CEL admission validation requires `nodeImageVersion` to be unset before `kubernetesVersion` can be changed to a new value.
4. **Kubernetes version downgrade is not supported:** In accordance with AKS policy, this design does not support user-driven Kubernetes version rollback. Customers can pin or roll back the node image version only within the currently valid desired Kubernetes version. If a previously used image version was recorded with an older Kubernetes version and is no longer compatible with the current desired Kubernetes version, the rollback request is rejected rather than lowering `kubernetesVersion` to match it.

**AKS version skew constraint (semantic requirement):**

AKS enforces Kubernetes version compatibility between control plane and node pools with the following constraints: major versions must match; node pool minor version cannot be greater than the control plane minor version; node pool can be at most three minor versions behind the control plane; and when node pool and control plane are on the same minor, node pool patch version cannot be greater than the control plane patch version.

When `spec.versions.kubernetesVersion` is set, Karpenter must validate that the specified version satisfies this constraint relative to both the current effective node Kubernetes version and the cluster control plane version before provisioning or during drift evaluation. For CEL transition rules, the current effective node Kubernetes version is the previously persisted value from `oldSelf.spec.versions.kubernetesVersion` when that field was already set; otherwise it falls back to the observed control plane version from `status.kubernetesVersion`.

1. The specified major version must match the control plane major version.
2. The specified minor version must not be greater than the control plane minor version.
3. The specified minor version must be at most three minors behind the control plane minor version.
4. When specified and control plane versions share the same minor version, the specified patch version must not be greater than the control plane patch version.
5. The specified version must not be lower than the current effective node Kubernetes version, since Kubernetes version downgrade is not supported by this design.

Violations should be surfaced as a condition on the NodeClass rather than silently accepted, since AKS is expected to reject machine creation once the requested version falls outside the supported skew window.

Karpenter should use the existing `status.kubernetesVersion` field as the observed control plane version for skew comparisons and as the fallback desired Kubernetes version when `spec.versions.kubernetesVersion` is unset. Because `status.kubernetesVersion` is a cached last-observed value rather than a synchronous admission-time lookup, reconcile-time enforcement remains the authoritative backstop.

**Validation and enforcement summary (details in Validation and Conditions):**

- **Admission checks:**
	- `kubernetesVersion` must be full `major.minor.patch` (for example `1.32.5`).
	- Changing `kubernetesVersion` requires `nodeImageVersion` to be unset first.
	- No downgrade relative to the current effective node Kubernetes version.
- **Change-triggered reconcile checks (on `spec.versions.kubernetesVersion` updates):**
	- Perform a fresh control plane version lookup.
	- Validate requested patch against AKS-supported version metadata.
	- Validate compatibility between requested effective node version and latest observed control plane version.
	- Validate that image resolution can produce a usable effective image set for the requested version.
- **Periodic checks (existing control-plane refresh interval):**
	- Re-check compatibility between effective node version and observed control plane version.
	- Do not rerun explicit requested-patch existence lookup unless the spec field changed.
- **Caching:**
  - AKS-supported version metadata may be cached and refreshed on miss/expiry/normal invalidation.

### nodeImageVersion Spec Field

The `nodeImageVersion` spec field is the unified customer surface for both node image pinning and rollback. It determines which node image release version Karpenter targets for new provisioning and drift evaluation.

**Semantics:**

1. **Unset (default):** Karpenter preserves the existing NAP image-resolution behavior. It resolves the latest gallery image version on every reconcile, updates `status.latestImageVersion`, and publishes the effective image set into `status.images` according to the current maintenance-window logic.
2. **Set to `status.latestImageVersion`:** Karpenter pins to the latest resolved version. New nodes are provisioned on that version. Automatic node image upgrades are paused — if a newer version becomes available, nodes will not drift until the customer updates or clears the pin.
3. **Set to a value in `status.recentlyUsedVersions[*].imageVersion`:** Karpenter rolls back to that previously used version. The rollback validation rules from `status.recentlyUsedVersions` apply: the requested version must match an entry in the array and the Kubernetes version of that entry must be compatible.
4. **Set to the suffix of `status.images[]`:** Karpenter pins to the current image. This could be the same as `status.latestImageVersion`, or it could differ if the latest image version hasn't yet been picked up by Karpenter nodes.
5. **Set to any other value:** CEL admission validation rejects the request. The only valid values are the current image version in `status.images[]`, `status.latestImageVersion` (pinning at latest), or a value present in `status.recentlyUsedVersions[*].imageVersion` (rollback to a previous version).

All three of the valid image versions are always visible from `status`.

**Decision: explicit version string.**

```yaml
spec:
  versions:
    nodeImageVersion: "202601.15.0"
```

Karpenter still derives the image family, architecture, generation, and runtime-specific image definition from the AKSNodeClass and selected instance type. This matters because multiple NodePools can share the same AKSNodeClass while selecting different instance types, which may resolve to different image definitions such as Gen1, Gen2, or Arm64 variants. Customers read the valid values from `status.recentlyUsedVersions[*].imageVersion` (for rollback), `status.images[]` (for pinning at current), or from `status.latestImageVersion` (for pinning at latest).

For maintained image families, these resolved Gen1, Gen2, and Arm64 variants are expected to move forward together on the same release version suffix. That makes a single customer-facing `nodeImageVersion` suffix a reasonable v1 API. The design must still tolerate exceptional cases where resolved image definitions for one NodeClass do not share a single suffix, such as frozen variants or other special rollout paths.

This behavior should stay internal to image resolution rather than becoming a customer-facing spec field. If at least one concrete image resolves for the requested suffix, Karpenter should publish the matching subset into `status.images`. If no concrete images resolve for the requested suffix, Karpenter should avoid publishing rollback/pin-effective `status.images` for that request and instead surface the failure through the existing readiness conditions.

Behavior:

1. Karpenter validates the requested version against the valid status-backed choices: the current effective image version in `status.images[]`, `status.latestImageVersion`, and `status.recentlyUsedVersions[*].imageVersion`.
2. Rollback is rejected if the requested version suffix is not present in `status.recentlyUsedVersions[*].imageVersion` or the Kubernetes version does not match.
3. For rollback or pinning, Karpenter resolves actual gallery images through the normal `List()` flow and publishes only the concrete returned images whose effective image version matches the selected version.

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
4. recentlyUsedVersions should retain a bounded reverse-chronological history rather than a single previous version, so recently validated rollback targets remain visible across more than one image release.
5. Rollback target selection is explicit: `spec.versions.nodeImageVersion` names the desired historical version, and Karpenter matches that value against `status.recentlyUsedVersions[*].imageVersion`.

Decision for v1: a single AKSNodeClass usually resolves image definitions that share one release version suffix, even when multiple definitions exist for different generations or architectures. A single `status.latestImageVersion` field is sufficient for this design. A requested or latest suffix is accepted if at least one concrete image resolves for the NodeClass, in which case Karpenter publishes the matching subset into `status.images`. If no concrete images resolve for the requested suffix, Karpenter should leave the previously effective image set in place and mark the NodeClass unready.

## Reconciliation Design

### Snapshot point

NodeImageReconciler in images.go updates `status.latestImageVersion` on every reconcile pass. A snapshot into `status.recentlyUsedVersions` is taken whenever the **effective image version changes**. The three triggers are:

- **Gallery advance becomes effective:** a newer gallery version is published into `status.images` while `nodeImageVersion` is unset, according to the existing maintenance-window and `ImagesReady` behavior.
- **Customer sets `nodeImageVersion`:** effective version changes from what was in `status.images` to the newly requested value.
- **Customer unsets `nodeImageVersion`:** the NodeClass returns to the normal image-resolution behavior, and a snapshot is taken when that changes the effective image set in `status.images`.

In all cases the snapshot captures the version being left, so each entry in `status.recentlyUsedVersions` represents a previously effective version. `status.recentlyUsedVersions[0].kubernetesVersion` (the most recent entry) reflects `spec.versions.kubernetesVersion` if it was set at snapshot time, otherwise the observed control plane version from `status.kubernetesVersion`.

### Rollback path

When `spec.versions.nodeImageVersion` is set to the previously used version:

1. Validate `status.recentlyUsedVersions` is non-empty.
2. Validate the requested `nodeImageVersion` matches an entry in `status.recentlyUsedVersions[*].imageVersion`.
3. Validate the currently desired Kubernetes version is compatible with that entry's `kubernetesVersion`. The desired Kubernetes version is `spec.versions.kubernetesVersion` if set, otherwise the observed control plane version from `status.kubernetesVersion`.
4. If valid, run the normal image `List()` flow for the NodeClass and desired Kubernetes version to resolve the concrete gallery images that are actually available.
5. Filter the returned images to the matched `recentlyUsedVersions` entry's `imageVersion`.
6. Publish the filtered images into `status.images` when at least one concrete image resolves for the requested version.

This means rollback is image-version rollback only. It is not a mechanism for downgrading the node Kubernetes version to match an older image/Kubernetes pairing.

### Image selection during rollback

status.images is already a list because a single AKSNodeClass can expose multiple compatible image definitions. For example, the same NodeClass may publish Gen2 amd64, Gen1 amd64, and Gen2 arm64 images, each with requirements that determine which instance types can use it.

Rollback should preserve this model:

1. Karpenter resolves the normal goal image list from the AKSNodeClass, Kubernetes version, FIPS mode, SIG/CIG mode, and supported image definitions.
2. That resolution path returns the concrete gallery images that are actually available for the NodeClass and desired Kubernetes version.
3. Each returned image keeps its existing requirements. Node launch continues to choose among the published images based on which requirements are compatible with the selected instance type.
4. Rollback does not synthesize image IDs by rewriting version suffixes. Instead, Karpenter filters the returned image set to the requested `nodeImageVersion` and publishes only those real matching images into `status.images`.

After filtering the `List()` results to the requested version:

1. Karpenter validates that the requested version resolves at least one concrete image for the NodeClass.
2. If zero images resolve, Karpenter does not publish rollback-effective `status.images` and instead marks the NodeClass unready.

### Applying rollback during reconcile

**Decision:** reconcile resolves real images and publishes the filtered effective set into `status.images`.

Karpenter does not synthesize rollback image IDs by rewriting image version suffixes. When `spec.versions.nodeImageVersion` is set, reconcile uses the normal image `List()` flow for the NodeClass and desired Kubernetes version, filters the returned concrete images to the selected effective image version, and publishes the resulting matching subset into `status.images` when at least one concrete image resolves. This keeps provisioning and drift logic simple because all consumers continue to read the already-resolved effective image set from status.

`status.images` always contains the effective image IDs Karpenter will use for provisioning and drift. When `spec.versions.nodeImageVersion` is unset, `status.images` continues to follow the existing image-resolution behavior, which may lag the latest gallery version until maintenance-window logic allows the new version to become effective.

`status.latestImageVersion` is always updated to the latest gallery version regardless of the active pin, serving two purposes: (1) it is a valid "pin at latest" target for validation, and (2) it lets operators see whether a newer version is available while the cluster is pinned. It is intentionally a gallery-view field, not a guarantee that the same version is currently effective in `status.images`.

## Validation and Conditions

No new top-level condition types are required for this design. AKSNodeClass readiness should continue to be expressed through the existing readiness-bearing conditions, with more specific reasons and messages attached to those conditions.

The relevant conditions are:

| Condition type | When false | Effect |
|---|---|---|
| `ValidationSucceeded` | The requested `nodeImageVersion` or `kubernetesVersion` value is invalid for the current object state | NodeClass is unready |
| `KubernetesVersionReady` | The desired effective Kubernetes version is unsupported or incompatible with the observed control plane version | NodeClass is unready |
| `ImagesReady` | Karpenter cannot resolve any concrete images for the selected Kubernetes version and image version | NodeClass is unready |

`Ready` remains the aggregate root condition derived from these existing readiness-bearing conditions together with the other pre-existing NodeClass readiness checks. This design does not add separate conditions for "rollback active", "pin active", or "Kubernetes version controlled" because those are operating modes derived from spec and status, not independent readiness signals.

Reason names below are recommended for implementation consistency; exact strings may evolve with API review.

**Recommended reasons:**

| Condition | Reason | Meaning |
|---|---|---|
| `ValidationSucceeded=False` | `NodeImageVersionInvalid` | `spec.versions.nodeImageVersion` is not one of the allowed status-backed choices |
| `ValidationSucceeded=False` | `RollbackTargetKubernetesVersionMismatch` | Requested rollback target exists, but its paired Kubernetes version is incompatible with the current desired Kubernetes version |
| `ValidationSucceeded=False` | `KubernetesVersionChangeRequiresNodeImageVersionUnset` | User attempted to change `spec.versions.kubernetesVersion` while `spec.versions.nodeImageVersion` is still set |
| `ValidationSucceeded=False` | `KubernetesVersionDowngradeNotSupported` | Requested `spec.versions.kubernetesVersion` is lower than the current effective node Kubernetes version |
| `ValidationSucceeded=False` | `KubernetesVersionInvalidFormat` | Requested `spec.versions.kubernetesVersion` is not full `major.minor.patch` |
| `KubernetesVersionReady=False` | `KubernetesVersionUnsupported` | Requested Kubernetes patch is not present in AKS-supported version metadata |
| `KubernetesVersionReady=False` | `KubernetesVersionControlPlaneIncompatible` | Requested effective Kubernetes version is incompatible with the latest observed control plane version or AKS-supported skew window |
| `ImagesReady=False` | `SIGRequiredForFIPS` | Existing reason; FIPS requires `UseSIG` |
| `ImagesReady=False` | `ImagesNotFound` | Existing reason; no images resolved for the NodeClass |
| `ImagesReady=False` | `KubernetesUpgrade` | Existing transition reason while image readiness is being refreshed after a Kubernetes version move |
| `ImagesReady=False` | `RequestedNodeImageVersionUnavailable` | Requested pin/rollback version passed object-state validation but image `List()` produced zero concrete matching images |

Transient provider or infrastructure errors should generally not be persisted as one of these `False` reasons unless the design explicitly wants that failure to remain visible in status. For transient lookup failures such as temporary AKS metadata retrieval issues, transient image-list failures, or other retryable provider errors, reconcile should usually return an error and retry instead of setting a sticky `False` condition. Persistent `False` conditions should be reserved for cases where Karpenter has enough information to conclude that the requested state is invalid or unusable.

### End-to-end validation flow

1. User updates `spec.versions.nodeImageVersion` or `spec.versions.kubernetesVersion`.
2. CEL performs best-effort admission checks using persisted object state and last-observed status values.
3. Reconcile performs authoritative checks with fresh lookups where required.
4. If request semantics are invalid for current object state, reconcile sets `ValidationSucceeded=False` with a specific reason.
5. If Kubernetes version is unsupported or control-plane incompatible, reconcile sets `KubernetesVersionReady=False`.
6. If image resolution produces zero concrete matching images, reconcile sets `ImagesReady=False` and does not publish the requested new effective image set.
7. If checks pass, reconcile updates status and readiness conditions so aggregate `Ready=True` can be restored.

### nodeImageVersion and kubernetesVersion Validation

In addition to rollback-specific validation, the following rules apply to `nodeImageVersion` and `kubernetesVersion`:

**CEL admission validation for `nodeImageVersion`:**

`spec.versions.nodeImageVersion` must equal the current effective image version in `status.images[]` (pin at current), `status.latestImageVersion` (pin at latest), or one of the `imageVersion` values in `status.recentlyUsedVersions` (rollback). All other values are rejected. Authoritative enforcement should happen in reconcile-time validation with clear NodeClass conditions; CEL can only provide best-effort checks where object state is available.

If rollback validation fails because `recentlyUsedVersions` is empty or the requested version is not present in `recentlyUsedVersions`, Karpenter should set `ValidationSucceeded=False` with reason `NodeImageVersionInvalid`. If the matched rollback target's paired Kubernetes version is incompatible with the current desired Kubernetes version, Karpenter should set `ValidationSucceeded=False` with reason `RollbackTargetKubernetesVersionMismatch`. If the requested version passes object-state validation but the normal image `List()` flow produces zero concrete matching images, it should set `ImagesReady=False` with reason `RequestedNodeImageVersionUnavailable` and avoid publishing the new requested image set into `status.images`. If at least one matching image resolves, Karpenter should publish that matching subset. Transient `List()` failures should return an error for retry instead of setting this reason.

**CEL admission validation for `kubernetesVersion`:**

1. Changing `kubernetesVersion` requires `nodeImageVersion` to be unset first — prevents conflicts between a pinned image and a new k8s version.
2. The specified version must satisfy the AKS-supported skew constraint: it must not be greater than the observed control plane version in `status.kubernetesVersion`, it must not be lower than the current effective node Kubernetes version, and it must remain within the platform-supported minor-version skew window.
3. For the no-downgrade check, CEL should use `oldSelf.spec.versions.kubernetesVersion` when the field was previously set on the object; otherwise it should fall back to `status.kubernetesVersion`.

Because `status.kubernetesVersion` is populated on the existing reconcile cadence and backed by a cache, CEL can only make a best-effort decision using the last observed control plane version. The authoritative skew check should run in reconcile-time logic, and the NodeClass should surface `KubernetesVersionReady=False` if a previously valid `kubernetesVersion` later becomes invalid after a control plane upgrade.

Example stale-status case: CEL may admit a request based on a last-observed control plane version, and reconcile may later set `KubernetesVersionReady=False` after a fresh lookup observes a newer incompatible control plane version.

At reconcile time, Karpenter should not rely exclusively on the cached control plane version when `spec.versions.kubernetesVersion` changes. It should fetch the latest control plane version, refresh `status.kubernetesVersion` and the cache if that observed value changed, and then evaluate compatibility against the latest observed control plane version. If the requested version is malformed for this workflow, conflicts with a pinned `nodeImageVersion`, or violates a no-downgrade transition rule, Karpenter should set `ValidationSucceeded=False` with reason `KubernetesVersionInvalidFormat`, `KubernetesVersionChangeRequiresNodeImageVersionUnset`, or `KubernetesVersionDowngradeNotSupported` as appropriate. If the requested version is well-formed but unsupported by AKS metadata, Karpenter should set `KubernetesVersionReady=False` with reason `KubernetesVersionUnsupported`. If it is incompatible with the latest observed control plane version or skew window, Karpenter should set `KubernetesVersionReady=False` with reason `KubernetesVersionControlPlaneIncompatible`. Transient control plane or AKS metadata lookup failures should return an error for retry instead of setting those reasons. Separately, the existing periodic control plane version refresh should re-run the compatibility check on its normal interval and set `KubernetesVersionReady=False` with reason `KubernetesVersionControlPlaneIncompatible` if the effective node Kubernetes version and control plane Kubernetes version are no longer compatible. The periodic refresh does not need to re-run the separate requested-patch existence lookup unless the spec field itself changed.

## Drift and Provisioning Behavior

Because `status.images` always contains the effective image IDs (pinned or latest), existing drift and provisioning paths consume rollback-effective images automatically.

### Existing nodes

When `spec.versions.nodeImageVersion` changes, it changes the AKSNodeClass hash, enqueuing affected NodeClaims. The drift logic compares each NodeClaim's current image against `status.images`. Nodes not on the effective version are drifted and replaced via normal disruption controls — no separate replacement mechanism is needed.

### New scale-ups

New NodeClaims are provisioned using `status.images` directly, which already contains the effective version.

If resolution produces zero concrete matching images for the requested pin or rollback version, scale-up for that request is blocked and the NodeClass condition explains that the requested image version could not be resolved.

### Drift trigger choice

`spec.versions` fields participate in AKSNodeClass hashing, so any change to `nodeImageVersion` or `kubernetesVersion` triggers NodeClassDrift on affected NodeClaims. Image-level drift (comparing a node's current image against `status.images`) also fires if the effective image set changes. Both paths must agree on the desired image — the invariant is that drift comparison and new node provisioning always use the same `status.images`.

### Kubernetes version drift

When `spec.versions.kubernetesVersion` is set, Karpenter uses it as the desired k8s version for drift detection instead of the control plane version. Nodes running a different version are drifted and replaced. New nodes are provisioned with the image compatible with the specified version. If unset, drift falls back to the observed control plane version in `status.kubernetesVersion`. `kubernetesVersion` participates in AKSNodeClass hashing, so changing it triggers NodeClassDrift.

If the latest observed control plane version and the effective node Kubernetes version become incompatible, drift should not attempt to converge toward an invalid target. Instead, Karpenter should set the relevant readiness condition false so the NodeClass remains unready until the compatibility issue is resolved.

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
3. A bounded history is better than a single-entry history because it preserves rollback reach across more than one release.

## Out of Scope Follow-up Designs

The following are intentionally deferred and must be designed separately:

1. Long-duration arbitrary node image pinning beyond the current and previously used versions. The `nodeImageVersion` spec field in this design supports pinning to the current or previously used version only. Pinning to arbitrary historical versions, SLA considerations for long-duration pinned clusters, and full image lifecycle management are not covered here.
2. Prepared image spec support that accepts full resource ID and maps to a dedicated AKS API field.
3. Image version selectors for filtering and ranking available node image versions. This design leaves room for a future `spec.versions.nodeImageSelectors` map of string key/value pairs alongside `nodeImageVersion` and `kubernetesVersion`, but does not specify selector semantics, supported keys, precedence, or whether selectors are mutually exclusive or composable with explicit version fields.
4. Support-window or stale-image warnings for long-duration pinned images.
5. **Multi-environment staged rollout across clusters.** This design uses `recentlyUsedVersions` as an array so more than one historical version can be retained, but it does not define staged-rollout guarantees, retention sizing guidance, or a long-horizon lifecycle policy for how many historical versions should be preserved across environments that intentionally lag several releases behind. If downstream clusters need stronger guarantees about how long a validated version remains pinable, that policy should be designed separately.
