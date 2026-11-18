# karpenter-provider-azure

## Attention: stale information

Secondary sources of information like this or other documentation may not reflect the latest state of what applies or what is being deployed. This could be a result of:

- Implementation/documentation inaccuracies
- Updates are applied to production but are not yet reflected here
- Undocumented variants of the implementation exist at the same time, which is common in multi-region systems with testing environments
- Etc.

So:

- **Fact-check them before making them high-stake assumptions**.
- **Try to keep the information here up-to-date when you notice inaccuracies**.
  - Maintain a high bar on the need to update. E.g., it should be updated only when there is an inaccuracy, not when there is not enough information or when "adding this line might be helpful".
  - **The secondary information should be kept minimal, only when there is a clear benefit compared to researching them live**, so we don't have too much info that can get outdated at any time.

## Overview

This repository is the AKS Karpenter Provider: the Azure cloud provider implementation for
[Karpenter](https://karpenter.sh/). It watches for unschedulable pods, evaluates their scheduling
constraints, provisions Azure nodes that satisfy them, and removes or consolidates nodes when they
are no longer needed.

## Important Deployment Configurations

- Each configuration may have different user expectations, design philosophy, codebase management, dev/test/release workflow, release timeline, and more.
  - However, the best practice is to **minimize those differences when able to**.
- **Pay extra attention to how code paths are being reused or diverge among modes in each dimension**.
  - When investigating, consider which configuration is in question, if applicable, and consider that the answer may or may not be the same between configurations.
  - **When implementing changes, consider which configurations should be targeted. Be prepared to center the discussions around them.**
    - By default, aim for all relevant configurations. **Justify violations in the discussions, if any.**
- There are more, mostly visible in the code where environment variables are located (most likely in the `options` package). The important ones are below.

### AKS

#### Deployment Models

- **Node Auto Provisioning (NAP)** — Karpenter runs as an AKS-managed addon. More details in [Microsoft Learn docs](https://learn.microsoft.com/azure/aks/use-node-auto-provisioning?pivots=azure-cli).
  - AKS system manages the deployment, token rotation, Helm charts, Karpenter version updates, etc.
  - Users just need to enable NAP for their AKS clusters.
  - The code responsible for integration with AKS-managed addon system is mostly internal, not visible in this repo.
- **Self-hosted** — Karpenter runs as a standalone deployment in the cluster.
  - Users manage the deployment, token rotation, Helm charts, Karpenter version updates, etc.
  - Users need to deploy Karpenter themselves on their AKS clusters. Resources should be provided in this repo.

Images are built differently for the two models and some code paths diverge, but the source is
shared. A change that is correct for one model is not automatically correct for the other.

#### Provisioning Modes

Defined in [`pkg/consts/consts.go`](./pkg/consts/consts.go) and selected via the `--provision-mode` flag /
`PROVISION_MODE` environment variable. They fall into several families:

- **AKS Machine instance-based** — Karpenter creates and manages "AKS Machine" resources, representing instances. AKS Machine API handles management of underlying resources (e.g., VMs, NICs, extensions). Common keywords are "AKS machine", "Machine API", "Machines", etc. E.g., [`pkg/providers/instance/aksmachineinstance.go`](./pkg/providers/instance/aksmachineinstance.go)
  - `aksmachineapi`
  - `aksmachineapiheaderbatch` - Extends `aksmachineapi` but with batching via API headers, improving perf/scale.
    - Being used by NAP
    - Not usable by self-hosted due to API restrictions
- **VM instance-based** — Karpenter creates and manages Azure VMs, NICs, and extensions directly. Common keywords are "VM instance", "Old VM mode", etc. E.g., [`pkg/providers/instance/vminstance.go`](./pkg/providers/instance/vminstance.go).
  - `aksscriptless` - Karpenter manages VM bootstrapping data
  - `bootstrappingclient` - Karpenter calls Node Bootstrapping API to get VM bootstrapping data
    - Being used by NAP, but being phased out in favor of `aksmachineapiheaderbatch`
    - Not usable by self-hosted due to API restrictions

## User-Configurable Interfaces

"User" in this case refers to a Kubernetes cluster admin of an AKS cluster with Karpenter deployed. They typically manage (at least):

- Relevant Azure resources (incl. AKS managed cluster instance) via Azure APIs
- The Kubernetes cluster via Kubernetes APIs

Common considerations when making changes, whether supporting new features or improving/fixing existing ones:

- **Keep these "user perspectives" in mind and be prepared to center the discussions around them.**
- **Consider general API change practices** (e.g., defaulting, validation, whether it is breaking, backward compatibility, trying to match existing patterns) **as well as Karpenter-specific mechanisms around it** (e.g., in CRDs: hash drift, drift detection, lifecycle consequences, whether anything else depends on the changing fields like labels).
- Keep in mind that interface changes may not always take effect on already-applied configurations in existing resources (clusters / deployed CRs). **Call out the analysis explicitly in the discussions.**

### Karpenter Deployment and Its Cluster-Level Configurations

See the [Deployment Models](#deployment-models) section for details.

### AKS Cluster-Level Configurations

This refers to existing AKS cluster-level features, where the instructions (e.g., `az aks update ...`) can be found in Microsoft Learn docs for AKS.

Enabling them on an AKS cluster with NAP/self-hosted Karpenter may intentionally change certain behaviors (e.g., the node Karpenter provisions may have those features enabled).

- **For NAP, it is expected that the integration will be smooth: it either just works or is blocked on an enablement attempt**
- For self-hosted, some features may require reconfiguring Karpenter deployment (e.g., enable certain flags), managed by the user

### Other AKS Cluster-Level Interfaces

There could be some other user interfaces that AKS exposes and that affect or are affected by Karpenter.

### Karpenter Pool-Level Configurations

They exist only in the form of K8s CRs as of today.

- For NAP:
  - AKS system deploys and handles upgrades of CRDs
  - AKS system may deploy default CRs that users can customize
  - Users may deploy and customize their own CRs

- For self-hosted:
  - Users handle all deployments

#### NodePool CRs

They are maintained by upstream Karpenter and are provider-agnostic. They represent a provisioning node pool. Each contains a reference to a NodeClass.

#### NodeClass CRs

They are maintained by the Karpenter provider (us here). They represent a set of expectations for nodes to be provisioned and contain provider-specific features and properties. For AKS, the properties here are roughly equivalent to what's in [AgentPools API](https://learn.microsoft.com/en-us/cli/azure/aks/nodepool?view=azure-cli-latest#az-aks-nodepool-add).

- **When adding a new AKS feature support based on an AgentPool-level feature, changes will most likely be needed here. Keep parity with AKS as much as practical, and call out the analysis explicitly in the discussions.**
- For `AKSNodeClass`, `v1beta1` is the current version; `v1alpha2` is deprecated, but **should usually be kept in parity** for compatibility.

## Code Structure

### Upstream Karpenter

- Not in this codebase, but rather referring to [kubernetes-sigs/karpenter](https://github.com/kubernetes-sigs/karpenter). The term "Upstream Karpenter" may be used interchangeably with "Karpenter Core".
- It is a library that each Karpenter "Provider" (incl. this repo) imports. The division is intended to separate provider-agnostic and provider-specific logic.
- Core controllers live there. At a minimum, providers are responsible for implementing the `CloudProvider` interface to complete those core controllers.
- Core APIs live there.

### Initialization

- [`pkg/operator/`](./pkg/operator/) — wiring with options/flags.

### Interface

- [`pkg/apis/`](./pkg/apis/) — user-facing API / K8s CRDs.
  - Provider-specific CRDs (e.g., `AKSNodeClass`) live here, as well as copies of core CRDs.
- [`charts/`](./charts/) — `karpenter` and `karpenter-crd` Helm charts. CRD templates are copied
  from `pkg/apis/crds` by `make verify`.

### Core Routines

- [`pkg/cloudprovider/`](./pkg/cloudprovider/) — implementation of the upstream Karpenter `CloudProvider` interface.
- [`pkg/controllers/`](./pkg/controllers/) — provider-specific controllers (e.g., garbage collections, instance in-place update, nodeclass, instance type)

### Downstream Logic

- [`pkg/providers/`](./pkg/providers/) — inner business logic by domain. Usually consumed by core routines.

### Dev/Test

- [`pkg/fake/`](./pkg/fake/) and [`pkg/test/`](./pkg/test/) — fakes and the test environment used by unit/acceptance tests.
- [`test/`](./test/) — real end-to-end suites run against a live AKS cluster. See [test/README.md](./test/README.md).
- [`hack/`](./hack/) — toolchain, codegen, validation, and release scripts.

## Testing

There are several layers of tests. **In many cases, you are recommended to add new tests in applicable categories.**

- For bug fixes, whenever applicable, the best way to demonstrate the impact is to show the added tests failing without your changes and passing with your changes.
- Keep in mind general testing best practices. E.g., a test is only useful if it would fail when the intended behavior regresses; be careful not to use a unit test to justify a bad behavior; do not over-test inner behaviors that can change without harm, etc.

### Traditional Unit Tests

Scattered around the codebase. Should be covered by `make test` and PR check-in.
Avoid using Ginkgo for new tests of this kind. Prefer the [standard Go testing framework](https://pkg.go.dev/testing).

### Acceptance Tests / "Unit End-to-End Tests"

These are unit tests targeting the integration between modules within the codebase, usually starting by simulating pending pod pressure. They often use fakes from `pkg/fake` to simulate dependencies. These tests are usually named `suite_*_test.go`.
The best practice is to keep these tests near their origin routines. E.g., `pkg/cloudprovider` or `pkg/controllers`. Always use [Ginkgo](https://pkg.go.dev/github.com/onsi/ginkgo). They should be covered by `make test` and PR check-in.

**When adding tests, extend, condition, or match the design/implementation/considerations of existing ones.** Only start new files or patterns when there is a valid design justification/clear trade-off, as it will be harder to compare and contrast existing tests/behaviors.

### E2E Tests

Tests run against Karpenter deployed on a real cluster. Relevant code and test suites are in the `test/` folder (not `pkg/test`, which is for acceptance tests). They are not covered by PR check-in.

- On making changes, research the possibilities of user-configurable interfaces, functionalities, or lifecycle/controller expectations affected or introduced by your changes. **Ensure that the E2E tests cover most possibilities of them, whether through existing tests or new tests. Explicitly call out the analysis, justifiable gaps, and results**. Be prepared to pitch why the results are enough to justify the correctness of the changes.
- In larger or framework-affecting changes, E2E testing strategy can be a design consideration to be called out explicitly.
- The cost of running E2E tests is higher than unit tests. Only do so on significant changes and on pull request submission.

#### Self-Hosted E2E Tests

Should be covered by `make e2etests TEST_SUITE=<suite>` after a cluster is created and Karpenter is deployed. See [Makefile-az.mk](Makefile-az.mk) for deployment utilities. Note that certain tests may have assumptions about what the cluster should look like.

Alternatively, [E2EMatrixTrigger](https://github.com/Azure/karpenter-provider-azure/actions/workflows/e2e-matrix-trigger.yaml) and [E2E](https://github.com/Azure/karpenter-provider-azure/actions/workflows/e2e.yaml) GitHub Actions will handle the process end-to-end.

#### NAP E2E Tests

AKS runs the same set of test suites on a cluster with NAP. This is not currently accessible by public contributors.

### Manual Testing

You can (and are encouraged to) deploy Karpenter with your changes on a real cluster, then see if things work as you intended. See [Makefile-az.mk](Makefile-az.mk) for deployment utilities. Usually, `make az-all` is the easiest.

## Development

See important Make targets below.

| Target | Purpose |
|--------|---------|
| `make verify` | Codegen, boilerplate, CRD copy, validation, linting, `actionlint`, `zz_generated.deepcopy.go`. **Run after any code change.** Do not run `go generate` directly and do not hand-edit generated output. |
| `make presubmit` | `make verify` plus unit tests. **Run before submitting.** |
| `make toolchain` | Install the correct tool versions; fixes most local environment failures. |

## Guidelines

See [Karpenter design guide](https://karpenter.sh/docs/contributing/design-guide/) as a starting point.

When reviewing a pull request, also read [REVIEW.md](REVIEW.md) for the review-specific checks and output requirements.

### Operational Goals

- Lean towards preserving the existing functional behavior. All changes must be justifiable and explicitly addressed in the discussions.
- **Always trace the behavioral consequences of the code change deeply, even if changes are not expected, and call out the analysis explicitly in the discussions.** E.g. research the possibilities of user-configurable interfaces, functionalities, or lifecycle/controller expectations affected or introduced by your changes.
  - Be mindful of design assumptions or resource dependencies, explicit or implicit, made by each component of the code. Assumption violations are not always obvious, but can have irreversible design issues.
- There are existing clusters with Karpenter/NAP in production. Consider the consequences to them in addition to the consequences to new deployments. **Explicitly call out the analysis on behavior changes**.
  - Not having existing cluster E2E tests is one of the OSS test framework gaps.
- Be mindful of changes to call patterns to dependency APIs. E.g., some changes may increase API volume, which may have throttling implications, etc. **Trace the consequence and explicitly call out the analysis on behavior changes**.

### Golang

- Follow Go best practices and the conventions already present in the surrounding package.
- Propagate `context.Context` and honor cancellation on every Azure and Kubernetes call.
- Wrap errors with enough context to identify the resource and operation.
- Use `sigs.k8s.io/controller-runtime/pkg/log` for logging, consistent with the rest of `pkg/`.
- Prefer the smallest change consistent with the existing code over an unrelated refactor.

### Dependency API Integrations

- On changes, make sure you fully understand at least the items below, **but do not limit your analysis to them**. Brainstorm possible issues and questions to answer. Make clear the assumptions we are making about them and their strength. **Call out the analysis as key design considerations.**
  - Contract (e.g., what's not allowed, what can change without notifying us)
  - Failure modes (e.g., silent errors, categorized errors)
  - Corner cases (e.g., does "creation succeed" mean the resource is already created and visible?)
  - Perf/scale concerns (e.g., throttling)
  - Availability/reliability concerns (e.g., reputation and issue history)
  - Maintenance concerns (e.g., versioning)
  - Other related behavioral assumptions.
- If the API involves resource management, consider the design of the resource ownership model. E.g., typically, whoever is responsible for resource creation is responsible for its lifecycle including garbage collection and cleanup. **Call out the analysis as key design considerations.**

### Dev Tools

- When writing `az` CLI commands in scripts or docs, always pass an explicit `--output` format.
  Users may have changed their default via `az configure`, which breaks scripts that assume JSON.

### GitHub Actions

- Pin actions to a full commit SHA, never a mutable tag.
- Workflows run behind `step-security/harden-runner` with `egress-policy: block`. A new network
  dependency requires adding the endpoint to `allowed-endpoints`, or the job fails.
- Keep `permissions` minimal and explicit.

### Documentation

- Do estimate the accumulated size and complexity of design changes, additions, decisions, considerations, assumptions, sub-problems, dependencies on other work/issues, potential FAQs, code merging strategy, testing strategy, rollout strategy, etc. Consider adding to [design docs](designs/) for large ones, while describing the changes in PR description for smaller ones.
  - It is a good practice to document even resolved concerns to avoid repeated questions, facilitate context research, and avoid violating design validity in the future.
- Add in-code comments or in-module docs whenever appropriate, not excessively.
- Refer to [#attention-stale-information](#attention-stale-information) for best practices on updates to existing documentation of any kind.

### Publishing

- Refer to [CONTRIBUTING.md](CONTRIBUTING.md) for more guidelines.
- It is your duty as an AI to remind the human author of what can only be done by humans and not by you. You may help guide them in the process.
- Don't forget to call out the analyses we planned. E.g., in the PR description.
- Keep all English text there especially concise. Use ASD-STE100 Simplified Technical English.
