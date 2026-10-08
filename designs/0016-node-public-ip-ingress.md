# Node Public IP Support for Direct Internet Ingress

**Author:** @wdarko1

**Last updated:** Oct 8, 2026

**Status:** Proposed

**Related issue:** [Azure/karpenter-provider-azure#656](https://github.com/Azure/karpenter-provider-azure/issues/656)

## Overview

Customers using Node Auto Provisioning (NAP) may need Internet clients to reach workloads exposed through `hostPort` on dynamically provisioned nodes. A node-level public IP provides a directly addressable endpoint. Customers may also need addresses allocated from a public IP prefix that they own, for predictable address ranges and allow-listing.

This design proposes opt-in node public IP configuration on the current `v1beta1` `AKSNodeClass`, consistent across VM-based and AKS Machine API-based provisioning. Users explicitly select the public address families; a dual-stack cluster does not implicitly receive public IPv6. In VM modes, the provider creates one public IP resource per requested family and associates it with the node NIC. In Machine API modes, the provider requests the capability through the AKS Machine resource and AKS owns the underlying public IP lifecycle. In both cases, supplied prefixes remain customer-owned.

The design is conditional on validating the AKS Resource Provider (RP) contract for Machine resources, including dual-stack capability on the target AKS NAP release. The pinned Azure SDK contains `EnableNodePublicIP` and `NodePublicIPPrefixID` fields, but SDK serialization, conventional AKS node-pool documentation, and a planned NAP dual-stack rollout do not establish that the AKS RP accepts or honors these properties. Do not expose a family or provisioning mode as supported until a supported AKS environment proves its create, read, and delete behavior.

### Goals

- Permit users to opt in to public IPv4 addressing, and explicitly request public IPv6 alongside IPv4 where the target deployment supports it.
- Optionally allocate each requested family from its matching customer-supplied public IP prefix.
- Preserve behavior for existing NodeClasses and nodes when the new fields are omitted.
- Support both VM provisioning modes and both Machine API create dispatch strategies, subject to verified AKS RP support.
- Replace nodes when their effective public IP configuration changes, using normal NodeClass hash/drift behavior.
- Define ownership, cleanup, authorization, prefix compatibility, placement, error, and end-to-end ingress expectations explicitly.

### Non-Goals

- Automatically creating, modifying, or deleting a customer-supplied public IP prefix.
- Automatically opening NSG/firewall ports or defining a cluster-wide inbound security policy.
- Guaranteeing an application is reachable merely because a node has a public IP. Routing, hostPort behavior, NSGs, firewalls, and workload health remain separate requirements.
- Adding public IPs to existing nodes in place; configuration changes take effect through node replacement.
- Providing a new NodeClaim status field or a stable workload endpoint/discovery service.
- Supporting arbitrary inbound traffic, load balancer configuration, or per-workload public IP allocation.
- Enabling this feature in AKS Machine API mode before the AKS RP contract is confirmed.
- IPv6-only public addressing unless separately designed and validated.

## Current Problem

The current NodeClass API has no setting for node public IPs. The direct VM path creates NICs without a public IP association and has no public IP client or cleanup path. The Machine API template has SDK model fields corresponding to node public IP enablement and prefix ID, but they are commented out and their server-side behavior is unverified. Current VM and Machine conversion paths also do not provide a public node address to consumers.

An address alone is not sufficient for Internet ingress. Traffic must also be routed to the node and permitted by the effective NSG/firewall policy; the hostPort datapath must then forward traffic to the selected pod. This feature configures node addressing only and requires a live-cluster test that exercises an actual inbound connection.

## Operating Model

| Concern | VM-based modes (`aksscriptless`, `bootstrappingclient`) | AKS Machine API modes (`aksmachineapi`, `aksmachineapiheaderbatch`) |
|---|---|---|
| Public IP creation | Provider creates one Azure Public IP resource per requested family, referencing that family's optional customer prefix, then associates it with the NIC. | Provider requests each explicitly selected family through Machine properties; AKS RP creates and manages the underlying resources if the verified contract supports it. |
| Prefix ownership | Customer-owned; provider only references each matching family prefix. | Customer-owned; AKS RP must accept and honor each matching family reference. |
| Per-node address lifecycle | Provider deletes each provider-owned Public IP after NIC detachment and recovers orphaned resources. | AKS RP owns creation and deletion with the Machine. |
| Batch behavior | Not applicable. | Both dispatch strategies use the same Machine template. Family requests and prefix mappings are shared template data, not per-machine header fields. |
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
    addressFamilies:
      - IPv4
      - IPv6
    prefixes:
      - addressFamily: IPv4
        id: /subscriptions/<subscription>/resourceGroups/<rg>/providers/Microsoft.Network/publicIPPrefixes/<ipv4-prefix>
      - addressFamily: IPv6
        id: /subscriptions/<subscription>/resourceGroups/<rg>/providers/Microsoft.Network/publicIPPrefixes/<ipv6-prefix>
```

The proposed Go shape is illustrative:

```go
type NodePublicIP struct {
    Enabled         *bool                       `json:"enabled,omitempty"`
    AddressFamilies []NodePublicIPAddressFamily `json:"addressFamilies,omitempty"`
    Prefixes        []NodePublicIPPrefix        `json:"prefixes,omitempty"`
}

type NodePublicIPPrefix struct {
    AddressFamily NodePublicIPAddressFamily `json:"addressFamily"`
    ID            string                    `json:"id"`
}
```

This is a conceptual shape, not a committed API contract. A singular `prefixID` cannot unambiguously describe dual-stack allocation. The proposal associates each prefix reference with its address family; consumers must map by family, never by list position. Final field names, collection ordering, serialization, and wire representation must be verified against the supported AKS Machine API contract before implementation. Do not assume a list can be serialized into or substituted for the SDK's singular prefix field.

Omitted `nodePublicIP` or `enabled: false` means disabled. When enabled, explicitly request address families; IPv4 is supported as a standalone request, including on a dual-stack cluster, while dual-stack public addressing requires an explicit IPv4-and-IPv6 request. Do not infer public IPv6 from cluster dual-stack capability. Reject IPv6-only public addressing unless a separate design establishes its contract. A prefix reference requires `enabled: true` and must identify a requested family exactly once. Validate local invariants and Azure resource ID syntax; verify existence, location, subscription, authorization, family, SKU, zone, and capacity before claiming readiness.

Before finalizing field names and validation, confirm the actual AKS API vocabulary, family-to-prefix mapping, wire representation, and supported prefix constraints. The example is proposed API, not a claim that the RP contract has been verified. Avoid a cluster-level global default in the first version: NodeClass scope keeps the behavior visible with the pool shape, isolates opt-in, and lets the NodeClass hash drive replacement.

### Compatibility, Defaulting, and Drift

- Add the new field only to `v1beta1`; `v1alpha2` is deprecated and unserved.
- Preserve omitted-field behavior exactly: old NodeClasses continue to create private nodes without public IP allocation.
- Define API defaulting and helper methods so nil and false have the same effective behavior in admission, VM construction, Machine construction, hashing, and tests.
- Include the effective configuration, requested address families, and family-to-prefix mapping in the NodeClass spec hash. Changing enablement, a requested family, or its prefix should mark existing nodes drifted and allow replacement through normal Karpenter disruption behavior.
- Test hashes for absent versus explicit false to avoid needless drift on upgrade. Evaluate whether the hash-version annotation requires a bump; only bump it if the hash algorithm/compatibility semantics change, not merely because a new optional field was added.
- Generate deepcopy code, CRDs, and chart CRD copies using the repository's `make verify` workflow; do not hand-edit generated artifacts.
- Do not mutate existing Azure resources in place. During replacement, old nodes retain their existing address until their normal termination path releases it.

## Decisions

### Decision 1: Where should users configure node public IPs?

#### Option A: A strongly typed `AKSNodeClass` field

Expose an opt-in `nodePublicIP` block with `enabled`, explicit `addressFamilies`, and optional family-specific prefix references.

**Pros**

- Public IP configuration is part of the node's provisioned shape and naturally participates in NodeClass hashing and drift.
- Requested address families and their matching prefixes are explicit, reviewable, and scoped to the NodeClass that uses them.
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

Use an opt-in NodeClass field. Do not add a global default or infer enablement or public IPv6 from the cluster's address-family configuration. Require an explicit family request; dual-stack public addressing requires both families to be requested.

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

1. Construct a node's public IP resource only when `nodePublicIP.enabled` is true, for each explicitly requested family. Use a unique resource name and stable ownership metadata sufficient to associate it with the NodeClaim/VM for recovery. Do not place supplied prefix IDs or other customer identifiers in logs or events.
2. Map each requested family to its matching prefix reference and associate the resulting address resource with the node NIC IP configuration. Do not rely on prefix collection ordering. Confirm the required Public IP SKU, allocation method, address family, zone behavior, and API version in a live Azure test before finalizing implementation.
3. Preserve the existing private-only path without an extra Azure call when disabled.
4. If either family allocation succeeds but another requested family or NIC/VM creation fails, do not report provisioning success. Delete all provider-owned per-node IP resources already created using a bounded, cancellation-aware cleanup context. Preserve the primary provisioning error and report cleanup failure with enough non-sensitive context for retry/recovery.
5. On deletion, detach/delete the NIC before deleting the provider-owned IP. Treat not-found as success and make retries idempotent.
6. Extend VM/NIC read, list, and orphan-garbage-collection paths to discover provider-owned IP resources for each family even after a partial create or process restart. Never create, resize, modify, or delete a customer prefix. Reconcile ownership before deleting an IP; do not infer ownership from a matching name alone.
7. Handle Azure eventual consistency, throttling, retryable errors, and cancellation using existing client patterns. A successful create response does not guarantee the IP is immediately readable.

### AKS Machine API provisioning

Set Machine network properties only when enabled, including the requested address families and the matching prefix reference for each family. The currently pinned SDK model exposes a singular prefix property, but its serialization and conventional AKS node-pool documentation do not prove that NAP Machine API supports dual-stack public addressing or family-specific prefixes. Verify the target AKS NAP capability and the exact API vocabulary, mapping, ordering, and wire representation against the deployed API version before finalizing the API. Never map multiple prefixes by list ordering or silently drop a requested family.

Machine GET/LIST/status conversion should be examined for the authoritative address representation. Do not assume a public address is present immediately after create or that a generic `IPAddresses` entry distinguishes public from private. The first version does not add an address status API. If AKS does not return enough information to verify or observe the address, document the customer-facing discovery path and verify the resource through Azure during E2E tests.

The AKS RP owns the machine-side IP resource lifecycle. Provider deletion must continue to delete the Machine and must not independently delete an RP-managed Public IP. A request for two families is not successful unless both are provisioned. Validate cleanup after normal deletion, Machine create failure, and partial/batch failure; specifically verify that the AKS RP cleans up an allocation for one family if allocation of the other family fails.

### Header batching

Both `aksmachineapi` and `aksmachineapiheaderbatch` share the same Machine template and differ only in how creates are dispatched. Keep the NodeClass-scoped family requests and family-to-prefix mapping in the shared template. They are not per-machine header fields and should not be added to the per-machine field registry.

Verify the batch grouping key includes all effective shared template properties, including requested address families and each family-mapped prefix. Requests with different family mappings or enablement must never share a template batch. Confirm batch limits, individual polling, and partial failure handling still work when the RP allocates public IPs asynchronously, including a partial dual-stack allocation failure.

## Networking and Security Boundaries

- A public IP does not guarantee inbound reachability. The effective route, subnet/NIC/cluster NSGs, Azure Firewall, hostPort implementation, pod placement, and application listener all affect the result.
- Do not weaken default inbound rules. Customers must explicitly allow only the desired protocol, port, and source ranges.
- Do not automatically configure Kubernetes Services, cloud-controller-manager load balancers, or AKS load balancer settings.
- Public address family is an explicit NodeClass choice, independent of cluster address-family capability. IPv4-only public addressing is valid on a dual-stack cluster; dual-stack public addressing must be explicitly requested and independently verified. IPv6-only public addressing is out of scope unless separately designed.
- Verify the target AKS NAP release actually supports dual-stack nodes and instance-level public addressing together. Neither a planned rollout nor conventional AKS node-pool documentation establishes NAP Machine API support.
- Public IP addresses and resource IDs can reveal customer infrastructure details. Keep them out of routine logs, events, conditions, and tags unless an existing documented diagnostic policy permits them.

### Prefix compatibility and ownership

For every supplied prefix, verify its address family, supported SKU, region, subscription, and authorization before relying on it. Each prefix and all associated node resources must be in the same region and subscription. Initially reject cross-subscription references; allow a different resource group only when the identity actually performing the operation is verified to have the required authorization there. Existing Network Contributor assignments on the node resource group do not prove access to an external prefix.

Treat customer prefixes as references only: the provider must never create, resize, modify, or delete them. Verify the supported SKU and family combinations against the target Azure and AKS contracts rather than assuming the SDK model or a generic Public IP document establishes NAP support.

### Zone and placement compatibility

Distinguish zone-redundant prefixes from single-zone prefixes. Verify prefix zone metadata semantics and AKS RP behavior for the deployed APIs; do not infer prefix redundancy from public-IP behavior or an empty zone metadata field. Prefer zone-redundant prefixes where supported.

If single-zone prefixes are supported, determine compatible node placements before instance selection by intersecting prefix constraints with NodePool and pod topology requirements. Preserve the placement restriction during replacement and consolidation. If constraints conflict, fail clearly; never override workload topology or allocate an address outside a supplied prefix. Validate dual-stack prefix pairs whose zone properties are incompatible and reject them before creating either address.

### API-server access

Verify NAP behavior when API-server authorized IP ranges are enabled. Where AKS requires node-public-IP prefixes to be authorized, require customer-managed coverage for each relevant prefix before provisioning. Do not automatically broaden authorized ranges or modify customer control-plane access policy.

For prefix rotation, authorize the new prefix before creating replacement nodes and keep the old prefix authorized until all old nodes have terminated. Verify both node registration and continued API-server connectivity; successful external ingress alone is insufficient.

### Identity and capacity

Identify separately which identity reads prefix metadata and which identity allocates an address for each deployment and provisioning path. Do not infer the AKS RP principal from the identity that submits a Machine request:

| Deployment and path | Identity contract to verify |
|---|---|
| NAP, VM-based modes | Identify the NAP/provider identity that reads prefix metadata and the principal that allocates each address. |
| NAP, Machine API modes (both create dispatch strategies) | Identify the identity that reads prefix metadata for compatibility checks, the Machine request identity, and the AKS RP principal that allocates each address. |
| Self-hosted, VM-based modes | Identify the configured self-hosted identity that reads prefix metadata and allocates each address. |
| Self-hosted, Machine API modes (both create dispatch strategies) | Identify the self-hosted identity that reads prefix metadata and submits the request, plus the AKS RP principal that allocates each address. |

Establish least-privilege permissions and scope for each verified operation; do not broaden role assignments without evidence. If the allocation principal cannot be established or authorized, the affected path is not ready for release.

Account for finite address capacity, regional public IP quota, all consumers sharing each prefix, concurrent allocations, and temporary old/new overlap during replacement. Capacity prechecks are advisory and do not reserve addresses. On concurrent exhaustion, return an actionable error with bounded retry/backoff; never silently fall back outside a supplied prefix or report a partially allocated dual-stack request as successful.

## Implementation Map

1. **Contract validation:** Verify target AKS NAP dual-stack availability at implementation and release time, plus the Machine RP API version, accepted address-family and family-to-prefix wire contract, identity, prefix constraints, returned addresses, zone behavior, and cleanup. Do not infer NAP support from SDK serialization or conventional node-pool documentation. Confirm direct VM Public IP SKU/API requirements and actual hostPort ingress behavior.
2. **API and drift:** Add the `v1beta1` field, defaulting, CEL validation for local invariants, deepcopy/code generation, schema/chart CRDs, helper methods, and hash/drift tests. Preserve nil/false compatibility and include explicit requested families and family-to-prefix mapping in the effective configuration.
3. **VM path:** Add the Public IP client/wiring for both VM modes, per-family create-and-attach behavior, partial-failure cleanup, read/list/GC recovery, quota/error classification, and required narrowly scoped authorization documentation.
4. **Machine path:** Enable only verified SDK fields in the shared template; extend read/list/status interpretation only if needed for lifecycle correctness. Confirm family mapping, batch template grouping, asynchronous polling, partial-failure cleanup by the AKS RP, and that the settings are not incorrectly modeled as per-machine headers.
5. **Operations and docs:** Document cost, address allocation, prefix ownership/constraints, zone and topology behavior, API-server authorized-range prerequisites and rotation ordering, replacement headroom, discovery, per-mode identity requirements, and the fact that NSG/firewall ingress rules remain customer-managed.
6. **End-to-end validation:** Exercise externally initiated IPv4 and IPv6 hostPort traffic independently on real AKS/Azure resources, including customer-owned prefixes and API-server connectivity. Validate both VM modes, both Machine create dispatch strategies, and NAP and self-hosted identities. Register any new `test/suites/` directory in `.github/workflows/e2e-matrix.yaml`.

The feature should not be marked complete for all modes until the matching VM and Machine paths are proven. If Machine RP support is not available, explicitly split the release scope and make unsupported mode behavior fail clearly rather than silently ignoring configuration.

## Testing

### API and drift tests

- Extend existing `v1beta1` API/defaulting/validation tests: omitted block, nil `enabled`, false, true, IPv4-only and explicit dual-stack family selections, IPv6-only rejection, valid family-mapped prefix references, malformed IDs, duplicate or unrequested families, prefix without enablement, and explicit disabled-plus-prefix.
- Verify generated schema has the expected optional fields and CEL rules; run repository generation/verification rather than editing generated output.
- Verify `Hash()` is unchanged for omitted configuration versus explicit disabled configuration if they have the same effective semantics; verify enablement and prefix changes alter the hash and cause normal static drift.
- Verify family-to-prefix mapping contributes to hashing and drift independent of prefix-list order, and that family changes or mapping changes trigger replacement.
- Cover existing NodeClasses created before the new field and ensure no default-induced node replacement.

### VM unit and acceptance coverage

Extend the established VM and NIC tests for both `aksscriptless` and `bootstrappingclient`:

- Disabled path creates no Public IP and preserves current NIC shape.
- Enabled path creates one IP per requested family, uses that family's prefix reference when configured, and associates each address with the correct NIC configuration.
- IPv4-only opt-in creates no IPv6 public address on a dual-stack cluster; an explicit IPv4+IPv6 request maps each family to its own prefix.
- A prefix omitted for a requested family allocates that family's address from the normal regional pool, subject to verified Azure/AKS behavior.
- Failures at IP create, NIC create/update, VM create, and later read/delete preserve the primary error and clean up already-created resources.
- If one requested family succeeds and another fails, provisioning fails and every provider-owned allocation already created is cleaned up.
- Cancellation during create/poll is honored; cleanup is bounded and safe.
- Delete and orphan-GC handle IP-not-found, NIC-not-found, transient read-after-create, and repeated reconciliation.
- Verify customer prefixes are never deleted and unrelated IP resources are not selected by GC.
- Cover public IP quota/API errors and ensure they surface as actionable provisioning failures.
- Verify prefix family, SKU, region, subscription, cross-resource-group authorization, single-zone placement, and incompatible dual-stack prefix-pair rejection.
- Ensure tests use the repository's existing fake clients and extend the established `_test.go` suites rather than creating parallel tests.

### Machine API unit and acceptance coverage

Extend existing Machine template/create/read/list/delete tests for both `aksmachineapi` and `aksmachineapiheaderbatch`:

- Nil/false omits the Machine property; requested families and family-mapped prefix references serialize only to the confirmed wire contract.
- GET/LIST round-trip behavior and missing/null network fields do not panic.
- Two different prefix configurations do not group into one batch; identical configurations remain batch-compatible.
- Cover per-machine polling, batch partial failure, retry, cancellation, Machine deletion, and RP-owned cleanup.
- Force one-family allocation failure after the other family succeeds; verify provisioning is not reported successful and AKS RP cleanup removes the partial allocation in both create dispatch strategies.
- Do not treat SDK serialization tests as evidence of RP support; require an AKS-backed contract/E2E test.

### E2E and deployment-model coverage

Exercise an externally initiated connection from outside the cluster to a workload listening on a hostPort on a node with a public IP. Verify both a permitted port and a denied/unconfigured port so the test proves there is no accidental broad ingress opening. Ensure the target pod is on the addressed node and validate the actual source-to-node-to-pod path.

Cover, where the test infrastructure supports them:

- VM modes and Machine API modes (including both Machine create dispatch strategies).
- NAP and self-hosted deployment models, with each model's actual identity and role assignments.
- IPv4-only cluster with IPv4 public addressing; dual-stack cluster with IPv4-only public addressing; and dual-stack cluster with explicitly requested IPv4+IPv6 public addressing. Verify IPv4 and IPv6 ingress independently and verify IPv4-only opt-in does not allocate IPv6. IPv6-only public addressing is not a supported case for this design.
- On the target AKS NAP RP, verify IPv4-only requests on dual-stack nodes do not allocate IPv6, and explicit dual-stack requests allocate both families with the correct prefix mapping.
- No-prefix and customer-prefix allocation; verify prefix remains unchanged after node deletion while per-node addresses are removed.
- Prefix address family, SKU, region, subscription, and authorization checks, including denied cross-subscription references and authorized cross-resource-group references.
- API-server authorized IP ranges: node registration/connectivity, customer-managed coverage for required prefixes, and rotation ordering that authorizes the new prefix before replacement while retaining old authorization through old-node termination.
- Prefix placement matrix: zone-redundant prefix across supported placements; single-zone prefix restricted to compatible NodePool and pod topology; no-zone region; empty/unknown zone metadata; conflicting topology; and dual-stack prefix pairs with incompatible zones. Verify replacement and consolidation preserve the same restrictions.
- Prefix exhaustion for either family, shared consumers, regional quota, concurrent allocation, and sufficient old/new overlap for replacement. Verify advisory prechecks, actionable errors, bounded retry/backoff, and no fallback outside a supplied prefix.
- Node replacement after changing enablement, requested family, or a family-specific prefix, with old addresses released only after old nodes terminate and new allocations verified.
- Provisioning failure and cleanup after partial resource creation.

Use real AKS/Azure E2E for target NAP capability availability, RP behavior, prefix authorization, quota/error shape, eventual consistency, zone semantics, partial dual-stack cleanup, API-server connectivity, and inbound networking. Register new suite directories in the E2E matrix. Helm rendering snapshots alone do not validate runtime ingress.

## FAQ

### Does enabling a node public IP open hostPort to the Internet?

No. It assigns an address only. The customer's route and NSG/firewall policy must permit the desired inbound traffic, and the workload must listen on the hostPort with a working hostPort datapath.

### Does Karpenter own or delete the public IP prefix?

No. The prefix is customer-owned and must outlive the NodeClass configuration that references it. VM mode creates and deletes per-node Public IP resources; Machine API mode relies on AKS RP ownership.

### Will existing nodes change when this feature is introduced?

No. Omitted configuration is disabled and must preserve the existing node shape. If a user later changes the NodeClass setting, normal drift/replacement behavior applies; the provider does not mutate a node in place.

### Is AKS Machine API support confirmed?

Not by SDK availability or conventional AKS node-pool documentation. RP request acceptance, target NAP dual-stack availability, realized per-family networking, prefix allocation, readback, identity authorization, zone compatibility, and cleanup require verification on a supported AKS environment.

### Does a dual-stack cluster automatically receive public IPv6?

No. Public address families are explicit. IPv4-only public addressing can be requested on a dual-stack cluster; public IPv6 is allocated only as part of an explicit dual-stack public-address request. IPv6-only public addressing is outside this design.

### Will users get the public address in NodeClaim status?

Not in this design. Karpenter's current NodeClaim flow does not provide a general external-address contract. Users can use the configured prefix range or query the Azure-managed node resource. Revisit address reporting only with a clear, mode-independent source of truth.

## Production Readiness

- [ ] Target AKS NAP dual-stack capability is verified as available at implementation/release time; no rollout timing is treated as a guarantee.
- [ ] AKS RP contract tested for all Machine operations, both Machine create dispatch strategies, and supported API versions; SDK serialization and conventional node-pool docs are not used as proof.
- [ ] IPv4-only cluster+IPv4, dual-stack cluster+IPv4-only, and dual-stack cluster+explicit dual-stack public addressing are tested; IPv4 and IPv6 ingress are independently verified and IPv4-only does not allocate IPv6.
- [ ] Public IP SKU, allocation behavior, each address family, prefix family mapping, regional/subscription/authorization constraints, and zone semantics are documented and tested.
- [ ] Zone-redundant/single-zone/no-zone matrix, conflicting NodePool/pod topology, replacement/consolidation restrictions, and incompatible dual-stack prefix pairs are tested.
- [ ] API-server authorized-range prerequisites, customer-managed coverage, node registration/connectivity, and safe prefix rotation ordering are verified.
- [ ] Prefix read/allocation identities and least-privilege scope are verified for NAP and self-hosted VM and Machine API paths.
- [ ] Capacity accounts for shared consumers, regional quota, concurrency, and old/new replacement overlap; exhaustion is actionable, bounded, and never falls back outside the supplied prefix.
- [ ] Both provisioning families preserve disabled defaults and have complete failure cleanup/orphan recovery.
- [ ] Partial dual-stack failure does not report success; VM-owned allocations are cleaned up and AKS RP cleanup is verified for Machine API modes.
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
