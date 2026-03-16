# CRD Validation Sync Strategy with AKS Machine API

**Author:** @comtalyst

**Last updated:** Mar 15, 2026

**Status:** Proposed

## Overview

Karpenter's CRD validation (NodePool, AKSNodeClass) and AKS Machine API validation are independent systems with different rule sets, lifecycles, and release cadences. When these diverge, users can create CRD objects that pass admission but fail at Machine API creation time — producing late, confusing errors deep in the provisioning pipeline rather than immediate feedback at `kubectl apply`.

This document proposes a **layered validation architecture** that incrementally closes the gap between CRD-side and server-side validation, with clear trade-offs at each layer and a phased rollout plan.

### Current State

Today, the gap is managed through a combination of:

1. **Workarounds in the provisioning path** — e.g., all taints are redirected to `nodeInitializationTaints` to avoid `nodeTaints` server-side validation rules (system-mode taint restrictions); labels with `kubernetes.azure.com` prefix are stripped before sending to Machine API to avoid conflicts with server-managed labels.
2. **Accepting the gap** — design doc [0006 (Requirements and Labels)](./0006-requirements-and-labels.md) explicitly decided to wait for Machine API migration for full label parity rather than duplicating validation.
3. **Missing field validations** — fields like `cpuCFSQuotaPeriod` and `allowedUnsafeSysctls` had no CRD-level validation, causing Machine API rejections that were difficult to diagnose. These have been recently addressed with CEL rules.

The fundamental tension: Karpenter's CRDs are intentionally more permissive than Machine API (users should be able to express their intent freely), but Machine API enforces AKS platform constraints that Karpenter cannot override. The solution is not to make CRDs as restrictive as Machine API, but to give users fast, clear feedback when their intent will hit a platform constraint.

### Goals

* Define a layered architecture where each layer adds validation coverage with increasing fidelity but also increasing cost/complexity
* Provide a phased rollout plan that delivers incremental value — each phase stands alone
* Improve error surfacing for Machine API rejections so users can self-diagnose
* Establish patterns for adding new validations as Machine API evolves, without requiring coordinated releases
* Work correctly in both NAP (managed control plane) and self-hosted deployment paths

### Non-Goals

* Achieve 100% validation parity between CRDs and Machine API — some constraints are inherently server-side (quota, capacity, RBAC) and cannot be pre-validated
* Replace Machine API validation — the server remains the source of truth
* Add validation for non-Machine-API provisioning modes (bootstrapping client, VM direct) — those paths are being deprecated in favor of Machine API
* Validate cross-resource constraints (e.g., "this NodePool + this AKSNodeClass combination is invalid") — that's a broader problem

## Decisions

### Decision 1: What validation architecture should we adopt?

#### Option A: Full Validation Duplication

Duplicate every Machine API validation rule in CRD markers (kubebuilder + CEL). Keep them in sync manually.

**Pros:**
* Immediate feedback at `kubectl apply` time for all known constraints

**Cons:**
* Fragile — Machine API validation rules change independently, and any drift creates false positives (CRD rejects valid config) or false negatives (CRD accepts invalid config)
* Requires reverse-engineering Machine API validation, which is not publicly documented as a contract
* CEL has expressiveness limitations — some Machine API rules involve cross-field logic, external state, or Azure-specific constraints that CEL cannot express
* High maintenance burden with no automation path

#### Option B: Accept the Gap Entirely

Keep CRD validation minimal. Users discover Machine API constraints through provisioning failures.

**Pros:**
* Zero maintenance overhead for validation sync
* No risk of false positives blocking valid configurations

**Cons:**
* Poor user experience — errors appear minutes after CRD creation, in controller logs or NodeClaim conditions, not at `kubectl apply` time
* Debugging requires Karpenter controller log access, which end users often lack
* Repeated create-fail-debug cycles waste cluster resources and user time

#### Option C: Layered Validation (Proposed)

Four layers, each adding coverage at a different fidelity/cost point. Each layer is independently valuable and deployable.

**Pros:**
* Incremental value — each layer improves UX without requiring the next
* Matches validation to the appropriate mechanism (static rules in CRD, dynamic checks in reconciler, error translation in provisioner, real-time validation in webhook)
* Avoids the fragility of full duplication by keeping complex validation close to its source of truth

**Cons:**
* More architectural surface area than a single approach
* Layers 2-4 require ongoing investment beyond the initial implementation

