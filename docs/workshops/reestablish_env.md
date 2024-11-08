## Overview

This document details a quick and easy way to reestablish the workshop env, if you've been disconnected from the Cloud Shell and find the setup is missing.

## Determine Impact

If its just a simple disconnect the only thing missing will be the environment variables that were set.

You can test this with a simple `ls` command:
```bash
ls
```

If you see the `environment` folder as the output than its been a simple disconnect:
```
environment
```

In that case, all that's needed are reexporting the environment variables as follows, and you're done:
```bash
export PATH=$PATH:~/environment/karpenter/bin
export CLUSTER_NAME=karpenter
export RG=karpenter
export LOCATION=westus3
export KARPENTER_NAMESPACE=kube-system
```

Otherwise, continue the steps in this doc.

## Scripts

Re-select your subscription to use (replace `<personal-azure-sub>` with your azure subscription guid):

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