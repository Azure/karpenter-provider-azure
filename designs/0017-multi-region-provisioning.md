# Multi-Region Provisioning for AKSNodeClass

**Author:** @GuetaHen (drafted with Copilot)

**Last updated:** Nov 19, 2026

**Status:** Proposed

## Overview

An AKSNodeClass provisions nodes only in the region of its AKS cluster, the
*cluster region*. When the cluster region has no capacity for a VM size, for
example a GPU size or Spot capacity, pods stay pending even when other regions
have capacity. Today the options are a cluster per region, or a second
autoscaler beside Karpenter.

This design proposes an optional, experimental `spec.regions` field on
AKSNodeClass. Each entry names an Azure region and a pre-existing node subnet in
that region. One Karpenter controller and one NodePool can then launch nodes in
all listed regions for one cluster. The controller option
`--enable-multi-region` gates the feature and is off by default. An
AKSNodeClass without `spec.regions` behaves exactly as before.

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: multi-region-overflow
spec:
  regions:
    - name: centralus
      vnetSubnetID: /subscriptions/<subscription>/resourceGroups/<group>/providers/Microsoft.Network/virtualNetworks/<vnet>/subnets/<subnet>
    - name: westus3
      vnetSubnetID: /subscriptions/<subscription>/resourceGroups/<group>/providers/Microsoft.Network/virtualNetworks/<vnet>/subnets/<subnet>
