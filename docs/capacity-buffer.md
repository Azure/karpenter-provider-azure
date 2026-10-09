# CapacityBuffer

Status: alpha, opt-in for self-hosted deployments

CapacityBuffer lets Karpenter maintain spare schedulable capacity before real workloads need it. Karpenter resolves a workload shape, creates virtual Pods in memory, and provisions or preserves nodes for that demand. It does not create placeholder Pods in the Kubernetes API.

For the upstream concepts and behavior, see the [Karpenter CapacityBuffers documentation](https://karpenter.sh/docs/concepts/capacitybuffers/). This document covers Azure-specific setup and limitations.

> [!WARNING]
> Karpenter core v1.14.1 has a known deletion/reference-loss defect tracked in
> [kubernetes-sigs/karpenter#3258](https://github.com/kubernetes-sigs/karpenter/issues/3258).
> On an otherwise idle cluster, stale in-memory buffer placement can delay
> `WhenEmpty` disruption after a CapacityBuffer or its backing reference is
> deleted. Keep the feature limited to alpha evaluation until a fixed core
> revision is adopted. Restarting the controller rebuilds this in-memory state.

## Enable The Feature

CapacityBuffer is disabled by default in self-hosted deployments. Enable it when installing or upgrading the self-hosted Karpenter chart:

```yaml
settings:
  featureGates:
    capacityBuffer: true
```

On a fresh main-chart installation, Helm installs CapacityBuffer CRD automatically.
On `helm upgrade`, Helm does not install new CRDs or  update existing ones from the
main chart's `crds/` directory. **Before enabling the gate on an existing self-hosted
installation, install the CRD from the controller's target release:**

- If you already use the standalone `karpenter-crd` chart, upgrade that chart
  to the matching release first.
- If you use only the main chart, apply its release-matched CapacityBuffer
  CRD before upgrading the controller. For the published chart and
  `KARPENTER_VERSION` used in the [self-hosted installation instructions](../README.md#install-karpenter):

  ```bash
  (
    set -e
    chart_dir="$(mktemp -d)"
    trap 'rm -r "$chart_dir"' EXIT
    helm pull oci://mcr.microsoft.com/aks/karpenter/karpenter \
      --version "${KARPENTER_VERSION}" --untar --untardir "$chart_dir"
    kubectl apply -f "$chart_dir/karpenter/crds/autoscaling.x-k8s.io_capacitybuffers.yaml"
  )
  ```

  For a snapshot installation, use its matching chart URL and version instead.

After either path, verify that the CapacityBuffer CRD is established before
enabling `capacityBuffer` and upgrading the main chart:

```bash
kubectl wait --for=condition=Established \
  crd/capacitybuffers.autoscaling.x-k8s.io --timeout=60s
```

Otherwise, the enabled controller times out waiting for the missing CRD
at startup.

Enabling the gate also grants the controller permission to read CapacityBuffers and PodTemplates and update CapacityBuffer status.

AKS-managed NAP activation is configured outside this chart and is not enabled
by this change.

## Create A Buffer

Apply the CapacityBuffer example:

```bash
kubectl apply -f examples/v1/capacity-buffer.yaml
kubectl get capacitybuffers.autoscaling.x-k8s.io
```

The example creates a dedicated AKSNodeClass and NodePool, then requests two
buffer chunks, each represented by a virtual Pod requesting 1 CPU and 1 GiB
of memory. Both the CapacityBuffer and NodePool include limits that bound
possible cloud spend.

A CapacityBuffer can instead reference a Deployment, ReplicaSet, or StatefulSet
through `spec.scalableRef`. References must be in the same namespace. Karpenter
periodically resolves scalable workload size and computes the requested buffer
percentage.

> [!NOTE]
> With Karpenter core v1.14.1, the CapacityBuffer CRD descriptions differ from
> controller behavior: when both `replicas` and `percentage` are set, Karpenter
> uses the larger count (capped by `limits`), not the smaller.
>
> Percentage-derived counts round up: 33% of 10 workload replicas is 3.3,
> so Karpenter requests 4 chunks. Setting `percentage: 0` contributes no
> percentage-derived chunks, despite the CRD's "minimum of 1" wording.
>
> An omitted `scalableRef.apiGroup` means `apps`, not the core API group.
> Set `apiGroup: apps` explicitly for supported workloads; core-group
> references are not supported.

## How Buffered Capacity Is Placed and Used

Neither a CapacityBuffer nor a PodTemplate references a NodePool directly.
Karpenter simulates virtual pods using the referenced PodTemplate's resource
requests and scheduling constraints (or the workload's pod template with
`scalableRef`). They can fit on compatible existing nodes without creating new
ones; if there is not enough room, Karpenter can provision from any compatible
NodePool. At the same time, without additional configuration, nothing prevents
other workloads from using that newly provisioned capacity.

This means that if you intend to provide workload-specific headroom - a very
common use case - you must use appropriate selectors, taints, and tolerations
to enforce that isolation. That is what the example does: its PodTemplate
selects the `intent=capacity-buffer` label on the NodePool
and tolerates its `intent=capacity-buffer:NoSchedule` taint. (These are ordinary
example values, not CapacityBuffer-specific keys.) The selector keeps virtual
demand on nodes with that label; the taint stops pods without a matching
toleration from scheduling there. The real workload intended to use this
headroom also needs the toleration and, if it must run on this pool, a matching
node selector.

For shared headroom, remove the example's `intent` selector, taint, and
toleration together; the now-unused NodePool label can go too.

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
