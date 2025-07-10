---
title: "Managing VM Images"  
linkTitle: "Managing VM Images"
weight: 10
description: >
  Learn to customize and manage VM images for Karpenter nodes
---

Karpenter for Azure provisions nodes using VM images. By default, Karpenter uses Azure-managed images that are optimized for AKS, but you can also specify custom images for specialized use cases.

## Default Image Families

Karpenter supports the following image families out of the box:

### Ubuntu2204
The default image family, providing Ubuntu 22.04 LTS with AKS optimizations:
- Container runtime: containerd
- Kubernetes versions: Latest AKS-supported versions
- GPU support: NVIDIA GPU drivers pre-installed
- Security: Regular security updates via AKS

### AzureLinux  
Microsoft's optimized Linux distribution for Azure workloads:
- Lightweight and secure
- Optimized for containerized workloads
- Fast boot times
- Azure-specific optimizations

## Specifying Image Families

Configure the image family in your `AKSNodeClass`:

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: custom-images
spec:
  imageFamily: Ubuntu2204  # or AzureLinux
```

## Using Custom Images

For specialized requirements, you can use custom VM images. Custom images must:
- Be based on a supported base image (Ubuntu 22.04 or Azure Linux)
- Include the AKS node binaries and configuration
- Have containerd properly configured
- Include any required GPU drivers or specialized software

### Creating Custom Images

1. **Start with a base AKS-optimized image**:
```bash
# List available AKS images
az vm image list --publisher microsoft-aks --all --output table
```

2. **Create a VM from the base image**:
```bash
az vm create \
  --resource-group myResourceGroup \
  --name image-builder-vm \
  --image microsoft-aks:aks:ubuntu-2204-202310:latest \
  --size Standard_D2s_v3 \
  --admin-username azureuser \
  --generate-ssh-keys
```

3. **Customize the VM**:
```bash
# SSH into the VM and install your customizations
ssh azureuser@<VM_IP>

# Example: Install additional software
sudo apt update
sudo apt install -y my-custom-package

# Install custom GPU drivers if needed
# Configure additional security tools
# Set up monitoring agents
```

4. **Prepare for image capture**:
```bash
# Deprovision the VM (removes user data and generalizes)
sudo waagent -deprovision+user -force
exit
```

5. **Capture the image**:
```bash
# Deallocate and generalize the VM
az vm deallocate --resource-group myResourceGroup --name image-builder-vm
az vm generalize --resource-group myResourceGroup --name image-builder-vm

# Create the custom image
az image create \
  --resource-group myResourceGroup \
  --name myCustomAKSImage \
  --source image-builder-vm
```

### Using Custom Images with Karpenter

Reference your custom image in the `AKSNodeClass`:

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: custom-image-nodeclass
spec:
  # Use custom image by specifying the image ID
  imageID: "/subscriptions/<subscription-id>/resourceGroups/<rg>/providers/Microsoft.Compute/images/myCustomAKSImage"
  
  # Or use a custom image family (requires Karpenter configuration)
  imageFamily: "Custom"
```

## Image Requirements

All images used with Karpenter must meet these requirements:

### Base Requirements
- Linux-based (Ubuntu 22.04 LTS or Azure Linux)
- x86_64 or ARM64 architecture
- Minimum 30 GB disk space
- containerd container runtime
- systemd init system

### AKS-Specific Requirements
- AKS node binaries (kubelet, kubeadm, etc.)
- Azure VM agent (waagent)
- Proper network configuration for Azure CNI
- AKS-specific systemd units and configuration

### Security Requirements
- Up-to-date security patches
- Disabled password authentication (SSH key only)
- Proper firewall configuration
- No unnecessary services running

## GPU Images

For GPU workloads, ensure your image includes:

### NVIDIA GPUs
- NVIDIA driver (version compatible with your CUDA requirements)
- NVIDIA Container Toolkit
- CUDA runtime (if needed)

```bash
# Example: Install NVIDIA drivers in custom image
sudo apt update
sudo apt install -y nvidia-driver-525
sudo apt install -y nvidia-container-toolkit
```

### Verify GPU Support
```yaml
apiVersion: karpenter.azure.com/v1beta1
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
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: gpu-image-nodeclass
```

## Image Updates and Lifecycle

### Automatic Updates
- Default AKS images are automatically updated by Microsoft
- New image versions include security patches and AKS improvements
- Karpenter automatically uses the latest available image version

### Custom Image Updates
- Custom images require manual updating
- Plan for regular security updates
- Test image updates in non-production environments first

### Rolling Updates
When updating images:

1. **Create new image version**
2. **Update AKSNodeClass** with new image reference
3. **Trigger node refresh** to roll out new images:

```bash
# Cordon and drain nodes to force replacement
kubectl cordon <node-name>
kubectl drain <node-name> --ignore-daemonsets --delete-emptydir-data
```

## Best Practices

### Image Management
- Use infrastructure as code (ARM templates, Terraform) to create images
- Implement automated image building pipelines
- Tag images with version numbers and creation dates
- Store images in the same region as your AKS cluster

### Security
- Regularly update base images with security patches
- Scan images for vulnerabilities before deployment
- Use minimal images with only required packages
- Implement image signing and verification

### Testing
- Test custom images in development environments first
- Validate that all required software is properly installed
- Verify compatibility with your AKS cluster version
- Test node bootstrap process

### Monitoring
- Monitor image performance and reliability
- Track image usage across different NodePools
- Set up alerts for image-related failures
- Log image deployment metrics

## Troubleshooting

### Common Issues

**Image Not Found**
```bash
# Verify image exists and permissions
az image show --resource-group myResourceGroup --name myCustomAKSImage
```

**Node Bootstrap Failures**
```bash
# Check node logs for bootstrap errors
kubectl logs -n kube-system <node-name>
```

**Permission Issues**
```bash
# Verify Karpenter has access to custom images
az role assignment list --assignee <karpenter-identity> --scope /subscriptions/<subscription-id>
```

### Debug Commands
```bash
# Check AKSNodeClass status
kubectl describe aksnodeclass <nodeclass-name>

# View node events
kubectl get events --field-selector involvedObject.kind=Node

# Check Karpenter controller logs
kubectl logs -n karpenter deployment/karpenter -f
```