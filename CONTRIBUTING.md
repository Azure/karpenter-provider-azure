# Contributing

## Overview
This project welcomes contributions and suggestions. Most contributions require you to agree to a Contributor License Agreement (CLA) declaring that you have the right to, and actually do, grant us the rights to use your contribution. For details, visit https://cla.microsoft.com.

When you submit a pull request, a CLA-bot will automatically determine whether you need to provide a CLA and decorate the PR appropriately (e.g., label, comment). Simply follow the instructions provided by the bot. You will only need to do this once across all repositories using our CLA.

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/). For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.

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
**When to use**: Acceptance tests are coarse grained tests that integrate with the upstream karpenter-core library and only fake the API calls to Azure clients. These are behavior-driven and should start from pending pod pressure whenever possible.

**File Locations**: Under `pkg/` next to the related components.   

**Testing framework**: [Ginkgo](https://pkg.go.dev/github.com/onsi/ginkgo)

### E2E Tests
**When to use**: E2E tests aim to be as close to prod as possible. An actual cluster is spun up, scale ups + downs occur, and actual Azure clients are invoked rather than utilizing fakes/mocks.

**File Locations**: Under `test/` with specific suites under `test/pkg/suites`  

**Testing framework**: [Ginkgo](https://pkg.go.dev/github.com/onsi/ginkgo)

## FAQs

### What's the difference between `config.go` and `settings.go`?
* `config.go` is in the [auth](https://github.com/Azure/karpenter/blob/main/pkg/auth/config.go) package and provides configurations needed to authenticate with Azure clients. 
* `settings.go` is in the [apis](https://github.com/Azure/karpenter/blob/main/pkg/apis/settings/settings.go) package and provides settings needed for Karpenter to  access a particular cluster.
### What should be used for logging?
* [klog](https://github.com/search?q=repo%3AAzure%2Fkarpenter%20klog&type=code) is only invoked when creating clients or authorizers.
* [zapr](https://github.com/search?q=repo%3AAzure%2Fkarpenter%20zap&type=code) is only invoked in our debug package.
* [knative.dev/pkg/logging](https://pkg.go.dev/knative.dev/pkg/logging) _should_ be used everywhere else.

### What is `skaffold.yaml`?
[skaffold.yaml](https://github.com/Azure/karpenter/blob/main/skaffold.yaml) is the configuration file for deploying Karpenter locally via [skaffold](https://skaffold.dev/docs/).

Why are its modifications showing up locally even though I didn't change it? 
  * To deploy/test locally, we run `make az-all`. This make command is composed of many different steps needed to deploy. One step patches your local skaffold.yaml file by updating certain variables based on env vars defined in the `Makefile-az.mk`. 

### Why are there multiple locations for testing environments?
* [pkg/test/environment.go](https://github.com/Azure/karpenter/blob/main/pkg/test/environment.go)
* [test/pkg/environment/common/environment.go](https://github.com/Azure/karpenter/blob/main/test/pkg/environment/common/environment.go)

