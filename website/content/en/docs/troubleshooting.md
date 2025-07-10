---
title: "Troubleshooting"
linkTitle: "Troubleshooting" 
weight: 70
description: >
  Debug and resolve common Karpenter issues
---

This guide covers common issues you might encounter when using Karpenter for Azure and how to troubleshoot them.

## Getting Started

### Check Karpenter Status

First, verify that Karpenter is running correctly:

```bash
# Check Karpenter deployment
kubectl get deployment -n karpenter

# Check Karpenter pods
kubectl get pods -n karpenter

# View Karpenter logs
kubectl logs -n karpenter deployment/karpenter -f
```

### Verify Configuration

```bash
# List NodePools
kubectl get nodepools

# List AKSNodeClasses  
kubectl get aksnodeclasses

# Describe your NodePool
kubectl describe nodepool <nodepool-name>

# Describe your AKSNodeClass
kubectl describe aksnodeclass <nodeclass-name>
```

## Common Issues

### Pods Not Being Scheduled

**Symptoms**: Pods remain in Pending state, no new nodes are created.

**Debugging Steps**:

1. **Check pod status**:
```bash
kubectl describe pod <pod-name>
```
Look for events and scheduling failures.

2. **Verify NodePool matches pod requirements**:
```bash
kubectl get nodepool <nodepool-name> -o yaml
```
Ensure the NodePool requirements are compatible with your pod's nodeSelector, affinity, and tolerations.

3. **Check Karpenter logs**:
```bash
kubectl logs -n karpenter deployment/karpenter | grep -i "pod\|schedule\|provision"
```

**Common Causes**:
- Pod requirements don't match any NodePool
- NodePool limits exceeded
- Insufficient Azure quota
- Pod has unschedulable constraints

**Solutions**:
- Adjust NodePool requirements or pod constraints
- Increase NodePool limits
- Request Azure quota increases
- Review pod scheduling constraints

### Node Provisioning Failures

**Symptoms**: Karpenter attempts to provision nodes but they fail to join the cluster.

**Debugging Steps**:

1. **Check Azure Activity Log**:
```bash
az monitor activity-log list --resource-group <node-resource-group> --max-events 20
```

2. **Verify Azure permissions**:
```bash
# Check if Karpenter can create VMs
az vm list --resource-group <node-resource-group>
```

3. **Check VM boot diagnostics**:
```bash
az vm boot-diagnostics get-boot-log --resource-group <rg> --name <vm-name>
```

**Common Causes**:
- Insufficient Azure permissions
- Subnet has no available IP addresses
- VM size not available in the region/zone
- Azure quota exceeded
- Network security group blocking traffic

**Solutions**:
- Verify Karpenter service principal permissions
- Ensure subnet has sufficient IP addresses
- Check VM size availability: `az vm list-sizes --location <region>`
- Request quota increases
- Review NSG rules for required ports

### Nodes Not Joining Cluster

**Symptoms**: Azure VMs are created but don't appear as Kubernetes nodes.

**Debugging Steps**:

1. **Check node bootstrap logs**:
```bash
# SSH to the node and check logs
journalctl -u kubelet -f
```

2. **Verify cluster connectivity**:
```bash
# Test connectivity to API server
curl -k https://<cluster-endpoint>:443
```

3. **Check AKS cluster status**:
```bash
az aks show --resource-group <rg> --name <cluster-name> --query "powerState"
```

**Common Causes**:
- Incorrect cluster endpoint or CA bundle
- Network connectivity issues
- Authentication problems
- Wrong AKS node image

**Solutions**:
- Verify CLUSTER_ENDPOINT and CLUSTER_CA_BUNDLE settings
- Check network connectivity from node subnet to AKS
- Ensure proper managed identity configuration
- Verify image family in AKSNodeClass

### Slow Node Provisioning

**Symptoms**: Nodes take longer than expected to become ready.

**Debugging Steps**:

1. **Monitor provisioning stages**:
```bash
kubectl get events --field-selector reason=ProvisioningLaunched,reason=ProvisioningSucceeded
```

2. **Check VM startup time**:
```bash
az vm show --resource-group <rg> --name <vm-name> --query "timeCreated"
```

**Common Causes**:
- Large VM images
- Slow image pulls
- Complex bootstrap scripts
- Azure region capacity constraints

**Solutions**:
- Use smaller, optimized images
- Pre-pull common container images
- Optimize bootstrap processes
- Consider multiple regions/zones

### Nodes Not Being Removed

**Symptoms**: Underutilized nodes remain in the cluster longer than expected.

**Debugging Steps**:

1. **Check node utilization**:
```bash
kubectl top nodes
kubectl describe node <node-name>
```

2. **Look for blocking pods**:
```bash
kubectl get pods --all-namespaces --field-selector spec.nodeName=<node-name>
```

