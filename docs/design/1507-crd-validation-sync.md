# Design: Sync Karpenter CRD Validation with AKS Machine API Validation

**Ticket:** Azure/karpenter-poc#1507 (follow-up of #1459)  
**Author:** comtalyst  
**Status:** Draft  
**Date:** 2026-03-19

## Problem

Karpenter CRD validation (CEL rules on `AKSNodeClass`, label/taint filtering in `configureLabelsAndMode`/`configureTaints`) is a **best-effort duplication** of AKS Machine API validation. This is unhealthy because:

1. **Drift risk**: When AKS RP changes validation rules (e.g., new reserved labels, modified taint constraints for system mode), Karpenter must manually duplicate the change. Until it does, users experience confusing late-binding 400 errors from the Machine API at provisioning time.
2. **Inconsistent UX**: Some invalid configurations pass Karpenter's CRD admission but fail at Machine API call time. The error surfaces asynchronously via `ProvisioningError` on the Machine, not as an immediate API rejection.
3. **Maintenance burden**: Two teams must keep two validation codebases in sync indefinitely.

### Current State of Validation

| Layer | What it validates | Mechanism |
|-------|-------------------|-----------|
| CRD CEL rules | `AKSNodeClass` fields (imageFamily, FIPS, tags, VNetSubnetID, OSDiskSizeGB, LocalDNS) | `+kubebuilder:validation:XValidation` |
| `configureLabelsAndMode()` | Filters out `kubernetes.azure.com/*` labels, kubelet-managed labels before sending to Machine API | Go code |
| `configureTaints()` | Puts all taints in `nodeInitializationTaints` to avoid Machine API's server-side reconciliation/validation on `nodeTaints` | Go code workaround |
| AKS Machine API (RP) | Full validation of the Machine object (labels, taints, mode constraints, VNet, OSSKU, etc.) | Server-side |

### Specific Known Inconsistencies (from #1459)

- Label validation/defaulting differences between Karpenter and AKS Machine API
- System mode taint restrictions (only `CriticalAddonsOnly` hard taint allowed) — currently worked around by using `nodeInitializationTaints` for everything
- Potential label key/value constraints that AKS RP enforces but Karpenter doesn't check

## Proposed Solution: Validating Webhook Calling AKS RP Validation API

### Why Webhooks

The ticket lists three approaches. Here's the evaluation:

