# Requirements and Labels

**Author:** @matthchr

**Last updated:** Oct 30, 2025

**Status:** Proposed

## Overview

This document examines Karpenter and AKS managed labels and discusses what is needed to bring what we currently do more
in line with AKS does.

There are 2 main topics:
* Label parity with AKS, setting labels that we don't currently set.
* Support for scheduling simulation on labels we don't currently support, but should to enable easier migrations from cluster autoscaler to Karpenter.

Today, Karpenter has the following categories of labels:

1. Labels Karpenter understands and can schedule (Requirements)
    * Can be set on Pod when not set on NodePool/NodeClaim
    * Can be set on NodePool/NodeClaim
    * May trigger behavior other than just setting a label (inform VM size, zone, etc)
2. Labels written to the nodes but that Karpenter doesn't understand (can't schedule)
    * Can NOT be set on Pod when not set on NodePool/NodeClaim
    * Can be set on NodePool/NodeClaim, but depending on the value set may conflict with the "computed" value internally.
    * Note that labels written by other parts of the system (daemonsets, operators, etc) generally fall into this category.
3. Labels the user owns entirely (probably not in the `kubernetes.io`/`karpenter.azure.com`/`kubernetes.azure.com` domains)
    * Can NOT be set on Pod when not set on NodePool/NodeClaim
    * Can be set on NodePool/NodeClaim

### Well-known requirements