#### Conclusion: Option C — Layered Validation

The layered approach is the only option that delivers incremental value without creating a maintenance trap. Full duplication (Option A) is the wrong design — it builds on the assumption that we can perfectly mirror an external system's validation, which is false by definition. Accepting the gap (Option B) is the current state, and the user feedback is poor enough to justify investment.

### Decision 2: What are the validation layers?

#### Layer 1: Declarative CRD Validation (Improve Current)

**Mechanism:** kubebuilder markers (`+kubebuilder:validation:*`) and CEL XValidation rules on the CRD schema.

**What it catches:** Field-level constraints that are stable, well-understood, and expressible in CEL. Examples:
* Value ranges: `osDiskSizeGB` min/max, `cpuCFSQuotaPeriod` 1ms-1s range
* Pattern matching: `vnetSubnetID` regex, `allowedUnsafeSysctls` namespace restrictions
* Enum enforcement: `imageFamily`, `fipsMode`
* Cross-field rules: FIPS + imageFamily incompatibility, `imageGCHighThresholdPercent` > `imageGCLowThresholdPercent`

**When feedback arrives:** Immediately at `kubectl apply` time (admission rejection).

**Limitations:**
* Cannot validate against external state (RBAC, quota, capacity)
* Cannot express rules that depend on the provisioning mode or cluster configuration
* CEL has no network access — cannot call Machine API
* Adding new CEL rules requires a CRD schema update, which in NAP requires a Karpenter release

**Guideline for adding rules at this layer:** A constraint belongs here if:
1. It is a stable Machine API contract (not likely to change between releases)
2. It is expressible in CEL without external state
3. A false positive (CRD rejecting valid config) would be worse than a false negative (CRD accepting invalid config) — if so, don't add it here

#### Layer 2: Reconciler-Based Validation (New — Medium-Term)

**Mechanism:** Extend the existing `ValidationReconciler` (currently validates DES RBAC only) to perform additional pre-creation checks against external services.

**What it catches:** Constraints that require external state or complex logic that CEL cannot express. Examples:
* RBAC and permission checks beyond DES (e.g., subnet access, image gallery access)
* Machine API dry-run validation — submit a template to Machine API with a dry-run flag (if/when supported) to validate without creating
* Cross-resource validation — e.g., "this AKSNodeClass references a subnet that doesn't exist"

**When feedback arrives:** Asynchronously, via AKSNodeClass status conditions. Not immediate, but before any Machine is created. Users see it via `kubectl describe aksnodeclass`.

**Architecture:**
```
AKSNodeClass created/updated
  → ValidationReconciler detects change
  → Runs validation checks (DES RBAC, subnet existence, dry-run Machine API, ...)
  → Sets ConditionTypeValidationSucceeded = True/False with descriptive message
  → Provisioning controllers check this condition before creating Machines
```

**Limitations:**
* Async — not at `kubectl apply` time (seconds to minutes delay)
* Adds API calls to external services on every reconcile — needs rate limiting and caching
* Dry-run Machine API validation depends on an endpoint that doesn't exist yet

