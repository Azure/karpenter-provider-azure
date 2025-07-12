---
title: "Scheduling"
linkTitle: "Scheduling"
weight: 3
description: >
  Learn about scheduling workloads with Karpenter
---

If your pods have no requirements for how or where to run, you can let Karpenter choose nodes from the full range of available cloud provider resources.
However, by taking advantage of Karpenter's model of layered constraints, you can be sure that the precise type and amount of resources needed are available to your pods.
Reasons for constraining where your pods run could include:

* Needing to run in zones where dependent applications or storage are available
* Requiring certain kinds of processors or other hardware
* Wanting to use techniques like topology spread to help ensure high availability

Your Cloud Provider defines the first layer of constraints, including all instance types, architectures, zones, and purchase types available to its cloud.
The cluster administrator adds the next layer of constraints by creating one or more NodePools.
The final layer comes from you adding specifications to your Kubernetes pod deployments.
Pod scheduling constraints must fall within a NodePool's constraints or the pods will not deploy.
For example, if the NodePool sets limits that allow only a particular zone to be used, and a pod asks for a different zone, it will not be scheduled.

Constraints you can request include:

* **Resource requests**: Request that certain amount of memory or CPU be available.
* **Node selection**: Choose to run on a node that is has a particular label (`nodeSelector`).
* **Node affinity**: Draws a pod to run on nodes with particular attributes (affinity).
* **Topology spread**: Use topology spread to help ensure availability of the application.
* **Pod affinity/anti-affinity**: Draws pods towards or away from topology domains based on the scheduling of other pods.

Karpenter supports standard Kubernetes scheduling constraints.
This allows you to define a single set of rules that apply to both existing and provisioned capacity.