Well-known requirements (those which come automatically from [computeRequirements](https://github.com/Azure/karpenter-provider-azure/blob/e322adfbad3567eb5ff34d776a2c365e060a5009/pkg/providers/instancetype/instancetype.go#L135))
help us schedule workloads to nodes by ensuring that the intersection of node requirements and workload requirements align.

Even though the structure in Karpenter that requirements are transported on is `cloudprovider.InstanceType`, they can and should contain more information
than _just_ information about the instance type itself. For example information about the OS and the instances deployment circumstances (zone, capacity reservation, etc)
also make sense as well-known requirements.

We currently shy away from including things that aren't directly related to the instance type, OS, or how it is provisioned in the set of well-known requirements.
This is because:
* It's marginally easier to reason about possible configurations when you can look at a well-typed object describing the options, which well-known labels does not support well.
  This is especially the case with requirements which may come associated with complex configuration - it's not just `thing: on|off`, it's also `thingConfig: 5 options`.
* Historical reasons (including following what upstream/AWS and other providers do).
* Avoiding having too many default requirements which can cause cpu/memory burden.

**Note**: Well-known requirements that aren't directly about the instance SKU selection and are instead about provisioning details such as OS or zone require special handling in the code to ensure
that provisioning is done considering them. This is often accomplished via [createOfferings](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/instancetype/instancetypes.go#L215).

**TODO: Do we agree on this? Is this a principled enough stance?**

### Goals

* Determine the plan for achieving parity with AKS and CAS on what labels appear on nodes.
* Determine which labels should be supported for scheduling (== requirements) and which should not.

### Non-Goals

* Support every label for scheduling.
* Adding new currently unsupported labels in the karpenter.sh namespace.

## Decisions

There is a large set of labels where the value is determined by the system, or by the cluster on which Karpenter is running. See the below table for a list.

The user should not be in control of these labels because the correct values for these labels are determined by the system and should not be set by the user.

### Decision 1: What to do with system-determined global static (single-value) labels?

Labels for which there is one value and that value is (at least for now) static. Examples: `topology.kubernetes.io/region`, `kubernetes.azure.com/cluster`

#### Option A: Block
Block setting these labels on the NodePool/NodeClaim.

#### Option B: Requirements
Allow setting these labels on the NodePool/NodeClaim, but specify them in requirements, which will effectively require that the user either doesn't request the label on their workload or NodePool/NodeClaim, or if they do
that it matches the static value.

#### Conclusion: Neither option is appropriate for all labels. We need a combination.
I propose the following guidance for choosing which labels fall into each category:
* Choose **Option A (Block)** if it is impossible for the label to ever change or vary and/or it doesn't make any sense to attempt to schedule on it.
  For example `kubernetes.azure.com/cluster` seems unlikely to ever change over time. The cluster resource group will be the resource group always.
* Choose **Option B (Requirements)** if the label is static now but may theoretically be expanded/relaxed to be non-static/multi-valued in the future.
  For example `topology.kubernetes.io/region` - today we do not support cross-region node provisioning but we theoretically could.

**TODO**: Not sure the above is that well-defined

### Decision 2: What to do with system-determined cluster-wide labels?
Labels for which there is one correct value for the whole cluster, but that value may change over time as the cluster topology changes (usually driven by changes to the AKS ManagedCluster data model).
Examples: `kubernetes.azure.com/ebpf-dataplane`, `kubernetes.azure.com/network-name`

**TODO**: Fill this in... answer for ebpf-dataplane may need to be special...?

### Decision 3: What to do with dynamic system-determined labels?
Labels that are set at either the per-node or (more likely) per-AgentPool. Examples: `kubernetes.azure.com/os-sku`, `kubernetes.azure.com/os-sku-requested`, `kubernetes.azure.com/kata-vm-isolation`, `kubernetes.azure.com/security-type`.

#### Option A: Block + NodeClass
Block setting these labels on the NodePool/NodeClaim. If they are related to a feature (many are) like `kubernetes.azure.com/kata-vm-isolation`, they can be enabled through strongly typed fields on the `AKSNodeClass` instead.

#### Option B: Requirements
Allow setting these labels on the NodePool/NodeClaim, but specify them in requirements. The main advantage of this is that it allows a single NodePool to allocate nodes with the feature enabled or disabled.
Note that this only works well if the feature is relatively simple and can be controlled through a single simple flag corresponding to a label. If there's more complex configuration required (parameter tuning, enabling various options)
it may still be more appropriate to control via `AKSNodeClass` instead of requirements.

#### Conclusion: Neither option is appropriate for all labels. We need a combination.
* Choose **Option A (Block + NodeClass)** if the label has nothing to do with the VM instance, its OS, or provisioning. There is some leeway here for things that impact those indirectly,
  but anything with complex configuration should be done via the `AKSNodeClass` instead.
  For example `kubernetes.azure.com/kata-vm-isolation` and `kubernetes.azure.com/artifactstreaming-enabled` may have additional configuration and make more sense as a NodeClass feature.
* Choose **Option B (Requirements)** if the label related to node instance selection or provisioning, or (in some cases) if the feature needs to be able to be specified per-workload rather than per-NodePool.
  For example `kubernetes.azure.com/os-sku`, `kubernetes.azure.com/os-sku-requested`, and `kubernetes.azure.com/security-type` all are directly related to VM size selection (security-type is about confidential VMs which are basically specific VM sizes)
  and so would probably make sense as requirements. Note that even though `kubernetes.azure.com/os-sku` is about `Ubuntu` vs `AzureLinux` which is specified in the NodeClass, it still fits into requirements as well because it's about the OS which is
  very commonly required for scheduling.

### Decision 4: Which labels will we block, and which labels will we allow scheduling on?

Here's a (probably not exhaustive) list of labels that AKS writes.
The ones that I think Karpenter should allow scheduling on (but doesn't currently) are **in bold**.

| Label                                                   | AKS (documented) | AKS (observed or code) | Karpenter (schedulable) | Karpenter (written to node) | Notes                                                                                   |
| ------------------------------------------------------- | ---------------- | ---------------------- | ----------------------- | --------------------------- | --------------------------------------------------------------------------------------- |
| beta.kubernetes.io/arch                                 | ❌               | ✅                     | ✅                      | ✅                          | Handled in Karpenter core via NormalizedLabels                                          |
| beta.kubernetes.io/instance-type                        | ❌               | ✅                     | ✅                      | ✅                          | Handled in Karpenter core via NormalizedLabels                                          |
| beta.kubernetes.io/os                                   | ❌               | ✅                     | ✅                      | ✅                          | Handled in Karpenter core via NormalizedLabels                                          |
| failure-domain.beta.kubernetes.io/region                | ❌               | ✅                     | ✅                      | ✅                          | Handled in Karpenter core via NormalizedLabels                                          |
| failure-domain.beta.kubernetes.io/zone                  | ❌               | ✅                     | ✅                      | ✅                          | Handled in Karpenter core via NormalizedLabels                                          |
| kubernetes.azure.com/agentpool                          | ✅               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.io/arch                                      | ✅               | ✅                     | ✅                      | ✅                          |                                                                                         |
| kubernetes.io/os                                        | ✅               | ✅                     | ✅                      | ✅                          |                                                                                         |
| node.kubernetes.io/instance-type                        | ✅               | ✅                     | ✅                      | ✅                          |                                                                                         |
| topology.kubernetes.io/region                           | ✅               | ✅                     | ✅                      | ✅                          |                                                                                         |
| topology.kubernetes.io/zone                             | ✅               | ✅                     | ✅                      | ✅                          |                                                                                         |
| kubernetes.azure.com/cluster                            | ✅               | ✅                     | ❌                      | ✅                          | TODO: Today user can overwrite what AKS sets, we need to block                          |
| kubernetes.azure.com/managedby                          | ✅               |                        | ❌                      | ❌                          | Not written to nodes I don't think, deployments/daemonsets, etc instead                 |
| **kubernetes.azure.com/mode**                           | ✅               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/role                               | ✅               | ✅                     | ❌                      | ✅                          |                                                                                         |
| **kubernetes.azure.com/scalesetpriority**               | ✅               | ✅                     | ❌                      | ❌                          | Only written on spot nodes                                                              |
| kubernetes.io/hostname                                  | ✅               | ✅                     | ❌                      | ✅                          |                                                                                         |
| storageprofile                                          | ❌               | ✅                     | ❌                      | ❌                          | I believe this is semi-deprecated, although AgentBaker still writes it                  |
| storagetier                                             | ❌               | ✅                     | ❌                      | ❌                          | I believe this is semi-deprecated, although AgentBaker still writes it                  |
| kubernetes.azure.com/storageprofile                     | ✅               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/storagetier                        | ✅               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/instance-sku                       | ✅               | ❌                     | ❌                      | ❌                          | Can find no reference to it in code                                                     |
| **kubernetes.azure.com/node-image-version**             | ✅               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/subnet                             | ✅               | ❌                     | ❌                      | ❌                          | Can find no reference to it in code                                                     |
| kubernetes.azure.com/vnet                               | ✅               | ❌                     | ❌                      | ❌                          | Can find no reference to it in code                                                     |
| kubernetes.azure.com/ppg                                | ✅               | ❌                     | ❌                      | ❌                          | Can find no reference to it in code                                                     |
| kubernetes.azure.com/encrypted-set                      | ✅               | ❌                     | ❌                      | ❌                          | Can find no reference to it in code (maybe in AgentBaker?)                              |
| accelerator                                             | ❌               | ✅                     | ❌                      | ❌                          | Soon to be deprecated                                                                   |
| **kubernetes.azure.com/accelerator**                    | ✅               | ✅                     | ❌                      | ❌                          | Removed by #837, due to redundant w/ sku-gpu-name                                       |
| **kubernetes.azure.com/fips_enabled**                   | ✅               | ✅                     | ❌                      | ❌                          | Users specifically asking for this for scheduling even though it's via AKSNodeClass too |
| **kubernetes.azure.com/os-sku**                         | ✅               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/sku-cpu                            | ✅               | ✅                     | ✅                      | ✅                          |                                                                                         |
| kubernetes.azure.com/sku-memory                         | ✅               | ✅                     | ✅                      | ✅                          |                                                                                         |
| kubernetes.azure.com/network-policy                     | ❌               | ✅                     | ❌                      | ❌                          | none or calico or azure, we have this data today but don't write a label for it         |
| kubernetes.azure.com/azure-cni-overlay                  | ❌               | ✅                     | ❌                      | ✅                          |                                                                                         |
| kubernetes.azure.com/consolidated-additional-properties | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| **kubernetes.azure.com/ebpf-dataplane**                 | ❌               | ✅                     | ❌                      | ✅                          | Unclear???                                                                              |
| kubernetes.azure.com/kubelet-identity-client-id         | ❌               | ✅                     | ❌                      | ✅                          |                                                                                         |
| kubernetes.azure.com/kubelet-serving-ca                 | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/localdns-state                     | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/network-name                       | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/network-resourcegroup              | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/network-stateless-cni              | ❌               | ✅                     | ❌                      | ✅                          |                                                                                         |
| kubernetes.azure.com/network-subnet                     | ❌               | ✅                     | ❌                      | ✅                          |                                                                                         |
| kubernetes.azure.com/network-subscription               | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/nodenetwork-vnetguid               | ❌               | ✅                     | ❌                      | ✅                          |                                                                                         |
| kubernetes.azure.com/podnetwork-subscription            | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/podnetwork-resourcegroup           | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/podnetwork-name                    | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/podnetwork-subnet                  | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/podnetwork-delegationguid          | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/podnetwork-type                    | ❌               | ✅                     | ❌                      | ✅                          |                                                                                         |
| kubernetes.azure.com/podv6network-type                  | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/podnetwork-multi-tenancy-enabled   | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/podnetwork-swiftv2-enabled         | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/nodepool-type                      | ❌               | ✅                     | ❌                      | ❌                          | Should really be documented                                                             |
| **kubernetes.azure.com/os-sku-effective**               | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| **kubernetes.azure.com/os-sku-requested**               | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| **kubernetes.azure.com/security-type**                  | ❌               | ✅                     | ❌                      | ❌                          | ConfidentialVM                                                                          |
| kubernetes.azure.com/kata-mshv-vm-isolation             | ❌               | ✅                     | ❌                      | ❌                          | Being deprecated in favor of below                                                      |
| kubernetes.azure.com/kata-vm-isolation                  | ❌               | ✅                     | ❌                      | ❌                          |                                                                                         |
| kubernetes.azure.com/artifactstreaming-enabled          | ❌               | ✅                     | ❌                      | ❌                          | true                                                                                    |
| kubernetes.azure.com/hobovm                             | ❌               | ✅                     | ❌                      | ❌                          | true                                                                                    |


For completeness, there are also the following karpenter labels. These labels we already support scheduling on and set, so there is no change for them going forward.

| Label                                               | AKS (documented) | AKS (observed or code) | Karpenter (schedulable) | Karpenter (written to node) | Notes |
| --------------------------------------------------- | ---------------- | ---------------------- | ----------------------- | --------------------------- | ----- |
| karpenter.sh/capacity-type                          |                  |                        | ✅                      | ✅                          |       |
| karpenter.azure.com/sku-cpu                         |                  |                        | ✅                      | ✅                          |       |
| karpenter.azure.com/sku-memory                      |                  |                        | ✅                      | ✅                          |       |
| karpenter.azure.com/sku-gpu-name                    |                  |                        | ✅                      | ✅                          |       |
| karpenter.azure.com/sku-gpu-manufacturer            |                  |                        | ✅                      | ✅                          |       |
| karpenter.azure.com/sku-gpu-count                   |                  |                        | ✅                      | ✅                          |       |
| karpenter.azure.com/sku-name                        |                  |                        | ✅                      | ✅                          |       |
| karpenter.azure.com/sku-family                      |                  |                        | ✅                      | ✅                          |       |
| karpenter.azure.com/sku-version                     |                  |                        | ✅                      | ✅                          |       |
| karpenter.azure.com/sku-storage-ephemeralos-maxsize |                  |                        | ✅                      | ✅                          |       |
| karpenter.azure.com/sku-storage-premium-capable     |                  |                        | ✅                      | ✅                          |       |
| karpenter.azure.com/sku-networking-accelerated      |                  |                        | ✅                      | ✅                          |       |
| karpenter.azure.com/sku-hyperv-generation           |                  |                        | ✅                      | ✅                          |       |

#### Conclusion

We will add these labels to WellKnownLabels + requirements:
* kubernetes.azure.com/scalesetpriority
* kubernetes.azure.com/node-image-version
* kubernetes.azure.com/fips_enabled
* kubernetes.azure.com/os-sku
* kubernetes.azure.com/mode

We will consider adding these labels to WellKnownLabels + requirements in the future:

* kubernetes.azure.com/ebpf-dataplane
* kubernetes.azure.com/os-sku-effective
* kubernetes.azure.com/os-sku-requested
* kubernetes.azure.com/security-type
* kubernetes.azure.com/accelerator

We will allow these labels to be set on the NodePool/NodeClaim (in addition to the labels we already support as schedulable outlined in the tables above):
* kubernetes.azure.com/ebpf-dataplane
* kubernetes.azure.com/cluster-health-monitor-checker-synthetic (due to its high usage...) **TODO:** Learn what this is

Based on usage data, the following `kubernetes.azure.com` labels have some usage today in `NodePool` labels or requirements.

Most usage

* kubernetes.azure.com/ebpf-dataplane
* kubernetes.azure.com/mode
* kubernetes.azure.com/cluster-health-monitor-checker-synthetic
* kubernetes.azure.com/scalesetpriority

Less usage
* kubernetes.azure.com/agentpool
* kubernetes.azure.com/accelerator
* kubernetes.azure.com/agentpool-family
* kubernetes.azure.com/os-sku
* kubernetes.azure.com/aksnodeclass
* kubernetes.azure.com/storageprofile
* kubernetes.azure.com/storagetier

We already block usage of the `karpenter.azure.com` and `karpenter.sh` namespaces except for certain labels we understand. This will not change.

We will also block all `kubernetes.azure.com` namespaced labels except for those called out above as either well known labels, or allowed.

### Decision 5: How will we get labels that Karpenter doesn't currently write set on nodes?

#### Option A: Wait for Machine API migration

Migrating to the machine API will automatically get all missing labels set on VMs allocated via the machine API.

#### Option B: Add some/all labels to our current label writing mechanism

There are some labels that Karpenter sets but that aren't schedulable, defined at [AddAgentBakerGeneratedLabels](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/imagefamily/labels/labels.go#L23).
This has already been accounted for in the tables above. We could add more labels there.

#### Conclusion: Wait for machine API for complete parity

Machine API is not that far away, and we already write most critical labels. Some additional labels such as `kubernetes.azure.com/os-sku` and `kubernetes.azure.com/fips_enabled` will be enabled for scheduling
(and thus be written to the nodes) based on decision 4, but other labels and full label parity will come with the machine API migration.

## FAQ

### How do the labels get onto the Node?

* Karpenter core writes all NodeClaim labels to the node, see: [syncNode](https://github.com/kubernetes-sigs/karpenter/blob/38e728c99b530f660f685d53d01f2e9ec9696668/pkg/controllers/nodeclaim/lifecycle/registration.go#L128).
* Karpenter core translates most requirements from the NodeClaim spec into labels, see [PopulateNodeClaimDetails](https://github.com/kubernetes-sigs/karpenter/blob/38e728c99b530f660f685d53d01f2e9ec9696668/pkg/controllers/nodeclaim/lifecycle/launch.go#L129)
    * Note that `.Labels()` does exclude multi-valued requirements and labels defined in `WellKnownLabels` (registered by the cloud provider). See [Labels()](https://github.com/kubernetes-sigs/karpenter/blob/38e728c99b530f660f685d53d01f2e9ec9696668/pkg/scheduling/requirements.go#L270).
* Labels that get skipped by core (either due to `WellKnownLabels` or having multiple values) are added to the NodeClaim by AKS Karpenter provider in [vmInstanceToNodeClaim](https://github.com/Azure/karpenter-provider-azure/blob/b9c8c82edb289ac5b281c85b0851b5a0c45bc4bb/pkg/cloudprovider/cloudprovider.go#L476).
  * Note that `WellKnownLabels` seems to serve two purposes:
      1. It prevents Karpenter core from automatically including those labels on the node object (though note that AKS Karpenter provider ends up including most of them on the node object anyway).
      2. It allows pods to ask for that label when the NodeClaim/requirements don't actually have it, and still have scheduling take place (I don't fully understand where/why this is needed but I can see the code for it [here](https://github.com/kubernetes-sigs/karpenter/blob/main/pkg/scheduling/requirements.go#L175))
* Requirements come from InstanceType via [computeRequirements](https://github.com/Azure/karpenter-provider-azure/blob/1f327c6d7ac62b3a3a1ad83c0f14f95001ab4ae8/pkg/providers/instancetype/instancetype.go#L135)

### What's the difference between NodePool/NodeClaim spec.template.metadata.labels and spec.template.requirements?

There isn't really a difference. NodePool labels _become_ requirements, see [pkg/controllers/provisioning/scheduling/nodeclaimtemplate.go](https://github.com/kubernetes-sigs/karpenter/blob/main/pkg/controllers/provisioning/scheduling/nodeclaimtemplate.go#L75).

### What happens if user sets a non-schedulable (== non-requirements) label affinity on their pod?

WellKnownLabels (like: `kubernetes.azure.com/cluster`): Infinite nodes if the label doesn't match the expected value - otherwise one node.
Non-WellKnownLabels that we don't write (like: `kubernetes.azure.com/network-name`): Results in `"incompatible requirements, label \"kubernetes.azure.com/network-name\" does not have known values`
Non-WellKnownLabels that we do write (like `kubernetes.azure.com/ebpf-dataplane`): Results in `incompatible requirements, label \"kubernetes.azure.com/kubelet-identity-clientid\" does not have known values"` - because the simulation engine in Karpenter doesn't know we're going to write it.

### Where can I see more about how AKS manages labels?

https://github.com/Azure/karpenter-poc/issues/1517#issuecomment-3470039930 discusses this further.
