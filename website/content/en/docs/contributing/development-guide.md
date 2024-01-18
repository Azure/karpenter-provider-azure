---
title: "Development Guide"
linkTitle: "Development Guide"
weight: 80
description: >
  Set up a Karpenter Azure provider development environment
---

A [GitHub Codespaces]((https://github.com/features/codespaces)) development flow is described below, which you can use to test Karpenter functionality on your own cluster, and to aid rapid development of this project.

1. **Install VSCode**:

    Go [here](https://code.visualstudio.com/download) to download VSCode for your platform. After installation, in your VSCode app install the "GitHub Codespaces" Extension. See [here](https://code.visualstudio.com/docs/remote/codespaces) for more information about this extension.
    
2. **Create Codespace** (~2min):

    In browser, click Code / "Create a codespace on main" (for better experience customize to use 4cores/8GB), wait for codespace to be created. It is created with everything needed for development (Go, Azure CLI, kubectl, skaffold, useful plugins, etc.) Now you can open up the Codespace in VSCode: Click on Codespaces in the lower left corner in the browser status bar region, choose "Open in VSCode Desktop". (Pretty much everything except for `az login` and some `az role assignment` works in browser; but VSCode is a better experience anyway.)
    
    More information on GitHub Codespaces is [here](https://github.com/features/codespaces).
    
3. **Provision cluster, build and deploy Karpenter** (~5min):

    Set `AZURE_SUBSCRIPTION_ID` to your subscription (and customize region in `Makefile-az.mk` if desired). Then at the VSCode command line run `make az-all`. This logs into Azure (follow the prompts), provisions AKS and ACR (using resource group `$CODESPACE_NAME`, so everything is unique / scoped to codespace), builds and deploys Karpenter, deploys sample `default` Provisioner and `inflate` Deployment workload.
    
    Manually scale the `inflate` Deployment workload, watch Karpenter controller log and Nodes in the cluster. Explore further with `make help` (mostly `az-*` targets).
    
    To debug Karpenter in-cluster, use `make az-debug`, wait for it to deploy, and attach from VSCode using Start Debugging (F5). After that you should be able to set breakpoints, examine variables, single step, etc. (Behind the scenes, besides building and deploying Karpenter, `skaffold debug` automatically and transparently applies the necessary flags during build, instruments the deployment with Delve, adjusts health probe timeouts - to allow for delays introduced by breakpoints, sets up port-forwarding, etc.; more on how this works is [here](https://skaffold.dev/docs/workflows/debug/).
    
    Once done, you can delete all infra with `make az-rmrg` (it deletes the resource group), and can delete the codespace (though it will be automatically suspended when not used, and deleted after 30 days.)
    
## Developer notes

- During step 1 you will observe `Running postCreateCommand...` which takes > 10 minutes. You don't have to wait for it to finish to proceed to step 2.
- The following errors can be ignored during step 2:

```shell
ERRO[0007] gcloud binary not found
...
ERRO[0003] gcloud binary not found
...
ERRO[0187] walk.go:74: found symbolic link in path: /workspaces/karpenter/charts/karpenter/crds resolves to /workspaces/karpenter/pkg/apis/crds. Contents of linked file included and used  subtask=0 task=Render
```

- If you see platform architecture error during `skaffold debug`, adjust (or comment out) `--platform` argument.
- If you are not able to set/hit breakpoints, it could be an issue with source paths mapping; see comments in debug launch configuration (`launch.json`)
