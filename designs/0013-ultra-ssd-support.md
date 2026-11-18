# Ultra SSD Support for NAP

**Author:** @pablotrivino

**Last updated:** July 17, 2026

**Status:** Implemented

## Overview

AKS supports Azure Ultra Disks by enabling Ultra SSD on the cluster or on a nodepool at creation time with `--enable-ultra-ssd`. Nodes created from that cluster or node pool can then attach Persistent Volumes backed by the `UltraSSD_LRS` storage class.

Today in AKS, `--enable-ultra-ssd` ultimately enables `AdditionalCapabilities.UltraSSDEnabled = true` on the underlying VM or VMSS model. That does not automatically add labels, taints, or tolerations for scheduling. It only makes the node capable of attaching Ultra SSDs for workloads that use an UltraSSD-backed PV. Placement policy remains the user's responsibility.

For Node Auto Provisioning (NAP), we need the equivalent behavior on dynamically created capacity. This means Karpenter must be able to:

- let users request Ultra SSD-capable offerings through requirements,
- filter out VM sizes and zonal offerings that do not support Ultra SSD when requested,
- set the correct downstream API fields when creating capacity.

This document describes the Ultra SSD support implemented for NAP.

### Goals

- Add support for enabling Ultra SSD on dynamically provisioned nodes.
- Support both VM provisioning mode and AKS Machine API mode.
- Filter instance types and offerings to only Ultra SSD-capable SKU plus zone combinations when the well-known label is requested.

### Non-Goals

- Adding provider-managed scheduling controls beyond offerings filtering, such as automatic Requirements, labels, taints, or tolerations.
- Automatically steering Ultra SSD workloads onto Ultra SSD-capable nodes.

## Decisions

### Decision 1: Where should Ultra SSD be configured?

#### Option A: Add a strongly typed field to `AKSNodeClass`

Proposed shape:

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
spec:
	ultraSSD:
		enabled: true
```

Suggested Go shape:

```go
type UltraSSD struct {
		Enabled *bool `json:"enabled,omitempty"`
}

type AKSNodeClassSpec struct {
		// ... existing fields ...
		UltraSSD *UltraSSD `json:"ultraSSD,omitempty"`
}

