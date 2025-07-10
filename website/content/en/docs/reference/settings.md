---
title: "Settings"
linkTitle: "Settings"
weight: 5
description: >
  Configure Karpenter
---

Karpenter surfaces environment variables and CLI parameters to allow you to configure certain global settings on the controllers. These settings are described below.

| Environment Variable | CLI Flag | Description |
|--|--|--|
| AZURE_CLIENT_ID | \-\-azure-client-id | Azure client ID for authentication. Used when authenticating with Azure APIs. |
| AZURE_TENANT_ID | \-\-azure-tenant-id | Azure tenant ID for authentication. |
| AZURE_SUBSCRIPTION_ID | \-\-azure-subscription-id | [REQUIRED] Azure subscription ID where resources will be created. |
| AZURE_RESOURCE_GROUP | \-\-azure-resource-group | [REQUIRED] Azure resource group name where the AKS cluster resides. |
| AZURE_NODE_RESOURCE_GROUP | \-\-azure-node-resource-group | Azure node resource group name where VMs will be created. If not specified, defaults to the MC_ resource group created by AKS. |
| AZURE_LOCATION | \-\-azure-location | [REQUIRED] Azure region/location where resources will be created. |
| AZURE_VNET_SUBNET_ID | \-\-azure-vnet-subnet-id | [REQUIRED] Azure VNet subnet ID for node network interfaces. |
| BATCH_IDLE_DURATION | \-\-batch-idle-duration | The maximum amount of time with no new pending pods that if exceeded ends the current batching window. If pods arrive faster than this time, the batching window will be extended up to the maxDuration. If they arrive slower, the pods will be batched separately. (default = 1s)|
| BATCH_MAX_DURATION | \-\-batch-max-duration | The maximum length of a batch window. The longer this is, the more pods we can consider for provisioning at one time which usually results in fewer but larger nodes. (default = 10s)|
| CLUSTER_CA_BUNDLE | \-\-cluster-ca-bundle | Cluster CA bundle for nodes to use for TLS connections with the API server. If not set, this is taken from the controller's TLS configuration.|
| CLUSTER_ENDPOINT | \-\-cluster-endpoint | The external kubernetes cluster endpoint for new nodes to connect with. If not specified, will discover the cluster endpoint using Azure APIs.|
| CLUSTER_NAME | \-\-cluster-name | [REQUIRED] The kubernetes cluster name for resource discovery.|
| DISABLE_WEBHOOK | \-\-disable-webhook | Disable the admission and validation webhooks|
| ENABLE_PROFILING | \-\-enable-profiling | Enable the profiling on the metric endpoint|
| FEATURE_GATES | \-\-feature-gates | Optional features can be enabled / disabled using feature gates. Current options are: Drift,SpotToSpotConsolidation (default = Drift=true,SpotToSpotConsolidation=false)|
| HEALTH_PROBE_PORT | \-\-health-probe-port | The port the health probe endpoint binds to for reporting controller health (default = 8081)|
| INTERRUPTION_QUEUE | \-\-interruption-queue | Azure Service Bus queue name for spot interruption notifications. Interruption handling is disabled if not specified.|
| KARPENTER_SERVICE | \-\-karpenter-service | The Karpenter Service name for the dynamic webhook certificate|
| KUBE_CLIENT_BURST | \-\-kube-client-burst | The maximum allowed burst of queries to the kube-apiserver (default = 300)|
| KUBE_CLIENT_QPS | \-\-kube-client-qps | The smoothed rate of qps to kube-apiserver (default = 200)|
| LEADER_ELECT | \-\-leader-elect | Start leader election client and gain leadership before executing the main loop. Enable this when running replicated components for high availability.|
| LOG_LEVEL | \-\-log-level | Log verbosity level. Can be one of 'debug', 'info', or 'error' (default = info)|
| MEMORY_LIMIT | \-\-memory-limit | Memory limit on the container running the controller. The GC soft memory limit is set to 90% of this value. (default = -1)|
| METRICS_PORT | \-\-metrics-port | The port the metric endpoint binds to for operating metrics about the controller itself (default = 8000)|
| VM_MEMORY_OVERHEAD_PERCENT | \-\-vm-memory-overhead-percent | The VM memory overhead as a percent that will be subtracted from the total memory for all instance types. (default = 0.075)|
| WEBHOOK_METRICS_PORT | \-\-webhook-metrics-port | The port the webhook metric endpoing binds to for operating metrics about the webhook (default = 8001)|
| WEBHOOK_PORT | \-\-webhook-port | The port the webhook endpoint binds to for validation and mutation of resources (default = 8443)|

### Feature Gates

Karpenter uses [feature gates](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/#feature-gates-for-alpha-or-beta-features) You can enable the feature gates through the `--feature-gates` CLI environment variable or the `FEATURE_GATES` environment variable in the Karpenter deployment. For example, you can configure drift, spotToSpotConsolidation by setting the CLI argument: `--feature-gates Drift=true,SpotToSpotConsolidation=true`.

| Feature                 | Default | Stage | Since   | Until   |
|-------------------------|---------|-------|---------|---------|
| Drift                   | false   | Alpha | v0.21.x | v0.32.x |
| Drift                   | true    | Beta  | v0.33.x |         |
| SpotToSpotConsolidation | false   | Beta  | v0.34.x |         |

### Batching Parameters

The batching parameters control how Karpenter batches an incoming stream of pending pods.  Reducing these values may trade off a slightly faster time from pending pod to node launch, in exchange for launching smaller nodes.  Increasing the values can do the inverse.  Karpenter provides reasonable defaults for these values, but if you have specific knowledge about your workloads you can tweak these parameters to match the expected rate of incoming pods.

For a standard deployment scale-up, the pods arrive at the QPS setting of the `kube-controller-manager`, and the default values are typically fine.  These settings are intended for use cases where other systems may create large numbers of pods over a period of many seconds or minutes and there is a desire to batch them together.

#### Batch Idle Duration

The batch idle duration duration is the period of time that a new pending pod extends the current batching window. This can be increased to handle scenarios where pods arrive slower than one second part, but it would be preferable if they were batched together onto a single larger node.

This value is expressed as a string value like `10s`, `1m` or `2h45m`. The valid time units are `ns`, `us` (or `µs`), `ms`, `s`, `m`, `h`.

#### Batch Max Duration

The batch max duration is the maximum period of time a batching window can be extended to. Increasing this value will allow the maximum batch window size to increase to collect more pending pods into a single batch at the expense of a longer delay from when the first pending pod was created.

This value is expressed as a string value like `10s`, `1m` or `2h45m`. The valid time units are `ns`, `us` (or `µs`), `ms`, `s`, `m`, `h`.

### Azure-Specific Configuration

#### Authentication

Karpenter for Azure supports multiple authentication methods:

1. **Managed Identity** (Recommended): Use Azure Managed Identity assigned to the AKS cluster
2. **Service Principal**: Use Azure Service Principal with client ID and secret
3. **Azure CLI**: Use Azure CLI authentication (for development only)

#### Resource Groups

Azure organizes resources into resource groups. Karpenter needs to know:
- **Cluster Resource Group**: Where the AKS cluster control plane resources are located
- **Node Resource Group**: Where the VM instances will be created (usually the MC_ resource group)

#### Networking

Karpenter requires network configuration:
- **VNet Subnet ID**: The subnet where node NICs will be created
- **Network Security Groups**: Applied automatically based on AKS cluster configuration

#### Interruption Handling

For Azure Spot VM interruption handling, configure:
- **Azure Service Bus**: Queue for receiving spot interruption notifications
- **Event Grid**: Subscription to forward Azure platform events to Service Bus