```

**Terminology.** In this repository, *regional* means non-zonal (see
[0009](./0009-regional-vm-provisioning.md)). This design says *multi-region*
for an AKSNodeClass with `spec.regions` and for its nodes.

### Goals

* One AKSNodeClass, and so one NodePool, can launch nodes in several physical
  Azure regions.
* An AKSNodeClass without `spec.regions` keeps its API, hash, behavior and Azure
  requests, with the feature on or off.
* Each region keeps its own SKU catalog, prices, quota and capacity-failure
  state, and a failing region does not block the others.
* Retries are idempotent: the region and subnet of a NodeClaim are recorded
  before the first Azure write.
* Unsupported provisioning modes and network setups are refused before any Azure
  write, with a status reason that names the problem.
* The customer keeps ownership of the network of each region.

### Non-Goals

* Creating VNets, subnets, peerings, routes, NAT gateways, firewalls or
  container registries.
* Pod networking across regions for Azure CNI, in Overlay mode or with pod IPs
  from the node subnet.
* The AKS Machine API provisioning modes, until the Machine API can place a
  machine outside the cluster region (see Decision 4).
* Load balancer backend pools for nodes in other regions, and storage topology
  across regions.
* A provider policy that prefers the cluster region; NodePool weights already
  express this (see Decision 6).
* Windows nodes, disk encryption sets, capacity reservation groups, node public
  IPs and node image version pinning together with `spec.regions`.

### Network contract

The customer owns the network of each listed region:

* an existing subnet with its own NSG, whose addresses overlap no other node
  subnet, pod range or Service range;
* outbound connectivity, for example a NAT gateway on each subnet;
* global VNet peering between the cluster VNet and every listed VNet, and
  between every two listed VNets, with NSG rules that allow the node and pod
  ranges of all regions;
* a path to the API server from every region;
* container registry replicas near the nodes, where pull latency or egress cost
  matters.

The provider only reads the network: it checks the location and subscription of
each VNet, that the subnet exists and that the subnet has an NSG. These checks
cannot prove connectivity; the e2e suite does (see Testing).

### Related work

Open pull request
[#2081](https://github.com/Azure/karpenter-provider-azure/pull/2081) moves the
construction of the VNet client into `AZClient`. It also gives the startup
lookup of the VNet GUID the context of its caller. It does not change
provisioning, network validation or placement. Its description calls it a
preparatory cleanup for "a possible future opt-in, region-aware multi-VNet
fallback for user worker nodes when regional capacity is constrained". In that
direction, "the AKS control plane and every system pool remain in their home
region". The pull request asks the maintainers if that direction needs a
separate design discussion.

This design is one possible form of that direction:

* Only NodePools that reference an AKSNodeClass with `spec.regions` place nodes
  in other regions. The control plane and the AKS agent pools, system pools
  included, stay in the cluster region.
* NodePool weights keep the cluster region first (see Decision 6).
* The provider reads the location of each VNet from its Azure metadata, as the
  pull request recommends. It does not use names or the location of a resource
  group.

The two changes do not depend on each other. If #2081 merges first, the VNet
reads of this design can use its shared read-only VNet client instead of a
client of their own.

## Decisions

### Decision 1: Where to declare the regions?

#### Option A: `spec.regions` on AKSNodeClass

A list of `{name, vnetSubnetID}` with 1 to 8 entries, keyed by name and mutually
exclusive with `vnetSubnetID`. NodePool requirements on
`topology.kubernetes.io/region` can still narrow the regions.

#### Option B: A controller option that maps regions to subnets

NodePools select regions with `topology.kubernetes.io/region`, and the
controller gets one subnet per region. Two AKSNodeClasses cannot use different
subnets in one region, and every change needs a controller restart.

#### Option C: One AKSNodeClass and one NodePool per region

No API change. Limits, weights, disruption budgets and consolidation do not span
regions, and the scheduler cannot compare offerings across regions inside one
NodePool.

#### Conclusion: Option A

The subnet already belongs to the AKSNodeClass (`vnetSubnetID`), and a list
keeps one NodePool for all regions. The field is added to `v1beta1` and, for
parity, to the unserved `v1alpha2`.

### Decision 2: How do changes to `spec.regions` drift nodes?

#### Option A: Include `spec.regions` in the NodeClass hash

Every change, such as a new region, drifts every node of the NodeClass.

#### Option B: Exclude `spec.regions` from the hash and drift each NodeClaim by its placement

Before the first Azure write, the provider records the region and subnet of the
NodeClaim in the `karpenter.azure.com/region-placement` annotation. A NodeClaim
drifts only when its own region or subnet is no longer listed.

#### Conclusion: Option B

Adding a region does not replace nodes. When a region is removed, core drift
usually reports `InstanceTypeNotFound` first, because the NodeClass no longer
offers that region. The provider reports `MultiRegionPlacementDrift` for
NodeClaims that core has not checked yet and after a subnet change. Core
replaces drifted nodes within the disruption budgets of their NodePool.

### Decision 3: How to keep the state of each region?

#### Option A: Providers per region behind one catalog

A registry builds the instance type, node image, quota and VM providers of a
listed region on first use. The catalog of the AKSNodeClass merges the offerings
of all its regions, and every offering carries its region and zone.

#### Option B: Derive the offerings of other regions from the cluster region

SKUs, prices, zones and quota differ by region, so these offerings would be
guesses. A price premium to prefer the cluster region would also be artificial.

#### Conclusion: Option A

Each region keeps its own SKU catalog, prices, quota and unavailable offerings.
A region that fails its checks gets no new nodes while the other regions keep
serving, and the `SubnetsReady` condition names it.

### Decision 4: Which provisioning modes?

#### Option A: `aksscriptless` only

The provider creates the VM and its NIC and can place them in any region.

#### Option B: Also the AKS Machine API modes

In the public `armcontainerservice/v9` SDK (v9.7.0-beta.1, API version
2026-07-02-preview), `Machine` has `ID`, `Name`, `Type`, `SystemData`, `Zones`
and `Properties`, and no `Location`, and `MachineProperties` has no identity
field. A machine is created in the region of its agent pool.

#### Conclusion: Option A

`spec.regions` is refused in the `aksmachineapi`, `aksmachineapiheaderbatch`
and `bootstrappingclient` modes with reason
`MultiRegionProvisionModeUnsupported`, before any Azure write. The Machine API
modes can follow when the Machine API accepts and returns a location per
machine, and accepts a subnet and identities in that region.

### Decision 5: Which network plugins?

#### Option A: Bring-your-own CNI only

`networkPlugin` `none` with an empty `networkPluginMode`. The CNI carries pod
traffic between regions, for example by encapsulation between node IPs.

#### Option B: Azure CNI Overlay

Overlay routes pod ranges only inside the cluster VNet. Carrying them across
peerings needs changes to AKS-managed components, such as the Cilium
configuration, which
[AKS does not support](https://learn.microsoft.com/azure/aks/azure-cni-powered-by-cilium).

#### Option C: Azure CNI with pod IPs from the node subnet

Pod IPs are routable across peerings, but the AKS-managed IP address management
and masquerade rules are not verified for nodes in other regions.

#### Conclusion: Option A

Azure CNI is refused in every mode with reason
`MultiRegionUnsupportedConfiguration`. Option C can be reconsidered after a
live check of pod source addresses, masquerading and network policy across
regions.

### Decision 6: How to prefer the cluster region?

#### Option A: A provider policy

The provider ranks the offerings of the cluster region first. This adds a
second order beside price and availability, and the right preference differs by
workload.

#### Option B: NodePool weights

As in the
[weighted NodePools pattern of 0009](./0009-regional-vm-provisioning.md#preference-pattern-weighted-nodepools):
a higher-weight NodePool whose AKSNodeClass omits `spec.regions`, and a
lower-weight overflow NodePool whose AKSNodeClass lists other regions.

#### Conclusion: Option B

No new API. The overflow NodePool gets only what the home NodePool cannot
place. One controller must own both NodePools: a second autoscaler can launch a
node for the same pod.

### Decision 7: Load balancers, NSG and IP forwarding

The load balancers and NSG of the cluster are regional resources that a NIC in
another region cannot use. The nodes of an AKSNodeClass with `spec.regions`,
including those in the cluster region, join no backend pool, get no NIC NSG and
carry `node.kubernetes.io/exclude-from-external-load-balancers=true`. The NSG
of the subnet applies. NICs keep IP forwarding off, as all Karpenter NICs do.

### Decision 8: Identities

Some identities can be assigned only to VMs in some regions.
`--multi-region-node-identities` gives the VMs of a listed region their own node
and kubelet identities instead of the global ones. The cluster region cannot
have an entry.

## FAQ

### Why is the cluster region not added implicitly?

An overflow AKSNodeClass must be able to list only other regions. List the
cluster region to use it.

### What happens when a region is removed?

New NodeClaims do not go there. Existing NodeClaims in that region drift (see
Decision 2), and core replaces them within the disruption budgets. Garbage
collection lists the whole node resource group, so it also finds VMs in a
removed region.

### What happens when a region fails?

The region gets no new nodes until its checks pass again, and `SubnetsReady`
stays true and names the failing regions. The NodeClass is not ready only while
every listed region fails, or while a region that has never loaded its SKU
catalog has NodeClaims (reason `MultiRegionCatalogUnavailable`).

### Does this change existing clusters?

No. The field is optional and excluded from the hash, and the option is off by
default. With the option off, no per-region provider, cache or ARM client is
built. A test compares the VM, NIC and bootstrap requests of an AKSNodeClass
without `spec.regions` with the option on and off.

## Testing

* Unit and acceptance tests with synthetic Azure APIs in two regions:
  placement, retries, per-region failures and quota, drift, identities,
  home-first NodePools with overflow, and the refusals before any Azure write.
* An opt-in `MultiRegion` e2e suite for a cluster with a bring-your-own CNI and
  networks in other regions. It checks the peerings, launches a node in each
  region, checks each VM and NIC in ARM and probes pod traffic between regions.
  It skips without `MULTI_REGION_TEST_REGIONS`. It is not in the E2E matrix,
  because each matrix entry creates a cluster in the `aksmachineapi` mode by
  default and without networks in other regions.

## Production Readiness

* The feature is experimental and off by default.
* Status reasons: `MultiRegionDisabled`, `MultiRegionProvisionModeUnsupported`,
  `MultiRegionUnsupportedConfiguration`, `RegionsUnavailable`,
  `MultiRegionCatalogUnavailable` and `MultiRegionSubnetInvalid`; drift reason
  `MultiRegionPlacementDrift`; metric
  `karpenter_multiregion_inconsistent_instance_type_offerings_total`.
* Before the feature leaves experimental status: a live run of the e2e suite,
  and a decision about Azure CNI with pod IPs from the node subnet.
* The AKS-managed addon (NAP) uses the AKS Machine API modes. It needs a
  location per machine on PUT, GET and LIST, a machine subnet in another region,
  node identities that work in that region and, for Azure CNI Overlay, supported
  pod networking across regions.