func (in *AKSNodeClass) IsUltraSSDEnabled() bool {
		return in.Spec.UltraSSD != nil &&
				in.Spec.UltraSSD.Enabled != nil &&
				*in.Spec.UltraSSD.Enabled
}
```

This would match the existing API style for feature toggles such as `artifactStreaming`, `security.encryptionAtHost`, and `localDNS`.

Arguments for this option:

- it is a provisioning feature, not a schedulable label,
- it aligns with the current `AKSNodeClass` design pattern.

The user expectation of “default false” is still satisfied. If `spec.ultraSSD` or `spec.ultraSSD.enabled` is omitted, the effective value is disabled.

We are not choosing this option because it makes Ultra SSD a NodeClass-level pool shape even though availability is offering-specific and users need to express the requirement through scheduling constraints.

#### Option B: Use a Well-Known Label

Ultra SSD is a scheduling consideration. Workloads that are using Ultra SSD should not be scheduled on nodes that do not have Ultra SSD enabled, as attaching the disk will fail.

Scheduling requirements are handled by Requirements, usually defined in the NodePool. Therefore, we should make Ultra SSD enablement a well-known label. This matches precedents set by labels such as premium-storage-capable. Furthermore, this is a simple true/false configuration, which makes defining it as a rigid definition in the NodeClass overkill.

Users will use the well-known label on their workload or NodePool to request UltraSSD-enabled nodes. On the Karpenter-Provider side, this means filtering out Offerings that do not support it, and automatically enabling it if and only if the user has specifically requested it. The resulting label on the node is either true or false and answers the question "Is UltraSSD enabled on this node?".

#### Conclusion: Well-Known Label

We choose to use a well-known label. This path is consistent with the fact that Requirements are designed to handle scheduling concerns and the configuration is simple.

### Decision 2: How should we filter for compatible Instances?

#### Offerings Filtering

Ultra SSD is only available in regions and zones that support it, and only by specific SKUs. Therefore, we need to check availability for each zone when creating Offerings for InstanceTypes. We add the well-known label to the offering to reflect the availability.

Adding this label to the offering lets us use it as a Requirement for checking compatibility with the incoming NodeClaim.

This is a slight deviation from the normal offering shape. When a SKU and zone support Ultra SSD, the offering advertises both `true` and `false` for `karpenter.azure.com/sku-storage-ultra-ssd`. When they do not support Ultra SSD, the offering advertises only `false`. We do this instead of creating separate offerings for Ultra SSD enabled and disabled states. Creating multiple offerings would multiply the offerings list by feature permutations times the number of existing offerings, creating unnecessary performance cost and setting the precedent that each new feature should enlarge the offerings list by another permutation.

### Decision 3: Should we always enable Ultra SSD by default?

#### Option A: Do not always enable Ultra SSD

Under this option, Karpenter only enables Ultra SSD when the NodeClaim explicitly requests `karpenter.azure.com/sku-storage-ultra-ssd In ["true"]`. If the label is omitted, ambiguous, or allows both `true` and `false`, Ultra SSD remains disabled.

Arguments for this option:

- Enabling Ultra SSD has a reservation fee, so defaulting it on could create cost for users who did not ask for it.
- The user experience is already streamlined through the well-known label. Users request Ultra SSD the same way they request other schedulable capabilities.
- It mirrors AKS more closely. AKS does not silently enable Ultra SSD on every pool; users opt in with `--enable-ultra-ssd`.
- It reduces the chance of future compatibility or billing surprises because the provider only enables the Azure capability when a workload or NodePool explicitly requires it.

#### Option B: Always enable Ultra SSD when the selected offering supports it

Under this option, Karpenter would enable Ultra SSD on every node whose selected SKU and zone support Ultra SSD, even when the NodeClaim did not explicitly request it.

Arguments for this option:

- The code path is a little cleaner because offerings would only need to advertise a passive capability. They would not need to carry both `true` and `false` as valid values for supported SKU and zone combinations.
- Provisioning would not need to distinguish between explicit user intent and selected offering capability.

#### Conclusion: Option A

We choose Option A. Ultra SSD remains opt-in and is enabled only when the NodeClaim explicitly selects `karpenter.azure.com/sku-storage-ultra-ssd In ["true"]`. This keeps costs and Azure capability enablement tied to user intent, matches AKS behavior, and avoids turning passive SKU capability into default node configuration.

## Implementation

### Well-Known Labels

Ultra SSD is defined as the well-known label `karpenter.azure.com/sku-storage-ultra-ssd` with well-known values of `true` and `false`.

Example NodePool requirement:

```yaml
requirements:
- key: karpenter.azure.com/sku-storage-ultra-ssd
  operator: In
  values: ["true"]
