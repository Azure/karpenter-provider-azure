# Guidance: Enabling a New Azure/AKS Feature in Karpenter

**Last updated:** August 27, 2026

**Status:** Guidance

## Overview

A checklist and set of callouts for contributors adding
support for an Azure or AKS feature that Karpenter/NAP does not yet expose. Derived from
how features such as `security.encryptionAtHost`, `security.trustedLaunch`, `artifactStreaming`, `localDNS`,
`gpu.mode`, `linuxOSConfig`, and Ultra SSD actually landed.

It does not replace a feature-specific design doc. Write one from
[`0000-TEMPLATE.md`](./0000-TEMPLATE.md), including its **FAQ**, **Testing**, and
**Production Readiness** sections.

### Goals

* Give contributors the decision framework and the file-level checklist in one place.
* Surface the failure modes that have historically required a revert or a follow-up PR.
* Make review expectations explicit before code is written.

### Non-Goals

* Restate `make verify` / CI enforcement.
* Replace [`0015-nodeclass-vs-labels.md`](./0015-nodeclass-vs-labels.md), the authority on
  the label-vs-NodeClass decision.
* Cover non-feature work (refactors, dependency bumps).

---

## Step -1: Readiness gate

Answer these before writing code. Each maps to a question a reviewer has actually asked.

