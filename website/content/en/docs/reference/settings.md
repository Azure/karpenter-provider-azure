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
| BATCH_IDLE_DURATION | \-\-batch-idle-duration | The maximum amount of time with no new pending pods that if exceeded ends the current batching window. If pods arrive faster than this time, the batching window will be extended up to the maxDuration. If they arrive slower, the pods will be batched separately. (default = 1s)|
| BATCH_MAX_DURATION | \-\-batch-max-duration | The maximum length of a batch window. The longer this is, the more pods we can consider for provisioning at one time which usually results in fewer but larger nodes. (default = 10s)|
| CLUSTER_CA_BUNDLE | \-\-cluster-ca-bundle | Cluster CA bundle for nodes to use for TLS connections with the API server. If not set, this is taken from the controller's TLS configuration.|
| CLUSTER_ENDPOINT | \-\-cluster-endpoint | [REQUIRED] The external kubernetes cluster endpoint for new nodes to connect with.|
| CLUSTER_NAME | \-\-cluster-name | [REQUIRED] The kubernetes cluster name for resource tags.|
| DISABLE_WEBHOOK | \-\-disable-webhook | Disable the admission and validation webhooks|
| ENABLE_PROFILING | \-\-enable-profiling | Enable the profiling on the metric endpoint|
| FEATURE_GATES | \-\-feature-gates | Optional features can be enabled / disabled using feature gates. Current options are: Drift,SpotToSpotConsolidation (default = Drift=true,SpotToSpotConsolidation=false)|
| HEALTH_PROBE_PORT | \-\-health-probe-port | The port the health probe endpoint binds to for reporting controller health (default = 8081)|
| KARPENTER_SERVICE | \-\-karpenter-service | The Karpenter Service name for the dynamic webhook certificate|
| KUBE_CLIENT_BURST | \-\-kube-client-burst | The maximum allowed burst of queries to the kube-apiserver (default = 300)|
| KUBE_CLIENT_QPS | \-\-kube-client-qps | The smoothed rate of qps to kube-apiserver (default = 200)|
| KUBELET_BOOTSTRAP_TOKEN | \-\-kubelet-bootstrap-token | [REQUIRED] The bootstrap token for new nodes to join the cluster.|
| KUBELET_IDENTITY_CLIENT_ID | \-\-kubelet-identity-client-id | The client ID of the kubelet identity.|
| LEADER_ELECT | \-\-leader-elect | Start leader election client and gain leadership before executing the main loop. Enable this when running replicated components for high availability.|
| LINUX_ADMIN_USERNAME | \-\-linux-admin-username | The admin username for Linux VMs. (default = azureuser)|
| LOG_LEVEL | \-\-log-level | Log verbosity level. Can be one of 'debug', 'info', or 'error' (default = info)|
| MEMORY_LIMIT | \-\-memory-limit | Memory limit on the container running the controller. The GC soft memory limit is set to 90% of this value. (default = -1)|
| METRICS_PORT | \-\-metrics-port | The port the metric endpoint binds to for operating metrics about the controller itself (default = 8000)|
| NETWORK_DATAPLANE | \-\-network-dataplane | The network dataplane used by the cluster. (default = cilium)|
| NETWORK_PLUGIN | \-\-network-plugin | The network plugin used by the cluster. (default = azure)|
| NETWORK_PLUGIN_MODE | \-\-network-plugin-mode | Network plugin mode of the cluster. (default = overlay)|
| NETWORK_POLICY | \-\-network-policy | The network policy used by the cluster.|
| NODEBOOTSTRAPPING_SERVER_URL | \-\-nodebootstrapping-server-url | [UNSUPPORTED] The url for the node bootstrapping provider server.|
| NODE_IDENTITIES | \-\-node-identities | User assigned identities for nodes.|
| AZURE_NODE_RESOURCE_GROUP | \-\-node-resource-group | [REQUIRED] The resource group created and managed by AKS where the nodes live|
| PROVISION_MODE | \-\-provision-mode | [UNSUPPORTED] The provision mode for the cluster. (default = AKS-scriptless)|
| SIG_ACCESS_TOKEN_SCOPE | \-\-sig-access-token-scope | The scope for the SIG access token. Only used for AKS managed karpenter. UseSIG must be set to true for this to take effect.|
| SIG_ACCESS_TOKEN_SERVER_URL | \-\-sig-access-token-server-url | The URL for the SIG access token server. Only used for AKS managed karpenter. UseSIG must be set to true for this to take effect.|
| SIG_SUBSCRIPTION_ID | \-\-sig-subscription-id | The subscription ID of the shared image gallery.|
| SSH_PUBLIC_KEY | \-\-ssh-public-key | [REQUIRED] VM SSH public key.|
| USE_SIG | \-\-use-sig | If set to true karpenter will use the AKS managed shared image galleries and the node image versions api. If set to false karpenter will use community image galleries. Only a subset of image features will be available in the community image galleries and this flag is only for the managed node provisioning addon. (default = false)|
| VNET_GUID | \-\-vnet-guid | The vnet guid of the clusters vnet, only required by azure cni with overlay + byo vnet|
| VNET_SUBNET_ID | \-\-vnet-subnet-id | [REQUIRED] The default subnet ID to use for new nodes. This must be a valid ARM resource ID for subnet that does not overlap with the service CIDR or the pod CIDR.|
| VM_MEMORY_OVERHEAD_PERCENT | \-\-vm-memory-overhead-percent | The VM memory overhead as a percent that will be subtracted from the total memory for all instance types. (default = 0.075)|
| WEBHOOK_METRICS_PORT | \-\-webhook-metrics-port | The port the webhook metric endpoint binds to for operating metrics about the webhook (default = 8001)|
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

Karpenter for Azure supports Azure Managed Identity and Service Principal authentication. The kubelet identity client ID is used for node authentication with Azure APIs.

#### Cluster Configuration

Required cluster settings include:
- **CLUSTER_NAME**: The AKS cluster name for resource tagging
- **CLUSTER_ENDPOINT**: The Kubernetes API server endpoint
- **AZURE_NODE_RESOURCE_GROUP**: The MC_ resource group where VMs are created

#### Networking

Network configuration options:
- **VNET_SUBNET_ID**: Required subnet ID for node network interfaces
- **VNET_GUID**: VNet GUID for Azure CNI with overlay + BYO VNet scenarios
- **NETWORK_PLUGIN**: Network plugin (default: azure)
- **NETWORK_PLUGIN_MODE**: Network plugin mode (default: overlay)
- **NETWORK_DATAPLANE**: Network dataplane (default: cilium)
- **NETWORK_POLICY**: Optional network policy configuration

#### Node Configuration

Node-specific settings:
- **SSH_PUBLIC_KEY**: Required SSH public key for VM access
- **LINUX_ADMIN_USERNAME**: Admin username for Linux VMs (default: azureuser)
- **NODE_IDENTITIES**: Comma-separated list of user-assigned identities
- **KUBELET_BOOTSTRAP_TOKEN**: Required bootstrap token for cluster joining

#### Image Management

For managed Karpenter instances (NAP):
- **USE_SIG**: Enable AKS managed shared image galleries (default: false)
- **SIG_SUBSCRIPTION_ID**: Subscription ID for shared image gallery
- **SIG_ACCESS_TOKEN_SERVER_URL**: Token server URL for SIG access
- **SIG_ACCESS_TOKEN_SCOPE**: Token scope for SIG access

