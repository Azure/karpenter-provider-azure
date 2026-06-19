# Node Image Version Pinning in Karpenter

**Status:** Draft
**Date:** 2026-05-21
**Authors:** @rakechill
**Related:**
- [AKS Prepared Image Specification (AKS#4704)](https://github.com/Azure/AKS/issues/4704)
- [Add support for setting ImageID for nodeClass](https://github.com/Azure/karpenter-provider-azure/issues/1220)

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

The _anticipated_ policy model is:

| Concept | Definition |
|---|---|
| **In-window** | Image age ≤ 90 days from release date. No restrictions; full support. |
| **Outside-supported-window** | Image age > 90 days. Support may require upgrading to a newer image as a prerequisite for resolution. **Scale-up and all other cluster operations remain unconditionally allowed** — no operations are blocked. |
| **Unsupported** | Potential future enforcement state where specific operations (e.g., scale-up) could be blocked. Not part of initial rollout. |

### Key Policy Direction

1. AKS ships node images frequently: **weekly for Linux, monthly for Windows**.
2. Customers are expected to update regularly — via auto-upgrade channels or manual upgrades.
3. **Rollback and pinning are supported flexibility mechanisms**, but they **do not override lifecycle expectations**. They are meant for short-term operational recovery, not long-term avoidance of updates.
4. Scale-up and all other cluster operations are **unconditionally allowed** regardless of image age. Running an image outside the support window does not block any operations.

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

## 2. How Karpenter Selects Node Images Today

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

3. **Version format**: AKS image versions are observed in at least two
   formats:
   - Current format: `YYYYMM.DD.patch` (e.g., `202604.24.0`)
   - Legacy format: `YYYY.MM.DD` (e.g., `2022.10.03`)
   Both are valid pin targets if they exist in the gallery.

4. **All architecture variants share the same version** within a release.
   AKS releases all variants (gen1, gen2, arm64) together within 24 hours.

5. The **`ImageDefinition`** (e.g., `2204gen2containerd`) is derived from
   `imageFamily` + instance type requirements. It is not user-facing.

6. Image data is **cached for 3 days** (`ImageExpirationInterval`), with
   hourly cleanup. The **cache key** is currently `hash(supportedImages,
   k8sVersion)`. `imageVersion` is not part of the key — this must be
   fixed as part of this design (see section 5.5.1).

  This cache is in-memory in the operator process, so the 3-day TTL is
  a maximum, not a guarantee. Any operator pod restart clears the cache
  and causes the next reconcile to perform a fresh gallery lookup,
  which may refresh `status.images[]` earlier than 3 days in practice.

7. **SIG image versions are resolved via the public `ListNodeImageVersions`
  API** (`GET .../providers/Microsoft.ContainerService/locations/{location}/nodeImageVersions`).
  This is the authoritative source for which versions exist in SIG. Existence
  validation for a pinned `imageVersion` should check against this list before
  accepting the version as valid.

---

## 3. The Gap: What Customers Can't Do Today

A Karpenter user can set `imageFamily: Ubuntu2204` but **cannot**:

1. **Pin to a known-good version** when the latest image has a regression.
2. **Observe what version is running** without inspecting the full image
   ID in status and extracting the version segment.

The workaround today is to disable Karpenter entirely and fall back to
AKS autoscaling groups until a fixed image version ships, which defeats
the purpose of using Karpenter.

### Non-goals

- **`imageVersion` pinning for AKS Machine API + CIG**: `GetAKSMachineNodeImageVersionFromImageID()` does not support CIG images today (it returns an explicit error). Drift detection for that combination is already silently skipped. Supporting CIG pinning via the AKS Machine API path is out of scope for this design.
- **Pinning for clusters using the AKS Machine API + CIG**: the supported pinning path for CIG is clusters using the **bootstrapping client** (non-Machine-API), where drift uses a full image ID comparison and works naturally.
- **Exposing `imageVersion` on `v1alpha2`**: `imageVersion` will be added to `v1beta1` only (the storage version). Users of `v1alpha2` manifests cannot set it. This is acceptable because `v1beta1` is the current recommended API version.

---

## 4. Relationship Map

```
┌──────────────────────────────────────────────────────────┐
│                  AKSNodeClass.spec                       │
│                                                          │
│  imageFamily: Ubuntu2204          ◄── exists today       │
│                                                          │
│  imageVersion: "202604.24.0"      ◄── P0                │
│    • Pins within AKS-managed galleries                   │
│    • Advisory: within AKS support window recommended,    │
│      but not enforced — warning surfaced via condition   │
│    • Applied as version filter in SIG/CIG resolution     │
│                                                          │
│  (future) imageRef: <resource-id>  ◄── stretch            │
│    • References customer Prepared Image resource         │
│    • Bypasses gallery resolution entirely                │
│    • Orthogonal to imageFamily/imageVersion              │
│                                                          │
│  Precedence: imageRef > imageVersion                     │
│  AKSNodeClassSpec.ImageID is an unused stub (json:"-"). │
│  See section 6.4 for disposition options.                 │
│                                                          │
└──────────────────────────────────────────────────────────┘
                        │
                        ▼
┌──────────────────────────────────────────────────────────┐
│              AKSNodeClass.status                         │
│                                                          │
│  images[]:                                               │
│    - id: /CommunityGalleries/.../versions/202604.24.0    │
│      version: "202604.24.0"           ◄── new: surfaced   │
│      imageCreateDate: "2026-04-24"    ◄── new: release date│
│      requirements: [...]                                 │
│                                                          │
│  conditions[]:                                           │
│    - type: ImagesReady                                   │
│    - type: ImageWithinSupportWindow ◄── new: age warning │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

---

## 5. Design: `imageVersion` Field

### 5.1 API Surface

Add a new optional field to `AKSNodeClassSpec`:

```go
type AKSNodeClassSpec struct {
    // ...existing fields...

    // imageVersion pins node image selection to a specific AKS node image
    // version (e.g., "202604.24.0"). When set, Karpenter will use this
    // version instead of automatically selecting the latest available
    // image. The version must exist in the image gallery and should be
    // within the AKS node image support window.
      // +kubebuilder:validation:Pattern=`^(\d{6}\.\d{2}\.\d+|\d{4}\.\d{2}\.\d{2})$`
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

**UX with `imageVersion` set:** After setting `imageVersion`, a user can confirm the pin is active and the correct version is resolved by inspecting both spec and status:

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: default
spec:
  imageFamily: Ubuntu2204
  imageVersion: "202604.24.0"   # ← pinned version set by user
status:
  images:
    - id: /CommunityGalleries/AKSUbuntu-38d80f77-.../versions/202604.24.0
      version: "202604.24.0"         # ← confirms resolved version matches pin
      imageCreateDate: "2026-04-24"  # ← release date for age/staleness visibility
      requirements: [...]
  conditions:
    - type: ImagesReady
      status: "True"
    - type: ImageWithinSupportWindow
      status: "True"   # "False" + reason: ImageOutsideSupportWindow if > 90 days old
```

When `imageVersion` is **not** set, `spec.imageVersion` is absent (`omitempty`), but the active version is still always visible via `status.images[].version`, populated by the status reconciler.

**Regex validation**: `^(\d{6}\.\d{2}\.\d+|\d{4}\.\d{2}\.\d{2})$` accepts
either `YYYYMM.DD.patch` (e.g., `202604.24.0`) **OR** `YYYY.MM.DD`
(e.g., `2022.10.03`). This is enforced at admission time via the CRD
schema. Gallery existence validation in the status reconciler remains
authoritative.

### 5.2 Image Version Existence Validation

When `imageVersion` is set, the **status reconciler** must validate that
the specified version exists in the image gallery (SIG or CIG) for the
resolved image family's definitions.

Each `imageFamily` resolves to multiple **image definitions** — one per
architecture/generation variant (e.g., `2204containerd` for gen1/amd64,
`2204gen2containerd` for gen2/amd64, `2204gen2arm64containerd` for
arm64). AKS releases all variants together, so the pinned version will
typically either exist for all definitions or none. The status reconciler
populates `status.images[]` with all definitions where the version is
found. At provisioning time, `ResolveNodeImageFromNodeClass()` selects
the specific image compatible with the instance type's architecture and
hypervisor generation — which is ultimately driven by NodePool
requirements.

If no matching images are found for any definition, the reconciler sets
a status condition:

```yaml
status:
  conditions:
    - type: ImagesReady
      status: "False"
      reason: ImageVersionNotFound
      message: "imageVersion 202604.24.0 not found in gallery for image family Ubuntu2204"
```

**Why status-time, not admission-time**: Karpenter does not currently
have a validating webhook for `AKSNodeClass`, so admission-time
validation would require adding one. While webhooks run in the same pod
as the operator and could technically access the in-memory cache,
the standard pattern for this type of external-state validation in
upstream Karpenter is status conditions on the NodeClass (e.g., subnet
existence, kubernetes version readiness). Using a webhook would also
add synchronous latency and a new failure mode on every `kubectl apply`.
The status reconciler already queries the gallery and surfaces errors
asynchronously — this is the preferred pattern.

### 5.3 Image Create Date: Per-Image Field

Add `imageCreateDate` to the `NodeImage` struct so that each image in
`status.images[]` carries its own release date:

```go
type NodeImage struct {
    // ...existing fields (ID, Requirements)...

    // version is the image version extracted from the image ID
    // (e.g., "202604.24.0"). Surfaced for observability.
    // +optional
    Version string `json:"version,omitempty"`

    // imageCreateDate is the release date of this image, extracted from
    // the version string's encoded date (e.g., "2026-04-24" for version
    // "202604.24.0"). Stored as an immutable date fact; consumers
    // (controllers, CEL validation, printer columns) compute age on
    // read. This follows the same principle as creationTimestamp and
    // lastTransitionTime in upstream Kubernetes APIs.
    // +optional
    ImageCreateDate *metav1.Time `json:"imageCreateDate,omitempty"`
}
```

**Why a date, not age-in-days:** Age-in-days would need to be updated
daily or it becomes stale, causing unnecessary status churn and watch
events. A static creation date is an immutable fact — written once at
reconciliation time and never updated. Consumers can compute age, compare
dates, or bucket by month on read. This matches Kubernetes conventions
(`creationTimestamp`, `lastTransitionTime`) and avoids clock-skew
ambiguity inherent in relative integer fields.

**Why per-image, not top-level:** Different images in `status.images[]`
could have different dates if `overrideAnyGoalStateVersionsWithExisting`
retains older versions for some SKUs. Storing `imageCreateDate` on each
image avoids ambiguity and lets the support window warning compare against
the oldest entry.

**Date source:** The version string encodes a date prefix in both
supported formats. Parsing requires two branches, following the same
segment-splitting pattern used by `isNewerVersion()` in
`nodeimageversionsclient.go`:

```go
// parseImageVersionDate extracts year/month/day from an AKS image version.
// Supports both observed AKS formats:
//   YYYYMM.DD.patch  e.g. "202604.24.0"  → segments[0]="202604", segments[1]="24"
//   YYYY.MM.DD       e.g. "2022.10.03"   → segments[0]="2022",   segments[1]="10", segments[2]="03"
func parseImageVersionDate(version string) (time.Time, error) {
    segments := strings.Split(version, ".")
    if len(segments[0]) == 6 {
        // Current format: YYYYMM.DD.patch
        year, _ := strconv.Atoi(segments[0][:4])
        month, _ := strconv.Atoi(segments[0][4:])
        day, _ := strconv.Atoi(segments[1])
        return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC), nil
    } else if len(segments[0]) == 4 && len(segments) >= 3 {
        // Legacy format: YYYY.MM.DD
        year, _ := strconv.Atoi(segments[0])
        month, _ := strconv.Atoi(segments[1])
        day, _ := strconv.Atoi(segments[2])
        return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC), nil
    }
    return time.Time{}, fmt.Errorf("unrecognized image version format: %s", version)
}
```

This avoids depending on a `PublishedDate` API field which is available
for CIG but not currently exposed in the SIG path (see section 2, key
observation 3). If a `PublishedDate` field becomes available for both
gallery types in the future, it can be used instead for higher
accuracy.

### 5.4 Support Window Warning

When the resolved image's `imageCreateDate` is more than 90 days before
the current date, the status reconciler sets a warning condition. The
90-day threshold matches the existing AppLens diagnostic insight. The
enforcement support window has not yet been defined by AKS — this is a
warning only; no operations are blocked regardless of image age.

```yaml
status:
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

These conditions live on the **`AKSNodeClass.status.conditions[]`**
field, alongside the resolved **`AKSNodeClass.status.images[]`** entries
described above. In other words, both the pinned/latest resolved image
state and the support-window warning are surfaced on the `AKSNodeClass`
resource itself; nothing new is added to `NodePool` or `NodeClaim` for
this feature.

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
can alert on image staleness. This metric is computed at scrape time
from `imageCreateDate` (i.e., `now - imageCreateDate` in days), so it
remains accurate without requiring status updates.

### 5.5 Changes to Image Resolution Pipeline

Changes span two layers: `NodeImageProvider.List()` (image resolution)
and `NodeImageReconciler.Reconcile()` (status reconciliation).

#### 5.5.1 `NodeImageProvider.List()` — version resolution

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
        │    SIG: BuildImageIDSIG(..., imageVersion) directly
        │         — skip "pick latest" lookup; existence check still runs (see 5.5.4)
        │    CIG: BuildImageIDCIG(..., imageVersion) directly
        │         — skip "pick latest" lookup; existence check still runs (see 5.5.4)
        │    Cache key: hash(supportedImages, k8sVersion, imageVersion)
        │
        │  If imageVersion is UNSET:
        │    (unchanged — pick latest by date/SKU as today)
        │    Cache key: hash(supportedImages, k8sVersion)  ← unchanged
        │
        ▼
NodeImageReconciler.Reconcile()      (see 5.5.2 and 5.5.3)
        ▼
AKSNodeClass.status.images[]         (populated with pinned or latest version + imageCreateDate)
        ▼
ResolveNodeImageFromNodeClass()      (unchanged)
        ▼
VM creation with resolved image ID
```

**Cache key change required**: `cacheKey()` currently hashes only
`supportedImages + k8sVersion`. If `imageVersion` is not included,
setting or changing a pin on an existing NodeClass will return the
previously cached result for up to 3 days with no error. Fix: pass
`nodeClass.Spec.ImageVersion` into `cacheKey()` so any version change
immediately produces a cache miss and forces a fresh gallery lookup.

This is the key change needed for the status controller to correctly
take `.spec.imageVersion` into account. The first reconcile after a pin
is added, removed, or changed must bypass any cache entry produced for a
different spec state; otherwise `status.images[]` could continue to
surface the previously resolved version for up to the full 3-day TTL.
Once the cache key includes `imageVersion`, subsequent reconciles within
the TTL will correctly reuse the pinned result, because that cache entry
is now scoped to the exact spec version being requested.

The `AKSNodeClassSpec.ImageID` stub (`json:"-"`) is unused today and
is not part of this design. See section 6.4 for options on its future.

#### 5.5.2 Maintenance window interaction

The images reconciler currently decides whether to update to latest via
two triggers:

1. **`imageVersionsUnready()`** — `ImagesReady` condition is `False`
   (new nodeclass, K8s upgrade reset, etc.)
2. **`isMaintenanceWindowOpen()`** — maintenance window is currently
   open

If neither is true, `overrideAnyGoalStateVersionsWithExisting()` keeps
existing image versions and only adds newly discovered SKUs.

**When `imageVersion` is set**, the maintenance window check is
irrelevant for determining the target version — the pin is the target
regardless of window state. The reconciler should:

- Skip `isMaintenanceWindowOpen()` entirely when `imageVersion` is set.
- Skip `overrideAnyGoalStateVersionsWithExisting()` — the pin is
  authoritative; there is no "latest" to hold back.
- Always use the images returned by `NodeImageProvider.List()` directly,
  since they already reflect the pinned version.

This simplifies the reconciler path when a pin is active to:

```
imageVersion SET?
    │ YES
    ▼
goalImages = nodeImageProvider.List(ctx, nodeClass)   // returns pinned version
    │
    ▼
nodeClass.Status.Images = goalImages                  // skip MW / override logic
    │
    ▼
set ImagesReady = True (or False if version not found)
```

#### 5.5.3 Kubernetes upgrade interaction

When the K8s version reconciler detects a control plane upgrade, it sets
`ImagesReady` to `False`, which triggers a full image refresh
(Scenario A, case 2 in the existing code).

**When `imageVersion` is set**, this refresh should still resolve to the
pinned version — not latest. The pin is the customer's explicit intent
and takes precedence. Since `NodeImageProvider.List()` returns the
pinned version when `imageVersion` is set, this works naturally: the
refresh re-resolves to the same pinned version.

Note: If the pinned image is incompatible with the new K8s version, the
node may fail to join. This is an acceptable tradeoff — the customer
explicitly pinned the version and is responsible for compatibility. The
`imageCreateDate` field and support window warning provide signals to update.

#### 5.5.4 Existence validation code path

When `imageVersion` is set and `NodeImageProvider.List()` returns no
images (the pinned version doesn't exist in the gallery for any image
definition), the reconciler surfaces this through the existing
`ImagesReady` condition:

```go
// In NodeImageReconciler.Reconcile(), after List():
nodeImages, err := r.nodeImageProvider.List(ctx, nodeClass)
if err != nil {
    return reconcile.Result{}, fmt.Errorf("getting nodeimages, %w", err)
}

// ... map to goalImages ...

if len(goalImages) == 0 {
    nodeClass.Status.Images = nil
    if nodeClass.Spec.ImageVersion != nil {
        nodeClass.StatusConditions().SetFalse(
            v1beta1.ConditionTypeImagesReady,
            "ImageVersionNotFound",
            fmt.Sprintf("imageVersion %s not found in gallery for image family %s",
                *nodeClass.Spec.ImageVersion, nodeClass.Spec.ImageFamily),
        )
    } else {
        nodeClass.StatusConditions().SetFalse(
            v1beta1.ConditionTypeImagesReady,
            "ImagesNotFound",
            "ImageSelectors did not match any Images",
        )
    }
    return reconcile.Result{RequeueAfter: 5 * time.Minute}, nil
}
```

This reuses the existing "no images found" path with a more specific
reason and message when a pin is active.

### 5.6 Drift Detection

When `imageVersion` is **set**, drift detection should compare the
running node's image version against the pinned version. If they match,
the node is not drifted.

When `imageVersion` is **unset** (default), drift detection works as
today — comparing against the latest available version.

No changes are needed to `isImageVersionDrifted()` itself — it already
compares `nodeClaim.Status.ImageID` against `nodeClass.Status.Images[]`.
Since `status.images[]` will contain the pinned version when
`imageVersion` is set, the comparison naturally works: nodes running the
pinned version are not drifted, and nodes running a different version
are.

**Drift by provisioning path:**

| Path | Drift mechanism | `imageVersion` pinning supported? |
|---|---|---|
| Bootstrapping client + CIG | Full image ID comparison (`availableImage.ID == nodeClaim.Status.ImageID`) | ✅ Works naturally |
| Bootstrapping client + SIG | Full image ID comparison | ✅ Works naturally |
| AKS Machine API + SIG | `GetAKSMachineNodeImageVersionFromImageID()` converts to `gallery-definition-version` string | ✅ Works naturally |
| AKS Machine API + CIG | `GetAKSMachineNodeImageVersionFromImageID()` returns an explicit error for CIG IDs | ❌ Non-goal (see section 3) |

`imageCreateDate` is set once at reconciliation time and never updated —
it is an immutable fact derived from the version string. Age is computed
from it on read (e.g., by the metrics emitter or support window warning
logic), so there is no status churn from this field.

---

## 6. Future Extensibility: Prepared Image Specification

This section is out of scope for the `imageVersion` design but is
included to demonstrate that the API shape in section 5 is
forward-compatible with upcoming AKS capabilities.

### 6.0 Background: Prepared Image Specification

AKS is developing a **Prepared Image Specification** feature
([AKS#4704](https://github.com/Azure/AKS/issues/4704)), targeting public
preview with regional availability beginning June 2026.

#### What It Is

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

#### How It Differs from `imageVersion` Pinning

| Dimension | `imageVersion` Pinning | Prepared Image Spec |
|---|---|---|
| **What's controlled** | Which AKS-managed VHD version is used | A custom image derived from an AKS base image, with user-specified layers |
| **Image source** | AKS Community/Shared Image Gallery | Customer resource group (`customImage` resource) |
| **Lifecycle** | Governed by AKS support window; version ages out | Customer-managed; presumably rebuilt on newer base images periodically |
| **Use case** | "Don't use the latest image, it broke my workload" | "I need containers/drivers/configs cached on the node at boot" |
| **Karpenter integration** | Version is a filter within existing image family resolution | Entirely different image source; bypasses gallery resolution |

The two features are **orthogonal but share the API surface**:

- `imageVersion` operates **within** the AKS-managed image family
  (SIG/CIG) — it's a version pin.
- Prepared Image Spec operates **outside** the AKS-managed galleries —
  it references a customer-owned image resource.

A well-shaped API keeps these as distinct, non-overlapping fields
rather than overloading a single `imageID` field with two different
semantics.

### 6.1 Prerequisite: AKS Machine API Support

The AKS Machine API must support Prepared Image Spec before Karpenter
can expose it via `AKSNodeClass`. Karpenter provisions nodes through the
Machine API, and if the underlying API cannot accept a prepared image
reference, exposing it in the CRD would be non-functional.

**Gating criteria**: The AKS Machine API `CreateOrUpdate` operation must
accept a prepared image resource ID and use it for node provisioning.
Until this is confirmed, `imageRef` remains a design placeholder only.

### 6.2 Proposed API Field

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

### 6.3 Field Precedence Hierarchy

When multiple image fields are set, the following precedence applies
(using `imageRef` as the placeholder name — see section 6.4 for naming
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

### 6.4 Disposition of `AKSNodeClassSpec.ImageID`

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

### 6.5 Impact on Image Resolution Pipeline

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
        (section 5 design)
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

### 6.6 Open Questions for `imageRef`

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

## 7. Open Questions

Remaining questions not covered by the design sections above:

### Operational
1. Should we emit Kubernetes events when a pinned version crosses the
   warning threshold?
2. What is the interaction between `imageVersion` and Karpenter's
   built-in disruption budgets / node expiry?

### Support Window
3. **Resolved**: The warning threshold should be a **controller-level
   flag** (`--image-support-window-warning-days`, default `90`), not a
   CRD field or a Helm chart value. A controller flag applies uniformly
   to both NAP-managed and self-hosted Karpenter deployments, so
   self-hosted users are not left with a hardcoded threshold they cannot
   change without a code update. Helm values are not used here because
   they would not reach self-hosted deployments that manage their own
   controller flags.
4. When AKS defines an enforcement window, should Karpenter block
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
