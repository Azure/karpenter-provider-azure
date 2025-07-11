---
title: "NodePools"
linkTitle: "NodePools"
weight: 1
description: >
  Configure Karpenter with NodePools
---

When you first installed Karpenter, you set up a default NodePool. The NodePool sets constraints on the nodes that can be created by Karpenter and the pods that can run on those nodes. The NodePool can be set to do things like:

* Define taints to limit the pods that can run on nodes Karpenter creates
* Define any startup taints to inform Karpenter that it should taint the node initially, but that the taint is temporary.
* Limit node creation to certain zones, instance types, and computer architectures
* Set defaults for node expiration

You can change your NodePool or add other NodePools to Karpenter.
Here are things you should know about NodePools:

* Karpenter won't do anything if there is not at least one NodePool configured.
* Each NodePool that is configured is looped through by Karpenter.
* If Karpenter encounters a taint in the NodePool that is not tolerated by a Pod, Karpenter won't use that NodePool to provision the pod.
* If Karpenter encounters a startup taint in the NodePool it will be applied to nodes that are provisioned, but pods do not need to tolerate the taint.  Karpenter assumes that the taint is temporary and some other system will remove the taint.
* It is recommended to create NodePools that are mutually exclusive. So no Pod should match multiple NodePools. If multiple NodePools are matched, Karpenter will use the NodePool with the highest [weight](#specweight).

For some example `NodePool` configurations, see the [examples in the Karpenter GitHub repository](https://github.com/Azure/karpenter-provider-azure/tree/main/examples/).

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: default
spec:
  # Template section that describes how to template out NodeClaim resources that Karpenter will provision
  # Karpenter will consider this template to be the minimum requirements needed to provision a Node using this NodePool
  # It will overlay this NodePool with Pods that need to schedule to further constrain the NodeClaims
  # Karpenter will provision to launch new Nodes for the cluster
  template:
    metadata:
      # Labels are arbitrary key-values that are applied to all nodes
      labels:
        billing-team: my-team

      # Annotations are arbitrary key-values that are applied to all nodes
      annotations:
        example.com/owner: "my-team"
    spec:
      # References the Cloud Provider's NodeClass resource, see your cloud provider specific documentation
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: default

      # Provisioned nodes will have these taints
      # Taints may prevent pods from scheduling if they are not tolerated by the pod.
      taints:
        - key: example.com/special-taint
          effect: NoSchedule

      # Provisioned nodes will have these taints, but pods do not need to tolerate these taints to be provisioned by this
      # NodePool. These taints are expected to be temporary and some other entity (e.g. a DaemonSet) is responsible for
      # removing the taint after it has finished initializing the node.
      startupTaints:
        - key: example.com/another-taint
          effect: NoSchedule

      # Requirements that constrain the parameters of provisioned nodes.
      # These requirements are combined with pod.spec.topologySpreadConstraints, pod.spec.affinity.nodeAffinity, pod.spec.affinity.podAffinity, and pod.spec.nodeSelector rules.
      # Operators { In, NotIn, Exists, DoesNotExist, Gt, and Lt } are supported.
      # https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#operators
      requirements:
        - key: "karpenter.azure.com/sku-family"
          operator: In
          values: ["D", "E", "F"]
          # minValues here enforces the scheduler to consider at least that number of unique sku-family to schedule the pods.
          # This field is ALPHA and can be dropped or replaced at any time 
          minValues: 2
        - key: "karpenter.azure.com/sku-name"
          operator: In
          values: ["Standard_D2s_v3","Standard_D4s_v3","Standard_E2s_v3","Standard_E4s_v3","Standard_F2s_v2","Standard_F4s_v2"]
          minValues: 5
        - key: "karpenter.azure.com/sku-cpu"
          operator: In
          values: ["2", "4", "8", "16"]
        - key: "karpenter.azure.com/sku-version"
          operator: Gt
          values: ["2"]
        - key: "topology.kubernetes.io/zone"
          operator: In
          values: ["eastus-1", "eastus-2"]
        - key: "kubernetes.io/arch"
          operator: In
          values: ["arm64", "amd64"]
        - key: "karpenter.sh/capacity-type"
          operator: In
          values: ["spot", "on-demand"]

      # ExpireAfter is the duration the controller will wait
      # before terminating a node, measured from when the node is created. This
      # is useful to implement features like eventually consistent node upgrade,
      # memory leak protection, and disruption testing.
      expireAfter: 720h

      # TerminationGracePeriod is the maximum duration the controller will wait before forcefully deleting the pods on a node, measured from when deletion is first initiated.
      # 
      # Warning: this feature takes precedence over a Pod's terminationGracePeriodSeconds value, and bypasses any blocked PDBs or the karpenter.sh/do-not-disrupt annotation.
      # 
      # This field is intended to be used by cluster administrators to enforce that nodes can be cycled within a given time period.
      # When set, drifted nodes will begin draining even if there are pods blocking eviction. Draining will respect PDBs and the do-not-disrupt annotation until the TGP is reached.
      # 
      # Karpenter will preemptively delete pods so their terminationGracePeriodSeconds align with the node's terminationGracePeriod.
      # If a pod would be terminated without being granted its full terminationGracePeriodSeconds prior to the node timeout,
      # that pod will be deleted at T = node timeout - pod terminationGracePeriodSeconds.
      # 
      # The feature can also be used to allow maximum time limits for long-running jobs which can delay node termination with preStop hooks.
      # If left undefined, the controller will wait indefinitely for pods to be drained.
      terminationGracePeriod: 30s

  # Disruption section which describes the ways in which Karpenter can disrupt and replace Nodes
  # Configuration in this section constrains how aggressive Karpenter can be with performing operations
  # like rolling Nodes due to them hitting their maximum lifetime (expiry) or scaling down nodes to reduce cluster cost
  disruption:
    # Describes which types of Nodes Karpenter should consider for consolidation
    # If using 'WhenUnderutilized', Karpenter will consider all nodes for consolidation and attempt to remove or replace Nodes when it discovers that the Node is underutilized and could be changed to reduce cost
    # If using `WhenEmpty`, Karpenter will only consider nodes for consolidation that contain no workload pods
    consolidationPolicy: WhenUnderutilized

    # The amount of time Karpenter should wait after discovering a consolidation decision
    # This value can currently only be set when the consolidationPolicy is 'WhenEmpty'
    # You can choose to disable consolidation entirely by setting the string value 'Never' here
    consolidateAfter: 30s

    # Budgets control the speed Karpenter can scale down nodes.
    # Karpenter will respect the minimum of the currently active budgets, and will round up
    # when considering percentages. Duration and Schedule must be set together.
    budgets:
    - nodes: 10%
    # On Weekdays during business hours, don't do any deprovisioning.
    - schedule: "0 9 * * mon-fri"
      duration: 8h
      nodes: "0"

  # Resource limits constrain the total size of the pool.
  # Limits prevent Karpenter from creating new instances once the limit is exceeded.
  limits:
    cpu: "1000"
    memory: 1000Gi

  # Priority given to the NodePool when the scheduler considers which NodePool
  # to select. Higher weights indicate higher priority when comparing NodePools.
  # Specifying no weight is equivalent to specifying a weight of 0.
  weight: 10
```

## spec.template.spec.requirements

Kubernetes defines the following [Well-Known Labels](https://kubernetes.io/docs/reference/labels-annotations-taints/), and cloud providers (e.g., Azure) implement them. They are defined at the "spec.requirements" section of the NodePool API.

In addition to the well-known labels from Kubernetes, Karpenter supports Azure-specific labels for more advanced scheduling. See the full list [here](../scheduling/#well-known-labels).

These well-known labels may be specified at the NodePool level, or in a workload definition (e.g., nodeSelector on a pod.spec). Nodes are chosen using both the NodePool's and pod's requirements. If there is no overlap, nodes will not be launched. In other words, a pod's requirements must be within the NodePool's requirements. If a requirement is not defined for a well known label, any value available to the cloud provider may be chosen.

For example, an instance type may be specified using a nodeSelector in a pod spec. If the instance type requested is not included in the NodePool list and the NodePool has instance type requirements, Karpenter will not create a node or schedule the pod.

### Well-Known Labels

#### Instance Types

- key: `node.kubernetes.io/instance-type`
- key: `karpenter.azure.com/sku-family`
- key: `karpenter.azure.com/sku-name`
- key: `karpenter.azure.com/sku-version`

Generally, instance types should be a list and not a single value. Leaving these requirements undefined is recommended, as it maximizes choices for efficiently placing pods.

Review [Azure VM sizes](../instance-types). Most VM sizes are supported with the exclusion of specialized sizes that don't support AKS.

#### Availability Zones

- key: `topology.kubernetes.io/zone`
- value example: `eastus-1`
- value list: `az account list-locations --output table`

Karpenter can be configured to create nodes in a particular zone. Note that the Availability Zone `eastus-1` for your Azure subscription might not have the same location as `eastus-1` for another Azure subscription.

[Learn more about Azure Availability Zones.](https://docs.microsoft.com/en-us/azure/availability-zones/)

#### Architecture

- key: `kubernetes.io/arch`
- values
  - `amd64`
  - `arm64`

Karpenter supports `amd64` nodes, and `arm64` nodes.

#### Operating System
 - key: `kubernetes.io/os`
 - values
   - `linux`
   - `windows`

Karpenter supports `linux` and `windows` operating systems.

#### Capacity Type

- key: `karpenter.sh/capacity-type`
- values
  - `spot`
  - `on-demand`

Karpenter supports specifying capacity type, which is analogous to [Azure VM purchase options](https://docs.microsoft.com/en-us/azure/virtual-machines/spot-vms).

Karpenter prioritizes Spot offerings if the NodePool allows Spot and on-demand instances. If the Azure API indicates Spot capacity is unavailable, Karpenter caches that result across all attempts to provision Azure capacity for that instance type and zone for the next 45 seconds. If there are no other possible offerings available for Spot, Karpenter will attempt to provision on-demand instances, generally within milliseconds.

Karpenter also allows `karpenter.sh/capacity-type` to be used as a topology key for enforcing topology-spread.

### Min Values

Along with the combination of [key,operator,values] in the requirements, Karpenter also supports `minValues` in the NodePool requirements block, allowing the scheduler to be aware of user-specified flexibility minimums while scheduling pods to a cluster. If Karpenter cannot meet this minimum flexibility for each key when scheduling a pod, it will fail the scheduling loop for that NodePool, either falling back to another NodePool which meets the pod requirements or failing scheduling the pod altogether.

For example, the below spec will use spot instance type for all provisioned instances and enforces `minValues` to various keys where it is defined
i.e at least 2 unique instance families from [D,E,F], 5 unique sku names, 10 unique instance types is required for scheduling the pods.

```yaml
spec:
  template:
    spec:
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]
        - key: kubernetes.io/os
          operator: In
          values: ["linux"]
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D", "E", "F"]
          minValues: 2
        - key: karpenter.azure.com/sku-name
          operator: Exists
          minValues: 5
        - key: node.kubernetes.io/instance-type
          operator: Exists
          minValues: 10
        - key: karpenter.azure.com/sku-version
          operator: Gt
          values: ["2"]
```

Note that `minValues` can be used with multiple operators and multiple requirements. And if the `minValues` are defined with multiple operators for the same requirement key, scheduler considers the max of all the `minValues` for that requirement. For example, the below spec requires scheduler to consider at least 5 sku-name to schedule the pods.

```yaml
spec:
  template:
    spec:
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]
        - key: kubernetes.io/os
          operator: In
          values: ["linux"]
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D", "E", "F"]
          minValues: 2
        - key: karpenter.azure.com/sku-name
          operator: Exists
          minValues: 5
        - key: karpenter.azure.com/sku-name
          operator: In
          values: ["Standard_D2s_v3","Standard_D4s_v3","Standard_E2s_v3","Standard_E4s_v3","Standard_F2s_v2","Standard_F4s_v2"]
          minValues: 3
        - key: node.kubernetes.io/instance-type
          operator: Exists
          minValues: 10
        - key: karpenter.azure.com/sku-version
          operator: Gt
          values: ["2"]
