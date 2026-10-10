# Node Public IP

Status: available in the AKS Machine API provisioning modes

An `AKSNodeClass` can give every node it launches its own public IP address, optionally
allocated from a [public IP prefix](https://learn.microsoft.com/azure/virtual-network/ip-services/public-ip-address-prefix)
you own. This is the Karpenter equivalent of an agent pool's
[instance-level public IPs](https://learn.microsoft.com/azure/aks/use-node-public-ips).

AKS creates each address together with the node and deletes it with the node. Karpenter
has no cleanup of its own to do.

## Contents

- [Supported configurations](#supported-configurations)
- [Limitations](#limitations)
- [Prerequisites](#prerequisites)
- [Enable it on a NodeClass](#enable-it-on-a-nodeclass)
- [Zones](#zones)
- [Routing preference](#routing-preference)
- [Sizing a prefix](#sizing-a-prefix)
- [Outbound traffic](#outbound-traffic)
- [API server authorized IP ranges](#api-server-authorized-ip-ranges)
- [Network security and host ports](#network-security-and-host-ports)
- [Finding a node's address](#finding-a-nodes-address)
- [Troubleshooting](#troubleshooting)

## Supported configurations

| Provisioning mode | Supported |
| --- | --- |
| `aksmachineapi` | Yes, on NAP and self-hosted |
| `aksmachineapiheaderbatch` | Yes, on NAP and self-hosted |
| `aksscriptless` | No |
| `bootstrappingclient` | No |

The self-hosted default mode is `aksscriptless`, so most self-hosted installations must
switch to an AKS Machine API mode before they can use this feature.

In an unsupported mode, a NodeClass with `nodePublicIP.enabled: true` isn't ready. Its
`ValidationSucceeded` condition is `False` with reason `NodePublicIPUnsupportedProvisionMode`,
and no nodes are launched from it. Switching an existing installation from an AKS Machine API
mode to a VM mode has the same effect: existing nodes keep running, but no new capacity is
created.

## Limitations

- **VM-based modes.** `aksscriptless` and `bootstrappingclient` aren't supported.
- **Dual-stack clusters.** Not supported. On a dual-stack cluster, each node would also get a
  public IPv6 address. That address never comes from the prefix, so it isn't on any
  allow-list you built from the prefix, and NSG rules for IPv6 Internet traffic apply to it.
  Karpenter doesn't detect this case, so don't enable the feature on a dual-stack cluster.
- **IPv4 only, one prefix.** `prefixIDs` takes at most one entry, and it must be an IPv4 prefix.
  Karpenter can't tell a prefix's address family from its ID, so an IPv6 prefix is accepted
  and then fails at launch.
- **IP tags.** `ipTags` and `prefixIDs` can't be combined. `RoutingPreference=Internet` requires
  regional nodes, an IPv4 address, Kubernetes 1.29 or later, and a
  [supported region](https://learn.microsoft.com/azure/virtual-network/ip-services/routing-preference-overview#regional-availability).
  See [Routing preference](#routing-preference). Azure restricts other tag types, such as
  `FirstPartyUsage`, to some subscriptions.
- **Outbound type `none`.** Not supported. See [Outbound traffic](#outbound-traffic).
- **Windows nodes.** Node public IP behavior on Windows is unverified. A host port on a Windows
  node also needs a Windows Firewall rule if you turned the firewall back on; AKS turns it off
  on Windows nodes.

## Prerequisites

### Karpenter's identity and the cluster identity can join the prefix

Skip this if you don't use a prefix.

Two identities need `Microsoft.Network/publicIPPrefixes/join/action` on the prefix:

- **Karpenter's identity.** When Karpenter creates a node, Azure Resource Manager checks that
  the caller can join every prefix the node references. Without the permission, the request is
  rejected with `LinkedAuthorizationFailed` before AKS sees it.
- **The AKS cluster identity.** AKS creates the node's VM, and so its address, with this
  identity, as listed in
  [AKS service permissions](https://learn.microsoft.com/azure/aks/aks-service-permissions).
  Without the permission, the node fails while it's being created, also with
  `LinkedAuthorizationFailed`.

Both identities need join permission covering the prefix. If existing role assignments,
including inherited permissions, don't provide it, grant the built-in `Network Contributor`
role on the prefix. The kubelet identity needs no additional permissions for this feature.

Role assignments take a few minutes to apply; launches that fail in the meantime are retried on
their own.

#### NAP

Karpenter calls AKS as the cluster identity, so grant the cluster identity only. This is the
same model as [custom subnets on NAP](https://learn.microsoft.com/azure/aks/node-auto-provisioning-networking#rbac-setup-for-custom-subnet-configurations).

Find the cluster identity:

```bash
az aks show -g <cluster-rg> -n <cluster> --query identity --output json
```

For a system-assigned identity, use `principalId`. For a user-assigned identity, use the
`principalId` under `userAssignedIdentities`. Then grant it join on the prefix:

```bash
PREFIX_ID=/subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Network/publicIPPrefixes/<name>
CLUSTER_PRINCIPAL_ID=$(az aks show -g <cluster-rg> -n <cluster> \
  --query "identity.principalId || values(identity.userAssignedIdentities)[0].principalId" --output tsv)

az role assignment create \
  --assignee-object-id "${CLUSTER_PRINCIPAL_ID}" \
  --assignee-principal-type ServicePrincipal \
  --role "Network Contributor" \
  --scope "${PREFIX_ID}" \
  --output none
```

#### Self-hosted

Karpenter calls AKS as its own identity: the managed identity its service account federates
with. Grant both that identity and the cluster identity. If they're the same identity, one
grant covers both.

```bash
PREFIX_ID=/subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Network/publicIPPrefixes/<name>
CLUSTER_PRINCIPAL_ID=$(az aks show -g <cluster-rg> -n <cluster> \
  --query "identity.principalId || values(identity.userAssignedIdentities)[0].principalId" --output tsv)
KARPENTER_PRINCIPAL_ID=$(az identity show -g <identity-rg> -n <karpenter-identity> \
  --query principalId --output tsv)

for PRINCIPAL_ID in $(printf '%s\n' "${CLUSTER_PRINCIPAL_ID}" "${KARPENTER_PRINCIPAL_ID}" | sort -u); do
  az role assignment create \
    --assignee-object-id "${PRINCIPAL_ID}" \
    --assignee-principal-type ServicePrincipal \
    --role "Network Contributor" \
    --scope "${PREFIX_ID}" \
    --output none
done
```

### The prefix is in the cluster's region and subscription

The prefix can be in any resource group, but it must be in the same region and subscription
as the cluster. See [Public IP prefix limitations](https://learn.microsoft.com/azure/virtual-network/ip-services/public-ip-address-prefix#limitations).

### The prefix serves every zone the nodes can use

A prefix is either **zone-redundant** or **zonal**, and addresses drawn from it take its zone
setting. See [Zones](#zones).

## Enable it on a NodeClass

Without a prefix, each node gets an address from Azure's pool:

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: public
spec:
  nodePublicIP:
    enabled: true
```

With a prefix, each node gets an address from that prefix:

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: public-prefixed
spec:
  nodePublicIP:
    enabled: true
    prefixIDs:
      - /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Network/publicIPPrefixes/<name>
```

| Field | Description |
| --- | --- |
| `nodePublicIP.enabled` | Gives each node a public IP address. Omitted or `false` means no public IP. |
| `nodePublicIP.prefixIDs` | Optional. The ARM ID of the **IPv4** public IP prefix to allocate from. At most one entry. Requires `enabled: true`. |
| `nodePublicIP.ipTags` | Optional. [IP tags](https://learn.microsoft.com/azure/virtual-network/ip-services/public-ip-addresses#ip-tags) on each node's public IP, as `ipTagType` and `tag` pairs. These aren't Azure resource tags. At most 4 unique entries. Requires `enabled: true`, and can't be combined with `prefixIDs`. See [Routing preference](#routing-preference) for an example. |

Any change that affects the result, such as enabling, disabling, or changing the prefix or
the IP tags, drifts existing nodes, and Karpenter replaces them according to the NodePool's
disruption budgets. IP tags are set when a node is created, so replacement is the only way to
change them. Omitting the block, `{}`, and `enabled: false` are equivalent and don't drift nodes,
and neither does a change in the prefix ID's letter case or in the order of IP tags.

## Zones

Karpenter picks a node's zone before it asks AKS for the node, and it doesn't read the
prefix. Two setups are supported:

- **A zone-redundant prefix**, or a non-zonal prefix in a region without zones, with any
  NodePool. Zone-redundant is the default when you create a Standard prefix without zones.
- **A zonal prefix** whose NodeClass is used only by NodePools that restrict
  `topology.kubernetes.io/zone` to the prefix's zone.

Any other setup, such as a zonal prefix with a NodePool that spans several zones, isn't
supported. Nodes placed in the prefix's zone launch, and nodes placed elsewhere fail at launch.

To use zonal prefixes across several zones, create one NodeClass and one NodePool per prefix.
Karpenter schedules across all NodePools at once, so topology spread across them still works:

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: public-zone-1
spec:
  nodePublicIP:
    enabled: true
    prefixIDs:
      - /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Network/publicIPPrefixes/<zone-1-prefix>
---
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: public-zone-1
spec:
  template:
    spec:
      nodeClassRef:
        group: karpenter.azure.com
        kind: AKSNodeClass
        name: public-zone-1
      requirements:
        - key: topology.kubernetes.io/zone
          operator: In
          values: ["<region>-1"]
  limits:
    nodes: "12"
```

Repeat for each zone, with that zone's prefix and `topology.kubernetes.io/zone` value.

## Routing preference

The `RoutingPreference=Internet` IP tag routes traffic to and from a node's public IP over ISP
networks rather than Microsoft's global network. See
[Routing preference](https://learn.microsoft.com/azure/virtual-network/ip-services/routing-preference-overview).

Azure supports it only on zone-redundant public IPs, and AKS creates those only for regional
nodes. Every NodePool that uses the NodeClass must require
`karpenter.azure.com/placement-scope: regional`. Regional nodes have no zone guarantee: Azure
picks the zone, and the node's `topology.kubernetes.io/zone` label is `0`, so these nodes can't
satisfy zone topology spread or zone requirements. A zonal node fails to launch. See
[Troubleshooting](#troubleshooting).

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: public-internet-routing
spec:
  nodePublicIP:
    enabled: true
    ipTags:
      - ipTagType: RoutingPreference
        tag: Internet
---
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: public-internet-routing
spec:
  template:
    spec:
      nodeClassRef:
        group: karpenter.azure.com
        kind: AKSNodeClass
        name: public-internet-routing
      requirements:
        - key: karpenter.azure.com/placement-scope
          operator: In
          values: ["regional"]
```

## Sizing a prefix

IPv4 public IP prefixes hold at most 16 addresses (`/28`) by default, and each node uses one.

- Use one prefix per NodeClass. Karpenter doesn't count how many addresses a prefix has left.
- Across the NodePools that use the NodeClass, keep the sum of `spec.limits.nodes` within the
  prefix size, minus headroom for replacements. Drift and consolidation that require replacement
  capacity launch replacement nodes before they remove the old ones, so a prefix with no free
  address blocks those replacements.
- [Expiration](https://karpenter.sh/docs/concepts/disruption/#expiration) is forceful: expired nodes
  begin draining without waiting for replacement capacity. Prefix exhaustion doesn't prevent
  expiration and can leave workloads pending while replacement nodes fail to provision.

For example, for a single NodePool using a `/28` prefix:

```yaml
spec:
  limits:
    nodes: "12"   # 16 addresses, with 4 left for replacements
```

## Outbound traffic

The outbound type is set per cluster, and Karpenter doesn't read it.

Karpenter configures node public IPs. AKS and Azure networking determine routing, egress source
addresses, and inbound access.

| Outbound type | Supported | Egress source | Inbound to the node IP |
| --- | --- | --- | --- |
| `loadBalancer` | Yes | **The node's own IP.** AKS removes the node from the load balancer's outbound pool, so egress no longer uses the load balancer's outbound IPs. Update any allow-list keyed on those IPs. | Works, subject to NSG rules |
| `managedNATGateway`, `userAssignedNATGateway` | Yes | The NAT gateway's IPs | Works, subject to NSG rules |
| `userDefinedRouting` | Rejected by AKS today | See below | See below |
| `none` | No | | |

**`userDefinedRouting`.** AKS currently rejects node public IP on these clusters with
`UDRWithNodePublicIPNotAllowed`, and Karpenter shows that error on the NodeClaim. If AKS
accepts it, the required `0.0.0.0/0` route to your appliance also applies to traffic to and
from the node IP. Inbound connections to the node IP work only for clients whose prefixes
have a route with next hop `Internet`, or when the appliance handles the traffic. Egress goes
through the appliance, except to those prefixes.

**`none`.** Node public IPs give a
[network-isolated cluster](https://learn.microsoft.com/azure/aks/concepts-network-isolated)
direct Internet egress, which defeats the isolation. Karpenter doesn't block it, so don't
enable the feature on these clusters.

**Separate pod subnet.** If pods use their own subnet with a NAT gateway, pod egress leaves
from the NAT gateway's IPs, not from the node IP.

## API server authorized IP ranges

If the cluster uses
[API server authorized IP ranges](https://learn.microsoft.com/azure/aks/api-server-authorized-ip-ranges),
each NodeClass with node public IP must use a prefix, and the prefix's range must be in the
authorized list.

- AKS rejects a NodeClass without a prefix with `InvalidParameter`, unless the cluster uses
  API Server VNet Integration.
- AKS doesn't check that the prefix is in the authorized list. If it isn't, nodes may fail to
  reach the API server and never register.

## Network security and host ports

The feature only assigns addresses. Standard public IPs are
[closed to inbound traffic](https://learn.microsoft.com/azure/virtual-network/ip-services/public-ip-addresses)
unless a network security group (NSG) allows it.

- **Audit existing NSG rules first.** Any allow rule with source `Internet` or `Any` that covers
  the node becomes reachable directly through the node IP. Before, it was reachable only through
  a load balancer or from the VNet.
- **Host ports need your own NSG rules.** Unlike agent pools, these nodes have no
  `allowedHostPorts` or application security groups, so AKS doesn't open host ports for you.
  Add an inbound rule to the AKS-managed NSG in the node resource group, and to your subnet's NSG
  if it has one. Keep the source as narrow as you can. Use the node's **private** IP, or the
  subnet range, as the destination, not its public IP. NSGs
  [evaluate inbound traffic](https://learn.microsoft.com/azure/virtual-network/network-security-groups-overview#security-rules)
  after Azure translates the public IP to the private IP, so a rule with the public IP as its
  destination never matches.
- **Node resource group lockdown.** With
  [node resource group lockdown](https://learn.microsoft.com/azure/aks/node-resource-group-lockdown)
  set to `ReadOnly`, the AKS-managed NSG can't be changed, so host ports can't be opened. AKS
  Automatic enables lockdown by default.
- **AKS-managed VNet.** Persistence of custom rules in the AKS-managed NSG across AKS
  reconciliation is unverified. Check that required rules remain in place after reconciliation.
- **BYO VNet.** Attachment of the AKS-managed NSG to node NICs is unverified for this setup.
  Inspect each node NIC's NSG association before relying on subnet NSG rules alone.

## Finding a node's address

Karpenter doesn't publish the address on the NodeClaim or NodeClass. Read the node's
`ExternalIP`:

```bash
kubectl get node <node> -o jsonpath='{.status.addresses[?(@.type=="ExternalIP")].address}'
```

## Troubleshooting

Public IP errors are about the NodeClass, not the VM size or zone, so Karpenter doesn't try
other sizes or zones, and it doesn't mark any offering as unavailable. It shows the Azure error
code and message in one of two places.

**Rejected when the node is requested.** The NodeClaim's `Launched` condition is `Unknown`
with reason `CreateInstanceFailed`, and the message carries the code. The NodeClaim stays and
Karpenter retries it.

```bash
kubectl get nodeclaim <name> -o jsonpath='{.status.conditions[?(@.type=="Launched")].message}'
```

| Code | Cause | Fix |
| --- | --- | --- |
| `UDRWithNodePublicIPNotAllowed` | The cluster's outbound type is `userDefinedRouting` | See [Outbound traffic](#outbound-traffic) |
| `InvalidPublicIPPrefixDifferentSub` | The prefix is in another subscription | Use a prefix in the cluster's subscription |
| `LinkedAuthorizationFailed`, naming Karpenter's identity (the cluster identity on NAP) | Karpenter's identity can't join the prefix, or the prefix doesn't exist | Fix the prefix ID, or grant the permission. See [Prerequisites](#karpenters-identity-and-the-cluster-identity-can-join-the-prefix) |
| `GetPublicIPPrefixByResourceIDError` | The prefix doesn't exist. You only get this code if Karpenter's identity can join prefixes at a broader scope, such as the resource group | Fix the prefix ID |
| `PublicIpPrefixOutOfIpAddressesForVMScaleSet` (`PublicIPPrefixInsufficientIPs`) | The prefix is too small | Use a larger prefix. See [Sizing a prefix](#sizing-a-prefix) |
| `InvalidParameter` | The cluster uses authorized IP ranges and the NodeClass has no prefix | Add a prefix that's in the authorized list. See [API server authorized IP ranges](#api-server-authorized-ip-ranges) |
| `UnsupportedIPTagType` | An `ipTagType` that Azure doesn't support. The message lists the supported types | Fix the NodeClass's `ipTags` |

**Failed while the node is created.** The NodeClaim gets an `AsyncProvisioningError` warning
event, `Failed to register: …`, carrying AKS's code and message, and is then deleted. Pending
pods get a new NodeClaim, which fails the same way until the NodeClass or the prefix is fixed.

```bash
kubectl get events -A --field-selector involvedObject.kind=NodeClaim,reason=AsyncProvisioningError
```

| Cause | Fix |
| --- | --- |
| The prefix has no free addresses | Lower the NodePools' `spec.limits.nodes`, remove nodes that use the prefix, or use a larger prefix |
| The prefix is in a different region from the cluster | Use a prefix in the cluster's region and subscription |
| A zonal prefix in a different zone from the node | See [Zones](#zones) |
| The cluster identity can't join the prefix (`LinkedAuthorizationFailed`, naming the cluster identity) | Grant it. See [Prerequisites](#karpenters-identity-and-the-cluster-identity-can-join-the-prefix) |
| The subscription's public IP quota is exhausted | Request a quota increase, or remove unused public IPs |
| The prefix is IPv6 | Use an IPv4 prefix |
| "A zonal PublicIPAddress … cannot support for routing preference feature", with no code | The NodeClass sets `RoutingPreference=Internet` and the node is zonal | Require regional nodes in every NodePool that uses the NodeClass. See [Routing preference](#routing-preference) |

Where AKS doesn't pass on a more specific network error, the code is
`CreateOrUpdatePublicIPAddressError`.
