# Capacity Reservations

Status: available on the direct-VM provisioning path

An `AKSNodeClass` can name one Azure Capacity Reservation Group (CRG). Every VM that
NodeClass launches then targets that group, so a Karpenter-managed cluster consumes
capacity you have already reserved and are already paying for.

Nodes stay labelled `karpenter.sh/capacity-type: on-demand`. Azure does not report which
VM holds an SLA-backed reserved unit, so Karpenter does not claim one does.

## Contents

- [Grant access to the group](#grant-access-to-the-group)
- [Point a NodeClass at the group](#point-a-nodeclass-at-the-group)
- [One NodePool per member reservation](#one-nodepool-per-member-reservation)
- [Stable baseline: a static NodePool](#stable-baseline-a-static-nodepool)
- [Elastic: dynamic NodePools](#elastic-dynamic-nodepools)
- [Flexible: a NodeOverlay](#flexible-a-nodeoverlay)
- [Costs](#costs)
- [Not supported, and not reconciled](#not-supported-and-not-reconciled)
- [Troubleshooting](#troubleshooting)
- [Feature gates](#feature-gates)

## Grant access to the group

The Karpenter identity needs three actions on the group:

| Action | Needed for |
| --- | --- |
| `Microsoft.Compute/capacityReservationGroups/read` | resolving the group |
| `Microsoft.Compute/capacityReservationGroups/capacityReservations/read` | listing its members |
| `Microsoft.Compute/capacityReservationGroups/deploy/action` | launching into it |

`Contributor` on the group covers all three, through its `*` action, and is what AKS asks
for today. It is the simplest grant and more than the feature needs.

To grant only the three, define a custom role. No built-in role is scoped to capacity
reservations, so this is the narrow option:

```bash
az role definition create --role-definition '{
  "Name": "Karpenter Capacity Reservation User",
  "Description": "Lets Karpenter resolve a capacity reservation group and deploy into it.",
  "AssignableScopes": ["/subscriptions/<sub>"],
  "Actions": [
    "Microsoft.Compute/capacityReservationGroups/read",
    "Microsoft.Compute/capacityReservationGroups/capacityReservations/read",
    "Microsoft.Compute/capacityReservationGroups/deploy/action"
  ]
}'

az role assignment create \
  --assignee-object-id <karpenter-identity-principal-id> \
  --assignee-principal-type ServicePrincipal \
  --role "Karpenter Capacity Reservation User" \
  --scope /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Compute/capacityReservationGroups/<name>
```

Both steps need an administrator. Creating the role needs
`Microsoft.Authorization/roleDefinitions/write` at the subscription, and assigning it needs
`Microsoft.Authorization/roleAssignments/write` at the group. Whoever installs Karpenter
typically has neither, so treat this as a separate request to whoever owns the
subscription, alongside the request for the group itself.

A grant is normally required even for a group in your own subscription, because groups are
usually pre-created in a capacity team's resource group.

**Expect a delay after granting.** Azure takes a few minutes to make a role assignment
effective. Until it does, the NodeClass reports `CapacityReservationGroupAccessDenied`, or
a launch fails with `LinkedAuthorizationFailed`. Both clear on their own; the NodeClass is
retried every minute. Do not re-grant in response.

Read access is verified at readiness, but deploy access is not — nothing can check it
before a launch. An identity with read but not deploy reports `Ready` and then fails every
launch.

## Point a NodeClass at the group

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: reserved
spec:
  imageFamily: Ubuntu
  capacityReservationGroupID: /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Compute/capacityReservationGroups/<name>
```

Setting the field is the opt-in; there is no feature flag. It is a launch constraint, not a
preference: every launch from this NodeClass targets the group, and the NodeClass emits
offerings only for VM sizes and placements the group actually reserves.

Karpenter resolves the group into status:

```bash
kubectl get aksnodeclass reserved -o jsonpath='{.status.capacityReservationGroup}' | jq
```

```json
{
  "id": "/subscriptions/.../capacityReservationGroups/prod-eastus",
  "location": "eastus",
  "zones": ["1"],
  "capacityReservations": [
    { "name": "d8s-1", "vmSize": "Standard_D8s_v5", "zones": ["1"], "quantity": 6, "provisioningState": "Succeeded" }
  ]
}
```

Each launched NodeClaim and Node is annotated with the group the NodeClass named at launch:

```
karpenter.azure.com/capacity-reservation-group-id: /subscriptions/.../prod-eastus
```

That is audit metadata only. Nothing refreshes it, and it is not evidence of the VM's
current association.

## One NodePool per member reservation

A group is a container. The unit that carries a quantity is the **member reservation**,
which is one VM size in one placement. A NodePool admitting several sizes or zones spans
several members and nothing keeps it balanced — it can fill one member while the others sit
idle.

Pin each reservation-backed NodePool to exactly one VM size and one placement, and give a
group covering four buckets four NodePools. Only then does a node count mean anything about
a reserved quantity.

For a regional (zone-less) group, pin the placement scope rather than a zone:

```yaml
      - key: karpenter.azure.com/placement-scope
        operator: In
        values: ["regional"]
```

Azure reports regional nodes with `topology.kubernetes.io/zone: "0"`.

## Stable baseline: a static NodePool

For a reservation bought to guarantee a steady baseline, give each member a static NodePool
with `replicas` set to its reserved quantity. The nodes always exist, and consolidation
skips static NodePools, so a reserved node is never removed to save money already spent.

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: reserved-d8s-eastus-1
spec:
  replicas: 6
  limits:
    nodes: "7"           # replicas + 1; see below
  template:
    spec:
      nodeClassRef:
        group: karpenter.azure.com
        kind: AKSNodeClass
        name: reserved
      expireAfter: Never
      requirements:
      - key: node.kubernetes.io/instance-type
        operator: In
        values: ["Standard_D8s_v5"]
      - key: topology.kubernetes.io/zone
        operator: In
        values: ["eastus-1"]
```

> **Set `limits.nodes` to at least `replicas + 1`.** Static drift creates the replacement
> before removing the node it replaces, and reserves against `limits.nodes` without
> bursting over. With `replicas: N` and `limits.nodes: N`, drift **stalls indefinitely** —
> and it is silent: nodes report drifted, nothing replaces them, and the NodePool still
> reports `Ready=True`. Image and security updates stop landing on exactly the nodes you
> care most about. During a replacement the member is transiently overallocated by one,
> which needs ordinary quota and regional capacity.

`expireAfter: Never` matters too. Expiration is not skipped for static pools and defaults to
720h; it deletes a node and lets the replica count restore it, briefly leaving the member
under-filled.

Occupancy comes from the replica count, whatever pods land there. Steering *which* workloads
land on reserved capacity is optional and uses ordinary Kubernetes tools: a pool label with
`preferred` node affinity biases work toward it, `required` pins it, and a taint with
matching tolerations keeps everything else off.

Send burst above the reservation to a separate NodePool with no CRG association.

## Elastic: dynamic NodePools

When the baseline is not steady enough to pin a replica count:

- give reservation-backed pools a higher `weight` than the unassociated fallback pool.
  Weight — not price — is what makes reserved capacity the first choice. It applies only
  when Karpenter must provision: a pod that already fits somewhere never reaches it. The
  CRD caps weight at 100.
- cap each pool with `limits.nodes` at that member's reserved quantity.
- set a `0`-node disruption budget for the `Underutilized` reason, so a node filling a
  reserved unit is not consolidated away.

```yaml
spec:
  weight: 50
  limits:
    nodes: "6"
  disruption:
    consolidateAfter: 0s   # required whenever disruption is set explicitly
    budgets:
    - reasons: ["Underutilized"]
      nodes: "0"
```

`limits.nodes` is a guardrail, not an exact bound. It is enforced from observed cluster
state, so it lags, and it counts only the nodes this cluster creates — anything else
consuming the group is invisible to it.

A `0` budget keeps *occupied* units occupied. It does not keep every unit occupied: empty
reserved nodes are still consolidated, and demand still governs how many exist. Guaranteed
occupancy is what the static option buys.

## Flexible: a NodeOverlay

Within a pool, consolidation still values a reserved node at list price. A NodePool-scoped
`NodeOverlay` priced at `0` makes provisioning and consolidation read the same declared
price, which lets consolidation move work *into* an expensive reserved bucket from the
unprotected fallback pool.

```yaml
apiVersion: karpenter.sh/v1alpha1
kind: NodeOverlay
metadata:
  name: reserved-d16s-eastus-1
spec:
  requirements:
  - key: karpenter.sh/nodepool
    operator: In
    values: ["reserved-d16s-eastus-1"]
  price: "0"
```

> **Create the overlay only after the NodeClass reports `Ready`.** `NodeOverlay` keys its
> price updates by offering requirements, and resolving a capacity reservation group
> rewrites those requirements. An overlay created while the NodeClass is still resolving
> keys nothing that scheduling later looks up, and **silently has no effect** — the overlay
> still reports `Ready` throughout. Wait for
> `kubectl wait --for=condition=Ready aksnodeclass/reserved` before applying it.

The declared price is your assertion that these nodes occupy prepaid units. Nothing
enforces it: another team or cluster consuming the same group can exhaust it while every
node still carries the declared zero price. Keep the pool's limit in step when the
reservation is resized.

## Costs

Reserved capacity is billed whether or not a VM occupies it, so consolidating a reserved
node reclaims nothing on the compute charge. Standing capacity is close to free against a
paid-for reservation — but not free: a running node still draws disks, networking, IP
addresses, per-node licensing and extensions.

**Watch the overallocated count.** The reserved quantity is not a launch cap: Azure happily
creates VMs beyond it, carrying ordinary incremental compute cost. Overallocation is the
only signal that a pool has allocated past what you prepaid. Karpenter does not publish this
yet, so read it from Azure — a member is overallocated when the allocated count exceeds its
reserved quantity:

```bash
az capacity reservation show \
  --resource-group <rg> --capacity-reservation-group <name> --name <member> \
  --instance-view \
  --query '{reserved: sku.capacity, allocated: length(instanceView.utilizationInfo.virtualMachinesAllocated || `[]`)}'
```

## Not supported, and not reconciled

| Case | Behaviour |
| --- | --- |
| Spot, Ultra Disk, proximity placement groups | Azure does not support these with capacity reservations, so those offerings are not emitted for a configured NodeClass |
| AKS Machine API provisioning mode | Fails readiness with `CapacityReservationGroupUnsupportedProvisionMode`. The per-Machine field is not published yet |
| Clouds other than Azure Cloud, Azure Government, Azure China | Fails readiness with `CapacityReservationGroupUnsupportedCloud` |
| Cross-subscription (shared) groups | Not supported yet; same-subscription Targeted groups only |
| Association changed outside Karpenter | **Not detected and not reconciled.** Manage association only through the NodeClass. These VMs live in the AKS node resource group, where direct edits are unsupported and are blocked outright when node resource group lockdown is enabled |
| Changing `capacityReservationGroupID` | Drifts affected nodes and replaces them through ordinary disruption, paced by disruption budgets |

Setting the field on a NodeClass that already has nodes drifts all of them. On a busy
cluster, prefer a new NodeClass when that churn is unwelcome.

## Troubleshooting

`kubectl get aksnodeclass <name> -o jsonpath='{.status.conditions}'` — the
`CapacityReservationGroupReady` condition gates `Ready` whenever the field is set.

| Reason | Meaning |
| --- | --- |
| `CapacityReservationGroupIDInvalid` | The value is not a capacity reservation group resource ID |
| `CapacityReservationGroupNotFound` | No such group |
| `CapacityReservationGroupAccessDenied` | The identity cannot read the group. Expected for a few minutes after granting the role. Also reported for a group that does not exist, because Azure answers 403 rather than 404 when the identity cannot read the enclosing scope |
| `CapacityReservationGroupSubscriptionMismatch` | The group is in another subscription |
| `CapacityReservationGroupRegionMismatch` | The group is in another region |
| `CapacityReservationGroupUnsupportedReservationType` | Only `Targeted` groups are supported |
| `CapacityReservationGroupNoReservations` | The group has no member reservations yet |
| `CapacityReservationGroupNoEligibleReservations` | Members exist but none has provisioned successfully |
| `CapacityReservationGroupNoCompatibleReservations` | No member reserves a VM size this NodeClass can use — check the region offers the size, and that the NodeClass does not exclude it |
| `CapacityReservationGroupUnsupportedProvisionMode` | AKS Machine API mode; see above |
| `CapacityReservationGroupUnsupportedCloud` | This cloud has no capacity reservations |
| `CapacityReservationGroupUnknownError` | Unexpected Azure error; the message carries the detail |

A member reservation with `quantity: 0` is valid and useful: it is the documented way to
associate VMs with a group before raising the reserved quantity. Such VMs are overallocated
until you do.

Reservable capacity is scarcer than on-demand capacity. Creating a reservation consumes
family quota exactly as creating a VM does, but ample quota does not mean the capacity can
be reserved: if Azure refuses to create a reservation, try another zone, size, or region.

## Feature gates

`spec.replicas` (`StaticCapacity`) and `NodeOverlay` are alpha. They are off by default in
the Helm chart and on by default in AKS Node Auto Provisioning, so on NAP clusters the
arrangements above need no gate changes.