```

{{% alert title="Recommended" color="primary" %}}
Karpenter allows you to be extremely flexible with your NodePools by only constraining your instance types in ways that are absolutely necessary for your cluster. By default, Karpenter will enforce that you specify the `spec.template.spec.requirements` field, but will not enforce that you specify any requirements within the field. If you choose to specify `requirements: []`, this means that you will completely flexible to _all_ instance types that your cloud provider supports.

Though Karpenter doesn't enforce these defaults, for most use-cases, we recommend that you specify _some_ requirements to avoid odd behavior or exotic instance types. Below, is a high-level recommendation for requirements that should fit the majority of use-cases for generic workloads

```yaml
spec:
  template:
    spec:
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]
        - key: kubernetes.io/os
          operator: In
          values: ["linux"]
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["on-demand"]
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D", "E", "F"]
        - key: karpenter.azure.com/sku-version
          operator: Gt
          values: ["2"]
```

{{% /alert %}}

## spec.template.spec.nodeClassRef

This field points to the Cloud Provider NodeClass resource. Learn more about [AKSNodeClasses]({{<ref "nodeclasses" >}}).

## spec.template.spec.expireAfter

`expireAfter` is the duration the controller will wait before terminating a node, measured from when the node is created. This is useful to implement features like eventually consistent node upgrade, memory leak protection, and disruption testing.

The default value is `720h` (30 days). You can disable node expiration by setting the value to `Never`.

```yaml
spec:
  template:
    spec:
      expireAfter: 24h  # Expire nodes after 24 hours