| Approach | Pros | Cons | Verdict |
|----------|------|------|---------|
| **Webhooks** | Single source of truth; instant user feedback; no duplication | Adds infra (webhook server, TLS certs); availability concern (webhook down = can't create NodePools); latency on admission | ✅ Recommended |
| **Exposing validation API from AKS RP** | Clean separation; RP owns validation | Requires RP team to build/ship a new API; latency; availability dependency | ✅ Good complement (backend for the webhook) |
| **AgentPool representation** | Reuses existing API surface | Doesn't cover Machine API code path; different validation rules | ❌ Doesn't solve the problem |

**Recommendation: Webhooks backed by an AKS RP validation endpoint.**

The webhook acts as the admission-time enforcement point. It calls an AKS RP endpoint that performs the same validation as the Machine PUT path, but without creating a resource (dry-run / validate-only).

### Architecture

```
User applies NodePool/AKSNodeClass
        │
        ▼
┌─────────────────────────┐
│  K8s API Server         │
│  (admission webhook)    │
└──────────┬──────────────┘
           │ ValidatingWebhookConfiguration
           ▼
┌─────────────────────────┐
│  Karpenter Controller   │
│  Webhook Server         │
│  /validate-nodepool     │
│  /validate-aksnodeclass │
└──────────┬──────────────┘
           │ Builds Machine template (same logic as BeginCreate)
           │ Calls AKS RP validation
           ▼
┌─────────────────────────┐
│  AKS RP                 │
│  Machine Validation API │
│  (dry-run / validate)   │
└─────────────────────────┘
```

### Design Details

#### 1. Webhook Server in Karpenter

Karpenter already runs as a controller-runtime manager. Adding a webhook server is straightforward:

```go
// Register validating webhooks
mgr.GetWebhookServer().Register(
    "/validate-karpenter-azure-nodepool",
    &webhook.Admission{Handler: &NodePoolValidator{...}},
)
```

The webhook would intercept `CREATE` and `UPDATE` operations on:
- `NodePool` (for labels, taints, requirements that flow to Machine API)
- `AKSNodeClass` (for fields like imageFamily, FIPS, OSDiskSizeGB, VNetSubnetID, etc.)

#### 2. Validation Logic

**Option A — Full dry-run Machine build + RP call (Recommended for Machine API fields):**

For `AKSNodeClass` + `NodePool` changes that affect Machine properties:
1. Build a synthetic `armcontainerservice.Machine` template using existing `buildAKSMachineTemplate()` logic (or a subset).
2. Call an AKS RP validation endpoint (see §3 below).
3. Return the RP's validation errors as webhook denial reasons.

**Option B — Client-side rule mirroring (Fallback / supplement):**

For validations where the RP call isn't feasible (e.g., no RP endpoint yet, or pure Kubernetes-level constraints):
- Keep existing CEL validations on `AKSNodeClass` for basic field-level constraints.
- Add Go-level validation in the webhook for cross-field rules.

**Recommended hybrid:** Use CEL for simple field constraints (already exists, no change needed). Use the webhook + RP call for Machine-API-specific constraints (labels, taints, mode interactions).

#### 3. AKS RP Validation Endpoint

**Dependency:** This requires the AKS RP team to expose a validation-only endpoint.

Possible shapes:
- `POST /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.ContainerService/managedClusters/{cluster}/agentPools/{pool}/machines/{name}/validate` — dedicated validate endpoint
- `PUT ...?validate=true` or using `X-Ms-Validate-Only: true` header — dry-run mode on existing PUT endpoint (similar to ARM deployment validation pattern)

If the RP endpoint is **not available yet**, the webhook can still add value by:
- Performing the label/taint filtering and validation that `configureLabelsAndMode()` and `configureTaints()` do today, but at admission time.
- Catching obvious errors early (e.g., reserved AKS labels, system mode + incompatible taints).

#### 4. Webhook Availability & Failure Policy

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: karpenter-azure-validation
webhooks:
- name: validate.nodepool.karpenter.azure.com
  failurePolicy: Fail    # Strict: block on webhook unavailability
  # OR
  failurePolicy: Ignore  # Permissive: allow through if webhook is down
  timeoutSeconds: 10
  matchPolicy: Equivalent
  sideEffects: None
```

**Recommendation:** Start with `failurePolicy: Fail` for NAP (managed deployment where Karpenter availability is guaranteed). For self-hosted, allow configuration to `Ignore`.

#### 5. What to Validate Where

| Validation | Current Location | Proposed Location | Notes |
|-----------|-----------------|-------------------|-------|
| AKSNodeClass field constraints (imageFamily, FIPS, tags, etc.) | CEL on CRD | **Keep as-is** (CEL) | No change needed; these are stable and Azure-specific |
| Label filtering (AKS labels, kubelet-managed) | `configureLabelsAndMode()` at create time | **Move to webhook** | Fail fast at admission instead of silently filtering |
| Taint constraints (system mode, CriticalAddonsOnly) | Workaround in `configureTaints()` | **Webhook + RP validation** | Remove `nodeInitializationTaints` workaround when RP validates |
| VNet subnet RBAC | Status reconciler | **Keep as-is** | Runtime check, can't validate at admission |
| DES RBAC | Status reconciler | **Keep as-is** | Runtime check, can't validate at admission |
| Label/taint format (K8s standard) | Karpenter core | **Keep as-is** | Not our concern |

#### 6. Migration Path

**Phase 1 — Webhook without RP dependency (immediate):**
- Add validating webhook to Karpenter controller
- Move label/taint validation from `configureLabelsAndMode()`/`configureTaints()` to webhook
- Keep existing filtering in `configureLabelsAndMode()` as defense-in-depth
- Validate at admission: warn/reject NodePools with labels that will be filtered or cause Machine API errors

**Phase 2 — RP validation endpoint integration:**
- Coordinate with AKS RP team to expose validation endpoint
- Webhook calls RP for full Machine template validation
- Remove duplicated validation logic from Karpenter (keep CEL for basics)

**Phase 3 — Taint normalization:**
- Once RP validation is in place and inconsistencies resolved, potentially move taints back from `nodeInitializationTaints` to `nodeTaints` (if RP supports the needed flexibility)

### Testing Strategy

1. **Unit tests:** Webhook handler tests with various label/taint combinations
2. **Integration tests (envtest):** Webhook admission with real CRD validation
3. **E2E tests:** End-to-end validation that invalid NodePools are rejected at admission

### Alternatives Considered

#### Shared Validation Library
- AKS RP and Karpenter share a Go module with validation rules
- **Rejected:** Different languages/environments; tight coupling; versioning nightmare

#### Status Condition Validation (current pattern extended)
- Extend the existing `ValidationReconciler` pattern to validate more things post-creation
- **Rejected:** Doesn't give users immediate feedback; resources exist in invalid state

## Open Questions

1. **RP validation endpoint:** Does the AKS RP team have appetite to build a validate-only endpoint? What's the timeline?
2. **Self-hosted webhook availability:** For self-hosted Karpenter, should the webhook be a separate deployment for HA, or in-process?
3. **Scope of webhook:** Should it validate only Machine-API-destined fields, or also bootstrap-client-destined fields?
4. **Backward compatibility:** When moving validation earlier (to admission), existing users with "working" but technically invalid configs will be blocked on update. Migration plan?

## Implementation Plan

### Phase 1 Tasks (No RP dependency)

1. Add webhook server infrastructure to Karpenter controller
2. Implement `NodePoolValidator` webhook handler
3. Implement `AKSNodeClassValidator` webhook handler  
4. Move label validation from `configureLabelsAndMode()` to webhook (keep filtering as defense-in-depth)
5. Add validation for known taint constraints (system mode + taint restrictions)
6. Add `ValidatingWebhookConfiguration` to Helm chart / deployment manifests
7. Add unit + integration tests
8. Update documentation

### Phase 2 Tasks (RP dependency)

9. Coordinate with AKS RP team on validation endpoint design
10. Implement RP validation client in webhook
11. Remove duplicated validation rules from webhook (defer to RP)
12. E2E tests with real RP validation
