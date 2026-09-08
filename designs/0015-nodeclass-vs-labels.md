# Labels vs. AKSNodeClass

**Author:** @pablotrivino

**Last updated:** August 11, 2026

**Status:** Guidance

## Overview

Karpenter for Azure has two main places where provider-specific behavior can be exposed to users:

- [Well-known labels](../pkg/apis/v1beta1/labels.go) can be used as scheduling requirements on workloads and NodePools.
- Strongly typed fields on [AKSNodeClass](../pkg/apis/v1beta1/aksnodeclass.go), which describe how Azure capacity should be provisioned.

Both surfaces are useful, but they solve different problems. A label is best when the user is making a scheduling decision: they need to express that a workload can run only on nodes with a particular capability, offering, operating mode, or provider-computed property. A NodeClass field is best when the user is configuring a feature: they are choosing how nodes should be created, often with multiple parameters, validation rules, defaults, or drift behavior.

This document is guidance for new features being added to Karpenter for Azure. It defines the rules feature authors should use when deciding whether a new Azure concept belongs in a label or in `AKSNodeClass`.

## Guidance Goals

- Provide a consistent decision framework for future provider-specific options.
- Keep scheduling constraints expressible through Karpenter requirements when that is the user's real intent.
- Keep complex feature configuration strongly typed, validated, and owned by `AKSNodeClass`.
- Avoid adding labels that are hard to reason about, hard to validate, or only mirror configuration that should be structured API.

## Non-Goals

- Reclassify every existing label or `AKSNodeClass` field.
- Require every Azure feature to be schedulable.
- Replace detailed feature-specific design documents.

## Guidance

### Prefer a well-known label when it is a scheduling decision

Use a well-known label when the primary user question is:

> Which nodes are compatible with this workload?

This usually means the value participates in Karpenter requirements and changes which instance types, zones, offerings, or nodes are eligible for a `NodeClaim`.

Choose a label when most of these are true:

- The concept is an offering property, provider-computed scheduling attribute, or an intrinsic property of the instance type.
- Workloads reasonably need to select or spread by it using `nodeSelector`, node affinity, topology spread constraints, or `NodePool` requirements.
- The value is simple enough to model as a label value without losing important structure.
- Validation is simple and the feature is largely independent of other features. The feature might restrict VM sizes and other features, but those restrictions are straightforward and easy to understand. Complicated validation should instead go in AKSNodeClass, as the Label failure mode (no matching instance types) is harder to understand than AKSNodeClass status error.

For these cases, labels are the right API because they are part of Kubernetes and Karpenter scheduling semantics. They let users express compatibility without creating extra NodeClasses or encoding workload placement in provisioning configuration.

Examples:

- `topology.kubernetes.io/zone`, because zone is a placement constraint that directly changes which offerings can satisfy a workload and pods need to schedule against it/set topology constraints against it.
- `karpenter.sh/capacity-type`, because workloads may explicitly choose on-demand or spot capacity and the value is a simple scheduling dimension.
- `karpenter.azure.com/sku-storage-premium-capable`, because premium storage support is an intrinsic capability of the VM SKU. Exposing it as a label lets workloads and `NodePool` requirements restrict scheduling to instance types that support premium storage.
- Ultra SSD support is a simple yes/no node capability that directly affects workload placement. Although enabling Ultra SSD changes the underlying VM configuration, a Pod using an `UltraSSD_LRS` volume cannot attach that volume on a node where Ultra SSD is disabled. Therefore, this capability needs to be represented as a node scheduling requirement so the Pod is only placed on compatible nodes.

### Prefer `AKSNodeClass` when it is feature configuration

Use `AKSNodeClass` when the primary user question is:

> How should Azure create or configure this node?

This usually means the option is part of the node provisioning contract rather than a workload placement constraint.

Choose `AKSNodeClass` when most of these are true:

- The feature is configuration is complex, including having multiple fields, modes, objects, and other values. For example, a configuration might have nested structure or relationships that do not cleanly fit key-value labels.
- The setting needs strong typing, defaulting, or clear API documentation, especially when it depends on cluster state or other `AKSNodeClass` fields.
- Validation is complex. Feature settings require validating against multiple other features, cluster state, NodeClass fields, etc.
- The value is not useful as a pod-level scheduling constraint.

For these cases, NodeClass is the right API because labels are stringly typed and intentionally limited. A field on `AKSNodeClass` gives us a typed schema, API validation, clearer defaults, and room to evolve the feature without creating many loosely related labels.

Examples:

- Disk encryption configuration, because it involves resource IDs, precedence between cluster defaults and NodeClass overrides, mutability, and drift behavior.
- Networking configuration, because it can include subscriptions, resource groups, subnet names, dataplane choices, and multi-tenancy settings that should not be encoded into string labels.
- Trusted Launch configuration, because it has related settings such as vTPM and Secure Boot that need typed configuration and validation. It is primarily concerned with how Azure creates the VM and does not describe a pod-level scheduling compatibility requirement.
- LocalDNS configuration, because it is complex and includes modes, nested DNS override objects, forwarding policy, cache settings, and validation that depends on the whole configuration.

## Rule of Thumb

If it is a scheduling decision, and it is something that can be on by default or represented as a simple capability, make it a well-known label.

If it is a feature, especially a complex feature with many possible values or related settings, make it an `AKSNodeClass` field because the hard-typed API is better.

## Special Case: Both Labels and NodeClass

Sometimes a feature fits better as `AKSNodeClass` configuration, but workloads still need to make scheduling decisions based on the resulting node property. In those cases, the NodeClass field should remain the source of truth and the label should only expose the derived scheduling value.

This pattern has only a couple of precedents today: `kubernetes.azure.com/os-sku` and `kubernetes.azure.com/fips_enabled`. We generally discourage adding both surfaces because it creates two ways to think about one feature, and should only be used as a last resort when the feature cannot be modeled cleanly as label-only or NodeClass-only.

## Final Note: AKS Precedent

Feature design should generally try to mimic how the same capability works in AKS. AKS behavior is useful precedent because it gives users a familiar model and helps keep provider behavior aligned with the platform. However, it should not be treated as the end-all-be-all; Karpenter may expose a capability differently when scheduling semantics, API ergonomics, or provider constraints justify it.
