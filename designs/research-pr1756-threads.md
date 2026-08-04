# Research Notes: PR #1756 Open Threads

Covers threads 1, 2, 4, 6, 9 from @matthchr's review.

These notes distinguish between:

- **Observed current provider behavior** from local code in this repo.
- **Documented AKS behavior** from public Microsoft Learn / REST API docs.
- **Inference / design recommendation** where the docs do not fully prove the exact NAP behavior yet.

Primary references used here:

- [AKS agent pool Create/Update REST API](https://learn.microsoft.com/en-us/rest/api/aks/agent-pools/create-or-update?view=rest-aks-2025-07-01)
- [AKS agent pool Get Upgrade Profile REST API](https://learn.microsoft.com/en-us/rest/api/aks/agent-pools/get-upgrade-profile?view=rest-aks-2025-07-01)
- [AKS supported Kubernetes versions](https://learn.microsoft.com/en-us/azure/aks/supported-kubernetes-versions)
- [AKS planned maintenance](https://learn.microsoft.com/en-us/azure/aks/planned-maintenance)
- [AKS node OS auto-upgrade / node image upgrade behavior](https://learn.microsoft.com/en-us/azure/aks/auto-upgrade-node-os-image)
- [AWS Karpenter NodeClasses](https://karpenter.sh/docs/concepts/nodeclasses/)
- Local controller code: [pkg/controllers/nodeclass/status/images.go](../pkg/controllers/nodeclass/status/images.go)

---

## Thread 6 — When do images actually change? Maintenance windows vs cadence?

**This is the most operationally critical finding.**

### How NodeImageReconciler works today

This section is directly backed by local provider code in [pkg/controllers/nodeclass/status/images.go](../pkg/controllers/nodeclass/status/images.go).

The reconciler runs on every AKSNodeClass event **and** re-queues every **5 minutes** (`RequeueAfter: 5 * time.Minute`). On each pass it calls `r.nodeImageProvider.List(ctx, nodeClass)` to resolve the latest available images from the gallery. However, **whether it writes those latest images into `status.images` depends on the maintenance window:**

```
shouldUpdate := imageVersionsUnready(nodeClass)   // true only if ImagesReady condition is false
if !shouldUpdate {
    shouldUpdate, err = r.isMaintenanceWindowOpen(ctx)  // reads ConfigMap
}
if !shouldUpdate {
    goalImages = overrideAnyGoalStateVersionsWithExisting(nodeClass, goalImages)
    // ↑ KEEPS existing versions, only adds new SKUs
}
```

**So the flow is:**

1. **Every 5 minutes:** Reconciler wakes up and resolves the latest images from the gallery.
2. **If `ImagesReady` condition is false**: apply latest immediately, regardless of maintenance window. The code comments explicitly call out newly created NodeClasses and indirect handling of Kubernetes version changes here, although the exact controller path that flips `ImagesReady` false on k8s version changes should still be verified end-to-end.
3. **Else if maintenance window is open** (checked via the `upcoming-maintenance-window` ConfigMap in the system namespace, for the `aksManagedNodeOSUpgradeSchedule` channel): apply latest.
4. **Else** (maintenance window closed or not configured): run `overrideAnyGoalStateVersionsWithExisting` — only new SKUs get latest; existing SKUs keep their current version.

**If no maintenance window is configured** for `aksManagedNodeOSUpgradeSchedule`, the reconciler **fails open** — it always applies the latest. This lines up with AKS docs: planned maintenance is optional, the `NodeImage` channel ships on a weekly cadence, and maintenance windows control when those disruptive node image upgrades begin.

Relevant sources:

- Local code: [pkg/controllers/nodeclass/status/images.go](../pkg/controllers/nodeclass/status/images.go)
- AKS planned maintenance: [planned maintenance docs](https://learn.microsoft.com/en-us/azure/aks/planned-maintenance)
- AKS node image upgrades: [node OS auto-upgrade docs](https://learn.microsoft.com/en-us/azure/aks/auto-upgrade-node-os-image)

### Implications for `latestImageVersion`

The design proposes `status.latestImageVersion` as "the latest resolved image version from the gallery, updated on every reconcile pass regardless of pinning." This is **correct** as a read of the gallery — the reconciler always resolves gallery images.

**But there's a subtle distinction:**
- `status.latestImageVersion` = what is currently available in the gallery (always up to date)
- `status.images` = what Karpenter is actually using, which only moves to latest when the MW is open

When a customer is pinned and a MW has not yet opened, `status.latestImageVersion` may be ahead of what nodes would actually use if the pin were removed. This is still useful (it shows what's available), but the design should clarify this distinction.

**Proposed clarification for the design doc:**
> `latestImageVersion` reflects the latest version available in the gallery and is updated on every reconcile pass. It does not reflect whether that version has been or would be applied — that depends on maintenance window state. It is the authoritative source for validating `spec.versionSelection.nodeImageVersion` pin values.

---

## Thread 2 — kubernetesVersion: minor vs patch? AKS behavior?

### AKS agent pool `orchestratorVersion` behavior

From the AKS REST API `orchestratorVersion` field description (agent pool create/update):

> "Both patch version `<major.minor.patch>` (e.g. `1.20.13`) and `<major.minor>` (e.g. `1.20`) are supported. When `<major.minor>` is specified, the latest supported GA patch version is chosen automatically. Updating the cluster with the same `<major.minor>` once it has been created (e.g. `1.14.x → 1.14`) will **not** trigger an upgrade, even if a newer patch version is available."

The `currentOrchestratorVersion` response field always returns the full `major.minor.patch` regardless of what was specified. The upgrade profile API likewise returns concrete patch values via `properties.kubernetesVersion` and `properties.upgrades[*].kubernetesVersion`.

### AKS alias minor version

AKS supports specifying just a minor version (e.g. `1.32`) and it auto-resolves to the latest GA patch. Crucially, **if a new patch comes out, re-specifying the same minor version does NOT trigger a re-upgrade.** The customer would need to specify the new patch explicitly, or unset and rely on auto-upgrade.

Relevant sources:

- [AKS agent pool Create/Update REST API](https://learn.microsoft.com/en-us/rest/api/aks/agent-pools/create-or-update?view=rest-aks-2025-07-01)
- [AKS supported Kubernetes versions: alias minor version](https://learn.microsoft.com/en-us/azure/aks/supported-kubernetes-versions#alias-minor-version)
- [AKS agent pool Get Upgrade Profile REST API](https://learn.microsoft.com/en-us/rest/api/aks/agent-pools/get-upgrade-profile?view=rest-aks-2025-07-01)

### Implications for `spec.versionSelection.kubernetesVersion`

Two viable approaches:

**Option A: Support both formats, mirror AKS behavior**
- Accept `1.32` (minor) → Karpenter uses whatever patch is currently in `status.kubernetesVersion` for that minor (resolves via cluster)
- Accept `1.32.5` (patch) → pin to that exact patch
- Re-specifying the same minor doesn't trigger drift
- Drift comparison: compare running node's k8s version against `currentOrchestratorVersion`-equivalent

**Option B: Minor only**
- Simpler to implement and reason about
- Avoid ambiguity of whether `1.32.5` means "exactly this patch" or "at least this patch"
- Patch version management stays with AKS

**Recommendation:** Start with **minor only** for v1 simplicity. Add patch support later if customers need finer control. Document that specifying a minor version adopts the latest GA patch for that minor, consistent with AKS alias minor version semantics.

### What happens when a new patch releases for the pinned minor?

If `spec.versionSelection.kubernetesVersion = "1.32"` and AKS releases `1.32.6` as a new patch:
- If the node is on `1.32.5`, is it considered drifted?
- In AKS agent pool semantics: NO — re-specifying the same minor doesn't trigger a re-upgrade
- For NAP: this is an open question. Likely we should NOT drift on patch-within-minor to align with AKS semantics, but this should be confirmed with Node SIG.

---

## Thread 4 — Control plane upgrade + version skew interaction

### What AKS RP does today

The public AKS docs clearly establish that control plane and node pools must satisfy version skew rules, but they are not perfectly internally consistent about the exact window:

1. The supported-versions FAQ says AKS follows **N-3 starting from Kubernetes 1.28 onward**.
2. The agent-pool Create/Update REST description still says the node pool minor version must be within **two minor versions** of the control plane.

Because of that mismatch, the safest design conclusion is:

- Karpenter should assume the RP enforces skew compatibility.
- Karpenter should surface an explicit NodeClass condition before or when the RP rejects provisioning.
- We should avoid claiming exact control-plane-upgrade failure semantics until they are verified empirically for NAP-backed pools.

What is well supported by current docs:

1. **Provisioning-time compatibility is validated.** The docs state that node pool versions must remain within the allowed skew window and cannot be greater than the control plane version. A pinned `kubernetesVersion` outside that window should therefore be expected to fail at the RP boundary for new provisioning.

2. **Support / upgrade policy also depends on skew.** The supported-versions FAQ states that if you upgrade control plane independently from node pools, you must satisfy Kubernetes skew policies.

What is still inference and should be verified:

1. Whether a NAP-backed, version-pinned pool definitively blocks a cluster control plane upgrade at the RP.
2. Whether the exact enforced window for that path is N-2 or N-3 for the AKS versions we care about.

### How this interacts with `spec.versionSelection.kubernetesVersion`

If a customer pins `spec.versionSelection.kubernetesVersion = "1.30"` and the cluster control plane is at `1.33` (still within N-3), everything is fine. But if the control plane then upgrades to `1.34`:
- The skew becomes 4 minor versions
- The RP should be expected to **reject new node provisioning** for this NodeClass once the requested version is outside the supported skew window
- Karpenter should surface this as a condition (e.g. `KubernetesVersionSkewViolation`) rather than silently failing

**Additionally:** AKS RP may also block or constrain the control plane upgrade path itself if NAP-provisioned nodes are too far behind, but this note should treat that as a hypothesis to validate rather than a proven fact. That scenario is still important to design for because version locking in NAP creates a new way to hit skew edges that does not exist today.

Relevant sources:

- [AKS supported Kubernetes versions FAQ](https://learn.microsoft.com/en-us/azure/aks/supported-kubernetes-versions#what-is-the-allowed-difference-in-versions-between-the-control-plane-and-node-pools)
- [AKS agent pool Create/Update REST API](https://learn.microsoft.com/en-us/rest/api/aks/agent-pools/create-or-update?view=rest-aks-2025-07-01)

### Questions to resolve

1. Does AKS RP actually block control plane upgrades if NAP nodes are pinned at an old version? (Likely yes, since NAP nodes are AKS-managed agent pools under the hood.)
2. Should Karpenter proactively warn when the skew gap is approaching the limit (e.g. at N-2)?
3. Should Karpenter automatically clear `spec.versionSelection.kubernetesVersion` when a control plane upgrade would cause a skew violation? (Probably not — the customer should be in control.)

---

## Thread 1 — API future-proofing for selectors (AWS comparison)

### How AWS Karpenter does it

This section is an API-shape comparison, not evidence that Azure should copy AWS semantics exactly.

AWS uses `amiSelectorTerms` — an **array of term objects**, where:
- Each term is an AND of conditions (tags, id, name, alias, owner, etc.)
- Terms are OR'd together
- The `alias` field supports pinning: `alias: al2023@v20240807` or floating: `alias: al2023@latest`

```yaml
spec:
  amiSelectorTerms:
    - alias: al2023@v20240807        # pin to specific version
    - tags:
        my-org/validated: "true"      # select by tag
    - id: ami-123                     # select by ID
```

The key insight: AWS treats AMI selection as **declarative/selector-based** — you describe criteria for what you want, and the system resolves it. Pinning is just one kind of selector term.

### How our current design compares

Our design is more **imperative** — you specify the exact version string. This is simpler but less extensible. The reviewer's concern is that when we later add selectors (e.g. "select images from a validated channel"), they won't fit cleanly alongside a direct version string.

One important Azure-specific caveat: today this provider resolves a set of compatible image definitions first, then the design rewrites or validates the **version suffix** for each resolved image. That means a future Azure selector surface may be a filter/ranking mechanism over resolved image definitions, not necessarily a direct equivalent of AWS `amiSelectorTerms`.

**A more future-proof shape might look like:**

```yaml
spec:
  versionSelection:
    nodeImageVersion: "202601.15.0"       # v1: simple string
    kubernetesVersion: "1.32"
    # future:
    selectors:
      validated: "true"
      ring: "stable"
```

**Recommended approach for the design doc:**
- Keep `nodeImageVersion` as a simple string for v1 (explicit, easy to validate against status)
- Add a note that a future `selectors` map under `spec.versionSelection` is the intended extensibility path
- `versionSelection` provides a clear home for both explicit version fields and future free-form image-filter selectors

Relevant source:

- [AWS Karpenter NodeClasses](https://karpenter.sh/docs/concepts/nodeclasses/)

---

## Thread 9 — `latestImageVersion` for multiple image families

### The problem

`status.images` is already an array of `NodeImage` objects, each with:
- `id`: full gallery path including version suffix (e.g. `.../versions/202607.15.0`)
- `requirements`: which instance types/architectures can use this image

Today in practice, all images for a given NodeClass resolve to the **same version suffix** (e.g. `202607.15.0`). But the data model allows for different images to have different version suffixes. @matthchr noted this has apparently happened in rare edge cases.

### Problem with a single `latestImageVersion` string

If images can have different version suffixes, a single `status.latestImageVersion = "202607.15.0"` string may not accurately represent all images. For example:
- Gen2 amd64 is on `202607.15.0`
- Arm64 might be on `202607.14.0` (hypothetically)

In this case, `latestImageVersion` is ambiguous.

This is not only a status-modeling problem. It also affects validation semantics: if `spec.versionSelection.nodeImageVersion` is compared against a single `status.latestImageVersion`, that validation may claim a version is valid even when only some resolved image families actually have that suffix. Any v1 design that keeps a single string must also define how reconcile-time validation behaves when resolved image families diverge.

### Options

**Option A: Keep single string, document assumption**
Document that `latestImageVersion` reflects the version of the "primary" image (e.g. Gen2 amd64) and is expected to be consistent across all resolved images in practice. If divergence occurs, validation should catch it via reconcile-time comparison.

**Option B: Map from image family → version**
```go
LatestImageVersions map[string]string `json:"latestImageVersions,omitempty"`
// e.g. {"2204gen2containerd": "202607.15.0", "2204gen1containerd": "202607.15.0"}
```
More precise but more complex for customers to use in CEL validation.

**Option C: Per-image latest version embedded in `status.images`**
Add a `latestVersion` field alongside each `status.images` entry:
```go
type NodeImage struct {
    ID           string                       `json:"id"`
    Requirements []corev1.NodeSelectorRequirement `json:"requirements"`
    LatestVersion string                      `json:"latestVersion,omitempty"` // new
}
```
Most precise, but bloats the status.

**Recommended for v1:** Option A with a note. The design should acknowledge the multi-image version assumption and add it to the open questions or future design work.

Relevant source:

- [AKS agent pool Get Upgrade Profile REST API](https://learn.microsoft.com/en-us/rest/api/aks/agent-pools/get-upgrade-profile?view=rest-aks-2025-07-01)