1. **Does the downstream support exist today?** Check the vendored SDK for the
   corresponding `Machine*Profile` field and confirm the RP/agentbaker accepts it in your
   target regions. For example - `LocalDNSProfile` did not exist on `MachineProperties` until the
   armcontainerservice v9 bump (#1609), which is why #1610 was a separate, later PR.

2. **What is the AKS AgentPool API default?** Look it up first. See the worked failure in
   Step 1 — getting this wrong cost three PRs and a deleted helper function.

3. **Is it configurable at the cluster (MC) level as well as per-pool?** If both, state
   what happens when they disagree. From #1704: *"Concerned about defining this on the
   AKSNodeClass for machine api, when it will also be configurable on the MC. Might lead
   to unexpected states."*

4. **Is it mutable after creation?** For example - Ultra SSD can only be set at cluster or nodepool
   creation. Reviewers pushed back on that as unusual for a pool-level feature and asked
   *why*. If AKS forbids post-creation change, say so and confirm CEL/drift match.

5. **How does the AKS API validate it, and does Karpenter provably oblige the same rule?**
   From #1704: *"'prove' that Karpenter obliges it... so that we can avoid provisioning
   time failures or accidentally having to support what AKS doesn't."*

6. **What does it cost or tradeoffs in enabling this feature?** 
   Flag or test for any potential regression scenarios. State the measured effect on provisioning latency and per-node Azure API calls, or state why there is none.

7. **What is the removal cost?** From #1676: a label used only on the
   `bootstrappingclient` path becomes a future user-facing removal, because that path is
   planned for deprecation.

8. **Do you have a kill switch?** See Step 1.

> **Never ship a label or selector for a capability you cannot yet honor.** #872 was a
> BREAKING change removing `sku-encryptionathost-capable` for exactly this reason.

---

## Step 0: Decide the surface

[`0015-nodeclass-vs-labels.md`](./0015-nodeclass-vs-labels.md) is the authority:

> If it is a scheduling decision, **and it is something that can be on by default or
> represented as a simple capability**, make it a well-known label.
>
> If it is a feature, especially a complex feature with many possible values or related
> settings, make it an `AKSNodeClass` field.

The emphasized clause matters. Ultra SSD **affects pricing**, so it cannot be enabled by
default just because an offering supports it. As the author put it in review:

> *"the label on the offering means 'this offering supports Ultra SSD' whereas on the
> nodeclaim it means 'this nodeclaim wants nodes with it enabled'."*

That ambiguity is why `0015` **discourages** exposing both surfaces — only `os-sku` and
`fips_enabled` do so today.

`0015` also frames the NodeClass axis as **validation complexity**, not field count.

Note that #1704 **started as an AKSNodeClass field and became a label during review**,
with #1775 kept as a NodeClass implementation for comparison. Expect to prototype both if
the call is close.

`0015`'s closing note is the tiebreaker: **mimic how the capability works in AKS.**

---

## Step 1: API definition — `pkg/apis/v1beta1/aksnodeclass.go`

Add to **`v1beta1` only**. `v1alpha2` is deprecated; do not add parity fields there.

* **Pointer + `+optional`** on every new field.
* **Group by domain, not by feature.** From #1704: *"I wonder if we want a `Disk` or
  `Storage` section here, so we can group storage-related capabilities in the same way we
  do for GPU/Linux/etc, rather than `UltraSSD` which is too narrowly scoped to ever get new
  fields."* Prefer `storage.enableUltraSSD` over a top-level `ultraSSD` struct.
* **Named string type + `+kubebuilder:validation:Enum` on the type**, with a `const` block
  documenting each value (`GPUMode`, `LocalDNSMode`).
* **Nil-safe accessors** on `*AKSNodeClass`, below `Hash()`. All provisioning code calls
  the accessor, never the raw pointer chain.
* **Two accessors when effective value ≠ user intent.** `IsArtifactStreamingEnabled(arch)`
  (architecture-aware, provisioning) vs `IsArtifactStreamingExplicitlyEnabled()`
  (architecture-independent, instance-type filtering + cache key). Collapsing them makes
  the cache key wrong.

### Defaults: match the AKS AgentPool API

> **Worked failure — artifact streaming changed its default three times in two weeks.**
>
> | PR | Merged | Signature | Default |
> |---|---|---|---|
> | #1397 | Mar 19 | `IsEnabled(arch)` | `true` for all AMD64 |
> | #1600 | Mar 31 | `IsEnabled(arch, imageFamily)` + new `osSKUToImageFamily()` | Ubuntu `true`, AzureLinux `false` |
> | #1611 | Apr 2 | `IsEnabled(arch)` — helper **deleted** | `false` for all |
>
> #1397's description said the feature was "disabled by default with Karpenter (NAP)"
> while its code said `return true`. #1600 added a 10-line OSSKU→image-family mapper to
> `provisionclientbootstrap.go`; #1611 deleted it two days later on discovering the
> AgentPool API is simply opt-in. Each step churned the CRD text, the Go doc comment, the
> bootstrap call site, the unit table, the labels test, and the E2E case title.
>
> **Look up the AgentPool default before writing the accessor.** Put the full matrix —
> field value × architecture × image family → effective result — in the PR description,
> and check the description agrees with the diff.

Final shape, for reference:

```go
func (a *ArtifactStreaming) IsEnabled(arch string) bool {
    if arch == karpv1.ArchitectureArm64 { return false }
    return a != nil && a.Enabled != nil && *a.Enabled
}
```

### Kill switch

Ship one. Pick deliberately:

| Idiom | Example | Use when |
|---|---|---|
| Default-off optional field | `artifactStreaming.enabled` | User-facing toggle |
| Version threshold constant | `localDNSPreferredK8sVersionThreshold` | Gradual rollout; raise to disable |
| Operator option | `--enable-node-hardening` (#1818) | Behavior change with no per-NodeClass meaning |

A change that can only be undone by a revert will get pushback.

### Hash and drift

`AKSNodeClassHashVersion` is currently `"v3"`. Bump **only** when you change a default on
an already-hashed field, add a field with an already-set value to the hash, or remove one.
A new optional nil field under `IgnoreZeroValue: true` does **not** require a bump.
Getting this wrong replaces every node in every existing cluster.

`hash:"ignore"` is only for fields updatable in place (see `Tags`) — those go through
Step 3b.

Add entries to **both** tables in `aksnodeclass_hash_test.go`: the static-hash table (with
the literal expected hash) and the "should change hash" table.

### CEL validation

Its own review pass. #1301 exists purely to restore validation dropped from #1233.

* **`listType=map` enforces key uniqueness — do not write a CEL uniqueness rule.**
  Confirmed in the merged CRD: `localDNS.{kube,vnet}DNSOverrides` carry
  `x-kubernetes-list-type: map` with `x-kubernetes-list-map-keys: [zone]` and **no**
  uniqueness rule.
* **Place rules at the right level.** Intra-item rules go on the item
  (`LocalDNSZoneOverride`: `'cluster.local' cannot be forwarded to VnetDNS`); cross-item
  rules go on the array (`must contain required zones '.' and 'cluster.local'`);
  cross-field rules go on the spec (FIPS × imageFamily × trustedLaunch).
* **Keep a sensible `maxItems`.** LocalDNS uses `maxItems: 100`. It was originally required
  to bound a CEL uniqueness check; the check was dropped once `listType=map` was
  understood, but the limit was kept deliberately.
* **Adding CEL rules breaks existing fixtures** that were never valid. Expect churn.
* Per the #1301 discussion, CEL cost-budget failures surface at **CRD deployment**, not at
  `make verify` — so the signal arrives late in CI. Worth knowing when a large rule set
  passes locally.
* `pkg/apis/.../validation.go` is a deliberate skeleton for what CEL cannot express. Use it
  rather than inventing a location.
* **CEL only sees `self`.** It cannot reference NodePool. If validity depends on
  architecture, zone, or NodePool requirements you need a filter (Step 2), not validation.
  `ArtifactStreaming.IsEnabled`'s doc comment records this precedent explicitly.

---

## Step 2: Instance type and offering filtering

If the feature restricts which SKUs or zones are usable, wire it into
`pkg/providers/instancetype/instancetypes.go`:

1. **Add the field to `instanceTypeParameters`.** This struct is the instance-type cache
   key. Omitting it serves stale, wrongly-filtered instance types to a different NodeClass.
   Most commonly missed step.
2. Populate it in `List()` from the accessor.
3. Add `isInstanceTypeSupportedBy<Feature>` and chain it into
   `isInstanceTypeSupportedByFilters`.

```go
func (p *DefaultProvider) isInstanceTypeSupportedByFeature(sku *skewer.SKU, params *instanceTypeParameters) bool {
    if !params.FeatureEnabled { return true }        // never narrow when disabled
    value, err := sku.GetCapabilityString("FeatureSupported")
    if err != nil { return false }                   // absent capability == unsupported
    return strings.EqualFold(value, "True")
}
```

Fail **closed** (exclude) when the feature is requested; fail **open** when it is not.

* **Zonal variance needs evidence.** From #1704: *"Do we have examples where ultrassd is
  enabled in some zones but not others?"* Show the empirical case, not just that skewer
  exposes the API. Ultra SSD uses `sku.IsUltraSSDAvailableInAvailabilityZone(zone)` per
  zone, matching RP logic.
* A filter that can empty the candidate set surfaces as
  `no instance type has the required offering` with no mention of your feature. Add your
  filter to the table in [`0012`](./0012-quota-fungibility-reactivity-improvements.md).
* New failure modes need classification in `beginCreateErrorHandling` / the
  unavailable-offerings cache, or they degrade to generic `CreateInstanceFailed`.
* Update `pkg/fake/zz_generated.sku.*.go` so both a supporting and a non-supporting SKU
  exist in the test region.

---

## Step 3: Status resolution (only if the value must be resolved)

If the effective value depends on cluster state, do not resolve it at provisioning time.
Follow `pkg/controllers/nodeclass/status/localdns.go`.

* `spec.<feature>.mode` accepts `Preferred | Required | Disabled`.
* A sub-reconciler under `pkg/controllers/nodeclass/status/` is the **sole writer** of
  `status.<feature>State` and contributes a `<Feature>Ready` condition folded into the
  aggregate `Ready`.
* Provisioning reads **status**, never spec. `IsLocalDNSEnabled()` is a pure pointer read,
  making the per-SKU filter loop free.
* If downstream rejects the tri-state, translate at the wire boundary.
  `ResolvedLocalDNSForWire()` maps `Preferred` → `Required`/`Disabled` so the RP can never
  re-resolve to a different answer than the source of truth.

### Verified behaviors to copy

* **Sticky is asymmetric.** Enabled is sticky under `Preferred`; Disabled is not. Users opt
  out only by changing Mode.
* **Errors requeue; they do not fail safe.** `meetsClusterRequirements` propagates every
  error; `reconcilePreferred` sets `Ready=False` and returns it; controller-runtime backs
  off. There is **no `IsForbidden` special case** in the merged code — a review thread
  discussed adding one, but it is not there. Do not add one without discussion.
* **Periodic requeue is mandatory.** `localDNSPreferredRequeueAfter = 5 * time.Minute`,
  because cluster gate inputs (NetworkPolicies, upstream DaemonSets) mutate out-of-band
  without producing an AKSNodeClass event. Without it, your feature never re-resolves.
* **No watermark fields.** An earlier iteration stored `ObservedGeneration` /
  `ObservedKubernetesVersion` / `ResolveFailures`; review removed them (*"Can we just do
  this locally in-memory rather than storing it here?"*). Sticky reads
  `Status.LocalDNSState` itself; retry state lives in controller-runtime backoff.
* **Unknown enum value clears state and logs at Error**, with a comment noting CRD enum
  validation should make the branch unreachable.
* **Dynamic-client CRD probes check both `IsNotFound` and `meta.IsNoMatchError`** — the
  latter is what you actually get for an unregistered CRD.
* **Defend non-obvious optimizations in a code comment.** The `Limit: 2` NetworkPolicy list
  carries its correctness argument inline, because a reviewer challenged it (and the
  challenge turned out to be wrong).

### Constants and cache

* **Feature-local constants live next to the reconciler**, unexported.
  `localDNSPreferredK8sVersionThreshold` is a `const` in `localdns.go`, not in
  `pkg/consts`. `pkg/consts/consts.go` holds only cross-cutting values: network
  plugin/mode/dataplane/policy, provision modes, storage profiles, maxPods defaults, batch
  size, compute-recommendation modes, provisioning states.
* **Cache TTL determines convergence.** `KubernetesVersionTTL = 15 * time.Minute`
  (`pkg/cache/cache.go`). A control-plane upgrade takes up to that long to flip a
  version-gated state; nodes created in the stale window need drift to converge. State the
  observable latency in your PR.
* **Keep threshold constants and their doc comments in sync.** The LocalDNS threshold is
  currently `"1.99.0"` while the surrounding `LocalDNSReconciler` comment still says
  `k8s>=1.36`. Do not replicate this.

### RBAC

Cluster-state gates almost always need new permissions. LocalDNS needed `list` on
`networking.k8s.io/networkpolicies`, `cilium.io/cilium{,clusterwide}networkpolicies`, and
`crd.projectcalico.org/{network,globalnetwork}policies` (best-effort), `get` on
`apps/daemonsets`, and `patch` on `aksnodeclasses/status`. Update
`charts/karpenter/templates/clusterrole-core.yaml`.

### Two-place sync

If your gate logic mirrors an RP validator, say so in a code comment. It will drift.

---

## Step 3b: Day-2 / mutable features

* Mark the field `hash:"ignore"` in the spec.
* Add it to `vmInPlaceUpdateFields` and/or `aksMachineInPlaceUpdateFields`
  (`inplaceupdate/utils.go`). They are **not symmetric** — VM identities are handled
  server-side for AKS machines and are deliberately absent from the machine struct.
* Add a patcher to `vmPatchers` / `aksMachinePatchers`.
* **Extend the watch predicate.** `tagsChangedPredicate` only fires on tag changes; your
  field will not trigger reconcile without it.
* Changing the in-place hash struct changes the annotation on existing NodeClaims — a
  one-time patch storm. Budget for it.
* Prove it with an E2E that applies the change **without drifting the NodeClaim**
  (`test/suites/inplaceupdate/`).

---

## Step 4: VM-based path

`pkg/providers/instance/vminstance.go`. Follow the nil-guard-then-set style of
`setVMPropertiesSecurityProfile` / `setVMPropertiesAdditionalCapabilities`: allocate the
sub-profile only if nil, touch only your field. Overwriting a whole `SecurityProfile`
clobbers a concurrently-set sibling — note that the existing code checks
`if vmProperties.SecurityProfile.SecurityType == nil` before setting, precisely so
Trusted Launch does not stomp a value set by another path.

Some features are absent here by design: Ultra SSD's VM path leaves
`AdditionalCapabilities.UltraSSDEnabled` unset when disabled, mirroring AKS VMSS behavior
where the property is nil rather than false.

If the setting is delivered through bootstrap rather than the ARM payload, see Step 6.

---

## Step 5: AKS Machine API path

`pkg/providers/instance/aksmachineinstancehelpers.go`. Set the field on the right
`armcontainerservice.Machine*Profile` (`MachineSecurityProfile`, `MachineKubernetesProfile`,
`MachineOSProfile`, `LocalDNSProfile`, `GPUProfile`) via a
`configure<Feature>Profile(nodeClass, instanceType)` helper.

> **Grep the machine template for commented-out fields before you start.** #1607's entire
> functional diff replaces the literal line `// ArtifactStreamingProfile: nil,` with a real
> call. #1397 shipped with that placeholder in place and nothing caught it for two weeks.

### nil vs explicit false

Both patterns exist. The discriminator is **who owns the decision**:

* **Return `nil`** when the profile object is not applicable —
  `configureArtifactStreamingProfile` returns nil when disabled;
  `configureGPUProfile` returns nil for non-GPU SKUs.
* **Send explicit `true`/`false`** when Karpenter has already made the decision and used it
  for instance-type filtering — `MachineSecurityProfile.EnableEncryptionAtHost`,
  `EnableVTPM`, `EnableSecureBoot` are all `lo.ToPtr(...)` of a resolved bool. From #1704:
  *"Karpenter wouldn't want to give Machine API an unclear `nil` if Karpenter already
  assumes that it is `false` through instance filtering."*

Document the defaulting behavior **at each layer** in the design doc — reviewers asked for
this explicitly.

### Type conversions

Round-trip test anything non-trivial and assert on the SDK payload, not your own struct.
#1610 needed:

* `[]LocalDNSZoneOverride` (list keyed by `zone`) → `map[string]*LocalDNSOverride`
* `karpv1.NillableDuration` → **seconds as `int32`**
* enum values → direct casts between Karpenter and SDK string types

### Batching

`aksmachineapi` and `aksmachineapiheaderbatch` are one path with two create-dispatch
strategies. Anything derived from `instanceType` (architecture, VM size) is **per-machine**
and must be a `BatchPutMachine` header entry — a per-machine field on the shared machine
template is silently applied to every machine in the batch. Artifact streaming is the
worked example, since `configureArtifactStreamingProfile` reads
`instanceType.Requirements.Get(v1.LabelArchStable)`.

---

## Step 6: Bootstrapping path

Highest blast radius.

* `CustomScriptsNodeBootstrapping(...)` is **duplicated per image family**. Adding a
  parameter means editing `ubuntu_2004.go`, `ubuntu_2204.go`, `ubuntu_2404.go`,
  `azlinux.go`, `azlinux3.go`, the `ImageFamily` interface and `Resolve()` in
  `imagefamily/resolver.go`, plus every `*_test.go` — including positional calls like
  `CustomScriptsNodeBootstrapping(nil, nil, nil, nil, nil, "test-distro", "Standard_LRS", nil, nil, nil, nil)`
  where a missing argument is a compile error but a *misordered* one is not.
* Note the `_ *v1beta1.LocalDNS` blank parameter in `ubuntu_2004.go` — the established way
  to say "this family does not support the feature."
* Add the field to `ProvisionClientBootstrap` and thread it through
  `ConstructProvisionValues`.
* `provisionclientbootstrap.go` carries an explicit `ATTENTION!!!` that changes there may
  not take effect on AKS machine nodes. Treat it as a hard requirement to do Step 5 too.
* **Custom data changes drift nodes.** `ProvisionClientBootstrap.Labels` is tagged
  `hash:"set"`, which makes label ordering hash-stable.
* **Kubelet field naming mirrors upstream kubelet, not AKS.** Document the delta in a
  comment (`containerLogMaxSize` vs AKS `containerLogMaxSizeMB`; `podPidsLimit` int64 vs
  AKS `PodMaxPids` int32).
* Cross-field coupling is common — `kubelet.failSwapOn` must be false for
  `linuxOSConfig.swapFileSize`, enforced by a spec-level CEL rule.

> **Worked failure.** #1039 fixed OSSKU not reaching `GET NodeBootstrapping`, breaking
> artifact streaming enablement for **AzureLinux only, in `bootstrappingclient` mode only**.
> That is the exact shape of bug this section exists to prevent — and #1600/#1611 later
> added and removed an OSSKU→image-family mapper in the same file.

---

## Step 7: Labels, generated artifacts, wiring

* Label keys go in `pkg/providers/labels/labels.go` alongside
  `AKSArtifactStreamingEnabledLabelKey` / `AKSLocalDNSStateLabelKey`, emitted from
  `labels.Get`.
* **Match AKS's label-presence convention.** Artifact streaming sets the label only when
  enabled and omits it otherwise, rather than writing `"false"` — the code comment cites
  AKS RP behavior. Check what AKS actually does before choosing.
* **Expect to change shared helper signatures.** #1397 changed
  `labels.Get(ctx, nodeClass)` → `labels.Get(ctx, nodeClass, arch)`, rippling into
  `launchtemplate.go` and every labels test. Budget for it; do not work around it.
* Only assert a label value you control. AKS-set labels must be verified against real node
  output in E2E, not assumed.
* Run `make verify`. Never hand-edit `zz_generated.deepcopy.go`, `pkg/apis/crds/*.yaml`, or
  `charts/karpenter-crd/templates/*`. Note the CRD contains **both** `v1alpha2` and
  `v1beta1` schemas, so a `v1beta1`-only change still produces two hunks per file across
  two files.
* New Azure clients are constructed in `pkg/providers/azclient`; construction is
  mode-dependent — decide which modes need it.
* New operator flags go in `pkg/operator/options` **with a safe default**; NAP and
  self-hosted supply configuration differently.

---

## Appendix: category-specific concerns

The steps above are common to all features. These are the extra seams by category.

### Networking

* Configuration has **two sources**:
  `lo.Ternary(nodeClass.Spec.VNETSubnetID != nil, ..., opts.SubnetID)`. Reading only one
  breaks either self-hosted or per-NodeClass BYO VNET.
* Node labels are **conditional on plugin mode** — `opts.IsAzureCNIOverlay()` gates
  `AKSLabelSubnetName`, `AKSLabelVNetGUID`, `AKSLabelAzureCNIOverlay`,
  `AKSLabelPodNetworkType`. Extend the existing "Azure CNI node labels and agentbaker
  network plugin" `DescribeTable` in `instancetype/suite_test.go`; do not add a new one.
* `maxPods` defaulting is plugin-dependent (30 / 250 / 250 / 110 via `consts`), feeds
  `instanceTypeParameters`, and therefore the cache key.
* NICs are a separate lifecycle with a dedicated GC controller
  (`nodeclaimgarbagecollection.NewNetworkInterface`). AKS Machine mode does not create
  NICs — a networking change may legitimately be VM-only, but say so.
* Assert on `NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput`; see the BYO-VNET NSG
  tests in `instance/suite_test.go`. E2E is near-mandatory — subnet/NSG/route-table
  behavior is not faithfully faked.

### Security

* Almost always SKU-gated (`EncryptionAtHostSupported`, `sku.IsTrustedLaunchEnabled()`), so
  the field **must** enter `instanceTypeParameters` and the filter chain.
* Interlocks with image selection.
  `imagefamily.GetImageFamily(imageFamily, fipsMode, isTrustedLaunch, k8sVersion, ...)`
  takes security inputs directly — a new security field that changes the image lineage will
  **drift every existing node**. Check that first.
* Three CEL rules already constrain FIPS × imageFamily × trustedLaunch. Adding a fourth
  interacting field means re-deriving all of them.
* `logVMPatch` marshals the entire VM patch at V(1). If your field lands there, it gets
  logged.

### Bootstrapping

See Step 6 — this is the category with the most duplicated call sites and the highest
drift risk.

### Scheduling / capacity

* Offering-level, not NodeClass-level: availability varies per SKU **and per zone**.
* `toCreateError` surfaces classified reasons (`SKUNotAvailable`, `ZonalAllocationFailure`)
  onto the `Launched` condition; unclassified failures degrade to generic
  `CreateInstanceFailed`.
* Regional vs zonal zone values must be handled identically in offering construction and
  node labeling (`zones.MakeAKSLabelZoneFromARMZones`).

### Caching

Per [`0011-machine-cache.md`](./0011-machine-cache.md): cache is acceptable for drift
detection and pre-create existence checks; **never** for post-create validation, in-place
update, or `CloudProvider.Get`. If your feature adds a read, state which bucket it is in.

---

## Testing

| Level | Location | Cover |
|---|---|---|
| Unit | `pkg/apis/v1beta1/*_test.go` | Accessor nil/false/true/unset matrix; hash unchanged for an object that does not set the field |
| Unit | `pkg/apis/v1beta1/crd_validation_cel_test.go` | Accept omitted / true / false; reject every invalid combination |
| Unit | `pkg/providers/instancetype/instancetypes_test.go` | Filter no-ops when disabled; excludes non-capable SKUs when enabled; SKU with capability **absent**; cache key changes |
| Unit | `pkg/providers/instance/*_test.go`, `pkg/cloudprovider/suite_aksmachineapi_features_test.go` | Payload assertions for VM properties **and** the Machine profile |
| Acceptance | `pkg/` Ginkgo | Start from pending pod pressure; assert NodeClaim/labels |
| E2E | `test/suites/<existing suite>/` | Real Azure behavior only |

Rules reviewers apply:

* **Extend the existing sibling `_test.go`**, matching its fakes, `DescribeTable` shape,
  naming, and assertion style. A parallel new suite next to an existing one gets flagged.
* **If your PR inverts existing assertions, that is a behavior change.** #1397 flipped
  `BeFalse()` → `BeTrue()` across `provisionclientbootstrap_test.go` and deleted the
  arch/OS constraint assertions; #1600 flipped two back; #1611 collapsed the table from
  ~12 entries to 5. Call assertion inversions out explicitly in the description.
* **Find the right existing suite.** #1797 moved Ultra SSD tests into the **Storage** suite
  after the fact. New `test/suites/` directories are a last resort and must be added to
  `.github/workflows/e2e-matrix.yaml`.
* **Zone-sensitive features need both zonal and regional E2E runs**, linked separately
  (#1797 did this).
* **Verify on the node, not just the API.** The artifact streaming E2E schedules a
  privileged `hostPID` pod, greps for the `overlaybd-tcmu` process and for `overlaybd` in
  `/host/etc/containerd/config.toml`, and asserts on the combination. LocalDNS validation
  checked the node label, `systemctl is-active localdns.service`, and `/etc/resolv.conf`.
* **Gate E2E on the modes that support the feature.** The artifact streaming suite
  `Skip`s when `env.InClusterController` is true, because it requires NPS.
* Failure paths need coverage: filter-empties-everything, RP rejects the field, partial
  batch failure, deletion when the resource is already gone.
* Helm chart snapshot diffs prove template rendering only. Never cite them as behavioral
  evidence.
* Ask: **would this test fail if the behavior regressed?**

> `CONTRIBUTING.md` says E2E suites live under `test/pkg/suites`. The actual path is
> `test/suites/`. Worth a drive-by fix.

---

## Production Readiness

* Kill switch identified and shipped.
* Measured effect on provisioning latency and per-node Azure API call count stated.
* Convergence latency stated for anything behind a cached input (e.g. the 15-minute
  Kubernetes version TTL).
* Every created Azure resource has a cleanup path including on partial failure; "not found"
  during delete/GC is success.
* No credentials, tokens, or customer identifiers in logs, events, conditions, or tags.
* No references to systems external contributors cannot access — this repository is public
  and contributions must be reviewable from public sources alone.

---

## Pull request expectations

* Conventional-commit title (`feat:` / `fix:` / `BREAKING CHANGE:`).
* A filled-in `release-note` block. #1611 is the model: *"Users must explicitly set
  `artifactStreaming.enabled: true` in AKSNodeClass to enable it."*
* **Links to actual E2E workflow runs.** #1704, #1797, #1818 link runs; #1676 included a
  14-row on-node verification matrix.
* Which provisioning modes and deployment models were exercised, and which were not and why.
* The default matrix (field × architecture × image family → result), agreeing with the diff.
* **Verify AI-generated review findings before acting.** On #1676, three bot findings were
  real and fixed; two were wrong and correctly rejected after the reviewer reasoned through
  the code. Treat them as hypotheses.

---

## FAQ

### Do I need to bump `AKSNodeClassHashVersion`?

Only if you change a default on an already-hashed field, add a field with an already-set
value to the hash, or remove one. A new optional nil field does not.

### My feature only works on one provisioning mode. Is that acceptable?

Yes, if stated explicitly with the reason and a tracking issue for a temporary SDK or RP
gap. Silently no-opping in the other mode is not.

### Should I add the field to `v1alpha2` for parity?

No. `v1alpha2` is deprecated and planned for removal.

### Where do my constants go?

Next to the code that uses them, unexported, unless genuinely cross-cutting.
`pkg/consts/consts.go` is for values shared across packages.

### Do I need a CEL rule to enforce uniqueness in a list?

No, if the list uses `listType=map` — it enforces uniqueness on the map key. Keep a
`maxItems` bound anyway.

### The feature needs a label *and* NodeClass configuration.

Re-read `0015`'s "Special Case" section. Only `os-sku` and `fips_enabled` do this today and
it is discouraged, because it creates two ways to express one thing.
