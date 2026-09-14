# CapacityBuffer

Status: alpha, opt-in for self-hosted deployments

CapacityBuffer lets Karpenter maintain spare schedulable capacity before real workloads need it. Karpenter resolves a workload shape, creates virtual Pods in memory, and provisions or preserves nodes for that demand. It does not create placeholder Pods in the Kubernetes API.

## Enable The Feature

CapacityBuffer is disabled by default. Enable it when installing or upgrading the self-hosted Karpenter chart:

```yaml
settings:
  featureGates:
    capacityBuffer: true
```

The main chart and standalone CRD chart both ship `autoscaling.x-k8s.io/v1beta1`. Keep the controller and CRD chart on the same release. When using the standalone CRD chart, upgrade it before enabling the controller gate.

Enabling the gate also grants the controller permission to read CapacityBuffers and PodTemplates and update CapacityBuffer status.

AKS-managed NAP activation is configured outside this chart and is not enabled
by this change.

> [!WARNING]
> Karpenter core v1.14.1 has a known deletion/reference-loss defect tracked in
> [kubernetes-sigs/karpenter#3258](https://github.com/kubernetes-sigs/karpenter/issues/3258).
> On an otherwise idle cluster, stale in-memory buffer placement can delay
> `WhenEmpty` disruption after a CapacityBuffer or its backing reference is
> deleted. Keep the feature limited to alpha evaluation until a fixed core
> revision is adopted. Restarting the controller rebuilds this in-memory state.

## Create A Buffer

Apply the bounded PodTemplate example:

```bash
kubectl apply -f examples/v1/capacity-buffer.yaml
kubectl get capacitybuffers.autoscaling.x-k8s.io
```

The example creates a dedicated AKSNodeClass and NodePool, then requests two
chunks of spare capacity, each shaped as 1 CPU and 1 GiB of memory. Both the
CapacityBuffer and NodePool include limits that bound possible cloud spend.

A CapacityBuffer can instead reference a Deployment, ReplicaSet, or StatefulSet through `spec.scalableRef`. References must be in the same namespace. Karpenter periodically resolves scalable workload size and computes the requested buffer percentage.

## Status

`ReadyForProvisioning` reports whether the template or scalable reference resolved successfully. `Provisioning` reports whether Karpenter placed the virtual demand on existing capacity or needed new capacity.

```bash
kubectl describe capacitybuffer general-purpose-buffer
```

Deleting or reducing a buffer removes virtual demand. After the known v1.14.1
deletion issue above is resolved or the controller is restarted, excess nodes
are removed according to normal NodePool disruption budgets and
`consolidateAfter`; scale-down is not immediate.

## Operating Boundaries

- Run only one CapacityBuffer reconciler. Do not enable Karpenter and Cluster Autoscaler CapacityBuffer reconciliation in the same cluster.
- Treat permission to create CapacityBuffers as cost-bearing access. Use namespace RBAC and admission policies to constrain replicas, percentages, and limits.
- Use NodePool limits as the provider-side upper bound and monitor Azure regional quota and capacity errors.
- The initial Azure support scope covers active capacity, Linux CPU and memory shapes, same-namespace references, fixed replicas, percentages, and resource limits.
- Ephemeral strategy, cross-namespace references, PVC or ephemeral-volume reservation, DRA ResourceClaims, Windows, and GPU-shaped buffers are not supported initially.
- Clusters using kube-scheduler `LeastAllocated` are not guaranteed placement
  parity until
  [kubernetes-sigs/karpenter#3196](https://github.com/kubernetes-sigs/karpenter/issues/3196)
  is resolved.
