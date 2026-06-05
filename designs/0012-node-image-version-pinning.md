# Node Image Version Pinning in Karpenter

**Status:** Draft
**Date:** 2026-05-21
**Authors:** @rakechill
**Related:**
- [AKS Prepared Image Specification (AKS#4704)](https://github.com/Azure/AKS/issues/4704)

---

## 1. Background: The Node Image Support Window

AKS is formalizing its **support window for node images**. While no
official policy has been published on Microsoft Learn yet, there are
already public signals pointing in this direction:

- **AppLens** already surfaces a diagnostic insight for clusters running
  node images older than 90 days: *"We found node image versions that
  are older than 90 days. This may place the cluster outside of the
  support scope until node image upgrades are performed."*
- The **node image rollback** documentation recommends a [maximum
  timeframe of 30 days](https://learn.microsoft.com/en-us/azure/aks/node-image-upgrade)
  for staying on a rolled-back version, framing pinning as a temporary
  recovery mechanism.
- AKS support guidance already steers customers toward upgrading as a
  prerequisite for case resolution when running stale images.

The anticipated policy model is:

| Concept | Definition |
|---|---|
| **In-window** | Image age ≤ 90 days from release date. No restrictions; full support. |
| **Outside-supported-window** | Image age > 90 days. Support may require upgrading to a newer image as a prerequisite for resolution. |
| **Unsupported** | Potential future enforcement state where specific operations (e.g., scale-up) could be blocked. Not part of initial rollout. |

### Key Policy Direction

1. AKS ships node images frequently: **weekly for Linux, monthly for Windows**.
2. Customers are expected to update regularly — via auto-upgrade channels or manual upgrades.
3. **Rollback and pinning are supported flexibility mechanisms**, but they **do not override lifecycle expectations**. They are meant for short-term operational recovery, not long-term avoidance of updates.
4. Within the supported window, scale-up is expected to be **unconditionally allowed** regardless of cluster state.

### Release Cadence (empirical, from AgentBaker release notes)

| Metric | Linux (Ubuntu 22.04) | Linux (Ubuntu 24.04) | Windows Server 2022 |
|---|---|---|---|
| Average gap | 6.9 days | 8.5 days | 22.3 days |
| P95 gap | 17 days | 17 days | 36 days |
| Max gap | 28 days | 22 days | 47 days |

This means a customer pinning to a specific version will typically have
**6+ Linux releases** or **2–3 Windows releases** available within any
90-day window.

### What This Means for Karpenter

Karpenter's node image selection should be **designed with a support
window in mind**, even though the formal policy is not yet published.
If we expose a version-pinning mechanism, it should:

- Allow customers to pin within the supported window.
- Surface image age as observable state (e.g., status conditions).
- Warn — but not block — when a pinned image exceeds 90 days, based on
  existing AppLens diagnostics. The enforcement support window has not
  yet been defined.

---

## 2. Background: Prepared Image Specification

AKS is developing a **Prepared Image Specification** feature
([AKS#4704](https://github.com/Azure/AKS/issues/4704)), targeting public
preview with regional availability beginning June 2026.

### What It Is

A top-level `customImage` Azure resource in the customer's resource group
that lets them specify:

- **Container images to pre-cache** on the node (eliminates cold-pull on
  scale-up)
- **OS-level settings** (sysctls, kernel parameters, containerd config)
- **A customization script** — a list of instructions run during image
  preparation (GPU drivers, LLMs, security policies, etc.)

This is applied at the node pool or cluster level. Subsequent nodes
provisioned from that pool use the prepared image, which has all specified
customizations baked in.

### How It Differs from `imageVersion` Pinning

| Dimension | `imageVersion` Pinning | Prepared Image Spec |
|---|---|---|
| **What's controlled** | Which AKS-managed VHD version is used | A custom image derived from an AKS base image, with user-specified layers |
| **Image source** | AKS Community/Shared Image Gallery | Customer resource group (`customImage` resource) |
| **Lifecycle** | Governed by AKS support window; version ages out | Customer-managed; presumably rebuilt on newer base images periodically |
| **Use case** | "Don't use the latest image, it broke my workload" | "I need containers/drivers/configs cached on the node at boot" |
| **Karpenter integration** | Version is a filter within existing image family resolution | Entirely different image source; bypasses gallery resolution |

### What This Means for Karpenter

When we design the API surface for image selection, we need to ensure it
can evolve to accommodate Prepared Image Spec without breaking changes.
The two features are **orthogonal but share the API surface**:

- `imageVersion` operates **within** the AKS-managed image family
  (SIG/CIG) — it's a version pin.
- Prepared Image Spec operates **outside** the AKS-managed galleries —
  it references a customer-owned image resource.

A well-shaped API would keep these as distinct, non-overlapping fields
rather than overloading a single `imageID` field with two different
semantics.

---

## 3. How Karpenter Selects Node Images Today

The current image selection pipeline:

```
AKSNodeClass.spec.imageFamily        (user input: Ubuntu2204, AzureLinux, etc.)
        │
        ▼
ImageFamily.DefaultImages()          (returns []DefaultImageOutput per arch/gen variant)
        │                             e.g. { ImageDefinition: "2204gen2containerd",
        │                                    PublicGalleryURL: "AKSUbuntu-38d80f77-...",
        │                                    GalleryName: "AKSUbuntu",
        │                                    Requirements: [amd64, HyperV-v2] }
        ▼
NodeImageProvider.List()             (queries SIG or CIG for latest version per definition)
        │
        │  SIG: nodeImageVersions.List() → match by SKU → BuildImageIDSIG(..., version)
        │  CIG: latestNodeImageVersionCommunity() → pick newest by PublishedDate → BuildImageIDCIG(..., version)
        │
        ▼
AKSNodeClass.status.images[]         (reconciled by nodeclass status controller)
        │                             e.g. [{ ID: "/CommunityGalleries/.../versions/202604.24.0",
        │                                     Requirements: [...] }]
        ▼
ResolveNodeImageFromNodeClass()      (at provisioning time: pick first status image
                                      compatible with instance type requirements)
        │
        ▼
VM creation with resolved image ID
```

### Key observations

1. **`ImageID *string` already exists** in `AKSNodeClassSpec` but is
   hidden from the API (`json:"-"`). It is an **unused stub** — nothing
   in the codebase ever sets it. The *actual* resolved image ID lives on
   `NodeClaim.Status.ImageID` (an upstream Karpenter core field), which
   is populated after VM creation from the VM's storage profile or the
   AKS Machine's `NodeImageVersion`. These are two different fields on
   two different objects.

2. **Version is the last segment** of every image ID:
   - CIG: `/CommunityGalleries/{gallery}/images/{def}/versions/{version}`
   - SIG: `/subscriptions/{sub}/.../images/{def}/versions/{version}`

3. **Version format**: `YYYYMM.DD.patch` (e.g., `202604.24.0`). This is
   the same string customers see in `az aks nodepool show` and AgentBaker
   release notes.

4. **All architecture variants share the same version** within a release.
   AKS releases all variants (gen1, gen2, arm64) together within 24 hours.

5. The **`ImageDefinition`** (e.g., `2204gen2containerd`) is derived from
   `imageFamily` + instance type requirements. It is not user-facing.

6. Image data is **cached for 3 days** (`ImageExpirationInterval`), with
   hourly cleanup.

---

## 4. The Gap: What Customers Can't Do Today

A Karpenter user can set `imageFamily: Ubuntu2204` but **cannot**:

1. **Pin to a known-good version** when the latest image has a regression.
2. **Observe what version is running** without inspecting the full image
   ID in status and extracting the version segment.
3. **Reference a Prepared Image** (future) since image selection
   is hardcoded to AKS-managed galleries.

The workaround today is to disable Karpenter entirely and fall back to
AKS autoscaling groups until a fixed image version ships, which defeats
the purpose of using Karpenter.

---

## 5. Relationship Map

```
┌──────────────────────────────────────────────────────────┐
│                  AKSNodeClass.spec                       │
│                                                          │
│  imageFamily: Ubuntu2204          ◄── exists today       │
│                                                          │
│  imageVersion: "202604.24.0"      ◄── P0                │
│    • Pins within AKS-managed galleries                   │
│    • Should be within AKS support window                 │
│    • Applied as version filter in SIG/CIG resolution     │
│                                                          │
│  (future) imageRef: <resource-id>  ◄── stretch            │
│    • References customer Prepared Image resource         │
│    • Bypasses gallery resolution entirely                │
│    • Orthogonal to imageFamily/imageVersion              │
│                                                          │
│  Precedence: imageRef > imageVersion                     │
│  AKSNodeClassSpec.ImageID is an unused stub (json:"-"). │
│  See section 7.4 for disposition options.                 │
│                                                          │
└──────────────────────────────────────────────────────────┘
                        │
                        ▼
┌──────────────────────────────────────────────────────────┐
│              AKSNodeClass.status                         │
│                                                          │
│  images[]:                                               │
│    - id: /CommunityGalleries/.../versions/202604.24.0    │
│      version: "202604.24.0"        ◄── new: surfaced     │
│      publishedDate: 2026-04-24     ◄── new: for SLA calc │
│      requirements: [...]                                 │
│                                                          │
│  conditions[]:                                           │
│    - type: ImagesReady                                   │
│    - type: ImageWithinSupportWindow ◄── new: age warning │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

---

## 6. Design: `imageVersion` Field

### 6.1 API Surface

Add a new optional field to `AKSNodeClassSpec`:

```go
type AKSNodeClassSpec struct {
    // ...existing fields...

    // imageVersion pins node image selection to a specific AKS node image
    // version (e.g., "202604.24.0"). When set, Karpenter will use this
    // version instead of automatically selecting the latest available
    // image. The version must exist in the image gallery and should be
    // within the AKS node image support window.
    // +kubebuilder:validation:Pattern=`^\d{6}\.\d{2}\.\d+$`
    // +optional
    ImageVersion *string `json:"imageVersion,omitempty"`
}
```

**Field behavior:**

| `imageVersion` | `imageFamily` | Behavior |
|---|---|---|
| unset | set | **Today's behavior.** Latest version is auto-selected for the given image family. |
| set | set | Version is pinned. `imageFamily` determines which image definitions (arch/gen variants) are used; `imageVersion` determines which version of those definitions is resolved. |
| set | unset | Version is pinned. `imageFamily` defaults to `Ubuntu` (existing default behavior). |

**Regex validation**: `^\d{6}\.\d{2}\.\d+$` matches the `YYYYMM.DD.patch`
format (e.g., `202604.24.0`). This is enforced at admission time via the
CRD schema.

### 6.2 Image Version Existence Validation

When `imageVersion` is set, the **status reconciler** must validate that
the specified version exists in the image gallery (SIG or CIG) for at
least one image definition in the resolved image family.

**Approach**: The status reconciler already queries the gallery to
populate `status.images[]`. When `imageVersion` is set, the reconciler
filters the gallery response to only include images matching the pinned
version. If no matching images are found, the reconciler sets a status
condition:

```yaml
status:
  conditions:
    - type: ImagesReady
      status: "False"
      reason: ImageVersionNotFound
      message: "imageVersion 202604.24.0 not found in gallery for image family Ubuntu2204"
```

**Why status-time, not admission-time**: Validating at admission would
require a synchronous gallery API call in the webhook hot path, adding
latency and a failure mode. The status reconciler already queries the
gallery and can surface errors asynchronously. This is consistent with
how Karpenter validates other external state (e.g., subnet existence).

### 6.3 Image Age: Computed Status Field

Add `imageAge` as a computed field in `AKSNodeClass.status` to make the
image support window observable:

```go
type AKSNodeClassStatus struct {
    // ...existing fields...

    // imageAge is the age of the oldest image in status.images[],
    // computed from the image's published date. This reflects how
    // close the node class is to the support window boundary.
    // +optional
    ImageAge *metav1.Duration `json:"imageAge,omitempty"`
}
```

The status reconciler computes this from the `PublishedDate` returned by
the gallery API (available in both CIG and SIG responses). The age is
recomputed on each reconciliation cycle.

### 6.4 Support Window Warning

When the resolved image age exceeds the warning threshold, the status
reconciler sets a warning condition. The initial threshold is **90 days**,
matching the existing AppLens diagnostic insight. The enforcement support
window has not yet been defined by AKS — 90 days is a warning only.

```yaml
status:
  imageAge: "2160h0m0s"  # 90 days
  conditions:
    - type: ImageWithinSupportWindow
      status: "False"
      reason: ImageOutsideSupportWindow
      message: "Resolved image version 202601.13.0 is 120 days old and exceeds the 90-day warning threshold. Consider updating imageVersion or removing the pin to use the latest image."
```

**This is a warning only — scale-up is not blocked.** Karpenter will
continue to provision nodes with the pinned version regardless of age.

**Implementation via non-dependent status condition:** The operatorpkg
`ConditionSet` only rolls conditions registered in
`NewReadyConditions(...)` into the root `Ready` condition.
`ImageWithinSupportWindow` is intentionally **not** registered as a
readiness dependent, so calling `StatusConditions().SetFalse(...)` on it
will not cause `Ready` to become `False`. The condition is purely
informational — it surfaces in `kubectl describe` and can be consumed by
monitoring, but never gates provisioning.

This pattern has upstream precedent: the core Karpenter `NodeClaim`
registers only 3 of its 10 condition types (`Launched`, `Registered`,
`Initialized`) as readiness dependents. Non-dependent conditions like
`ConsistentStateFound` are freely set to `False` via
`nodeClaim.StatusConditions().SetFalse(v1.ConditionTypeConsistentStateFound, ...)` in
the [consistency controller](https://github.com/kubernetes-sigs/karpenter/blob/main/pkg/controllers/nodeclaim/consistency/controller.go)
without affecting `NodeClaim` readiness.

**Path to enforcement:** If AKS later formalizes an enforcement window,
`ImageWithinSupportWindow` can be promoted to a readiness dependent by
registering it in `NewReadyConditions(...)`. This would cause `Ready` to
become `False` when the image is outside the window, gating provisioning.
The warning threshold could also be made configurable (or driven by an
AKS API response) to track the official policy without code changes.

**Metrics**: Emit a gauge metric `karpenter_image_age_days` with labels
for `nodeclass`, `image_family`, and `image_version` so that operators
can alert on image staleness.

### 6.5 Changes to Image Resolution Pipeline

The existing pipeline changes minimally. The modification is isolated to
`NodeImageProvider.List()`:

```
AKSNodeClass.spec.imageFamily        (unchanged)
AKSNodeClass.spec.imageVersion       (NEW — optional pin)
        │
        ▼
ImageFamily.DefaultImages()          (unchanged — still resolves image definitions)
        ▼
NodeImageProvider.List()             (MODIFIED)
        │
        │  If imageVersion is SET:
        │    SIG: BuildImageIDSIG(..., imageVersion) directly — skip latest lookup
        │    CIG: BuildImageIDCIG(..., imageVersion) directly — skip latest lookup
        │    Then: validate version exists via gallery API
        │
        │  If imageVersion is UNSET:
        │    (unchanged — pick latest by date/SKU as today)
        │
        ▼
AKSNodeClass.status.images[]         (unchanged structure, version is now pinned or latest)
        ▼
ResolveNodeImageFromNodeClass()      (unchanged)
        ▼
VM creation with resolved image ID
```

The `AKSNodeClassSpec.ImageID` stub (`json:"-"`) is unused today and
is not part of this design. See section 7.4 for options on its future.

### 6.6 Drift Detection

When `imageVersion` is **set**, drift detection should compare the
running node's image version against the pinned version. If they match,
the node is not drifted.

When `imageVersion` is **unset** (default), drift detection works as
today — comparing against the latest available version.

The status controller should **continue polling for newer versions** even
when `imageVersion` is set. This enables:
- Surfacing "a newer image is available" information
- Computing accurate `imageAge`
- Allowing drift detection to work correctly if the user later removes
  the version pin

---

## 7. Design: Future Extension for Prepared Image Spec (`imageRef`)

> **Status**: Not for implementation now. This section establishes the
> API shape so that `imageVersion` (section 6) is designed to be
> forward-compatible.

### 7.1 Prerequisite: AKS Machine API Support

The AKS Machine API must support Prepared Image Spec before Karpenter
can expose it via `AKSNodeClass`. Karpenter provisions nodes through the
Machine API, and if the underlying API cannot accept a prepared image
reference, exposing it in the CRD would be non-functional.

**Gating criteria**: The AKS Machine API `CreateOrUpdate` operation must
accept a prepared image resource ID and use it for node provisioning.
Until this is confirmed, `imageRef` remains a design placeholder only.

### 7.2 Proposed API Field

```go
type AKSNodeClassSpec struct {
    // ...existing fields...

    // imageRef is the Azure resource ID of a Prepared Image Specification
    // resource. When set, Karpenter uses this image directly instead of
    // resolving from the AKS image gallery. imageRef takes precedence over
    // imageVersion.
    // +kubebuilder:validation:Pattern=`(?i)^\/subscriptions\/[^\/]+\/resourceGroups\/[^\/]+\/providers\/[^\/]+\/[^\/]+\/[^\/]+$`
    // +optional
    ImageRef *string `json:"imageRef,omitempty"`
}
```

### 7.3 Field Precedence Hierarchy

When multiple image fields are set, the following precedence applies
(using `imageRef` as the placeholder name — see section 7.4 for naming
options):

```
imageRef  >  imageVersion
```

`imageFamily` is not part of this hierarchy — it determines *which*
image definitions to resolve (arch/gen variants), not *which version or
source*. It is always used for image definition selection regardless of
whether `imageRef` or `imageVersion` is set.

| Fields set | Resolution behavior |
|---|---|
| `imageRef` only | Use the prepared image directly. Skip gallery resolution entirely. `imageFamily` is used only as a hint for bootstrapping/distro detection. |
| `imageRef` + `imageVersion` | `imageRef` wins. `imageVersion` is ignored. A warning condition is set. |
| `imageVersion` (± `imageFamily`) | Pin to the specified version within the image family's gallery definitions. |
| `imageFamily` only | Today's behavior — resolve latest version from gallery. |

**CEL validation rule** (future, when `imageRef` is exposed):

```yaml
x-kubernetes-validations:
  - message: "imageRef and imageVersion are mutually exclusive; imageRef takes precedence"
    rule: "!(has(self.imageRef) && has(self.imageVersion))"
```

### 7.4 Disposition of `AKSNodeClassSpec.ImageID`

The existing `ImageID *string` field on `AKSNodeClassSpec` is currently
an unused stub (`json:"-"`). Nothing sets or reads it. With the
introduction of `imageVersion` and a future prepared image spec field,
we need to decide what to do with it. Three options:

#### Option A: Remove `ImageID`, add `imageRef` (recommended)

Remove the `ImageID` stub entirely. Introduce a new `imageRef` field
for prepared image spec references. This gives the cleanest API surface:

- `imageFamily` — which OS image family (exists today)
- `imageVersion` — pin to a specific AKS gallery version (this design)
- `imageRef` — reference a Prepared Image Spec resource (future)

**Pros**: No legacy baggage. Clear naming — `imageRef` signals "a
reference to an external image resource" which is exactly what Prepared
Image Spec is. No confusion with `NodeClaim.Status.ImageID`.

**Cons**: Removing a field (even an unexposed one) requires verifying no
internal code depends on it.

#### Option B: Remove `ImageID`, add `preparedImageSpec`

Same as Option A but use `preparedImageSpec` instead of `imageRef` as
the field name. This is more descriptive and ties directly to the AKS
feature name.

**Pros**: Self-documenting — customers immediately understand what the
field is for. Aligns with AKS terminology.

**Cons**: Longer field name. If AKS renames the feature, the field name
becomes stale. Less generic if we ever need to support other custom
image types beyond Prepared Image Spec.

#### Option C: Repurpose existing `ImageID` for prepared image spec

Unhide the existing `ImageID` field (change `json:"-"` to
`json:"imageID,omitempty"`) and use it to hold the Prepared Image Spec
resource ID.

**Pros**: No new field needed. Reuses existing struct plumbing.

**Cons**: `imageID` is ambiguous — it could mean a gallery image ID, a
prepared image ID, or the same thing as `NodeClaim.Status.ImageID`.
Customers and maintainers would need to learn that `imageID` on
`AKSNodeClass.spec` means something different than `imageID` on
`NodeClaim.status`. The field was originally stubbed with gallery image
semantics in mind, not custom image references.

#### Recommendation

**Option A** provides the cleanest separation of concerns and avoids
naming confusion. The final decision depends on whether AKS settles on
a stable feature name and resource type for Prepared Image Spec before
we implement.

### 7.5 Impact on Image Resolution Pipeline

```
              prepared image field set?
                    ┌───┤
                    │ YES
                    ▼
              Use prepared image directly
              (skip gallery resolution)
              Populate status.images[] with single entry
                    │
                    │ NO
                    ▼
              imageVersion set?
              ┌───┤
              │ YES
              ▼
        Pin to imageVersion within imageFamily
        (section 6 design)
              │
              │ NO
              ▼
        Resolve latest from gallery
        (today's behavior)
              │
              ▼
        status.images[] populated
              │
              ▼
        ResolveNodeImageFromNodeClass()
              │
              ▼
        VM creation
```

### 7.6 Open Questions for `imageRef`

These must be resolved before `imageRef` implementation begins:

1. **Resource type**: What is the exact Azure resource type for a
   Prepared Image? This determines the `imageRef` validation pattern.
2. **Bootstrapping**: When using a prepared image, how does Karpenter
   determine the distro/OS for bootstrap script selection? Does
   `imageFamily` serve as a hint, or does the prepared image resource
   expose metadata?
3. **Lifecycle**: Are prepared images subject to the AKS support
   window, or do they follow a customer-managed lifecycle? This affects
   whether `ImageWithinSupportWindow` applies.
4. **Machine API contract**: What fields does the Machine API expect
   when provisioning with a prepared image? Is it a direct image
   reference, or does it go through a different API path?
5. **Role of `imageFamily` when `imageRef` is set**: Should `imageFamily`
   still be required or used when `imageRef` is explicitly set? It may be
   needed as a hint for bootstrapping/distro detection, or the prepared
   image resource itself may expose sufficient metadata to make it
   unnecessary.

---

## 8. Open Questions

Remaining questions not covered by the design sections above:

### Compatibility
1. How does `imageVersion` interact with NAP (Node Auto-Provisioning)?
   There is a proposed (not yet implemented) NAP behavior that would
   force latest image adoption when image age exceeds a threshold,
   bypassing maintenance windows. If this is implemented, does a
   Karpenter `imageVersion` pin override or defer to NAP behavior?
2. Does the AKS Machine API path (`ProvisionModeAKSMachineAPI`) need
   separate handling for version pinning, or does it flow through the
   same image resolution?

### Operational
3. Should we emit Kubernetes events when a pinned version crosses the
   warning threshold?
4. What is the interaction between `imageVersion` and Karpenter's
   built-in disruption budgets / node expiry?

### Support Window
5. Should the warning threshold (currently 90 days) be configurable,
   e.g., via a field on `AKSNodeClass.spec` or a controller flag? This
   would allow tracking the official AKS policy without code changes
   when the enforcement window is formalized.
6. When AKS defines an enforcement window, should Karpenter block
   provisioning (by promoting `ImageWithinSupportWindow` to a readiness
   dependent) or only surface the condition and leave enforcement to
   AKS-side controls?

---

## References

- [AKS Support Policies](https://learn.microsoft.com/en-us/azure/aks/support-policies)
- [AKS Node Image Auto-Upgrade](https://learn.microsoft.com/en-us/azure/aks/auto-upgrade-node-os-image)
- [AKS Custom Node Configuration](https://learn.microsoft.com/en-us/azure/aks/custom-node-configuration)
- [Prepared Image Specification (AKS#4704)](https://github.com/Azure/AKS/issues/4704)
- [Karpenter image resolution code](../pkg/providers/imagefamily/resolver.go)
- [Karpenter node image provider](../pkg/providers/imagefamily/nodeimage.go)