```

## spec.template.spec.terminationGracePeriod

`terminationGracePeriod` is the maximum duration the controller will wait before forcefully deleting the pods on a node, measured from when deletion is first initiated.

{{% alert title="Warning" color="warning" %}}
This feature takes precedence over a Pod's terminationGracePeriodSeconds value, and bypasses any blocked PDBs or the karpenter.sh/do-not-disrupt annotation.
{{% /alert %}}

This field is intended to be used by cluster administrators to enforce that nodes can be cycled within a given time period. When set, drifted nodes will begin draining even if there are pods blocking eviction. Draining will respect PDBs and the do-not-disrupt annotation until the TGP is reached.

If left undefined, the controller will wait indefinitely for pods to be drained.

```yaml
spec:
  template:
    spec:
      terminationGracePeriod: 30s
```

## spec.disruption

You can configure Karpenter to disrupt Nodes through your NodePool in multiple ways. You can use `spec.disruption.consolidationPolicy`, `spec.disruption.consolidateAfter` or `spec.disruption.expireAfter`. Read [Disruption]({{<ref "disruption" >}}) for more.

## spec.limits

The NodePool spec includes a limits section (`spec.limits`), which constrains the maximum amount of resources that the NodePool will manage.

Karpenter supports limits of any resource type reported by your cloudprovider. It limits instance types when scheduling to those that will not exceed the specified limits.  If a limit has been exceeded, nodes provisioning is prevented until some nodes have been terminated.

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: default
spec:
  template:
    spec:
      requirements:
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["spot"]
  limits:
    cpu: 1000
    memory: 1000Gi
    nvidia.com/gpu: 2
```