{{% alert title="Note" color="primary" %}}
Karpenter supports specific [Well-Known Labels, Annotations and Taints](https://kubernetes.io/docs/reference/labels-annotations-taints/) that are useful for scheduling.
{{% /alert %}}

## Resource requests

Within a Pod spec, you can both make requests and set limits on resources a pod needs, such as CPU and memory.
For example:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: myapp
spec:
  containers:
  - name: app
    image: myimage
    resources:
      requests:
        memory: "128Mi"
        cpu: "500m"
      limits:
        memory: "256Mi"
        cpu: "1000m"
```
In this example, the container is requesting 128MiB of memory and .5 CPU.
Its limits are set to 256MiB of memory and 1 CPU.
Instance type selection math only uses `requests`, but `limits` may be configured to enable resource oversubscription.


See [Managing Resources for Containers](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/) for details on resource types supported by Kubernetes, [Specify a memory request and a memory limit](https://kubernetes.io/docs/tasks/configure-pod-container/assign-memory-resource/#specify-a-memory-request-and-a-memory-limit) for examples of memory requests, and [NodePools]({{<ref "./nodepools" >}}) for a list of supported resources.

### Accelerators/GPU Resources

Accelerator (e.g., GPU) values include
- `nvidia.com/gpu`

Karpenter supports accelerators, such as GPUs.

Additionally, include a resource requirement in the workload manifest. This will cause the GPU dependent pod to be scheduled onto the appropriate node.

Here is an example of an accelerator resource in a workload manifest (e.g., pod):

```yaml
spec:
  template:
    spec:
      containers:
      - resources:
          limits:
            nvidia.com/gpu: "1"
```
{{% alert title="Note" color="primary" %}}
If you are provisioning GPU nodes, you need to deploy an appropriate GPU device plugin daemonset for those nodes.
Without the daemonset running, Karpenter will not see those nodes as initialized.
Refer to general [Kubernetes GPU](https://kubernetes.io/docs/tasks/manage-gpus/scheduling-gpus/) docs and the following specific GPU docs:
* `nvidia.com/gpu`: [NVIDIA device plugin for Kubernetes](https://github.com/NVIDIA/k8s-device-plugin)
  {{% /alert %}}

## Selecting nodes

With `nodeSelector` you can ask for a node that matches selected key-value pairs.
This can include well-known labels or custom labels you create yourself.

You can use `affinity` to define more complicated constraints, see [Node Affinity](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#node-affinity) for the complete specification.

### Labels
Well-known labels may be specified as NodePool requirements or pod scheduling constraints. You can also define your own custom labels by specifying `requirements` or `labels` on your NodePool and select them using `nodeAffinity` or `nodeSelectors` on your Pods.

{{% alert title="Warning" color="warning" %}}
Take care to ensure the label domains are correct. A well known label like `karpenter.azure.com/sku-family` will enforce node properties, but may be confused with `node.kubernetes.io/instance-family`, which is unknown to Karpenter, and treated as a custom label which will not enforce node properties.
{{% /alert %}}

#### Well-Known Labels

| Label                                                          | Example           | Description                                                                                                                                                     |
| -------------------------------------------------------------- | ----------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| topology.kubernetes.io/zone                                    | eastus-1          | Zones are defined by your cloud provider ([azure](https://docs.microsoft.com/en-us/azure/availability-zones/))                                               |
| node.kubernetes.io/instance-type                               | Standard_D4s_v3   | Instance types are defined by your cloud provider ([azure](https://docs.microsoft.com/en-us/azure/virtual-machines/sizes))                                   |
| node.kubernetes.io/windows-build                               | 10.0.17763        | Windows OS build in the format "MajorVersion.MinorVersion.BuildNumber". Can be `10.0.17763` for WS2019, or `10.0.20348` for WS2022. ([k8s](https://kubernetes.io/docs/reference/labels-annotations-taints/#nodekubernetesiowindows-build)) |
| kubernetes.io/os                                               | linux             | Operating systems are defined by [GOOS values](https://github.com/golang/go/blob/master/src/go/build/syslist.go#L10) on the instance                            |
| kubernetes.io/arch                                             | amd64             | Architectures are defined by [GOARCH values](https://github.com/golang/go/blob/master/src/go/build/syslist.go#L50) on the instance                              |
| karpenter.sh/capacity-type                                     | spot              | Capacity types include `spot`, `on-demand`                                                                                                                      |
| karpenter.azure.com/sku-name                                   | Standard_D4s_v3   | [Azure Specific] Azure VM SKU name                                                                                                                             |
| karpenter.azure.com/sku-family                                 | D                 | [Azure Specific] VM SKU family, usually the letter designation before the generation number                                                                    |
| karpenter.azure.com/sku-version                                | 3                 | [Azure Specific] VM SKU version number within a SKU family                                                                                                     |
| karpenter.azure.com/sku-cpu                                    | 4                 | [Azure Specific] Number of vCPUs on the VM                                                                                                                     |
| karpenter.azure.com/sku-memory                                 | 16384             | [Azure Specific] Number of mebibytes of memory on the VM                                                                                                       |
| karpenter.azure.com/sku-networking-accelerated                 | true              | [Azure Specific] Whether the VM supports accelerated networking                                                                                                |
| karpenter.azure.com/sku-storage-premium-capable                | true              | [Azure Specific] Whether the VM supports premium storage                                                                                                       |
| karpenter.azure.com/sku-storage-ephemeralos-maxsize            | 131072            | [Azure Specific] Maximum size in MiB for ephemeral OS disk                                                                                                     |
| karpenter.azure.com/sku-gpu-name                               | V100              | [Azure Specific] Name of the GPU on the VM, if available                                                                                                       |
| karpenter.azure.com/sku-gpu-manufacturer                       | nvidia            | [Azure Specific] Name of the GPU manufacturer                                                                                                                  |
| karpenter.azure.com/sku-gpu-count                              | 1                 | [Azure Specific] Number of GPUs on the VM                                                                                                                      |

{{% alert title="Note" color="primary" %}}
Karpenter translates the following deprecated labels to their stable equivalents: `failure-domain.beta.kubernetes.io/zone`, `failure-domain.beta.kubernetes.io/region`, `beta.kubernetes.io/arch`, `beta.kubernetes.io/os`, and `beta.kubernetes.io/instance-type`.
{{% /alert %}}

#### User-Defined Labels

Karpenter is aware of several well-known labels, deriving them from instance type details. If you specify a `nodeSelector` or a required `nodeAffinity` using a label that is not well-known to Karpenter, it will not launch nodes with these labels and pods will remain pending. For Karpenter to become aware that it can schedule for these labels, you must specify the label in the NodePool requirements with the `Exists` operator:

```yaml
requirements:
  - key: user.defined.label/type
    operator: Exists
```

#### Node selectors

Here is an example of a `nodeSelector` for selecting nodes:

```yaml
nodeSelector:
  topology.kubernetes.io/zone: eastus-1
  karpenter.sh/capacity-type: spot
```
This example features a well-known label (`topology.kubernetes.io/zone`) and a label that is well known to Karpenter (`karpenter.sh/capacity-type`).

If you want to create a custom label, you should do that at the NodePool level.
Then the pod can declare that custom label.


See [nodeSelector](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#nodeselector) in the Kubernetes documentation for details.

### Node affinity

Examples below illustrate how to use Node affinity to include (`In`) and exclude (`NotIn`) objects.
See [Node affinity](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#node-affinity) for details.
When setting rules, the following Node affinity types define how hard or soft each rule is:

* **requiredDuringSchedulingIgnoredDuringExecution**: This is a hard rule that must be met.
* **preferredDuringSchedulingIgnoredDuringExecution**: This is a preference, but the pod can run on a node where it is not guaranteed.

The `IgnoredDuringExecution` part of each tells the pod to keep running, even if conditions change on the node so the rules no longer matched.
You can think of these concepts as `required` and `preferred`, since Kubernetes never implemented other variants of these rules.

All examples below assume that the NodePool doesn't have constraints to prevent those zones from being used. The first constraint says you could use `eastus-1` or `eastus-2`, the second constraint makes it so only `eastus-2` can be used.

```yaml
 affinity:
   nodeAffinity:
     requiredDuringSchedulingIgnoredDuringExecution:
       nodeSelectorTerms:
         - matchExpressions:
           - key: "topology.kubernetes.io/zone"
             operator: "In"
             values: ["eastus-1", "eastus-2"]
           - key: "topology.kubernetes.io/zone"
             operator: "In"
             values: ["eastus-2"]
```

Changing the second operator to `NotIn` would allow the pod to run in `eastus-1` only:

```yaml
           - key: "topology.kubernetes.io/zone"
             operator: "In"
             values: ["eastus-1", "eastus-2"]
           - key: "topology.kubernetes.io/zone"
             operator: "NotIn"
             values: ["eastus-2"]
```

Continuing to add to the example, `nodeAffinity` lets you define terms so if one term doesn't work it goes to the next one.
Here, if `eastus-1` is not available, the second term will cause the pod to run on a spot instance in `eastus-3`.


```yaml
 affinity:
   nodeAffinity:
     requiredDuringSchedulingIgnoredDuringExecution:
       nodeSelectorTerms:
         - matchExpressions: # OR
           - key: "topology.kubernetes.io/zone" # AND
             operator: "In"
             values: ["eastus-1", "eastus-2"]
           - key: "topology.kubernetes.io/zone" # AND
             operator: "NotIn"
             values: ["eastus-2"]
         - matchExpressions: # OR
           - key: "karpenter.sh/capacity-type" # AND
             operator: "In"
             values: ["spot"]
           - key: "topology.kubernetes.io/zone" # AND
             operator: "In"
             values: ["eastus-3"]
```

## Taints and tolerations

Taints are used to prevent pods from being scheduled onto nodes. Tolerations are used to allow pods to be scheduled on nodes with matching taints.

See [Taints and Tolerations](https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/) for details.

You can use taints to dedicate nodes for specific uses. For example, if you want some nodes to be used exclusively for jobs that require GPUs, you can taint those nodes and have only the workloads that need the GPUs run on those nodes.

Example of using a taint on a NodePool:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: gpu-nodepool
spec:
  template:
    spec:
      requirements:
        - key: karpenter.azure.com/sku-gpu-count
          operator: Gt
          values: ["0"]
      taints:
        - key: example.com/special-use
          value: gpu
          effect: NoSchedule
```

For a pod to run on this tainted node, it would need a matching toleration:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: gpu-pod
spec:
  tolerations:
  - key: example.com/special-use
    operator: Equal
    value: gpu
    effect: NoSchedule
  containers:
  - name: gpu-container
    image: nvidia/cuda
    resources:
      limits:
        nvidia.com/gpu: 1
```

## Topology spread

You can use `topologySpreadConstraints` to encourage or discourage scheduling across various dimensions like zones, nodes, and more.

See [Pod Topology Spread Constraints](https://kubernetes.io/docs/concepts/workloads/pods/pod-topology-spread-constraints/) for details.

Example of topology spread that encourages even distribution across zones:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web-app
spec:
  replicas: 6
  selector:
    matchLabels:
      app: web-app
  template:
    metadata:
      labels:
        app: web-app
    spec:
      topologySpreadConstraints:
      - maxSkew: 1
        topologyKey: topology.kubernetes.io/zone
        whenUnsatisfiable: DoNotSchedule
        labelSelector:
          matchLabels:
            app: web-app
      containers:
      - name: web-app
        image: nginx
```

## Pod affinity and anti-affinity

Pod affinity and anti-affinity allow you to schedule pods based on the labels of other pods that are already running on nodes.

See [Inter-pod affinity and anti-affinity](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#inter-pod-affinity-and-anti-affinity) for details.

Example of pod anti-affinity to spread pods across different zones:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web-server
spec:
  selector:
    matchLabels:
      app: web-store
  replicas: 3
  template:
    metadata:
      labels:
        app: web-store
    spec:
      affinity:
        podAntiAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
          - labelSelector:
              matchExpressions:
              - key: app
                operator: In
                values:
                - web-store
            topologyKey: "topology.kubernetes.io/zone"
      containers:
      - name: web-app
        image: nginx:1.16-alpine
```

## Weighted NodePools

If multiple NodePools are available for a pod, Karpenter will use the NodePool with the highest weight. If no weight is specified, the NodePool has a weight of 0.

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: preferred-nodepool
spec:
  weight: 100
  template:
    spec:
      requirements:
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D"]
```

This allows you to prefer certain types of instances while still allowing Karpenter to fall back to other NodePools if the preferred options are not available.