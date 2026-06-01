# Ultra SSD Support for NAP

**Author:** @pablotrivino

**Last updated:** June 1, 2026

**Status:** Proposed

## Overview

AKS supports Azure Ultra Disks by enabling Ultra SSD on the cluster or on a node pool at creation time with `--enable-ultra-ssd`. Nodes created from that cluster or node pool can then attach Persistent Volumes backed by the `UltraSSD_LRS` storage class.

Today in AKS, `--enable-ultra-ssd` ultimately enables `AdditionalCapabilities.UltraSSDEnabled = true` on the underlying VM or VMSS model. That does not automatically add labels, taints, or tolerations for scheduling. It only makes the node capable of attaching Ultra SSDs for workloads that use an UltraSSD-backed PV. Placement policy remains the user's responsibility.

For Node Auto Provisioning (NAP), we need the equivalent behavior on dynamically created capacity. This means Karpenter must be able to:

- express Ultra SSD as part of node configuration,
- filter out VM sizes and zonal offerings that do not support Ultra SSD,
- set the correct downstream API fields when creating capacity

This document proposes how to complete that work for NAP.

### Goals

- Add support for enabling Ultra SSD on dynamically provisioned nodes.
- Support both VM provisioning mode and AKS Machine API mode.
- Filter instance types and offerings to only Ultra SSD-capable SKU plus zone combinations when the feature is enabled.

### Non-Goals

- Adding provider-managed scheduling controls beyond offerings filtering, such as automatic Requirements, labels, taints, or tolerations.
- Automatically steering Ultra SSD workloads onto Ultra SSD-capable nodes.

## Decisions

### Decision 1: Where should Ultra SSD be configured?

#### Add a strongly typed field to `AKSNodeClass`

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

This matches the existing API style for feature toggles such as `artifactStreaming`, `security.encryptionAtHost`, and `localDNS`.
Ultra SSD should be configured as a strongly typed `AKSNodeClass` feature, not as a raw requirement.

Reasons:

- it is a provisioning feature, not just a schedulable label,
- it aligns with the current `AKSNodeClass` design pattern

The user expectation of “default false” is still satisfied. If `spec.ultraSSD` or `spec.ultraSSD.enabled` is omitted, the effective value is disabled.

### Decision 2: How should we filter for compatible Instances?

#### Offerings Filtering

Ultra SSD is only available in regions and zones that support it, and only by specific SKUs. Therefore, we need to check availability for each zone when creating Offerings for InstanceTypes.

#### Decision 3: Should the provider add labels, requirements, taints, or tolerations?

#### No provider-managed scheduling projection

We will not add Ultra SSD-specific Requirements, Labels, Taints, or Tolerations from the provider.

Rationale:

- this matches current AKS behavior, where `--enable-ultra-ssd` enables attachment capability but does not impose placement policy,
- the primary job of this feature is to make the node capable of attaching Ultra SSDs, not to decide which workloads should land on it,
- users who want explicit scheduling separation can model that themselves in the `NodePool` using labels, taints, tolerations, or affinity.

#### Conclusion

The implementation should follow the established provider pattern:

1. strongly typed `AKSNodeClass` feature,
2. helper accessor like `IsUltraSSDEnabled()`,
3. instance type and offering filtering,
4. downstream API wiring in both provisioning modes.

## Proposed Implementation

### API changes

Add a new field to `AKSNodeClass`:

```yaml
spec:
	ultraSSD:
		enabled: true
```

Semantics:

- default is disabled when omitted,
- enabling it opts the node class into Ultra SSD-capable capacity only,
- changing it triggers node replacement through drift.

### Filtering

Filter out InstanceTypes that don't support UltraSSD when it is enabled.

- UltraSSD is also region and zone dependent, so we need to filter out at Offering level
- Add a check during createOfferings to verify that the zone + SKU support UltraSSD

### Scheduling behavior

The provider will not add Ultra SSD-specific Requirements, Labels, Taints, or Tolerations.

If users want workloads that use UltraSSD-backed PVs to land only on Ultra SSD-capable nodes, they must model that in their own `NodePool` and workload configuration.

Examples of user-managed policy include:

- adding labels to the `NodePool` template,
- adding taints to the `NodePool`,
- adding tolerations and affinity to workloads.

### VM mode wiring

Update VM creation so Ultra SSD-enabled node classes set `vm.Properties.AdditionalCapabilities.UltraSSDEnabled = true`.

This mirrors the current AKS behavior behind `--enable-ultra-ssd`: the node is made capable of attaching Ultra SSDs, but scheduling policy is left to the user.

### AKS Machine API wiring

Update AKS machine template creation so Ultra SSD-enabled node classes set `aksMachine.Properties.EnableUltraSSD = true`.

## References

- AKS Ultra Disks documentation: https://learn.microsoft.com/en-us/azure/aks/use-ultra-disks
- Related label and feature-toggle guidance in [designs/0006-requirements-and-labels.md](/Users/pablotrivino/go/src/aks/karpenter-provider-azure/designs/0006-requirements-and-labels.md)
