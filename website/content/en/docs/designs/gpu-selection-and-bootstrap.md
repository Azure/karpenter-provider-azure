
---
title: "GPU Provisioning Requirements for Preview"
linkTitle: "GPU Provisioning Requirements for Preview"
weight: 80
description: 
  This document outlines all of the functional requirements for the preview AKS API for karpenter GPU Support. 
---

### 1. Bootstrapping GPU Nodes

Nodes should successfully bootstrap GPU Nodes for both Generation 1 and Generation 2 Virtual Machine Generations using the Ubuntu2204 Image and the nvidia device plugin. The long term direction of AKS Is to leverage [GPU Operator](https://github.com/NVIDIA/gpu-operator#audience-and-use-cases) for installation of the required software to operate with GPUS in the AKS Cluster.
Karpenter should populate the following fields in the bootstrapping contract for preview:

- **1a.** GPUNode
- **1b.** GPUDriverVersion
- **1c.** ConfigGPUDriverIfNeeded
- **1d.** GPUImageSHA

**Note:** The following are explicitly ignored for the preview:

- **1e.** SGXNode
- **1f.** MIGNode
- **1g.** EnableGPUDevicePluginIfNeeded

### 2. User-Level Control

The user, through the requirements API in the provisioner, should be able to limit or exclude GPU instance types using the following dimensions:
- **2a.** vCPU
- **2b.** GPUCount

### 3. Provisioner Limits and Workload

Provisioner limits should respect GPU resource types. Workloads should be able to request GPUs via the `nvidia.com/gpu` resource requests.

**Provisioner Example:**

```yaml
apiVersion: karpenter.sh/v1alpha5
kind: Provisioner
metadata:
  name: default
spec:
  limits:
    resources:
      cpu: 100
      nvidia.com/gpu: 2 
```
**Workload Example**
```
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gpu-nvidia
spec:
  containers:
  - image: public.ecr.aws/eks-distro/kubernetes/pause:3.2
    name: gpu-nvidia
    resources:
      limits:
        nvidia.com/gpu: "1"
      requests:
        cpu: "1"
        memory: 256M
```


### 4. GPU Driver Testing

Bootstrapping should work for both graphics and compute workloads. Note that NV Series GPUs target graphics workloads while NC targets compute. The different series use different drivers (GRID for NV and CUDA for NC). We need to:

- **4a.** Test support for Grid drivers
- **4b.** Test support for CUDA drivers
- **4c.** Test support for Converged drivers

The way we determine these drivers is via trial and error, and there is not a great way to check other than manually provisioning if a specific sku with a driver, and seeing that it works.

For Converged drivers they are a mix of multiple drivers installing vanilla cuda drivers will fail to install with opaque errors.
nvidia-bug-report.sh may be helpful, but usually it tells you the pci card id is incompatible.

So manual trial and error, or leveraging other peoples manual trial and error, and published gpu drivers seems to be the prefered method for approaching this. 
see https://github.com/Azure/azhpc-extensions/blob/daaefd78df6f27012caf30f3b54c3bd6dc437652/NvidiaGPU/resources.json for the HPC list of skus and converged drivers, and the driver matrix used by HPC

**Ownership:** Node SIG is responsible for ensuring successful and functional installation. Our goal is to share a bootstrap contract, and the oblication of a functional successfully bootstrapped vhd relies on the node sig. 
Sharing between agentbaker and karpenter is an additional goal, as to which drivers are supported, and for which skus. As well as extending skewer for better GPU Support is also a goal.

#### 4a. GPU Driver installation 
The bootstrap parameter `DRIVER_VERSION` will select the driver version and `ConfigGPUDriverIfNeeded` serves the purpose of telling the VHD to install all gpu helpers such as nvidia-smi etc. 
### 5. containerd configuration 
GPU's also use a different container runtime, so we need to be able to dynamically modify the containerd configuration we pass into karpenter's bootstrapping contract to include the proper configuration of the gpu settings

[plugins."io.containerd.grpc.v1.cri".containerd]
Needs to be able to alternate between normal containerd configuration of "io.containerd.runc" and "nvidia-container-runtime" in order to properly connect with device plugin and add the capacity for "nvidia.com/gpu" to our nodes to allow for scheduling of GPU Nodes.

**Note** We also need to ensure that containerd, and kubelet are using the same cgroup driver. So when leveraging cgroupsv2, we need to make sure we are setting the kubelet flag, we need to update the containerd con 

### 6. Nvidia Device Plugin
The NVIDIA device plugin for Kubernetes is designed to enable GPU support within Kubernetes clusters. Kubernetes, by default, doesn’t recognize GPUs as a native resource. However, using device plugins, one can extend Kubernetes to manage other types of hardware resources, such as GPUs from NVIDIA.

We will require the customer to install the nvidia device plugin daemonset to enable GPU support through karpenter.

When a node with Nvidia GPUS joins the cluster, the device plugin detects available gpus and notifies the k8s scheduler that we have a new Allocatable Resource type of `nvidia.com/gpu` along with a resource quanity that can be considered for scheduling. 

Note the device plugin is also reponsible for the allocation of that resource and reporting that other pods can not use that resource and marking it as used by changing the allocatable capacity on the node.

## Changes to Requirements API

The Requirements API needs to provide an interface for selecting GPU SKUs, sizes, and preferences.

## Karpenter Azure Provisioner: Available Labels

Here are some relevant labels:

| Label                                 | Description                               |
|---------------------------------------|-------------------------------------------|
| `karpenter.azure.com/sku-family`       | Family of the SKU (N) for GPU          |
| `karpenter.azure.com/sku-cpu`          | Number of virtual CPUs                    |
| `karpenter.azure.com/sku-accelerator`  | Type of accelerator (e.g., Nvidia)        |

**Note:** GPU SKUs usually support only a single hypervisor generation. Explicit image selection is moot in most cases, though there are some exceptions.

## Karpenter AWS Provisioner: Existing GPU Labels

| Label                                         | Example Value | Description                                         |
|-----------------------------------------------|---------------|-----------------------------------------------------|
| `karpenter.k8s.aws/instance-gpu-name`          | `t4`          | Name of the GPU on the instance, if available       |
| `karpenter.k8s.aws/instance-gpu-manufacturer`  | `nvidia`      | Name of the GPU manufacturer                        |
| `karpenter.k8s.aws/instance-gpu-count`         | `1`           | Number of GPUs on the instance                      |
| `karpenter.k8s.aws/instance-gpu-memory`        | `16384`       | Number of mebibytes of memory on the GPU            |

**Note:** The AKS VHD Validator currently only supports Nvidia GPUs, which will be the focus for the preview. Our apis don't natively support gpu memory, so we will need to add this at a later date for proper support.

## Proposed New Labels for Azure

| Selector Label                            | Values  | Description                                        | Where to get the value                  |
|-------------------------------------------|---------|----------------------------------------------------|----------------------------------------|
| `karpenter.azure.com/instance-gpu-name`    | `t4`    | Name of the GPU on the instance, if available      | vmSizeAcceleratorType                  |
| `karpenter.azure.com/instance-gpu-manufacturer` | `nvidia` | GPU Manufacturer | Can be inferred, all Nvidia for preview    |
| `karpenter.azure.com/instance-gpu-count`   | `1`     | Number of GPUs on the instance                     | sku.capabilities["GPU"]                |

## Supported GPU SKUs and Expected Drivers

This table will outline for each SKU, the supported OS and the driver we commit to passing into the contract. This serves as documentation for which SKUs will be supported in Karpenter preview and should be actively expanded.

**Note:** For bootstrapping with Nvidia drivers, we need to ensure we are passing the correct driver and OS into the contract.


| SKU                     | Supported OS | GPU Driver                  |
|-------------------------|--------------|-----------------------------|
| standard_nc6            | Ubuntu       | Nvidia470CudaDriver   |
| standard_nc12           | Ubuntu       | Nvidia470CudaDriver   |
| standard_nc24           | Ubuntu       | Nvidia470CudaDriver   |
| standard_nc24r          | Ubuntu       | Nvidia470CudaDriver   |
| standard_nv6            | Ubuntu       | Nvidia535GridDriver   |
| standard_nv12           | Ubuntu       | Nvidia535GridDriver   |
| standard_nv12s_v3       | Ubuntu       | Nvidia535GridDriver   |
| standard_nv24           | Ubuntu       | Nvidia535GridDriver   |
| standard_nv24s_v3       | Ubuntu       | Nvidia535GridDriver   |
| standard_nv24r          | Ubuntu       | Nvidia535GridDriver   |
| standard_nv48s_v3       | Ubuntu       | Nvidia535GridDriver   |
| standard_nd6s           | Ubuntu       | Nvidia525CudaDriver   |
| standard_nd12s          | Ubuntu       | Nvidia525CudaDriver   |
| standard_nd24s          | Ubuntu       | Nvidia525CudaDriver   |
| standard_nd24rs         | Ubuntu       | Nvidia525CudaDriver   |
| standard_nc6s_v2        | Ubuntu       | Nvidia525CudaDriver   |
| standard_nc12s_v2       | Ubuntu       | Nvidia525CudaDriver   |
| standard_nc24s_v2       | Ubuntu       | Nvidia525CudaDriver   |
| standard_nc24rs_v2      | Ubuntu       | Nvidia525CudaDriver   |
| standard_nc6s_v3        | Mariner, Ubuntu  | Nvidia525CudaDriver  |
| standard_nc12s_v3       | Mariner, Ubuntu  | Nvidia525CudaDriver  |
| standard_nc24s_v3       | Mariner, Ubuntu  | Nvidia525CudaDriver  |
| standard_nc24rs_v3      | Mariner, Ubuntu  | Nvidia525CudaDriver  |
| standard_nd40s_v3       | Mariner, Ubuntu  | Nvidia525CudaDriver  |
| standard_nd40rs_v2      | Mariner, Ubuntu  | Nvidia525CudaDriver  |
| standard_nc4as_t4_v3    | Mariner, Ubuntu  | Nvidia525CudaDriver  |
| standard_nc8as_t4_v3    | Mariner, Ubuntu  | Nvidia525CudaDriver  |
| standard_nc16as_t4_v3   | Mariner, Ubuntu  | Nvidia525CudaDriver  |
| standard_nc64as_t4_v3   | Mariner, Ubuntu  | Nvidia525CudaDriver  |
| standard_nd96asr_v4     | Ubuntu       | Nvidia525CudaDriver   |
| standard_nd112asr_a100_v4| Ubuntu      | Nvidia525CudaDriver   |
| standard_nd120asr_a100_v4| Ubuntu      | Nvidia525CudaDriver   |
| standard_nd96amsr_a100_v4| Ubuntu      | Nvidia525CudaDriver   |
| standard_nd112amsr_a100_v4| Ubuntu     | Nvidia525CudaDriver   |
| standard_nd120amsr_a100_v4| Ubuntu     | Nvidia525CudaDriver   |
| standard_nc24ads_a100_v4| Ubuntu       | Nvidia525CudaDriver   |
| standard_nc48ads_a100_v4| Ubuntu       | Nvidia525CudaDriver   |
| standard_nc96ads_a100_v4| Ubuntu       | Nvidia525CudaDriver   |
| standard_ncads_a100_v4  | Ubuntu       | Nvidia525CudaDriver   |
| standard_nc8ads_a10_v4  | Ubuntu       | Nvidia535GridDriver   |
| standard_nc16ads_a10_v4 | Ubuntu       | Nvidia535GridDriver   |
| standard_nc32ads_a10_v4 | Ubuntu       | Nvidia535GridDriver   |
| standard_nv6ads_a10_v5  | Ubuntu       | Nvidia535GridDriver   |
| standard_nv12ads_a10_v5 | Ubuntu       | Nvidia535GridDriver   |
| standard_nv18ads_a10_v5 | Ubuntu       | Nvidia535GridDriver   |
| standard_nv36ads_a10_v5 | Ubuntu       | Nvidia535GridDriver   |
| standard_nv36adms_a10_v5| Ubuntu       | Nvidia535GridDriver   |
| standard_nv72ads_a10_v5 | Ubuntu       | Nvidia535GridDriver   |
| standard_nd96ams_v4     | Ubuntu       | Nvidia525CudaDriver   |
| standard_nd96ams_a100_v4| Ubuntu       | Nvidia525CudaDriver   |

