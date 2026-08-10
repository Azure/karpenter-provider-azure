# Capacity reservations for Karpenter on Azure

**Status:** proposal, for review.

## Contents

- [Summary](#summary)
- [What an Azure capacity reservation group provides](#what-an-azure-capacity-reservation-group-provides)
- [Recommendation](#recommendation)
- [Why nodes stay on-demand](#why-nodes-stay-on-demand)
- [What this does not do, and why](#what-this-does-not-do-and-why)
- [Filling a reservation with existing controls](#filling-a-reservation-with-existing-controls)
- [What is left for the provider to do](#what-is-left-for-the-provider-to-do)
- [Constraints and operational notes](#constraints-and-operational-notes)
- [Alternatives considered](#alternatives-considered)
- [Phases](#phases)
- [Open decisions](#open-decisions)
- [External dependencies](#external-dependencies)

## Summary

Let an `AKSNodeClass` name one Azure Capacity Reservation Group (CRG). Every VM
that NodeClass launches targets that group, so a cluster managed by Karpenter
can consume capacity the customer has already reserved and is already paying
for.

Nodes are labelled `karpenter.sh/capacity-type=on-demand`. Karpenter does not
assert that a particular node occupies an SLA-backed reserved unit, because
Azure does not report that per VM. Reservation state is published as aggregate
telemetry instead.

This is deliberately a first increment. It delivers association, correct
placement, lifecycle behavior, and measurement. It does not yet make
provisioning or consolidation actively prefer unused reserved capacity, though
pairing the association with a static NodePool already covers the common case
— see [filling a reservation with existing
controls](#filling-a-reservation-with-existing-controls). The
[phases](#phases) section separates the preferred end state from the decision
of when to invest in it: complete core support is the target design, while
telemetry informs its priority and measures its eventual benefit.

## What an Azure capacity reservation group provides

Three properties of the Azure model shape everything below.

**A VM targets the group.** A capacity reservation always lives inside a CRG,
and a VM references the group's resource ID rather than an individual member
reservation. Azure then matches the VM to a member reservation by VM size and
placement. A single group can therefore cover several VM sizes and zones.

**The reserved quantity is not a launch cap.** Once the reserved units in a
group are occupied, Azure can still create further VMs carrying the same
association, subject to ordinary quota and regional capacity. Microsoft
documents this as overallocating the reservation. Those VMs run normally; they
are outside the capacity reservation SLA and carry ordinary incremental compute
cost, subject to whatever Reserved Instance or savings plan coverage the
customer already holds. Later references to incremental cost mean that, not a
specific rate.

**Coverage is an aggregate property of each member reservation.** The instance
view reports, per member reservation, the reserved quantity and the set of
allocated VM IDs, which gives exact aggregate counts. When a member reservation
is overallocated it does not designate which of those VMs hold the SLA-backed
units. Microsoft's own lifecycle documentation shows an overallocated VM moving
into a reserved unit when a different VM is deallocated, without anything about
the promoted VM changing. Counts also do not pool across members: unused units
in one VM size or placement cannot cover demand in another.

## Recommendation

**API.** One optional field on `AKSNodeClass`:

```yaml
spec:
  capacityReservationGroupID: /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Compute/capacityReservationGroups/<name>
```

The ID is a launch constraint rather than a preference. If it is set, every
launch from that NodeClass targets the group. Setting the field is itself the
opt-in; there is no separate feature flag. The field is per-NodeClass rather
than cluster-wide, so different NodeClasses can target different groups, or
none. A group that cannot be read, is in
the wrong region, has an unsupported type, or has no eligible member reservation
matching any candidate VM size and placement makes the NodeClass unready rather
than being silently ignored. A member reservation whose quantity is zero is
valid: it is the documented way to associate VMs before raising the reserved
quantity.

**Member eligibility.** Only members that have provisioned successfully back
offerings. A member still being updated keeps its current projection until it
settles, so a quantity change does not blank out capacity mid-flight; one that
has never provisioned successfully has no projection to keep and stays
ineligible until its first success; a member in a terminal failure state is
dropped from projection. Readiness turns on whether any *eligible* member
remains, not on the member list being non-empty — otherwise a group of entirely
unusable members reports Ready and emits no offerings.

**Permissions.** The Karpenter identity needs to read the group and its member
reservations, and to deploy into the group —
`Microsoft.Compute/capacityReservationGroups/deploy/action` is the association
half. `Contributor` on the group's scope is the known-good grant and is what AKS
asks users to grant today; the minimal action set has not been confirmed against
Azure's operation catalog and should be before it is documented as supported. A
grant is usually required, because groups are commonly pre-created in a capacity
team's own resource group. Note the asymmetry: readiness can verify read access
but not deploy, so an identity with read-only access reports Ready and then
fails every launch. That half surfaces only as a scoped launch failure, which is
why the failure reason names the group.

**Offerings.** For a configured NodeClass the provider emits only Regular
offerings backed by a matching member reservation, and only in the group's
placement: a zone-less group yields regional offerings, a zonal group yields
exactly its zones. Spot and other configurations Azure does not support with
capacity reservations are excluded.

**Quota preflight.** Ordinary family-quota preflight can reject a launch that
Azure would have accepted, because capacity within a reservation does not
consume incremental quota: that quota was taken when the reservation was
created. For CRG-backed offerings that check is suppressed and Azure stays
authoritative at launch. The cost of being wrong is bounded and local. If the
reserved units are already taken, Azure does not reject the launch — it creates
the VM as an overallocation, which does draw on ordinary quota. The launch fails
only when that quota or regional capacity is genuinely unavailable, and the
failure is scoped to the group, VM size, and placement, costing a scheduling
round trip rather than mispricing anything.

**Launch.** The group ID goes in the VM's capacity reservation profile. Launch
failures are scoped to that group, VM size, and placement, so a CRG-specific
failure does not mark ordinary capacity unavailable.

**Classification.** Nodes are `on-demand`. The configured group ID is recorded
as an annotation, and the observed member reservation may be recorded as a
second annotation marked as a point-in-time observation. Annotations do not
participate in scheduling and can be corrected without replacing the node.

**Telemetry.** Per member reservation: reserved quantity, allocated count,
unused units, overallocated count, and observation age. This is what makes the
size of the potential economic opportunity measurable rather than assumed — see
[phases](#phases). These are deliberately aggregate signals: they do not claim
that Karpenter could have recovered every unused unit, and Phase 1 does not
retain or export candidate sets for every launch.
Overallocated count is the one to alert on: since the reserved quantity is not a
launch cap, it is the only signal that a pool has allocated beyond the prepaid
quantity, and that those VMs carry ordinary incremental compute cost.

**Operator controls.** Bounding spend against a reservation is expressed with
ordinary NodePool limits, and routing workloads to reservation-backed capacity
with ordinary NodePool labels and taints. Both work today and need no
reservation-specific semantics. Limits are a guardrail rather than an exact
bound: they are enforced from observed cluster state, so they lag, and they
count only the nodes this cluster creates — anything else consuming the group
is invisible to them. They also only correspond to a reserved quantity when the
NodePool maps to a single member reservation, in the sense described under
[filling a reservation with existing
controls](#filling-a-reservation-with-existing-controls).

## Why nodes stay `on-demand`

Karpenter core has a `reserved` capacity type. It means something specific: a
named, finite unit whose consumption is enforceable, so that the scheduler can
count units, prefer them, and price them as already paid for. Core's model was
built against reservation products where exceeding the reserved count fails the
launch and the resulting instance reports which reservation it consumed.

Azure's model differs on both points, as described above: exceeding the reserved
quantity succeeds, and the VM does not report which unit it holds. Emitting
`reserved` would therefore mean asserting something the platform cannot confirm.
The practical consequences are what matter:

- **It could not be verified.** A launch that Karpenter selected as reserved may
  land as overallocated capacity if another consumer takes the last unit first.
  Nothing in the result distinguishes the two cases.
- **It could not be repaired.** If the reserved quantity is reduced, or another
  consumer allocates into the group, more nodes carry the label than the group
  can cover, and there is no signal identifying which ones to correct.
- **It would misprice disruption.** Core values a running node through its
  capacity type. A node priced as prepaid but actually running as incremental
  capacity would be protected from consolidation it should be subject to.

Keeping nodes `on-demand` is the conservative and truthful choice: it prices
CRG-associated capacity at its worst-case marginal cost and makes no per-node
guarantee.

## What this does not do, and why

Two capabilities are worth having and are not in this increment.

**Reservation-first provisioning.** Ideally, when a group has unused reserved
units, provisioning would prefer them over launching incremental capacity, even
when the reserved VM size has a higher list price. Two levers could express that
preference, and the same consolidation behavior bounds both.

Core expresses preference through offering price, and price is also what
consolidation uses to value running nodes, so discounting an offering to attract
provisioning under-prices every node in the pool, including any running beyond
the reserved quantity. The provider has a second lever, since it re-derives the
candidate set at launch and picks one, but as [what is left for the provider to
do](#what-is-left-for-the-provider-to-do) explains, using that to prefer a more
expensive reserved size runs into the same problem with no price change
anywhere.

**Reservation-aware consolidation.** Ideally, consolidation would move workloads
into unused reserved units and avoid vacating nodes that are filling them. That
requires cost to be evaluated for the reservation pool as a whole rather than
per node, which core does not currently model.

These are not independent. A design that obtains the first through price while
leaving the second unchanged makes the two disagree about the same node:
provisioning selects the reservation-backed offering, consolidation values that
node at full list price, replaces it with something cheaper, and the next
scale-up selects the reservation-backed offering again. The result is repeated
node churn with no improvement in reservation use.

## Filling a reservation with existing controls

Before reaching for anything reservation-specific: reserved capacity is paid for
whether or not a VM occupies it. Consolidation exists to stop paying for idle
capacity, and against a reservation there is nothing to reclaim. That holds for
the compute charge, which is the dominant term but not the only one — a running
node still draws disks, networking, IP addresses, any per-node licensing and
extensions, and operational attention. Standing capacity is close to free
against a paid-for reservation, not free. The arrangements below follow from
that observation.

**First, one NodePool per member reservation.** A group is a container; the unit
that carries a quantity is the member reservation, which is one VM size in one
placement — a zone, or regional for a zone-less group. A NodePool admitting
several sizes or several placements spans several member reservations, and
nothing keeps it balanced across them — it can place every node into one bucket,
overallocating that member while the others sit idle. So pin the requirements of
a reservation-backed NodePool to exactly one VM size and exactly one placement,
and give a group covering four buckets four NodePools. Only then does a node
count on that pool mean anything about a reserved quantity.

**A worked example.** One group, `prod-eastus`, holding three member
reservations:

| Member | VM size | Placement | Quantity |
| --- | --- | --- | --- |
| 1 | `Standard_D8s_v5` | `eastus-1` | 6 |
| 2 | `Standard_D8s_v5` | `eastus-2` | 6 |
| 3 | `Standard_D16s_v5` | `eastus-1` | 4 |

Three members means three NodePools, whichever option follows. One NodeClass
names the group and is shared by all of them:

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: reserved
spec:
  imageFamily: Ubuntu
  capacityReservationGroupID: /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Compute/capacityReservationGroups/prod-eastus
```

Each example below shows member 1. Members 2 and 3 differ only in name, zone,
VM size, and quantity, except where an option says otherwise. For a regional
group, pin `karpenter.azure.com/placement-scope: regional` rather than a zone;
Azure reports regional nodes with `topology.kubernetes.io/zone: "0"`.

**Stable baseline.** For a reservation bought to guarantee a steady baseline,
give each bucket a static NodePool: `spec.replicas` set to that member's
reserved quantity, requirements pinned to its size and placement. Karpenter
keeps exactly that many nodes running, and three things follow:

- the nodes always exist, so reserved capacity is standing there for the
  scheduler rather than waiting to be provisioned;
- consolidation and emptiness skip static NodePools outright, so a reserved node
  is never taken away to save money that has already been spent;
- configuration changes still replace nodes, through a static-specific drift
  path that preserves the replica count, and node repair still applies.

The replica count fills the reservation whatever pods land on the nodes.
Steering *which* workloads land there is optional, and the usual Kubernetes
question with the usual answers: a pool label with `preferred` node affinity
biases chosen work toward reserved capacity, `required` pins it there, and a
taint with matching tolerations keeps everything else off.

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: reserved-d8s-eastus-1
spec:
  replicas: 6
  limits:
    nodes: "7"           # replicas + 1, so drift can stage a replacement
  template:
    metadata:
      labels:
        capacity-reservation: prod-eastus
    spec:
      nodeClassRef:
        group: karpenter.azure.com
        kind: AKSNodeClass
        name: reserved
      expireAfter: Never
      taints:
      - key: capacity-reservation
        value: prod-eastus
        effect: NoSchedule
      requirements:
      - key: node.kubernetes.io/instance-type
        operator: In
        values: ["Standard_D8s_v5"]
      - key: topology.kubernetes.io/zone
        operator: In
        values: ["eastus-1"]
```

The taint keeps other work off; the workload opts in and is biased toward the
reservation:

```yaml
      tolerations:
      - key: capacity-reservation
        value: prod-eastus
        effect: NoSchedule
      affinity:
        nodeAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 100
            preference:
              matchExpressions:
              - key: capacity-reservation
                operator: In
                values: ["prod-eastus"]
```

Leave room for that drift path. It creates the replacement before removing the
node it replaces, and reserves against `limits.nodes` without bursting over, so
`replicas: N` with `limits.nodes: N` stalls drift indefinitely. Set
`limits.nodes` to at least `N+1`, and expect the member reservation to be
transiently overallocated by one while a replacement and the node it replaces
coexist. That extra VM needs ordinary quota and regional capacity, and for its
duration the member is carrying more VMs than it has reserved units.

Expiration is not skipped for static pools the way consolidation is, and it
defaults to 30 days. It deletes the node and lets the replica count restore it,
so the member is briefly *under*-filled rather than over-filled — the reverse of
drift. `expireAfter: Never` above turns that off.

Burst above the reservation goes to a separate dynamic NodePool with no CRG
association. The trade for the whole arrangement is fixed capacity in a fixed
shape: a workload mix that packs poorly into the reserved sizes leaves units
partly idle.

This is deliberately close to an AKS agent pool associated with the same group,
which is fixed capacity pinned to a reservation and has been available all
along. The difference is whose lifecycle it lives in: the static NodePool uses
the same NodeClass, drift, and repair as the rest of the Karpenter-managed
cluster, so the reserved baseline and the burst around it are configured and
upgraded the same way.

**Simple elastic.** When the baseline is not steady enough to pin a replica
count, make each bucket an ordinary dynamic NodePool instead, and:

- give the reservation-backed pools a higher `weight` than the unassociated
  fallback pool. Karpenter evaluates NodePools by weight and takes the first
  that can host the pod, so weight — not price — is what makes reserved
  capacity the first choice. This applies only when Karpenter has to provision:
  a pod that already fits somewhere never reaches it, so weight biases new
  capacity, and the node affinity above is still what steers workloads;
- cap each pool with `limits.nodes` at that member's reserved quantity, subject
  to the guardrail caveats above;
- set a disruption budget of `0` nodes for the `Underutilized` reason on those
  pools, so a node filling a reserved unit is not consolidated away. This keeps
  occupied units occupied; it does not keep every unit occupied, since empty
  reserved nodes are still consolidated and demand still governs how many exist.
  Guaranteed occupancy is what the static option above buys.

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: reserved-d8s-eastus-1
spec:
  weight: 50             # above the fallback pool, below the D16 pool later on
  limits:
    nodes: "6"           # this member's reserved quantity
  disruption:
    consolidateAfter: 0s   # required whenever disruption is set explicitly
    budgets:
    - reasons: ["Underutilized"]
      nodes: "0"
  template:
    metadata:
      labels:
        capacity-reservation: prod-eastus
    spec:
      nodeClassRef:
        group: karpenter.azure.com
        kind: AKSNodeClass
        name: reserved
      requirements:
      - key: node.kubernetes.io/instance-type
        operator: In
        values: ["Standard_D8s_v5"]
      - key: topology.kubernetes.io/zone
        operator: In
        values: ["eastus-1"]
---
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: fallback
spec:
  weight: 1
  template:
    spec:
      nodeClassRef:
        group: karpenter.azure.com
        kind: AKSNodeClass
        name: default     # no capacityReservationGroupID
      requirements: []   # required field; empty admits anything
```

**Better elastic, with an overlay.** Weight decides which pool is tried first,
but within a pool consolidation still values a reserved node at list price. A
`NodeOverlay` scoped to the reservation-backed pools lowers the price of the
reserved sizes, and because the override is applied to the offering itself,
provisioning and consolidation read the same declared price.

Member 3 shows what that unlocks. `Standard_D16s_v5` costs roughly twice
`Standard_D8s_v5`, and the pools above give every reserved pool the same weight,
so a D8 pool wins the tie and the D16 units go unused. Raise member 3 above the
D8 pools, and add the overlay:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: reserved-d16s-eastus-1
spec:
  weight: 100            # preferred over the D8 pools at weight 50
  limits:
    nodes: "4"
  # abbreviated: disruption and template exactly as the D8 pool above,
  # with Standard_D16s_v5 in place of Standard_D8s_v5
---
apiVersion: karpenter.sh/v1alpha1
kind: NodeOverlay
metadata:
  name: reserved-d16s-eastus-1
spec:
  requirements:
  - key: karpenter.sh/nodepool
    operator: In
    values: ["reserved-d16s-eastus-1"]
  price: "0"             # marginal cost of a prepaid unit
```

The two do different jobs. Weight applies only when Karpenter provisions, so it
governs new nodes: a pod needing capacity gets a D16 node until that pool's
limit is reached, then a D8 one, then fallback.

The overlay governs nodes that already exist. Consolidation replaces a node, or
a group of nodes together, only when the replacement prices strictly below their
combined price. At list price that is not impossible for D16, only narrow: it
can never replace a single D8, costing about twice as much, and it can replace a
group only once their combined price exceeds its own — three D8 nodes or more,
at these prices. Priced at zero, D16 becomes eligible against any
positive-priced set of source nodes whose pods fit, so consolidation can move
work off pay-as-you-go nodes into the reservation.

Which work moves is worth being precise about. The migration source here is the
**fallback** pool, because the reserved D8 pools carry `Underutilized: 0` and a
zero budget removes their nodes from consolidation candidacy altogether — their
work does not move. If consolidating D8 work into D16 is also wanted, drop the
budget from the D8 pools and accept that their reserved nodes may be vacated in
the process.

Note what the overlay is *not* doing here: the `Underutilized: 0` budget already
prevents consolidation away from a reserved D16 node. The overlay is about
moving *into* one.

Agreement is not accuracy. The declared price is an operator's assertion that
these nodes occupy prepaid units, and nothing enforces it: another team,
cluster, or identity consuming the same group can exhaust it while every node in
the pool still carries the declared zero price, even though those nodes are now
overallocated and incurring ordinary incremental compute cost. The overlay
makes both code paths read one number; it does not make that number true.

The overlay complements the previous list rather than replacing it. It does not
remove the need for weight, because price does not order NodePools. And it does
not stop consolidation from deleting a reserved node whose pods happen to fit on
capacity already running: that path compares no prices at all, so no price makes
it safe. Keep the `Underutilized: 0` budget wherever occupied units must stay
occupied. Two further cautions: the override is static and does not track
observed headroom, and it reaches every node of those sizes in scope, so keep
the pool's limit in step when the reservation is resized.

**Optional refinements.** The three options above stand on their own. Each row
below answers a question they leave open; reach for one only when the situation
matches.

| Situation | Add | What it costs |
| --- | --- | --- |
| Workloads must never leave reservation-backed pools. Consolidation re-runs scheduling across every NodePool and every running node, so a cheaper fallback pool, or spare room on a node already up, is a valid destination for a reserved node's pods. | `required` node affinity on those pods, plus a taint on the reserved pools to keep other work out | Fallback stops being transparent: when reserved capacity is unavailable those pods stay pending rather than landing elsewhere. This bounds where work may go, not which nodes stay up — pods can still repack onto another reserved node, so occupancy is what `Underutilized: 0` or static `replicas` preserve |
| A whole pool should stop right-sizing, and per-reason budgets are more configuration than you want to carry. | `consolidationPolicy: WhenEmpty` in place of the `Underutilized: 0` budget | Blunter: the pool gives up right-sizing altogether, not only for its reserved nodes |
| The protection belongs to one workload rather than to a whole pool. | `karpenter.sh/do-not-disrupt` on that workload's pods | Blocks consolidation and drift alike. If the NodePool sets `terminationGracePeriod`, drift may select the node immediately and the annotation only holds during draining, up to that deadline |

`spec.replicas` and `NodeOverlay` are both alpha. They are off by default in the
Helm chart, and on by default in AKS Node Auto Provisioning, so on NAP clusters
the arrangements above need no gate changes.

## What is left for the provider to do

Everything above is operator configuration. The one thing left that the provider
could do on its own, with no core change, is let observed headroom influence
which candidate it launches. That is bounded, for a reason worth stating once.

The provider receives candidates core has already priced and ordered. Were it to
use headroom to pick a *more expensive* reserved size, the node would be created
and labelled `on-demand` at full list price, and consolidation — which replaces
a node only with something strictly cheaper — would see an expensive node and no
reservation. It would replace it, stranding the unit, and the next scale-up
would pick the reserved size again. Provisioning and consolidation would
disagree about the same node indefinitely: the same disagreement an overlay
removes by moving the declared price on both sides at once. Hence:

> The provider may use observed headroom to choose among candidates that core
> prices equally or more cheaply than the one it would otherwise have picked. It
> must not use headroom to select a candidate that core prices higher.

**What that leaves, honestly.** One benefit, conditional on one configuration
choice. A pool pinned to one VM size and one placement offers a single
candidate, so headroom cannot change the pick no matter what it observes — the
operator has already chosen. Steering only has alternatives to choose among when
a NodePool deliberately spans several member reservations.

That is a trade, not a convenience. A multi-member pool gives up per-member
`limits.nodes` and the deterministic occupancy that per-member pools provide; in
exchange, the provider balances across the members it contains. What it balances
with is information the operator does not have: `limits.nodes` counts only the
nodes this cluster creates, while the instance view counts every VM in the
group, including those another team, cluster, or identity created. For a
nonexclusive group — anything this cluster does not solely consume, whether or
not the group is shared across subscriptions — that is the only mechanism that
sees real headroom.

Two things narrow it further. The choice is confined to candidates inside the
price bound, so in practice it balances across members that are priced the same
— the same VM size in different placements, or sizes that happen to match. And
it stays best-effort, because the observation is periodic and Azure decides at
launch.

**Whether to build it.** Not yet. It cannot cross price tiers, so it never
delivers reservation-first provisioning. Its value appears only where a group is
nonexclusive *and* the operator has accepted a multi-member pool, and it is
worth having then only if same-price stranding is actually costing something.

Phase 1 telemetry narrows that question without settling it. Per-member counts
can show member A sitting unused while member B is overallocated, which is
evidence of candidate stranding — but not proof that steering would have helped,
since topology spread, affinity, volume placement, or any other requirement may
have excluded A from the launch in the first place. That aggregate pattern is
enough to decide whether a closer measurement is worth making.

If it is material, gate the feature with a bounded shadow study in the launch
allocator. The allocator can compare the candidate set it already holds with
observed headroom and count low-cardinality outcomes such as
selected-with-headroom and equal-price-headroom-stranded. It need not export
candidate identities, create per-candidate metrics, or make additional Azure
reads; the study may also be sampled, opt-in, or temporary. This is gate work
after Phase 1 finds a material pattern, not a permanent Phase 1 deliverable.

The work itself is cheap — the headroom observation already exists for telemetry
— but it adds a provider-side selection path that has to track core's price
ordering from then on.

The bound itself is a consequence of pricing each node individually at list
price. A pool-level cost model ([Phase 2](#phases)) values a node at zero
marginal cost while it fills a reserved unit, at which point consolidation stops
wanting to replace it and the preference becomes stable across price tiers.

## Constraints and operational notes

| Area | Behavior |
| --- | --- |
| Provisioning backend | The direct VM path can carry the group ID at create time. The AKS Machine path cannot until the per-Machine field is available in a published API version, so a NodeClass that sets the field in Machines mode fails readiness with a distinct reason rather than silently dropping the association. |
| Subscription scope | Initial support is same-subscription Targeted groups. Consuming a group shared in from another subscription is deferred: it adds authorization and logical-to-physical zone translation. A group this cluster owns may still be nonexclusive — other clusters, teams, or identities can allocate against it, and it may be shared out to other subscriptions. |
| Cloud environments | Capacity reservations are documented for Azure Cloud, Azure for Government, and Azure in China. A NodeClass configuring a group elsewhere fails readiness with a stable reason. |
| Placement | A zone-less group is regional and yields only regional offerings; a NodePool or workload requiring a specific zone against a regional group simply has no compatible offering, and never falls back to an unassociated zonal launch. |
| Unsupported combinations | Spot, proximity placement groups, and Ultra Disk are not supported with Azure capacity reservations, so those offerings are not emitted for a configured NodeClass. |
| Changing the group | Adding, changing, or removing the ID drifts affected nodes and replaces them through ordinary Karpenter disruption. Nodes are not mutated in place. The same applies when a VM's actual association is changed outside Karpenter: the mismatch between actual and desired is detected and the node is replaced through drift, not reconciled in place. |
| Enabling the feature | Setting the field on a NodeClass that already has nodes drifts all of them. This is paced by NodePool disruption budgets, and is worth doing on a new NodeClass when that churn is unwelcome. If the existing node count exceeds the reserved quantity, the nodes beyond it drift into overallocation, carrying ordinary incremental compute cost rather than occupying prepaid units. |
| No CRG configured | NodeClasses that do not set the field are unaffected in offering projection, pricing, quota preflight, drift, and failure handling. |
| Scale | Reservation state is refreshed per group on an interval and held in memory. Status carries counts and an observation time, not the allocated VM ID list, which would otherwise grow with the size of the group. |

## Alternatives considered

| Alternative | What it would add | Why not now |
| --- | --- | --- |
| Emit `reserved` from observed headroom, accepting occasional error | Activates core's existing reservation accounting and preference with no core changes | A raced launch still succeeds as overallocated capacity, so the error is silent, and no per-VM signal exists to correct it later. It would also misprice consolidation for as long as the node lives. |
| Exclusive ownership of the group plus a durable claim ledger | Makes `reserved` truthful by construction, by refusing launches that would exceed the quantity | Requires that no other cluster, team, or identity ever allocates into the group, which most real deployments cannot promise. It also refuses VMs Azure would have created, and a strict pool at full utilization cannot drift, consolidate by replacement, or self-heal, because Karpenter creates replacements before removing the nodes they replace. |
| Discount the offering price when headroom is observed | Attracts provisioning toward prepaid capacity without core changes | The same price value is used to evaluate running nodes, so the discount also applies to nodes running beyond the reserved quantity, and a point-in-time observation ends up driving disruption decisions that outlive it. What is rejected here is the provider deriving the discount from a live observation; an operator-declared `NodeOverlay` is the sound form of the same idea, because it is static and explicitly scoped — see [filling a reservation with existing controls](#filling-a-reservation-with-existing-controls). |
| Several groups per NodeClass | Lets one NodePool draw on separately managed reservations | A single group already spans VM sizes and zones, so the extra selection dimension buys little for the common case. Separate NodeClasses cover the rest today. Worth revisiting if reservations genuinely cannot be consolidated. |
| Delegate VM size selection to a future Azure Compute Fleet reservation-first strategy | Server-side placement that prefers unused reserved capacity | No such strategy exists today, and it would deliver launch placement only, not consolidation behavior. Worth watching. |

Two further directions are postponed rather than rejected; both appear in
[phases](#phases) below.

## Phases

| Phase | Content | Sequencing |
| --- | --- | --- |
| **1** | This proposal: association, readiness, offering projection, launch, drift, scoped failures, quota preflight relief, and aggregate reservation telemetry. Direct-VM path first, same-subscription groups. | First increment |
| **1.5** | Optional interim provider-side headroom steering at launch, bounded as described in [what is left for the provider to do](#what-is-left-for-the-provider-to-do). Narrow: it can only balance across equally priced members of a pool that deliberately spans several of them, and complete core support supersedes it. | Build only when multi-member pools over a nonexclusive group are required, Phase 1 telemetry shows material same-price imbalance, **and** a bounded launch shadow study confirms that the stranding was actionable |
| **2** | Upstream core advisory reservation support, with two implementation steps: account for finite reservation capacity independently of the `reserved` capacity type, then evaluate cost for the reservation pool rather than per node. Together they make reservation-first provisioning and reservation-aware consolidation correct and stable while nodes remain `on-demand`. The accounting step may land first, but has little user-visible value alone. | This is the target design whenever elastic reservation optimization is in scope and can start in parallel with Phase 1. If complete support already exists, adopt it; otherwise telemetry informs investment priority, not correctness |
| **3** | Strict reserved placement, if Azure gains a launch mode that fails rather than overallocating, together with per-VM evidence of consumption. Strict then becomes a small provider change on core's existing support rather than a subsystem. | Platform capability exists |

Phase 2 is one product capability, not two independent bets. Finite accounting
provides shared state and carries reservation intent; the pool-level cost model
turns that information into correct provisioning and consolidation decisions.
They should be designed together, even if separate upstream changes make them
easier to review and land safely.

Phase 1 telemetry answers how urgently to fund that work and how much value it
delivers, not whether Phase 2 is the right architecture. If complete core
support existed today, the Azure provider would use it. When elastic reservation
optimization is already a committed requirement, telemetry establishes a
baseline and validates the result; when it is not, observed idle cost and
focused follow-up analysis help rank the investment against other work.

## Open decisions

1. **Target product.** Is the immediate demand guaranteed Targeted capacity for
   a stable fleet, elastic cost optimization over Targeted reservations, or
   scheduled Block reservations? Phase 1 is close to sufficient for the first;
   Phase 2 is the target design for the second; the third needs a separate
   schedule and interruption design.
2. **Optimization scope.** Is association sufficient, is the narrow same-price
   launch bias in Phase 1.5 useful, or is full elastic optimization committed?
   The full outcome includes reservation-first provisioning and
   reservation-aware consolidation together; implementing only one through
   price makes the two control loops disagree.
3. **Overallocation contract.** Is Azure's native On-Demand fallback acceptable?
   This proposal recommends that contract. Strict SLA-backed placement is a
   different outcome and remains conditional on the Phase 3 platform
   capabilities.
4. **Initial sharing scope.** Is same-subscription Targeted support sufficient
   for the first release? Cross-subscription consumption adds authorization and
   physical-zone translation. A same-subscription group may still be
   nonexclusive, which Phase 1 already handles truthfully.
5. **Upstream ownership.** If full elastic optimization is committed, may
   Karpenter core evolve, and who owns the Phase 2 upstream work? The accounting
   and cost-model changes may land separately, but they are one product
   capability.

## External dependencies

| Dependency | Needed for | Effect while unavailable |
| --- | --- | --- |
| Confirmation that no supported API identifies which VMs hold SLA-backed units in an overallocated group | Ratifying the classification and reconciliation design | Until confirmed, proceed conservatively with `on-demand`. A positive answer would revise the reconciliation and classification analysis, but would not alone enable strict reserved placement, which also needs an enforceable launch boundary |
| Publication of the per-Machine CRG field in an AKS API version and SDK | Phase 1 in Machines mode | CRG-configured NodeClasses remain direct-VM only and fail readiness in Machines mode |
| Core acceptance of advisory reservation accounting and a pool-level cost model | Phase 2 | Phase 1 remains correct; full reservation-first provisioning and reservation-aware consolidation are unavailable |
| An Azure reservation-only launch policy plus per-VM consumption evidence | Phase 3 | Nodes remain truthfully `on-demand`; strict reserved placement is unavailable |