**Guideline for adding checks at this layer:** A check belongs here if:
1. It requires calling an external API or inspecting cluster state
2. The result is relatively stable (doesn't change per-Machine, changes per-AKSNodeClass or per-cluster)
3. False results are recoverable (the reconciler will re-check periodically)

#### Layer 3: Machine API Error Surfacing (New — Short-Term)

**Mechanism:** When Machine API rejects a create request due to a validation error, parse the error response, classify it, and surface it as a user-visible Karpenter event and/or NodeClaim condition.

**What it catches:** All Machine API validation errors, including those that Layers 1-2 cannot predict. This is the safety net.

**Current behavior:** Machine API errors are logged by the controller but not surfaced as NodeClaim conditions or Kubernetes events that users can easily discover. The error message is often a raw Azure SDK error wrapped in multiple layers.

**Proposed behavior:**
1. Parse `ResponseError` from Machine API create failures
2. Classify errors into categories:
   * **Validation error** (4xx with `InvalidParameter`, `BadRequest`, etc.) — deterministic, same input always fails
   * **Transient error** (5xx, network timeout) — may succeed on retry
   * **Quota/capacity error** — resource exhaustion, different from config errors
3. For validation errors:
   * Emit a Kubernetes Warning event on the NodeClaim with the parsed error message
   * Set a descriptive condition on the NodeClaim (e.g., `MachineCreateValidationFailed: "The label 'foo' is not allowed on system-mode nodes"`)
   * Avoid retry storms — if the error is deterministic (same input will always fail), back off or stop retrying for that specific NodeClaim
4. For transient errors: continue existing retry behavior

**When feedback arrives:** After the first Machine API create attempt fails (seconds to minutes after NodeClaim creation).

**Limitations:**
* Reactive — the user has already created the CRD and waited for provisioning to begin
* Depends on Machine API returning structured, parseable error messages (which it does today, but the format is not a guaranteed contract)
* Cannot prevent the failed create attempt and its associated cost (API call, potential partial resource creation)

**Guideline for this layer:** This is not opt-in — all Machine API create errors should flow through this classification and surfacing pipeline. The goal is zero silent failures.

#### Layer 4: Validation Webhook + RP Endpoint (Long-Term North Star)

**Mechanism:** AKS RP exposes a dedicated validation endpoint (e.g., `POST /validate-machine-template`) that accepts a Machine API request body and returns validation results without creating any resources. Karpenter registers a validating admission webhook that calls this endpoint at CRD create/update time.

**What it catches:** Everything Machine API validates, with real-time feedback at `kubectl apply` time.

**Architecture:**
```
kubectl apply -f nodepool.yaml
  → K8s API server calls Karpenter validating webhook
  → Webhook translates CRD spec → Machine API template
  → Webhook calls RP validation endpoint
  → RP returns validation result
  → Webhook accepts or rejects with specific error message
  → User sees result immediately
```

**When feedback arrives:** Immediately at `kubectl apply` time.

**Limitations:**
* Requires cross-team investment — RP must build and maintain the validation endpoint
* Webhook adds latency to every CRD create/update (network call to RP)
* Webhook availability becomes a dependency for CRD operations — if RP is down, CRD creates fail or the webhook must fail-open (reducing the value)
* NAP deployment path: webhook runs in the control plane and can reach RP; self-hosted: webhook runs in the customer cluster and needs network path to RP
* Not all CRD fields map cleanly to Machine API fields at admission time (some depend on scheduling decisions not yet made)

**Guideline for this layer:** This is the north star for validation UX, but requires demand justification before investing. The preceding layers should be sufficient for most users. Pursue this if:
1. Layer 3 data shows a high volume of Machine API validation rejections that users struggle to resolve
2. RP team is willing to build and maintain the validation endpoint
3. The operational complexity of running a webhook is acceptable for both deployment paths

### Decision 3: What is the rollout plan?

#### Phase 1: Immediate (Current Cycle)

**Layer 1 improvements + Layer 3 error surfacing.**

Layer 1 work:
* Continue adding CEL rules for stable, well-understood constraints as they are discovered (same pattern as `cpuCFSQuotaPeriod` and `allowedUnsafeSysctls`)
* Document the guideline for "when to add a CEL rule" so contributors can self-serve
* Audit existing workarounds (taint redirection, label sanitization) — for each, determine if a CRD validation rule would eliminate the need for the workaround or if the workaround is the correct long-term design

Layer 3 work:
* Implement Machine API error classification in the provisioning path
* Surface classified errors as Kubernetes events on NodeClaim objects
* Add a NodeClaim condition for persistent validation failures
* Add metrics for Machine API validation rejection rates (by error code) to track the scope of the problem and measure improvement over time

**Exit criteria:** Users can see Machine API validation errors via `kubectl describe nodeclaim` and `kubectl get events`. Metrics show validation rejection rates.

#### Phase 2: Next Cycle

**Layer 2 reconciler validation.**

* Extend `ValidationReconciler` with a plugin architecture for adding new checks
* Implement subnet existence check as the first plugin (common misconfiguration)
* Investigate Machine API dry-run feasibility with the RP team
* Gate Machine provisioning on `ConditionTypeValidationSucceeded` being True (already partially implemented — DES RBAC check sets this condition)

**Exit criteria:** `ValidationReconciler` supports pluggable checks. At least one new check beyond DES RBAC is in production.

#### Phase 3: Future (Demand-Driven)

**Layer 4 webhook + RP validation endpoint.**

* Evaluate demand based on Phase 1 metrics (Machine API validation rejection rates, user-reported confusion)
* If justified, propose the RP validation endpoint to the RP team
* Prototype the webhook in the self-hosted path first (simpler deployment model)

**Exit criteria:** Decision on whether to pursue, with supporting data.

## Alternatives Considered

### AgentPool Representation

Map CRD fields through the AgentPool API model for validation. This doesn't cover the Machine API path — Machine API is the provisioning target, and its validation rules differ from AgentPool's. Dead end.

### Full Validation Duplication

See Option A above. Fragile, out of sync by definition. Any static copy of external validation rules becomes stale the moment it's written.

### Accept the Gap Entirely

See Option B above. The current state, but user feedback is poor. Machine API validation errors are buried in controller logs and require cluster-admin access to diagnose. The layered approach improves this incrementally.

## FAQ

### Why not just make CRDs match Machine API exactly?

Three reasons:
1. **Different lifecycles** — Machine API validation evolves independently. Any static duplication drifts immediately.
2. **Different concerns** — Karpenter CRDs express user intent; Machine API enforces platform constraints. A user should be able to say "I want nodes with these taints" even if the platform will route them through a different field (as the `nodeInitializationTaints` workaround does).
3. **Expressiveness mismatch** — Some Machine API rules depend on server-side state (cluster mode, feature flags, API version) that the CRD schema cannot access.

### How does this interact with the Machine API migration?

The Machine API migration (design docs [0007](./0007-aks-instance-provisioning-with-aks-machine-api.md) / [0008](./0008-aks-instance-provisioning-with-aks-machine-api-ex.md)) changes the provisioning path but doesn't change the validation problem — it makes it more important, because Machine API has stricter validation than the previous VM-direct path. The layers proposed here are specifically designed for the Machine API provisioning mode.

### What about self-hosted vs. NAP differences?

Both deployment paths use the same CRD schema and the same provisioning code (for Machine API mode). The validation layers apply equally to both. The only difference is Layer 4 (webhook): in NAP, the webhook runs in the managed control plane with direct RP access; in self-hosted, it runs in the customer cluster and needs a network path to RP. This is a deployment concern, not an architectural one.

### What if Machine API adds a dry-run endpoint?

That would significantly accelerate Layer 2 — the `ValidationReconciler` could call dry-run on every AKSNodeClass reconcile to validate the entire template, catching constraints we haven't individually coded checks for. This is the most efficient path to high validation coverage with low maintenance. We should advocate for this with the RP team.

### How do we avoid false positives (CRD rejecting valid config)?

By being conservative about Layer 1 (CEL rules). The guideline is: only add a CEL rule if the constraint is stable and a false positive would be less harmful than a false negative. For constraints where we're uncertain, use Layer 2 (reconciler) or Layer 3 (error surfacing) instead — those can be updated without CRD schema changes.

## Testing

* **Layer 1 (CEL rules):** Unit tests via CRD validation test suites (already established pattern). Each new CEL rule gets positive and negative test cases.
* **Layer 2 (Reconciler):** Unit tests for `ValidationReconciler` check logic. Integration tests verifying that provisioning respects `ConditionTypeValidationSucceeded`. E2E tests with deliberately misconfigured AKSNodeClass to verify condition reporting.
* **Layer 3 (Error surfacing):** Unit tests for error classification logic. E2E tests that trigger Machine API validation rejections and verify events/conditions appear on NodeClaim.
* **Layer 4 (Webhook):** If pursued, requires webhook integration tests, RP endpoint contract tests, and failure-mode tests (webhook down, RP down, timeout).

## Production Readiness

* **Layer 1:** No runtime impact — validation runs at admission time only. CRD schema changes require careful rollout in NAP (coordinated with Karpenter version releases).
* **Layer 2:** Adds external API calls in the reconcile loop. Must implement rate limiting, caching, and circuit breaking to avoid overwhelming external services. Validation failures should not block existing healthy NodeClaims — only new ones matching the failing AKSNodeClass.
* **Layer 3:** Minimal new runtime overhead — classifies errors that already occur. The retry backoff for deterministic failures reduces load compared to current behavior (which retries blindly).
* **Layer 4:** Adds latency and an availability dependency to CRD operations. Must fail-open with appropriate logging/alerting. Requires monitoring of webhook latency and RP endpoint availability.
* **Metrics to add:**
  * `karpenter_machine_api_validation_errors_total` (by error code, error message pattern)
  * `karpenter_validation_reconciler_check_duration_seconds` (by check name)
  * `karpenter_validation_reconciler_check_result` (by check name, result)