{{% alert title="Note" color="primary" %}}
Karpenter provisioning is highly parallel. Because of this, limit checking is eventually consistent, which can result in overrun during rapid scale outs.
{{% /alert %}}

CPU limits are described with a `DecimalSI` value. Note that the Kubernetes API will coerce this into a string, so we recommend against using integers to avoid GitOps skew.

Memory limits are described with a [`BinarySI` value, such as 1000Gi.](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#meaning-of-memory)

You can view the current consumption of cpu and memory on your cluster by running:
```
kubectl get nodepool -o=jsonpath='{.items[0].status}'
```

Review the [Kubernetes core API](https://github.com/kubernetes/api/blob/37748cca582229600a3599b40e9a82a951d8bbbf/core/v1/resource.go#L23) (`k8s.io/api/core/v1`) for more information on `resources`.

## spec.weight

Karpenter allows you to describe NodePool preferences through a `weight` mechanism similar to how weight is described with [pod and node affinities](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#affinity-and-anti-affinity).

For more information on weighting NodePools, see the [Weighted NodePools section]({{<ref "scheduling#weighted-nodepools" >}}) in the scheduling docs.

## Examples

### Isolating Expensive Hardware

A NodePool can be set up to only provision nodes on particular processor types.
The following example sets a taint that only allows pods with tolerations for Nvidia GPUs to be scheduled:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: gpu
spec:
  disruption:
    consolidationPolicy: WhenUnderutilized
  template:
    spec:
      requirements:
      - key: node.kubernetes.io/instance-type
        operator: In
        values: ["Standard_NC6s_v3", "Standard_NC12s_v3"]
      taints:
      - key: nvidia.com/gpu
        value: "true"
        effect: NoSchedule
```
In order for a pod to run on a node defined in this NodePool, it must tolerate `nvidia.com/gpu` in its pod spec.

### Cilium Startup Taint

Per the Cilium [docs](https://docs.cilium.io/en/stable/installation/taints/#taint-effects), it's recommended to place a taint of `node.cilium.io/agent-not-ready=true:NoExecute` on nodes to allow Cilium to configure networking prior to other pods starting.  This can be accomplished via the use of Karpenter `startupTaints`.  These taints are placed on the node, but pods aren't required to tolerate these taints to be considered for provisioning.

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: cilium-startup
spec:
  disruption:
    consolidationPolicy: WhenUnderutilized
  template:
    spec:
      startupTaints:
      - key: node.cilium.io/agent-not-ready
        value: "true"
        effect: NoExecute
```