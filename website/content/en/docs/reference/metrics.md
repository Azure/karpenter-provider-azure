---
title: "Metrics"
linkTitle: "Metrics"
weight: 7

description: >
  Inspect Karpenter Metrics
---
Karpenter makes several metrics available in Prometheus format to allow monitoring cluster provisioning status. These metrics are available by default at `karpenter.karpenter.svc.cluster.local:8000/metrics` configurable via the `METRICS_PORT` environment variable documented [here](../settings)

### `karpenter_build_info`
A metric with a constant '1' value labeled by version from which karpenter was built.

## Nodepool Metrics

### `karpenter_nodepool_usage`
The nodepool usage is the amount of resources that have been provisioned by a particular nodepool. Labeled by nodepool name and resource type.

### `karpenter_nodepool_limit`
The nodepool limits are the limits specified on the nodepool that restrict the quantity of resources provisioned. Labeled by nodepool name and resource type.

## Nodes Metrics

### `karpenter_nodes_total_pod_requests`
Node total pod requests are the resources requested by non-DaemonSet pods bound to nodes.

### `karpenter_nodes_total_pod_limits`
Node total pod limits are the resources specified by non-DaemonSet pod limits.

### `karpenter_nodes_total_daemon_requests`
Node total daemon requests are the resource requested by DaemonSet pods bound to nodes.

### `karpenter_nodes_total_daemon_limits`
Node total daemon limits are the resources specified by DaemonSet pod limits.

### `karpenter_nodes_termination_time_seconds`
The time taken between a node's deletion request and the removal of its finalizer

### `karpenter_nodes_terminated`
Number of nodes terminated in total by Karpenter. Labeled by owning nodepool.

### `karpenter_nodes_system_overhead`
Node system daemon overhead are the resources reserved for system overhead, the difference between the node's capacity and allocatable values are reported by the status.

### `karpenter_nodes_leases_deleted`
Number of deleted leaked leases.

### `karpenter_nodes_eviction_queue_depth`
The number of pods currently waiting for a successful eviction in the eviction queue.

### `karpenter_nodes_created`
Number of nodes created in total by Karpenter. Labeled by owning nodepool.

### `karpenter_nodes_allocatable`
Node allocatable are the resources allocatable by nodes.

## Pods Metrics

### `karpenter_pods_state`
Pod state is the current state of pods. This metric can be used several ways as it is labeled by the pod name, namespace, owner, node, nodepool name, zone, architecture, capacity type, instance type and pod phase.

### `karpenter_pods_startup_time_seconds`
The time from pod creation until the pod is running.

## Provisioner Metrics

### `karpenter_provisioner_scheduling_simulation_duration_seconds`
Duration of scheduling simulations used for deprovisioning and provisioning in seconds.

### `karpenter_provisioner_scheduling_queue_depth`
The number of pods currently waiting to be scheduled.

### `karpenter_provisioner_scheduling_duration_seconds`
Duration of scheduling process in seconds.

## Nodeclaims Metrics

### `karpenter_nodeclaims_terminated`
Number of nodeclaims terminated in total by Karpenter. Labeled by reason the nodeclaim was terminated and the owning nodepool.

### `karpenter_nodeclaims_registered`
Number of nodeclaims registered in total by Karpenter. Labeled by the owning nodepool.

### `karpenter_nodeclaims_launched`
Number of nodeclaims launched in total by Karpenter. Labeled by the owning nodepool.

### `karpenter_nodeclaims_initialized`
Number of nodeclaims initialized in total by Karpenter. Labeled by the owning nodepool.

### `karpenter_nodeclaims_drifted`
Number of nodeclaims drifted reasons in total by Karpenter. Labeled by drift type of the nodeclaim and the owning nodepool.

### `karpenter_nodeclaims_disrupted`
Number of nodeclaims disrupted in total by Karpenter. Labeled by disruption type of the nodeclaim and the owning nodepool.

### `karpenter_nodeclaims_created`
Number of nodeclaims created in total by Karpenter. Labeled by reason the nodeclaim was created and the owning nodepool.

## Interruption Metrics

### `karpenter_interruption_received_messages_total`
Number of interruption messages received from Azure Service Bus. Labeled by message type.

### `karpenter_interruption_actions_performed_total` 
Number of interruption actions performed. Labeled by action type (taint, cordon, drain).

### `karpenter_interruption_message_latency_time_seconds`
Length of time between message generation and message receipt from the Azure Service Bus queue.

## Consistency Metrics

### `karpenter_consistency_errors`
Number of consistency errors encountered by Karpenter. Labeled by error type.

## Allocation Metrics

### `karpenter_allocation_controller_batch_duration_seconds`
Duration of batches for the allocation controller in seconds.

## Azure Metrics

### `karpenter_azure_api_duration_seconds`
Duration of Azure API calls. Labeled by the Azure service API and HTTP method.

## Cloudprovider Metrics

### `karpenter_cloudprovider_duration_seconds`
Duration of cloud provider method calls. Labeled by the method name and provider.

### `karpenter_cloudprovider_errors_total`
Total number of errors returned from CloudProvider calls. Labeled by the method name and provider.

### `karpenter_cloudprovider_instances_terminated_total`
Number of instances terminated by the cloud provider. Labeled by reason.

### `karpenter_cloudprovider_instances_created_total`
Number of instances created by the cloud provider.

## Cluster State Metrics

### `karpenter_cluster_state_node_count`
Current count of nodes in cluster state synced with the controller.

### `karpenter_cluster_state_synced`
Returns 1 if cluster state is synced and 0 otherwise.