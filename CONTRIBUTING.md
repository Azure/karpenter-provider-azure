# Contributing

## Overview
This project welcomes contributions and suggestions. Most contributions require you to agree to a Contributor License Agreement (CLA) declaring that you have the right to, and actually do, grant us the rights to use your contribution. For details, visit https://cla.microsoft.com.

When you submit a pull request, a CLA-bot will automatically determine whether you need to provide a CLA and decorate the PR appropriately (e.g., label, comment). Simply follow the instructions provided by the bot. You will only need to do this once across all repositories using our CLA.

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/). For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.

## Development

> A [GitHub Codespaces]((https://github.com/features/codespaces)) development flow is described below, which you can use to test Karpenter functionality on your own cluster, and to aid rapid development of this project.

1. **Install VSCode**: Go [here](https://code.visualstudio.com/download) to download VSCode for your platform. After installation, in your VSCode app install the "GitHub Codespaces" Extension. See [here](https://code.visualstudio.com/docs/remote/codespaces) for more information about this extension.

2. **Create Codespace** (~2min): In browser, click Code / "Create a codespace on main" (for better experience customize to use 4cores/8GB), wait for codespace to be created. It is created with everything needed for development (Go, Azure CLI, kubectl, skaffold, useful plugins, etc.) Now you can open up the Codespace in VSCode: Click on Codespaces in the lower left corner in the browser status bar region, choose "Open in VSCode Desktop". (Pretty much everything except for `az login` and some `az role assignment` works in browser; but VSCode is a better experience anyway.)

3. **Provision cluster, build and deploy Karpenter** (~5min): Set `AZURE_SUBSCRIPTION_ID` to your subscription (and customize region in `Makefile-az.mk` if desired). Then at the VSCode command line run `make az-all`. This logs into Azure (follow the prompts), provisions AKS and ACR (using resource group `$CODESPACE_NAME`, so everything is unique / scoped to codespace), builds and deploys Karpenter, deploys sample `default` Provisioner and `inflate` Deployment workload.

4. Manually scale the `inflate` Deployment workload, watch Karpenter controller log and Nodes in the cluster. Example of manually scaling up to 3 pods:
```
kubectl scale deployments/inflate --replicas=3
```

### Debugging
To debug Karpenter in-cluster, use `make az-debug`, wait for it to deploy, and attach from VSCode using Start Debugging (F5). After that you should be able to set breakpoints, examine variables, single step, etc. (Behind the scenes, besides building and deploying Karpenter, `skaffold debug` automatically and transparently applies the necessary flags during build, instruments the deployment with Delve, adjusts health probe timeouts - to allow for delays introduced by breakpoints, sets up port-forwarding, etc.; more on how this works is [here](https://skaffold.dev/docs/workflows/debug/).

Once done, you can delete all infra with `make az-rmrg` (it deletes the resource group), and can delete the codespace (though it will be automatically suspended when not used, and deleted after 30 days.)

### Developer notes
- If you see platform architecture error during `skaffold debug`, adjust (or comment out) `--platform` argument.
- If you are not able to set/hit breakpoints, it could be an issue with source paths mapping; see comments in debug launch configuration (`launch.json`)

### FAQs
Q: I was able to trigger Karpenter to execute scaling up nodes as expected, using my own customized deployment of pods. However, scaling down was not handled automatically when I removed the deployment. The two new nodes created by Karpenter were left around. What is going on?

A: Additional system workloads (such as metrics server) can get scheduled on the new nodes, preventing Karpenter from removing them. Note that you can always use `kubectl delete node <node>`, which will have Karpenter drain the node and terminate the instance from cloud provider.

Q: When running some of the tests locally, the environment failed to start. How can I resolve this?

A: Oftentimes, especially for pre-existing tests, running `make toolchain` will fix this. This target will ensure that you have the correct versions of binaries installed.

## Testing
We have three types of testing:
* Unit Tests
* Acceptance Tests
* End-to-end Tests

### Unit Tests
**When to use**: Use for fine grained testing of functions, classes, etc.

**File Location(s)**: Under `pkg/*` next to the related components.

**Testing framework**: [Go standard tests](https://pkg.go.dev/testing)

### Acceptance Tests
**When to use**: Acceptance tests are coarse grained tests that integrate with the upstream karpenter library and only fake the API calls to Azure clients. These are behavior-driven and should start from pending pod pressure whenever possible.

**File Locations**: Under `pkg/` next to the related components.

**Testing framework**: [Ginkgo](https://pkg.go.dev/github.com/onsi/ginkgo)

### E2E Tests
**When to use**: E2E tests aim to be as close to prod as possible. An actual cluster is spun up, scale ups + downs occur, and actual Azure clients are invoked rather than utilizing fakes/mocks.

**File Locations**: Under `test/` with specific suites under `test/pkg/suites`

**Testing framework**: [Ginkgo](https://pkg.go.dev/github.com/onsi/ginkgo)

## FAQs
### What's the difference between `config.go` and `settings.go`?
* `config.go` is in the [auth](https://github.com/Azure/karpenter/blob/main/pkg/auth/config.go) package and provides configurations needed to authenticate with Azure clients.
* `settings.go` is in the [apis](https://github.com/Azure/karpenter/blob/main/pkg/apis/settings/settings.go) package and provides settings needed for Karpenter to access a particular cluster.
### What should be used for logging?
* [klog](https://github.com/search?q=repo%3AAzure%2Fkarpenter%20klog&type=code) is only invoked when creating clients or authorizers.
* [zapr](https://github.com/search?q=repo%3AAzure%2Fkarpenter%20zap&type=code) is only invoked in our debug package.
* [sigs.k8s.io/controller-runtime/pkg/log](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/log) _should_ be used everywhere else.

### What is `skaffold.yaml`?
[skaffold.yaml](https://github.com/Azure/karpenter/blob/main/skaffold.yaml) is the configuration file for deploying Karpenter locally via [skaffold](https://skaffold.dev/docs/).

Why are its modifications showing up locally even though I didn't change it?
  * To deploy/test locally, we run `make az-all`. This make command is composed of many different steps needed to deploy. One step patches your local skaffold.yaml file by updating certain variables based on env vars defined in the `Makefile-az.mk`.

### Why are there multiple locations for testing environments?
* [pkg/test/environment.go](https://github.com/Azure/karpenter/blob/main/pkg/test/environment.go) is used for our acceptance tests.
* [test/pkg/environment/common/environment.go](https://github.com/Azure/karpenter/blob/main/test/pkg/environment/common/environment.go) is used for our end-to-end tests.
