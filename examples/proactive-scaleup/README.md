# Proactive Scale-Up for Azure Karpenter

This directory contains examples and documentation for implementing proactive scale-up with Azure Karpenter, similar to the Kubernetes Cluster Autoscaler's proactive scale-up feature.

## Overview

Proactive scale-up helps reduce pod scheduling latency by pre-provisioning capacity before workloads arrive. This is accomplished by creating low-priority placeholder pods that trigger Karpenter to provision nodes. When real workloads arrive, these placeholder pods are preempted, and the capacity is immediately available.

## How It Works

1. **Placeholder Pods**: Create pods with very low priority (-1000) that request minimal resources
2. **Karpenter Provisioning**: Karpenter sees these unschedulable pods and provisions nodes
3. **Preemption**: When real workloads arrive, they preempt the placeholder pods and use the pre-provisioned capacity
4. **Reduced Latency**: Real workloads don't wait for node provisioning

## Configuration Options

Azure Karpenter provides the following configuration options for proactive scale-up:

### Command-line Flags

- `--enable-proactive-scaleup`: Enable/disable proactive scale-up (default: `false`)
- `--pod-injection-limit`: Maximum total pods (real + placeholder) (default: `5000`)
- `--node-limit`: Maximum total nodes in cluster (default: `15000`)
- `--proactive-scaleup-threshold`: Cluster utilization threshold (0-1) to trigger scale-up (default: `0.8`)

### Environment Variables

- `ENABLE_PROACTIVE_SCALEUP`: Same as `--enable-proactive-scaleup`
- `POD_INJECTION_LIMIT`: Same as `--pod-injection-limit`
- `NODE_LIMIT`: Same as `--node-limit`
- `PROACTIVE_SCALEUP_THRESHOLD`: Same as `--proactive-scaleup-threshold`

## Usage Examples

See the YAML files in this directory for ready-to-use examples:

- `placeholder-deployment.yaml` - Basic placeholder deployment
- `gpu-placeholder.yaml` - GPU-specific placeholders
- `hpa-example.yaml` - Auto-scaling placeholders with HPA

## Quick Start

1. Deploy placeholder pods:
   ```bash
   kubectl apply -f placeholder-deployment.yaml
   ```

2. Monitor placeholder pods:
   ```bash
   kubectl get pods -l karpenter.azure.com/proactive-scaleup=placeholder
   ```

3. Deploy your workload and watch placeholders get preempted:
   ```bash
   kubectl get events --field-selector reason=Preempted
   ```

## Best Practices

1. **Start Small**: Begin with 5-10 placeholder pods and adjust based on workload patterns
2. **Set Appropriate Priorities**: Use priority -1000 or lower to ensure real workloads always preempt placeholders
3. **Match Workload Profiles**: Size placeholder pods to match your typical workload resource requests
4. **Monitor Costs**: Pre-provisioned capacity increases cloud costs; monitor and tune based on your latency vs. cost tradeoff

## References

- [Kubernetes Autoscaler PR #7145](https://github.com/kubernetes/autoscaler/pull/7145) - Original proactive scale-up implementation
- [Karpenter Documentation](https://karpenter.sh/) - Official Karpenter docs
- [Pod Priority and Preemption](https://kubernetes.io/docs/concepts/scheduling-eviction/pod-priority-preemption/) - Kubernetes documentation
