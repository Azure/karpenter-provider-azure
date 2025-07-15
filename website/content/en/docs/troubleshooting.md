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

1. **Debug bootstrap by connecting to a Karpenter node**:
```bash
# Create a debug pod on an existing node with SSH client
JUMP_NODE=$(kubectl get nodes -o name | head -n 1)
JUMP_POD=$(kubectl debug $JUMP_NODE --image kroniak/ssh-client -- sh -c "mkdir /root/.ssh; sleep 1h" | cut -d' ' -f4)
kubectl wait --for=condition=Ready pod/$JUMP_POD

# Copy your SSH key to the debug pod
kubectl cp ~/.ssh/id_rsa $JUMP_POD:/root/.ssh/id_rsa

# Get the private IP of the first Karpenter-managed node
NODE_IP=$(az network nic list -g MC_${AZURE_RESOURCE_GROUP}_${AZURE_CLUSTER_NAME}_${AZURE_LOCATION} \
  --query '[?tags."karpenter.azure.com_cluster"]|[0].ipConfigurations[0].privateIPAddress' -o tsv)

# SSH to the Karpenter node
kubectl exec $JUMP_POD -it -- ssh -o StrictHostKeyChecking=accept-new azureuser@$NODE_IP
```

2. **Check node bootstrap logs**:
```bash
journalctl -u kubelet.service  
cat /var/log/azure/aks/cluster-provision.log # has logs from the initial node bootstrapping, should contain clear errors 
cat /var/log/azure/aks/cluster-provision-cse-output.log
cat /var/log/syslog | grep containerd
```

3. **Verify cluster connectivity**:
```bash
# Test connectivity to API server
curl -k https://<cluster-endpoint>:443
```
4. Validate NSG Rules don't block any required binaries AKS is trying to pull

**Common Causes**:
- Network connectivity issues
- Authentication problems with the bootstrap token
- Broken AKS Node image


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
3. **If using azure cni with overlay or cilium** 
Validate your nodes have these labels 

```
    kubernetes.azure.com/azure-cni-overlay: "true"
    kubernetes.azure.com/network-name: aks-vnet-<redacted>
    kubernetes.azure.com/network-resourcegroup: <redacted>
    kubernetes.azure.com/network-subscription: <redacted>
```

4. **Validate the CNI configuration files**

The CNI conflist files define network plugin configurations. Check which files are present:

```bash
# List CNI configuration files
ls -la /etc/cni/net.d/

# Example output:
# 10-azure.conflist   15-azure-swift-overlay.conflist
```

**Understanding conflist files**:
- `10-azure.conflist`: Standard Azure CNI configuration for traditional networking with node subnet
- `15-azure-swift-overlay.conflist`: Azure CNI with overlay networking (used with Cilium or overlay mode)

**Inspect the configuration content**:
```bash
# Check the actual CNI configuration
cat /etc/cni/net.d/*.conflist

# Look for key fields:
# - "type": should be "azure-vnet" for Azure CNI
# - "mode": "bridge" for standard, "transparent" for overlay
# - "ipam": IP address management configuration
```

**Common conflist issues**:
- Missing or corrupted configuration files
- Incorrect network mode for your cluster setup
- Mismatched IPAM configuration
- Wrong plugin order in the configuration chain

5. **Check CNI to CNS communication**:
```bash
# Check CNS logs for IP allocation requests from CNI
kubectl logs -n kube-system -l k8s-app=azure-cns --tail=100
```

**CNI to CNS Troubleshooting**:
- **If CNS logs show "no IPs available"**: This indicates a CNS or aks's watch on the NNCs.
- **If CNI calls don't appear in CNS logs**: You likely have the wrong CNI installed. Verify the correct CNI plugin is deployed.

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

### DNS Service IP Issues

**Note**: The `--dns-service-ip` parameter is only supported for NAP (Node Auto Provisioning) clusters and is not available for self-hosted Karpenter installations.

**Symptoms**: Pods can't resolve DNS names or kubelet fails to register with API server due to DNS resolution failures.

**Debugging Steps**:

1. **Check kubelet DNS configuration**:
```bash
# SSH to the Karpenter node and check kubelet config
sudo cat /var/lib/kubelet/config.yaml | grep -A 5 clusterDNS

# Expected output should show the correct DNS service IP
# clusterDNS:
# - "10.0.0.10"  # This should match your cluster's DNS service IP
```

2. **Verify DNS service IP matches cluster configuration**:
```bash
# Get the actual DNS service IP from your cluster
kubectl get service -n kube-system kube-dns -o jsonpath='{.spec.clusterIP}'

# Compare with what AKS reports
az aks show --resource-group <rg> --name <cluster-name> --query "networkProfile.dnsServiceIp" -o tsv
```

3. **Test DNS resolution from the node**:
```bash
# SSH to the Karpenter node and test DNS resolution
# Test using the DNS service IP directly
dig @10.0.0.10 kubernetes.default.svc.cluster.local

# Test using system resolver
nslookup kubernetes.default.svc.cluster.local

# Test external DNS resolution
dig google.com
```

4. **Check DNS pods status**:
```bash
# Verify CoreDNS pods are running
kubectl get pods -n kube-system -l k8s-app=kube-dns

# Check CoreDNS logs for errors
kubectl logs -n kube-system -l k8s-app=kube-dns --tail=50
```

5. **Validate network connectivity to DNS service**:
```bash
# From the Karpenter node, test connectivity to DNS service
telnet 10.0.0.10 53  # Replace with your actual DNS service IP
# Or using nc if telnet is not available
nc -zv 10.0.0.10 53
```

**Common Causes**:
- Incorrect `--dns-service-ip` parameter in AKSNodeClass
- DNS service IP not in the service CIDR range
- Network connectivity issues between node and DNS service
- CoreDNS pods not running or misconfigured
- Firewall rules blocking DNS traffic

**Solutions**:
- Verify `--dns-service-ip` matches the actual DNS service: `kubectl get svc -n kube-system kube-dns -o jsonpath='{.spec.clusterIP}'`
- Ensure DNS service IP is within the service CIDR range specified during cluster creation
- Check that Karpenter nodes can reach the service subnet
- Restart CoreDNS pods if they're in error state: `kubectl rollout restart deployment/coredns -n kube-system`
- Verify NSG rules allow traffic on port 53 (TCP/UDP)

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