3. **Check for disruption blocks**:
```bash
kubectl get events | grep -i "disruption\|consolidation"
```

**Common Causes**:
- Pods without proper tolerations
- DaemonSets preventing drain
- Pod disruption budgets
- Nodes marked with `do-not-disrupt` annotation

**Solutions**:
- Add proper tolerations to pods
- Review DaemonSet configurations  
- Adjust pod disruption budgets
- Remove `do-not-disrupt` annotations if appropriate

## Performance Issues

### High Memory Usage

**Symptoms**: Karpenter controller consuming excessive memory.

**Debugging Steps**:

1. **Check resource usage**:
```bash
kubectl top pod -n karpenter
kubectl describe pod -n karpenter <karpenter-pod>
```

2. **Review memory limits**:
```bash
kubectl get deployment karpenter -n karpenter -o yaml | grep -A5 -B5 memory
```

**Solutions**:
- Increase memory limits for Karpenter deployment
- Reduce cluster size or node churn
- Consider multiple Karpenter instances for very large clusters

### API Rate Limiting

**Symptoms**: Azure API rate limiting errors in logs.

**Debugging Steps**:

1. **Check for rate limit errors**:
```bash
kubectl logs -n karpenter deployment/karpenter | grep -i "rate\|throttl\|429"
```

**Solutions**:
- Implement exponential backoff (usually automatic)
- Reduce provisioning frequency
- Use multiple Azure subscriptions for very large clusters

## Networking Issues

### Pod Connectivity Problems

**Symptoms**: Pods can't communicate with other pods or external services.

**Debugging Steps**:

1. **Test basic connectivity**:
```bash
# From within a pod
kubectl exec -it <pod-name> -- ping <target-ip>
kubectl exec -it <pod-name> -- nslookup kubernetes.default
```

2. **Check network plugin status**:
```bash
kubectl get pods -n kube-system | grep -E "azure-cni|kube-proxy"
```

**Common Causes**:
- Network security group rules
- Incorrect subnet configuration
- CNI plugin issues
- DNS resolution problems

**Solutions**:
- Review NSG rules for required traffic
- Verify subnet configuration in AKSNodeClass
- Restart CNI plugin pods
- Check CoreDNS configuration

## Azure-Specific Issues

### Spot VM Interruptions

**Symptoms**: Frequent unexpected node terminations.

**Debugging Steps**:

1. **Check for spot interruptions**:
```bash
kubectl get events | grep -i "spot\|interrupt\|evict"
```

2. **Monitor spot VM pricing**:
```bash
az vm list-sizes --location <region> --query "[?contains(name, 'Standard_D2s_v3')]"
```

**Solutions**:
- Use diverse instance types to reduce interruption impact
- Implement proper pod disruption budgets
- Consider mixed spot/on-demand strategies
- Use spot-tolerant workloads

### Quota Exceeded

**Symptoms**: VM creation fails with quota exceeded errors.

**Debugging Steps**:

1. **Check current quota usage**:
```bash
az vm list-usage --location <region> --query "[?currentValue >= limit]"
```

**Solutions**:
- Request quota increases through Azure portal
- Use different VM sizes with available quota
- Distribute workloads across multiple regions

## Log Analysis

### Important Log Patterns

Search for these patterns in Karpenter logs:

```bash
# Provisioning decisions
kubectl logs -n karpenter deployment/karpenter | grep "provisioning"

# Node termination events  
kubectl logs -n karpenter deployment/karpenter | grep "terminating\|deleting"

# Azure API errors
kubectl logs -n karpenter deployment/karpenter | grep -i "azure.*error"

# Scheduling failures
kubectl logs -n karpenter deployment/karpenter | grep "unschedulable\|failed.*schedule"
```

### Enabling Debug Logging

Increase log verbosity for more detailed information:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: karpenter
  namespace: karpenter
spec:
  template:
    spec:
      containers:
      - name: controller
        env:
        - name: LOG_LEVEL
          value: debug
```

## Getting Help

### Collecting Debug Information

When reporting issues, include:

1. **Karpenter version and configuration**:
```bash
kubectl get deployment karpenter -n karpenter -o yaml
```

2. **NodePool and AKSNodeClass configuration**:
```bash
kubectl get nodepool,aksnodeclass -o yaml
```

3. **Recent events and logs**:
```bash
kubectl get events --sort-by='.lastTimestamp' | tail -50
kubectl logs -n karpenter deployment/karpenter --tail=100
```

4. **Cluster information**:
```bash
kubectl version
az aks show --resource-group <rg> --name <cluster-name>
```

### Community Resources

- [GitHub Issues](https://github.com/Azure/karpenter-provider-azure/issues)
- [Kubernetes Slack #azure-provider](https://kubernetes.slack.com/channels/azure-provider)
- [Azure Documentation](https://docs.microsoft.com/en-us/azure/aks/)