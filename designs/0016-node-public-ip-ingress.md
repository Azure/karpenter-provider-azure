# Node Public IP Support for Direct Internet Ingress

**Author:** @wdarko1

**Last updated:** Oct 5, 2026

**Status:** Proposed

**Related issue:** [Azure/karpenter-provider-azure#656](https://github.com/Azure/karpenter-provider-azure/issues/656)

## Overview

Customers using Node Auto Provisioning (NAP) may need Internet clients to reach workloads exposed through `hostPort` on dynamically provisioned nodes. A node-level public IP provides a directly addressable endpoint. Customers may also need addresses allocated from a public IP prefix that they own, for predictable address ranges and allow-listing.

This design proposes opt-in node public IP configuration on the current `v1beta1` `AKSNodeClass`, consistent across VM-based and AKS Machine API-based provisioning. In VM modes, the provider creates one public IP resource per node and associates it with the node NIC. In Machine API modes, the provider requests the capability through the AKS Machine resource and AKS owns the underlying public IP lifecycle. In both cases, a supplied prefix remains customer-owned.

The design is conditional on validating the AKS Resource Provider (RP) contract for Machine resources. The pinned Azure SDK contains `EnableNodePublicIP` and `NodePublicIPPrefixID` fields, but SDK serialization alone does not establish that the AKS RP accepts or honors them. Do not expose the feature as supported in Machine API mode until a supported AKS environment proves the create, read, and delete behavior.

### Goals

- Permit users to opt in to one public IPv4 address per provisioned node.
- Optionally allocate those addresses from a customer-supplied public IP prefix.
- Preserve behavior for existing NodeClasses and nodes when the new fields are omitted.
- Support both VM provisioning modes and both Machine API create dispatch strategies, subject to verified AKS RP support.
- Replace nodes when their effective public IP configuration changes, using normal NodeClass hash/drift behavior.
- Define ownership, cleanup, authorization, error, and end-to-end ingress expectations explicitly.

### Non-Goals

- Automatically creating, modifying, or deleting a customer-supplied public IP prefix.
- Automatically opening NSG/firewall ports or defining a cluster-wide inbound security policy.
- Guaranteeing an application is reachable merely because a node has a public IP. Routing, hostPort behavior, NSGs, firewalls, and workload health remain separate requirements.
- Adding public IPs to existing nodes in place; configuration changes take effect through node replacement.
- Providing a new NodeClaim status field or a stable workload endpoint/discovery service.
- Supporting arbitrary inbound traffic, load balancer configuration, or per-workload public IP allocation.
- Enabling this feature in AKS Machine API mode before the AKS RP contract is confirmed.

## Current Problem

The current NodeClass API has no setting for node public IPs. The direct VM path creates NICs without a public IP association and has no public IP client or cleanup path. The Machine API template has SDK model fields corresponding to node public IP enablement and prefix ID, but they are commented out and their server-side behavior is unverified. Current VM and Machine conversion paths also do not provide a public node address to consumers.

An address alone is not sufficient for Internet ingress. Traffic must also be routed to the node and permitted by the effective NSG/firewall policy; the hostPort datapath must then forward traffic to the selected pod. This feature configures node addressing only and requires a live-cluster test that exercises an actual inbound connection.

## Operating Model

| Concern | VM-based modes (`aksscriptless`, `bootstrappingclient`) | AKS Machine API modes (`aksmachineapi`, `aksmachineapiheaderbatch`) |
|---|---|---|
| Public IP creation | Provider creates one Azure Public IP resource per node, referencing the optional customer prefix, then associates it with the NIC. | Provider requests node public IP through Machine properties; AKS RP creates and manages the underlying resource. |
| Prefix ownership | Customer-owned; provider only references it. | Customer-owned; AKS RP must accept and honor the prefix reference. |
| Per-node address lifecycle | Provider deletes the Public IP after NIC detachment and recovers orphaned resources. | AKS RP owns creation and deletion with the Machine. |
| Batch behavior | Not applicable. | Both dispatch strategies use the same Machine template. The prefix is shared template data, not a per-machine header field. |
| Address reporting | No new provider-level status contract in this design. | No new provider-level status contract in this design; confirm whether Machine GET/LIST returns useful public address data. |

Omitting the configuration means disabled. Do not allocate a public IP or change existing NIC/Machine behavior when it is absent. Enabling public IPs increases resource use and potential Internet exposure, so it must be an explicit opt-in.

## Proposed API

Add an optional strongly typed block to `AKSNodeClassSpec` in `v1beta1`:

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
spec:
  nodePublicIP:
    enabled: true
    prefixID: /subscriptions/<subscription>/resourceGroups/<rg>/providers/Microsoft.Network/publicIPPrefixes/<name>
```

The proposed Go shape is:

```go
type NodePublicIP struct {
    Enabled  *bool   `json:"enabled,omitempty"`
    PrefixID *string `json:"prefixID,omitempty"`
}
```

`nodePublicIP` and `enabled` omitted or false both mean disabled. A non-empty `prefixID` requires `enabled: true`; reject an explicitly disabled configuration with a prefix rather than silently ignoring the prefix. Validate that a supplied ID is a syntactically valid Azure resource ID. Azure remains authoritative for existence, location, subscription/scope authorization, address family, SKU, zone, and capacity compatibility.

Before finalizing field names and validation, confirm the actual AKS API vocabulary and supported prefix constraints. The example is proposed API, not a claim that the RP contract has been verified. Avoid a cluster-level global default in the first version: NodeClass scope keeps the behavior visible with the pool shape, isolates opt-in, and lets the NodeClass hash drive replacement.

### Compatibility, Defaulting, and Drift

- Add the new field only to `v1beta1`; `v1alpha2` is deprecated and unserved.
- Preserve omitted-field behavior exactly: old NodeClasses continue to create private nodes without public IP allocation.
- Define API defaulting and helper methods so nil and false have the same effective behavior in admission, VM construction, Machine construction, hashing, and tests.
- Include the effective configuration in the NodeClass spec hash. Changing enablement or prefix ID should mark existing nodes drifted and allow replacement through normal Karpenter disruption behavior.
- Test hashes for absent versus explicit false to avoid needless drift on upgrade. Evaluate whether the hash-version annotation requires a bump; only bump it if the hash algorithm/compatibility semantics change, not merely because a new optional field was added.
- Generate deepcopy code, CRDs, and chart CRD copies using the repository's `make verify` workflow; do not hand-edit generated artifacts.
- Do not mutate existing Azure resources in place. During replacement, old nodes retain their existing address until their normal termination path releases it.

## Decisions

### Decision 1: Where should users configure node public IPs?

#### Option A: A strongly typed `AKSNodeClass` field

Expose an opt-in `nodePublicIP` block with `enabled` and optional `prefixID`.

**Pros**

- Public IP configuration is part of the node's provisioned shape and naturally participates in NodeClass hashing and drift.
- The prefix is explicit, reviewable, and scoped to the NodeClass that uses it.
- The same user-facing setting can feed both VM and Machine API provisioning.

**Cons**

- Requires complete API lifecycle work: validation, defaulting, generated CRDs, hashing/drift tests, and compatibility coverage.
- The RP's exact field names and server behavior are not yet confirmed.

#### Option B: A global operator setting or implicit cluster default

**Pros**

- Could match a cluster-wide AKS setting if one is defined for this feature.

**Cons**

- Hides pool-level exposure and makes different NodePools harder to isolate.
- A global mutable value would need independent drift tracking and precedence rules.
- Self-hosted and AKS-managed deployments may not have identical configuration sources.

#### Conclusion: Option A

Use an opt-in NodeClass field. Do not add a global default or infer enablement from the presence of a prefix.

### Decision 2: Who owns each public IP?

#### Option A: Provider-owned per-node IP resources in every mode

The provider would allocate and attach the Public IP regardless of provisioning mode.

This gives a uniform resource lifecycle but conflicts with Machine API ownership: the AKS RP controls the underlying NIC and may not allow the provider to attach or manage its IP resources. It also duplicates responsibilities and permissions.

#### Option B: Provisioning-mode-specific ownership

The provider creates/deletes a per-node Public IP only for VM modes. For Machine API modes, AKS creates/deletes the address in response to the Machine configuration.

#### Conclusion: Option B

Use the owner of the NIC/VM lifecycle in each mode. The customer-supplied prefix is never owned or deleted by either provider path. Do not add a provider cleanup routine for Machine-owned IP resources.

### Decision 3: Should the provider open inbound ports?

#### Option A: Automatically allow inbound Internet traffic

This could make a first hostPort demo easier, but would require a port policy and could expose every node or workload unintentionally.

#### Option B: Configure addressing only; leave ingress policy to the customer

Customers configure narrowly scoped NSG/firewall rules for the required ports and validate routing and hostPort behavior.

#### Conclusion: Option B

This feature does not create broad NSG rules or change load balancer behavior. Documentation and tests must make clear that a public address is not equivalent to an open port. Any future provider-managed ingress policy needs a separate security design.

### Decision 4: Is a Machine SDK field sufficient to claim Machine API support?

#### Option A: Ship when the SDK models the property

The generated client can serialize a request, but this does not establish that the RP accepts the property, applies it to the VM/NIC, returns it consistently, or cleans it up.

#### Option B: Gate support on an AKS RP contract test

Verify request acceptance, actual public IP allocation and prefix use, GET/LIST visibility, replacement, and deletion on supported AKS versions before enabling the feature for Machine API modes.

#### Conclusion: Option B

Treat SDK representation as necessary but insufficient. If the RP contract is absent or unavailable, ship only after a product decision to block the entire feature or explicitly document a narrower supported mode; do not silently no-op in Machine API mode.

## Provisioning and Lifecycle Design

### VM-based provisioning

1. Construct a node's public IP resource only when `nodePublicIP.enabled` is true. Use a unique resource name and stable ownership metadata sufficient to associate it with the NodeClaim/VM for recovery. Do not place the supplied prefix ID or other customer identifiers in logs or events.
2. Reference the supplied prefix in the Public IP resource and associate the resulting address resource with the node NIC IP configuration. Confirm the required Public IP SKU, allocation method, address family, zone behavior, and API version in a live Azure test before finalizing implementation.
3. Preserve the existing private-only path without an extra Azure call when disabled.
4. If IP creation succeeds but NIC/VM creation fails, delete the just-created IP using a bounded, cancellation-aware cleanup context. Preserve the primary provisioning error and report cleanup failure with enough non-sensitive context for retry/recovery.
5. On deletion, detach/delete the NIC before deleting the provider-owned IP. Treat not-found as success and make retries idempotent.
6. Extend VM/NIC read, list, and orphan-garbage-collection paths to discover a provider-owned IP even after a partial create or process restart. Never delete a customer prefix. Reconcile ownership before deleting an IP; do not infer ownership from a matching name alone.
7. Handle Azure eventual consistency, throttling, retryable errors, and cancellation using existing client patterns. A successful create response does not guarantee the IP is immediately readable.

### AKS Machine API provisioning

Set the Machine network properties only when enabled, including the optional prefix resource ID. The currently pinned SDK model exposes corresponding properties, but their exact wire shape and AKS RP support must be proven against the actual deployed API version.

Machine GET/LIST/status conversion should be examined for the authoritative address representation. Do not assume a public address is present immediately after create or that a generic `IPAddresses` entry distinguishes public from private. The first version does not add an address status API. If AKS does not return enough information to verify or observe the address, document the customer-facing discovery path and verify the resource through Azure during E2E tests.

The AKS RP owns the machine-side IP resource lifecycle. Provider deletion must continue to delete the Machine and must not independently delete an RP-managed Public IP. Validate cleanup after normal deletion, Machine create failure, and partial/batch failure.

### Header batching

Both `aksmachineapi` and `aksmachineapiheaderbatch` share the same Machine template and differ only in how creates are dispatched. Keep the NodeClass-scoped prefix in the shared template. It is not a per-machine header field and should not be added to the per-machine field registry.

Verify the batch grouping key includes all effective shared template properties, including the public IP settings. Requests with different prefix IDs or enablement must never share a template batch. Confirm batch limits, individual polling, and partial failure handling still work when the RP allocates public IPs asynchronously.

## Networking and Security Boundaries

- A public IP does not guarantee inbound reachability. The effective route, subnet/NIC/cluster NSGs, Azure Firewall, hostPort implementation, pod placement, and application listener all affect the result.
- Do not weaken default inbound rules. Customers must explicitly allow only the desired protocol, port, and source ranges.
- Do not automatically configure Kubernetes Services, cloud-controller-manager load balancers, or AKS load balancer settings.
- Require an IPv4 prefix/address family for the initial scope unless an end-to-end design and tests establish dual-stack support.
- Validate cross-subscription or cross-resource-group prefix authorization with the actual identities used by NAP and self-hosted deployments. Existing Network Contributor role assignments on the node resource group do not prove authorization to reference a prefix outside that scope.
- Verify whether the AKS RP uses the cluster identity, control-plane identity, or another principal for prefix allocation and whether any additional role assignment is required. Do not broaden role scope without evidence.
- Public IP addresses and resource IDs can reveal customer infrastructure details. Keep them out of routine logs, events, conditions, and tags unless an existing documented diagnostic policy permits them.
- Public IP quotas are distinct from VM quotas. Ensure quota failures have actionable, non-misleading error handling; do not report a hard address quota failure as successful provisioning or retry it indefinitely as transient.

## Implementation Map

1. **Contract validation:** Verify supported AKS API version and RP behavior for Machine properties, prefix constraints, identity, returned addresses, and cleanup. Confirm direct VM Public IP SKU/API requirements and actual hostPort ingress behavior.
2. **API and drift:** Add the `v1beta1` field, defaulting, CEL validation for local invariants, deepcopy/code generation, schema/chart CRDs, helper methods, and hash/drift tests. Preserve nil/false compatibility.
3. **VM path:** Add the Public IP client/wiring for both VM modes, create-and-attach behavior, reverse-order cleanup, read/list/GC recovery, quota/error classification, and required narrowly scoped authorization documentation.
4. **Machine path:** Enable the verified SDK fields in the shared template; extend read/list/status interpretation only if needed for lifecycle correctness. Confirm the setting is in batch template grouping and not a header-only field.
5. **Operations and docs:** Document cost, address allocation, prefix ownership/constraints, replacement behavior, discovery, identity requirements, and the fact that NSG/firewall ingress rules remain customer-managed.
6. **End-to-end validation:** Exercise externally initiated hostPort traffic on real AKS/Azure resources, including a customer-owned prefix. Register any new `test/suites/` directory in `.github/workflows/e2e-matrix.yaml`.

The feature should not be marked complete for all modes until the matching VM and Machine paths are proven. If Machine RP support is not available, explicitly split the release scope and make unsupported mode behavior fail clearly rather than silently ignoring configuration.

## Testing

### API and drift tests

- Extend existing `v1beta1` API/defaulting/validation tests: omitted block, nil `enabled`, false, true, valid prefix ID, malformed ID, prefix without enablement, and explicit disabled-plus-prefix.
- Verify generated schema has the expected optional fields and CEL rules; run repository generation/verification rather than editing generated output.
- Verify `Hash()` is unchanged for omitted configuration versus explicit disabled configuration if they have the same effective semantics; verify enablement and prefix changes alter the hash and cause normal static drift.
- Cover existing NodeClasses created before the new field and ensure no default-induced node replacement.

### VM unit and acceptance coverage

Extend the established VM and NIC tests for both `aksscriptless` and `bootstrappingclient`:

- Disabled path creates no Public IP and preserves current NIC shape.
- Enabled path creates one IP, sets prefix reference when configured, and associates it with the correct NIC.
- Prefix omitted allocates from the normal regional pool.
- Failures at IP create, NIC create/update, VM create, and later read/delete preserve the primary error and clean up already-created resources.
- Cancellation during create/poll is honored; cleanup is bounded and safe.
- Delete and orphan-GC handle IP-not-found, NIC-not-found, transient read-after-create, and repeated reconciliation.
- Verify customer prefixes are never deleted and unrelated IP resources are not selected by GC.
- Cover public IP quota/API errors and ensure they surface as actionable provisioning failures.
- Ensure tests use the repository's existing fake clients and extend the established `_test.go` suites rather than creating parallel tests.

### Machine API unit and acceptance coverage

Extend existing Machine template/create/read/list/delete tests for both `aksmachineapi` and `aksmachineapiheaderbatch`:

- Nil/false omits the Machine property and true/prefix serializes to the confirmed wire contract.
- GET/LIST round-trip behavior and missing/null network fields do not panic.
- Two different prefix configurations do not group into one batch; identical configurations remain batch-compatible.
- Cover per-machine polling, batch partial failure, retry, cancellation, Machine deletion, and RP-owned cleanup.
- Do not treat SDK serialization tests as evidence of RP support; require an AKS-backed contract/E2E test.

### E2E and deployment-model coverage

Exercise an externally initiated connection from outside the cluster to a workload listening on a hostPort on a node with a public IP. Verify both a permitted port and a denied/unconfigured port so the test proves there is no accidental broad ingress opening. Ensure the target pod is on the addressed node and validate the actual source-to-node-to-pod path.

Cover, where the test infrastructure supports them:

- VM modes and Machine API modes (including both Machine create dispatch strategies).
- NAP and self-hosted deployment models, with each model's actual identity and role assignments.
- No-prefix and customer-prefix allocation; prefix remains after node deletion while per-node IP is removed.
- Node replacement after changing enablement/prefix, with the old address released and new allocation verified.
- Provisioning failure and cleanup after partial resource creation.

Use real AKS/Azure E2E for RP behavior, prefix authorization, quota/error shape, eventual consistency, and inbound networking. Register new suite directories in the E2E matrix. Helm rendering snapshots alone do not validate runtime ingress.

## FAQ

### Does enabling a node public IP open hostPort to the Internet?

No. It assigns an address only. The customer's route and NSG/firewall policy must permit the desired inbound traffic, and the workload must listen on the hostPort with a working hostPort datapath.

### Does Karpenter own or delete the public IP prefix?

No. The prefix is customer-owned and must outlive the NodeClass configuration that references it. VM mode creates and deletes per-node Public IP resources; Machine API mode relies on AKS RP ownership.

### Will existing nodes change when this feature is introduced?

No. Omitted configuration is disabled and must preserve the existing node shape. If a user later changes the NodeClass setting, normal drift/replacement behavior applies; the provider does not mutate a node in place.

### Is AKS Machine API support confirmed?

Not by SDK availability. The SDK has model fields, but RP request acceptance, realized networking, prefix allocation, readback, identity authorization, and cleanup require verification on a supported AKS environment.

### Will users get the public address in NodeClaim status?

Not in this design. Karpenter's current NodeClaim flow does not provide a general external-address contract. Users can use the configured prefix range or query the Azure-managed node resource. Revisit address reporting only with a clear, mode-independent source of truth.

## Production Readiness

- [ ] AKS RP contract tested for all Machine operations and supported API versions.
- [ ] Public IP SKU, allocation behavior, address family, regional/zonal compatibility, and prefix constraints documented and tested.
- [ ] Both provisioning families preserve disabled defaults and have complete failure cleanup/orphan recovery.
- [ ] Required identity permissions are least-privilege and verified separately for NAP and self-hosted.
- [ ] Public IP quota and Azure error behavior are actionable and do not create unbounded retry loops.
- [ ] No broad inbound NSG/firewall rule is introduced; documentation states customer ingress responsibilities.
- [ ] Customer prefix is never modified/deleted, and provider-owned per-node resources are safely cleaned up.
- [ ] Real inbound hostPort test passes from outside Azure/cluster network under explicit customer-authorized ingress rules.
- [ ] NodeClass hash, drift, batch grouping, and existing-object upgrade behavior are covered.

## References

The implementation map and current-state observations in this proposal were investigated against commit [`8878fa43373fbad8974b432fc63fa9b3af921604`](https://github.com/Azure/karpenter-provider-azure/tree/8878fa43373fbad8974b432fc63fa9b3af921604). Relevant source files:

- [`AGENTS.md`](https://github.com/Azure/karpenter-provider-azure/blob/8878fa43373fbad8974b432fc63fa9b3af921604/AGENTS.md) — API lifecycle, generated files, provisioning modes, and testing conventions.
- [`pkg/apis/v1beta1/aksnodeclass.go`](https://github.com/Azure/karpenter-provider-azure/blob/8878fa43373fbad8974b432fc63fa9b3af921604/pkg/apis/v1beta1/aksnodeclass.go) and [`pkg/apis/crds/karpenter.azure.com_aksnodeclasses.yaml`](https://github.com/Azure/karpenter-provider-azure/blob/8878fa43373fbad8974b432fc63fa9b3af921604/pkg/apis/crds/karpenter.azure.com_aksnodeclasses.yaml) — current NodeClass API, defaults, hash, and generated schema.
- [`pkg/cloudprovider/drift.go`](https://github.com/Azure/karpenter-provider-azure/blob/8878fa43373fbad8974b432fc63fa9b3af921604/pkg/cloudprovider/drift.go) — static drift behavior.
- [`pkg/providers/instance/vminstance.go`](https://github.com/Azure/karpenter-provider-azure/blob/8878fa43373fbad8974b432fc63fa9b3af921604/pkg/providers/instance/vminstance.go) — VM create/read/delete lifecycle.
- [`pkg/providers/instance/aksmachineinstance.go`](https://github.com/Azure/karpenter-provider-azure/blob/8878fa43373fbad8974b432fc63fa9b3af921604/pkg/providers/instance/aksmachineinstance.go) and [`pkg/providers/instance/aksmachineinstancehelpers.go`](https://github.com/Azure/karpenter-provider-azure/blob/8878fa43373fbad8974b432fc63fa9b3af921604/pkg/providers/instance/aksmachineinstancehelpers.go) — Machine template, create/read/delete, and instance conversion.
- [`pkg/providers/azclient/aksmachinesheaderbatch/`](https://github.com/Azure/karpenter-provider-azure/tree/8878fa43373fbad8974b432fc63fa9b3af921604/pkg/providers/azclient/aksmachinesheaderbatch) — shared-template batching and per-machine header behavior.
- [`pkg/providers/azclient/`](https://github.com/Azure/karpenter-provider-azure/tree/8878fa43373fbad8974b432fc63fa9b3af921604/pkg/providers/azclient) and [`charts/karpenter/`](https://github.com/Azure/karpenter-provider-azure/tree/8878fa43373fbad8974b432fc63fa9b3af921604/charts/karpenter) — Azure client and deployment/role configuration.
- [`designs/0010-aks-machines-batch-creation.md`](https://github.com/Azure/karpenter-provider-azure/blob/8878fa43373fbad8974b432fc63fa9b3af921604/designs/0010-aks-machines-batch-creation.md) and [`designs/0014-node-image-and-k8s-version-controls.md`](https://github.com/Azure/karpenter-provider-azure/blob/8878fa43373fbad8974b432fc63fa9b3af921604/designs/0014-node-image-and-k8s-version-controls.md) — precedent for batch semantics and NodeClass API/drift design.

The Machine model evidence is the repository's pinned `armcontainerservice` SDK dependency at that commit. It proves only that the client can represent/serialize the fields; it is not an AKS RP support guarantee.
