# Requirements and Labels

**Author:** @matthchr

**Last updated:** Nov 07, 2025

**Status:** Completed

## Overview
This document examines Karpenter and AKS managed labels and discusses what is needed to bring what we currently do more in line with what AKS does.

There are 2 main topics:
* Label parity with AKS, setting labels that we don't currently set.
* Support for scheduling simulation on labels we don't currently support, but should; to enable easier migrations from cluster autoscaler to Karpenter.

Today, Karpenter has the following categories of labels:

1. Labels Karpenter understands and can target for scheduling (Requirements)
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

We currently shy away from including things that aren't directly related to the instance type, OS, or how it is provisioned in the set of well-known requirements. For example
we do not currently support `kubernetes.azure.com/podnetwork-subscription`. That's partially because there hasn't been a lot of asks for it, but also partially because it's
not really a "property of the instance".

The reason for this distinction is:
* It's marginally easier to reason about possible configurations when you can look at a well-typed object describing the options, which well-known labels does not support well.
  This is especially the case with requirements which may come associated with complex configuration - it's not just `thing: on|off`, it's also `thingConfig: 5 options`.
* Historical reasons (including following what upstream/AWS and other providers do).
* Avoiding having too many default requirements which can cause cpu/memory burden.

**Note**: Well-known requirements that aren't directly about the instance type selection and are instead about provisioning details such as OS or zone require special handling in the code to ensure
that provisioning is done considering them. This is often accomplished via [createOfferings](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/instancetype/instancetypes.go#L215).

### Goals
* Determine the plan for achieving parity with AKS and CAS on what labels appear on nodes.
* Determine which labels should be supported for scheduling (== requirements) and which should not.

### Non-Goals
* Support every label for scheduling.
* Adding new currently unsupported labels in the karpenter.sh namespace.

## Decisions
There is a large set of labels where the value is determined by the system, or by the cluster on which Karpenter is running. See the below table for a list.

The user should not be in control of these labels because the correct values for these labels are determined by the system and should not be set by the user.

### Decision 1: What to do with system-managed labels?
This covers:
* Global static (single-value) labels, like `topology.kubernetes.io/region` and `kubernetes.azure.com/cluster`.
* Cluster-wide labels, like `kubernetes.azure.com/ebpf-dataplane` and `kubernetes.azure.com/network-name`.
  * These labels are tricky to deal with. Unlike static labels, for these labels Karpenter needs to be informed of their values, probably via operator-scoped configuration variables. Some of this is already
    sent to Karpenter today but not all of it. As we work on moving towards the Machine API, we will relax the need for Karpenter to know all of these values assuming we're OK with not supporting these
    labels as requirements. If we want to support these labels as well-known requirements nothing changes Karpenter still needs to know their values so that it can build the set of requirements.
    If the values backing these labels change, _ideally_ we would detect drift and recreate nodes. In practice we don't always have the data today to do what we need there.
* Dynamic (per-feature or per-AgentPool) labels, like `kubernetes.azure.com/os-sku`, `kubernetes.azure.com/os-sku-requested`, `kubernetes.azure.com/kata-vm-isolation`, and `kubernetes.azure.com/security-type`

Labels for which there is one correct value for the whole cluster, but that value may change over time as the cluster topology changes (usually driven by changes to the AKS ManagedCluster data model).
Examples: `kubernetes.azure.com/ebpf-dataplane`, `kubernetes.azure.com/network-name`

#### Option A: Block (and add to NodeClass where appropriate)
Block setting these labels on the NodePool/NodeClaim.

For labels that are related with a specific feature, like `kubernetes.azure.com/kata-vm-isolation`, they can be enabled through strongly typed fields on the `AKSNodeClass` instead and scheduling against these
labels can be approximated by scheduling against the NodePool.

#### Option B: Well-known Requirements
Allow setting these labels on the NodePool/NodeClaim, but specify them in the well-known requirements, which will effectively require that the user either doesn't request the label on their workload
or NodePool/NodeClaim, or if they do that it matches the static value.

For labels that can change (cluster-wide or per-feature) it would be strongly recommended to avoid hardcoding it on the NodePool and instead just request it on the workloads that need it.

For labels that are related to features, the main advantage of this approach is that it allows a single NodePool to allocate nodes with the feature enabled or disabled.
Note that this only works well if the feature is relatively simple and can be controlled through a single simple flag corresponding to a label. If there's more complex configuration required (parameter tuning, enabling various options)
it is more appropriate to control via `AKSNodeClass` instead of requirements.

#### Conclusion: Neither option is appropriate for all labels. We need a combination.
I propose the following guidance for choosing which labels fall into each category:
* Choose **Option A (Block)** if...
  * The label doesn't make sense to schedule against. For example `kubernetes.azure.com/podnetwork-resourcegroup` and `kubernetes.azure.com/cluster`.
  * The label is a feature-label and we're adding it to AKSNodeClass instead. For example `kubernetes.azure.com/kata-vm-isolation` and `kubernetes.azure.com/artifactstreaming-enabled`.
* Choose **Option B (Well-known Requirements)** if...
  * The label is required for scheduling (even if it is static now but may be expanded to be non-static or multi-valued in the future).
    For example `topology.kubernetes.io/region` - we could support cross-region scheduling in the future. Other labels like `kubernetes.azure.com/ebpf-dataplane` are required for scheduling certain daemonsets.
    Other labels like `kubernetes.azure.com/os-sku`, `kubernetes.azure.com/os-sku-requested`, and `kubernetes.azure.com/security-type` are all related to VM size or OS selection and likely to be commonly used
    to schedule against.

**Note** AWS has moved to a model where they are very cautious about adding new requirements because if users _only_ use those properties to schedule against and don't include a constraint on
`karpenter.azure.com/sku-name` or `karpenter.azure.com/sku-family` then users can be surprised by new VM sizes and end up broken if their workloads don't work on those sizes, so when evaluating if
something can be added as a requirement we should keep that in mind.

### Decision 2: Which labels will we block, and which labels will we allow scheduling on?
Here's a (probably not exhaustive) list of labels that AKS writes.
The ones that I think Karpenter should allow scheduling on (but doesn't currently) are **in bold**.

**Note**: AKS (observed or code) means we see the label written on nodes im practice by AKS. This doesn't necessarily mean that the AKS service writes every single one of these labels. Some of them are written by other components such
as directly CloudProvider [node controller](https://github.com/kubernetes/kubernetes/blob/d777de7741d36d1cc465162d94f39200e299070b/staging/src/k8s.io/cloud-provider/controllers/node/node_controller.go#L490). There may also be other
label writers as well.

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
| kubernetes.azure.com/cluster                            | ✅               | ✅                     | ❌                      | ✅                          |                                                                                         |
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
We will add these labels to WellKnownLabels + requirements now/soon:
* kubernetes.azure.com/scalesetpriority
* kubernetes.azure.com/node-image-version - if this turns out to be difficult to do correctly, will move down into future as opposed to doing it now.
* kubernetes.azure.com/fips_enabled
* kubernetes.azure.com/os-sku
* kubernetes.azure.com/mode

We will consider adding these labels to WellKnownLabels + requirements in the future:

* kubernetes.azure.com/ebpf-dataplane - we may need to add this to requirements in the future to support network-dataplane updates. In the migration from azure -> cilium,
  at least with how we manage NAP default pools now, we can't update the CRD instance once it has been created so we don't have a good way to dynamically add this to the NodePool.
  Even if we did, it would solve the migration problem for the managed NodePools but not for any user-created NodePools - the user would need to go update those pools manually themselves.
* kubernetes.azure.com/os-sku-effective
* kubernetes.azure.com/os-sku-requested
* kubernetes.azure.com/security-type
* kubernetes.azure.com/accelerator

We will allow these labels to be set on the NodePool/NodeClaim (in addition to the labels we already support as schedulable outlined in the tables above):
* kubernetes.azure.com/ebpf-dataplane - for legacy reasons
* kubernetes.azure.com/cluster-health-monitor-checker-synthetic (due to its high usage...) - This is set by AKS Automatic

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

### Decision 3: How will we get labels that Karpenter doesn't currently write set on nodes?

#### Option A: Wait for Machine API migration
Migrating to the machine API will automatically get all missing labels set on VMs allocated via the machine API.

#### Option B: Add some/all labels to our current label writing mechanism
There are some labels that Karpenter sets but that aren't schedulable, defined at [AddAgentBakerGeneratedLabels](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/imagefamily/labels/labels.go#L23).
This has already been accounted for in the tables above. We could add more labels there.

#### Conclusion: Option 3.A Wait for machine API for complete parity
Machine API is not that far away, and we already write most critical labels. Some additional labels such as `kubernetes.azure.com/os-sku` and `kubernetes.azure.com/fips_enabled` will be enabled for scheduling
(and thus be written to the nodes) based on decision 4, but other labels and full label parity will come with the machine API migration.

## FAQ

### How do Karpenter labels get onto the Node?
There are currently two paths for Karpenter labels to get onto the node:
* Written by Karpenter core onto the node object.
* Written by Kubelet onto the node object (configured by the Azure Karpenter provider)

#### Codepath for Karpenter-core label writes
**Note:** This is in reverse order (bottom-up), with the calls closest to the node/nodeclaim.

* Karpenter core writes all NodeClaim labels to the node, see: [syncNode](https://github.com/kubernetes-sigs/karpenter/blob/38e728c99b530f660f685d53d01f2e9ec9696668/pkg/controllers/nodeclaim/lifecycle/registration.go#L128).
* Karpenter core translates most requirements from the NodeClaim spec into labels, see
  [PopulateNodeClaimDetails](https://github.com/kubernetes-sigs/karpenter/blob/38e728c99b530f660f685d53d01f2e9ec9696668/pkg/controllers/nodeclaim/lifecycle/launch.go#L129)
  * Note that `.Labels()` excludes labels defined in `WellKnownLabels`, including both those registered by the cloud provider and core. It also excludes labels defined in `RestrictedLabelDomains`, which are
    labels that are prohibited by Kubelet or reserved by Karpenter, except for certain sub-domains within the restricted domains defined by `LabelDomainExceptions`.
    See [Labels()](https://github.com/kubernetes-sigs/karpenter/blob/38e728c99b530f660f685d53d01f2e9ec9696668/pkg/scheduling/requirements.go#L270).
  * Note that `WellKnownLabels` seems to serve two purposes:
      1. It prevents Karpenter core from automatically including those labels on the node object (though note that AKS Karpenter provider ends up including most of them on the node object anyway).
      2. It allows pods to ask for that label when the NodeClaim/requirements don't actually have it, and still have scheduling take place (I don't fully understand where/why this is needed but I can see the
         code for it [here](https://github.com/kubernetes-sigs/karpenter/blob/main/pkg/scheduling/requirements.go#L175))
* Cloud provider defined labels (as used in `PopulateNodeClaimDetails` mentioned above) are added to the NodeClaim by AKS Karpenter provider in
  [vmInstanceToNodeClaim](https://github.com/Azure/karpenter-provider-azure/blob/b9c8c82edb289ac5b281c85b0851b5a0c45bc4bb/pkg/cloudprovider/cloudprovider.go#L476) by reading
  the instance type requirements.
* InstanceType Requirements are determined via [computeRequirements](https://github.com/Azure/karpenter-provider-azure/blob/1f327c6d7ac62b3a3a1ad83c0f14f95001ab4ae8/pkg/providers/instancetype/instancetype.go#L135)

#### Codepath for Karpenter Azure provider labels via Kubelet
**Note:** This is in reverse order (bottom-up), with the calls closest to the node/nodeclaim.

**Note:** There is a similar codepath for other bootstrapping modes.

* Karpenter Azure provider writes the labels to CustomNodeLabels Kubelet configuration, see
  [ConstructProvisionValues](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/imagefamily/customscriptsbootstrap/provisionclientbootstrap.go#L125).
* These labels come from the OS labels collection, see [CustomScriptsNodeBootstrapping](https://github.com/Azure/karpenter-provider-azure/blob/fe7114917ce1877f13fdda847c6dfb6bde19fa22/pkg/providers/imagefamily/azlinux.go#L174).
* That flows through [staticParameters.Labels](https://github.com/Azure/karpenter-provider-azure/blob/fe7114917ce1877f13fdda847c6dfb6bde19fa22/pkg/providers/imagefamily/resolver.go#L162) from
  [LaunchTemplate](https://github.com/Azure/karpenter-provider-azure/blob/fe7114917ce1877f13fdda847c6dfb6bde19fa22/pkg/providers/launchtemplate/launchtemplate.go#L171), which come from the
  NodeClaim in [GetTemplate](https://github.com/Azure/karpenter-provider-azure/blob/fe7114917ce1877f13fdda847c6dfb6bde19fa22/pkg/providers/launchtemplate/launchtemplate.go#L106).
* The `additionalLabels` parameter is supplied by [getLaunchTemplate](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/instance/vminstance.go#L888) by
  calling [GetAllSingleValuedRequirementLabels](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/instance/offerings/offerings.go#L110).


### What's the difference between NodePool/NodeClaim spec.template.metadata.labels and spec.template.requirements?
There isn't really a difference. NodePool labels _become_ requirements, see [pkg/controllers/provisioning/scheduling/nodeclaimtemplate.go](https://github.com/kubernetes-sigs/karpenter/blob/main/pkg/controllers/provisioning/scheduling/nodeclaimtemplate.go#L75).

### What happens if user sets a non-schedulable (== non-requirements) label affinity on their pod?
* WellKnownLabels (like: `kubernetes.azure.com/cluster`): Infinite nodes if the label doesn't match the expected value - otherwise one node.
* Non-WellKnownLabels that we don't write (like: `kubernetes.azure.com/network-name`): Results in `"incompatible requirements, label \"kubernetes.azure.com/network-name\" does not have known values`
* Non-WellKnownLabels that we do write (like `kubernetes.azure.com/ebpf-dataplane`): Results in `incompatible requirements, label \"kubernetes.azure.com/kubelet-identity-clientid\" does not have known values"`.
  This is because the simulation engine in Karpenter doesn't know we're going to write it.
