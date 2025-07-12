---
title: "Troubleshooting"
linkTitle: "Troubleshooting" 
weight: 70
description: >
  Debug and resolve common Karpenter issues
---

This guide covers common issues you might encounter when using Karpenter for Azure and how to troubleshoot them.

## Common Issues

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

### Spot VM Issues

**Symptoms**: Unexpected node terminations when using spot instances.

**Debugging Steps**:

1. **Check node events**:
```bash
kubectl get events | grep -i "spot\|evict"
```

2. **Monitor spot VM pricing**:
```bash
az vm list-sizes --location <region> --query "[?contains(name, 'Standard_D2s_v3')]"
```

**Solutions**:
- Use diverse instance types for better availability
- Implement proper pod disruption budgets
- Consider mixed spot/on-demand strategies
- Use workloads tolerant of node preemption

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

## Community Resources

- [GitHub Issues](https://github.com/Azure/karpenter-provider-azure/issues)
- [Kubernetes Slack #azure-provider](https://kubernetes.slack.com/channels/azure-provider)
- [Azure Documentation](https://docs.microsoft.com/en-us/azure/aks/)