```

Because this is a well-known label, users may omit it. An omitted label means Ultra SSD is not requested; it does not prevent Karpenter from selecting an instance type that could support Ultra SSD for other reasons, but the provider will not enable Ultra SSD on the created node unless the NodeClaim explicitly requires `karpenter.azure.com/sku-storage-ultra-ssd In ["true"]`.

### Filtering

Set the label values when we create offerings.

- Offerings where the selected SKU and zone support Ultra SSD include both `true` and `false` for `karpenter.azure.com/sku-storage-ultra-ssd`.
- Offerings where the selected SKU and zone do not support Ultra SSD include only `false`.
- Incoming NodeClaims that require `karpenter.azure.com/sku-storage-ultra-ssd In ["true"]` are only compatible with offerings where the selected SKU and zone support Ultra SSD.
- Incoming NodeClaims that omit the label or allow both values can still use Ultra SSD-capable offerings, but Ultra SSD remains disabled at provisioning time.

### VM mode wiring

For both Machine and VMInstance, we check if the label is set to `true` in the incoming *NodeClaim*, not merely whether the selected offering supports it. This is because we do not want to enable Ultra SSD unless a workload or NodePool explicitly asks for it, even if the offering supports it. Furthermore, the incoming NodeClaim must specifically request it as `true`. Empty or `[true, false]` always defaults to false.

#### VM
VM creation sets `vm.Properties.AdditionalCapabilities.UltraSSDEnabled = true` for Ultra SSD-enabled NodeClaims. This is left nil if Ultra SSD is not enabled, which is consistent with AKS.

#### Machine
Machine API creation sets `armcontainerservice.Machine.Properties.Hardware.UltraSsdEnabled = true` if enabled and `false` if disabled. This is consistent with AKS.

##### Batching
For batching with Machine API we run into a validation issue. Batching code currently batches groups of PutMachine requests into one request, and uses a shared machine "template" that all the requested machines will share. This template only includes properties, and zones and tags are passed through the header instead. The problem arises when Ultra SSD is enabled, as this field requires at least one zone to be specified, otherwise the API call fails because it cannot validate that Ultra SSD is available (zones are dropped in the shared template). To get around this we have a few options:

##### Option A: Populate the Zone
We can simply fill in a zone if Ultra SSD is enabled. A batch might have multiple zones, but all the requests in a batch should share the same properties. In other words, Ultra SSD is enabled for all the requests, so we can safely assume any zone in the batch can be used to fill in the shared machine template's zone, which would allow validation to pass.

This is a relatively straightforward solution. Server-side, the zones get replaced by the header zones anyway. The drawback with this approach is that it could lead to unexpected behavior if the server-side behavior ever changes.

##### Option B: Group Requests Into Batches With the Same Zone
Instead of just populating the shared machine template with a zone, we edit the function that builds the template to include the zone when Ultra SSD is enabled. This means that machine requests with Ultra SSD enabled will be placed in batches with requests that also have Ultra SSD enabled and are in the same zone.

This approach removes uncertainty about specifying the zone, as the shared template will have the same zone as all requests in the batch. The drawback to this approach is we might end up with a lot of batches, effectively negating the advantages we gain from batching. This can happen if, for some reason, we enable Ultra SSD and end up picking multiple zones.

##### Decision: Option B
Option B essentially adds the zone to the shared template, allowing us to both consider the zone when grouping requests and specify a zone for validation. This option is good because it:

- Keeps the shared-template zone consistent with the zones in the header.
- Has low performance impact. There are typically only 3 zones per region, so we would only be distributing requests across a few additional batches.

### Customer Experience and AKS Parity

Customers wishing to use Ultra SSD will add a NodePool requirement for `karpenter.azure.com/sku-storage-ultra-ssd In ["true"]`. This requirement filters offerings to SKU and zone combinations that support Ultra SSD, and the resulting NodeClaim carries the same requirement into provisioning.

In AKS, creating a cluster with `--enable-ultra-ssd` means the initial system pool gets Ultra SSD capabilities. Additional pools must also explicitly include the `--enable-ultra-ssd` flag at creation time to enable it. Validation runs at cluster/pool validation and rejects the request if the user did not specify zones, or the SKU does not support Ultra SSD in any of the zones, and all the nodes belonging to a pool created with the flag are Ultra SSD capable. Clusters can have any mix of Ultra SSD-enabled and disabled pools, regardless if the cluster was initially created with `--enable-ultra-ssd` or not.

For NAP parity, requesting the well-known label means Karpenter will only consider offerings whose zone has Ultra SSD available for the given SKU, and it will set those nodes to support Ultra SSD. Removing the requirement from the NodePool makes future NodeClaims stop requesting Ultra SSD; existing NodeClaims may be considered drifted through the normal NodePool requirements drift path. AKS does not add any provider-specific taint for Ultra SSD, and NAP does not add one either. The node will carry the normal requirement-derived label so scheduling can distinguish Ultra SSD-capable nodes.

See References section for more information on what AKS does.

## References

- AKS Ultra Disks documentation: https://learn.microsoft.com/en-us/azure/aks/use-ultra-disks
