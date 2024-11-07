## Overview

This document details a quick and easy way to reestablish the workshop env, if you've been disconnected from the Cloud Shell and find the setup is missing.

## Scripts

Re-select your subscription to use:

```bash
export AZURE_SUBSCRIPTION_ID=<personal-azure-sub>
az account set --subscription ${AZURE_SUBSCRIPTION_ID}
```

With the subscription set, the folowing command will reestablish everything else needed for the workshop:

```bash
# Recreate workshop directory
mkdir -p ~/environment/karpenter/bin
export PATH=$PATH:~/environment/karpenter/bin


# Reinstall tools
cd ~/environment/karpenter/bin

# yq - used by some of the scripts below
wget https://github.com/mikefarah/yq/releases/latest/download/yq_linux_amd64 -O ~/environment/karpenter/bin/yq
chmod +x ~/environment/karpenter/bin/yq

# k9s - terminal UI to interact with the Kubernetes clusters
wget https://github.com/derailed/k9s/releases/download/v0.32.5/k9s_Linux_amd64.tar.gz -O ~/environment/karpenter/bin/k9s.tar.gz
tar -xf k9s.tar.gz


# Setup env vars
export CLUSTER_NAME=karpenter
export RG=karpenter
export LOCATION=westus3
export KARPENTER_NAMESPACE=kube-system

# Get kubeconfig again
az aks get-credentials --name "${CLUSTER_NAME}" --resource-group "${RG}" --overwrite-existing
```