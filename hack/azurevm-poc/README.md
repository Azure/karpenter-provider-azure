# AzureVM Provision Mode — PoC

Karpenter provisioning Azure VMs on any Kubernetes cluster (non-AKS), using `AzureNodeClass` CRD.

## Quick Start

```bash
# From the repo root on the comtalyst/azurevm-e2e-test branch:
git checkout comtalyst/azurevm-e2e-test

# Run the PoC (creates Azure infra, k3s, deploys karpenter, triggers provisioning):
./hack/azurevm-poc/run-poc.sh

# Or with custom resource group and location:
./hack/azurevm-poc/run-poc.sh my-poc-rg eastus
```

## What It Does

1. Creates Azure resource group + VNet + subnet
2. Creates an Ubuntu 24.04 VM with k3s installed
3. Grants VM managed identity Contributor (RG) + Reader (subscription)
4. Builds the karpenter controller binary from this branch
5. Deploys the controller as a pod on k3s with `--provision-mode=azurevm`
6. Applies all Karpenter CRDs (including `AzureNodeClass`)
7. Creates an `AzureNodeClass` + `NodePool` + pending pod
8. Karpenter detects the pending pod and attempts to provision an Azure VM

## What You'll See

```
# Controller discovers CRDs:
"all required CRDs are available"

# Pricing loaded:
"updated on-demand pricing", "instanceTypeCount": 1426

# AzureNodeClass controllers running:
"Starting Controller", "controller": "azurenodeclass.status"
"Starting Controller", "controller": "azurenodeclass.hash"

# Provisioner reacts to pending pod:
"found provisionable pod(s)"
"computed new nodeclaim(s) to fit pod(s)", "nodeclaims": 1

# VM creation attempt (NIC + VM Azure API calls in logs)
```

## Prerequisites

- Azure CLI authenticated (`az login`)
- Go 1.25+
- kubectl
- SSH key pair (`~/.ssh/id_rsa`)
- ~10 minutes

## Branch Contents

This branch merges:
- `comtalyst/instancetype-overrides` — full azurevm PR stack (CRD, provision mode, VM provider, multi-sub, instance type overrides)
- `comtalyst/restrict-aks-label-domain` — label domain allowlist fix (adds `karpenter.azure.com/azurenodeclass` to the NodeClaim CRD validation allowlist)

## Cleanup

```bash
az group delete --name karpenter-azurevm-poc --yes --no-wait
```

## Known Limitations

- The provisioned VM uses a community gallery image and won't join the k3s cluster (no bootstrap script). It proves the Azure VM creation path works, not node registration.
- For full node-join, you need a custom image with cloud-init/bootstrap that registers with the k3s server.
- The `instanceTypes` override is set to `Standard_D2s_v3` — change it to test other SKUs.
