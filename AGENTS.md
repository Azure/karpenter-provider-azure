# Overview

This repository is the AKS Karpenter Provider: the Azure cloud provider implementation for
[Karpenter](https://karpenter.sh/). It watches for unschedulable pods, evaluates their scheduling
constraints, provisions Azure nodes that satisfy them, and removes or consolidates nodes when they
are no longer needed.

The same codebase serves two deployment models, described in [README.md](./README.md):

- **Node Auto Provisioning (NAP)** — Karpenter runs as an AKS-managed addon. AKS manages token
  rotation, Helm charts, Karpenter version updates, VM OS disk updates, and Linux node image
  upgrades.
- **Self-hosted** — Karpenter runs as a standalone deployment in the cluster. The user manages
  upgrades, token rotation, and Helm charts directly.

Images are built differently for the two models and some code paths diverge, but the source is
shared. A change that is correct for one model is not automatically correct for the other.

## Provisioning Modes

The provider supports several provisioning modes, defined in
[`pkg/consts/consts.go`](./pkg/consts/consts.go) and selected via the `--provision-mode` flag /
`PROVISION_MODE` environment variable:

- `aksscriptless`
- `bootstrappingclient`
- `aksmachineapi`
- `aksmachineapiheaderbatch`

These fall into two families with materially different implementations:

- **VM-based** (`aksscriptless`, `bootstrappingclient`) — the provider creates and manages Azure
  VMs, NICs, and extensions directly. See
  [`pkg/providers/instance/vminstance.go`](./pkg/providers/instance/vminstance.go).
- **AKS Machine API-based** (`aksmachineapi`, `aksmachineapiheaderbatch`) — the provider creates AKS
  Machine resources and AKS performs the underlying compute operations. See
  [`pkg/providers/instance/aksmachineinstance.go`](./pkg/providers/instance/aksmachineinstance.go)
  and `aksmachineinstancehelpers.go`.

The two AKS Machine API modes are **the same provisioning path**, not separate implementations.
Both use `DefaultAKSMachineProvider`, the same machine template construction, and the same
lifecycle, read, update, and delete logic. `aksmachineapiheaderbatch` differs only in how creates
are dispatched: compatible concurrent creates are accumulated and sent as a single request carrying
a shared machine template plus a `BatchPutMachine` header describing each machine, then polled
individually via GET. See
[`pkg/providers/azclient/aksmachinesheaderbatch/`](./pkg/providers/azclient/aksmachinesheaderbatch/)
and the create-path branch in `aksmachineinstance.go`. Treat them as one mode with two create
dispatch strategies — a behavioral change generally applies to both, and batching concerns are
about grouping, partial failure, and batch limits rather than about a different machine model.

Several files carry explicit `ATTENTION!!!` comments stating that changes there may not take effect
on AKS machine nodes, and point at the counterpart implementation. Treat those comments as
authoritative — they mark the exact places where a one-sided change silently breaks a mode.

## Code Structure

- [`pkg/apis/`](./pkg/apis/) — `AKSNodeClass` API types. `v1beta1` is the current version;
  `v1alpha2` is deprecated and planned for removal. Also holds generated deepcopy code and CRDs
  under `pkg/apis/crds/`.
- [`pkg/cloudprovider/`](./pkg/cloudprovider/) — implementation of the upstream Karpenter
  `CloudProvider` interface (create, delete, get, list, drift).
- [`pkg/controllers/`](./pkg/controllers/) — provider-specific controllers: nodeclaim
  garbage collection and in-place update; nodeclass hash, status, and termination; instance type
  and quota controllers.
- [`pkg/providers/`](./pkg/providers/) — Azure integrations: `azclient`, `instance`, `instancetype`,
  `imagefamily`, `launchtemplate`, `kubernetesversion`, `loadbalancer`,
  `networksecuritygroup`, `pricing`, `quota`, `zone`, `allocationstrategy`, `labels`.
- [`pkg/operator/`](./pkg/operator/) — operator wiring and options/flags.
- [`pkg/fake/`](./pkg/fake/) and [`pkg/test/`](./pkg/test/) — fakes and the test environment used by
  acceptance tests.
- [`charts/`](./charts/) — `karpenter` and `karpenter-crd` Helm charts. CRD templates are copied
  from `pkg/apis/crds` by `make verify`.
- [`test/`](./test/) — real end-to-end suites run against a live AKS cluster. See
  [test/README.md](./test/README.md).
- [`hack/`](./hack/) — toolchain, codegen, validation, and release scripts.

## Development and Validation

The supported entry points are the Make targets; see [CONTRIBUTING.md](./CONTRIBUTING.md).

| Target | Purpose |
|--------|---------|
| `make verify` | Codegen, boilerplate, CRD copy, validation, linting, `actionlint`. Run after any code change. |
| `make presubmit` | `make verify` plus tests. Run before submitting. |
| `make test` | Unit and acceptance tests via Ginkgo. |
| `make e2etests` | E2E suites against a live cluster (`TEST_SUITE=<suite>`). |
| `make toolchain` | Install the correct tool versions; fixes most local environment failures. |

Do not run `go generate` directly and do not hand-edit generated output. `make verify` performs
code generation, copies upstream and provider CRDs into `pkg/apis/crds` and
`charts/karpenter-crd/templates`, and fails if the working tree is dirty afterwards.

CI runs `make ci-non-test` and `make ci-test` across Kubernetes versions 1.30 through 1.36. E2E
suites run through [`.github/workflows/e2e-matrix.yaml`](./.github/workflows/e2e-matrix.yaml); a new
suite directory under `test/suites/` must also be added to that matrix, as described in
[.github/workflows/README.md](./.github/workflows/README.md).

## Guidelines

### Operational Goals

- Preserve correctness of node provisioning above all else. A defect here does not degrade a
  feature, it fails to create nodes or leaks Azure resources for real clusters.
- Keep NAP and self-hosted behavior consistent. Losing parity is a trade-off that needs explicit
  justification.
- Keep VM-based and AKS Machine API-based provisioning consistent. Logic added to one instance
  implementation usually needs a counterpart in the other.
- Preserve backward compatibility of the `AKSNodeClass` API, node labels, and Helm values. Existing
  clusters upgrade in place.
- Avoid regressions in provisioning latency and in the number of Azure API calls per node.

### Golang

- Follow Go best practice and the conventions already present in the surrounding package.
- Propagate `context.Context` and honor cancellation on every Azure and Kubernetes call.
- Wrap errors with enough context to identify the resource and operation.
- Use `sigs.k8s.io/controller-runtime/pkg/log` for logging, consistent with the rest of `pkg/`.
- Prefer the smallest change consistent with the existing code over an unrelated refactor.

### Kubernetes APIs and CRDs

- `v1beta1` is the current `AKSNodeClass` version. `v1alpha2` is deprecated and planned for
  removal, so new fields and behavior belong in `v1beta1`. Do not add functionality to `v1alpha2`
  purely for parity.
- Adding or changing a spec field usually affects defaulting, CEL validation, the nodeclass hash,
  drift detection, and the generated CRDs. Consider all of them together.
- Generated files (`zz_generated.deepcopy.go`, CRD YAML, chart CRD copies) come from `make verify`.

### Azure Integrations

- All Azure clients are constructed in [`pkg/providers/azclient`](./pkg/providers/azclient/).
  Client construction is mode-dependent; adding a client means deciding which modes need it.
- Handle throttling, retries, pagination, long-running operation polling, and eventual consistency
  explicitly. Do not assume a resource is visible immediately after a successful write.
- Every created Azure resource needs a cleanup path, including on partial failure.
- When writing `az` CLI commands in scripts or docs, always pass an explicit `--output` format.
  Users may have changed their default via `az configure`, which breaks scripts that assume JSON.

### Testing

Three levels, as described in [CONTRIBUTING.md](./CONTRIBUTING.md):

- **Unit tests** — fine-grained, under `pkg/` next to the code, standard Go tests.
- **Acceptance tests** — Ginkgo, under `pkg/`, integrate with upstream Karpenter and fake only the
  Azure clients. Prefer starting from pending pod pressure.
- **E2E tests** — Ginkgo, under `test/`, real cluster and real Azure clients.

A test is only useful if it would fail when the intended behavior regresses.

Before adding a test, read the existing tests for the file you changed — usually the sibling
`_test.go` file, otherwise the suite covering that package. Match what is already there: the same
setup and teardown, fakes, table or `DescribeTable` structure, naming, and assertion style. Extend
the established test file rather than starting a disconnected one, and cover the same provisioning
modes and deployment models the surrounding cases already cover. A test that is correct but
inconsistent with its neighbors makes the suite harder to maintain and tends to duplicate or
contradict existing coverage.

### GitHub Actions

- Pin actions to a full commit SHA, never a mutable tag.
- Workflows run behind `step-security/harden-runner` with `egress-policy: block`. A new network
  dependency requires adding the endpoint to `allowed-endpoints`, or the job fails.
- Keep `permissions` minimal and explicit.
- `make verify` runs `actionlint`.

## Pull Request Review Guidelines

When reviewing pull requests, focus on correctness, security, and backward compatibility. Karpenter
provisions real infrastructure for existing clusters, so regressions here surface as failed node
provisioning, stranded Azure resources, or unschedulable workloads rather than as cosmetic bugs.

**Review Approach**: Focus on high-level architecture, security vulnerabilities, and logic bugs.
Apply deep reasoning — do not just pattern match, but understand the change's intent, its
dependencies, and its failure modes. Linting and formatting are already enforced by `make verify`
and CI; do not spend review budget on them.

### Breaking Change Detection

Analyze PRs for these compatibility scenarios.

**1. Provisioning Mode Compatibility**

- **Context**: The provider supports VM-based and AKS Machine API-based provisioning, with
  separate implementations in `pkg/providers/instance/`. Files that only affect one family are
  marked with `ATTENTION!!!` comments naming the counterpart. The two AKS Machine API modes are the
  same path and differ only in create dispatch, so a change to Machine API behavior normally
  applies to both.
- **What to check**: Which provisioning modes the change actually affects, and whether the
  counterpart implementation needs the same change.
- **Breaking signals**:
  - Logic added to `vminstance.go`, `launchtemplate/`, `imagefamily/resolver.go`, or
    `customscriptsbootstrap/` without the corresponding change in `aksmachineinstance.go` /
    `aksmachineinstancehelpers.go`, when the behavior is meant to apply to both.
  - A `switch` or `if` over provision mode that gains a new case in one place but not in others
    that switch on the same values.
  - New behavior that silently no-ops in a mode the PR claims to support.
  - Machine template changes that break batch grouping, or that add a per-machine field without
    the matching header entry so it is silently shared across a batch.
  - Batch handling that ignores the batch size limit or the idle/max accumulation durations, or
    that treats a partially failed batch as wholly succeeded or wholly failed.
  - Migration and mixed-environment paths (nodes created in one mode, then managed after a mode
    switch) left unhandled.

**2. NAP and Self-Hosted Compatibility**

- **Context**: One codebase backs both the AKS-managed addon and self-hosted deployments, with
  different image builds, different configuration sources, and different upgrade cadences.
- **What to check**: That the change is valid under both deployment models.
- **Breaking signals**:
  - Assuming a setting, environment variable, or Helm value is always present.
  - Assuming AKS manages something (tokens, node image upgrades, chart versions) in a path that
    self-hosted clusters also execute.
  - Changing a default in a way that alters behavior for existing clusters on upgrade.
  - AKS-facing labels, taints, or field values that are asserted rather than verified against what
    AKS actually sets.

**3. AKSNodeClass API and CRD Compatibility**

- **Context**: `v1beta1` is current and `v1alpha2` is deprecated and planned for removal. CRDs are
  generated, and the nodeclass hash feeds drift detection.
- **What to check**: Generated artifacts and upgrade impact on objects that already exist.
- **Breaking signals**:
  - New fields or behavior added to `v1alpha2` rather than `v1beta1`.
  - Hand-edited `zz_generated.deepcopy.go`, CRD YAML, or `charts/karpenter-crd/templates` content.
  - Removing or narrowing a field, tightening CEL validation, or changing a default — existing
    stored objects must remain valid.
  - Hash-relevant spec changes without the corresponding hash version bump and test update,
    which would drift every existing node.
  - Status conditions added or renamed without considering consumers that gate on them.

**4. NodeClaim Lifecycle and Resource Cleanup**

- **Context**: Create, register, initialize, drift, disrupt, in-place update, garbage collect, and
  delete are separate paths, and any of them can fail midway against a live Azure subscription.
- **What to check**: That every path is idempotent and leaves no orphaned Azure resources.
- **Breaking signals**:
  - A resource created before an operation that can fail, with no cleanup on the error path.
  - Non-idempotent logic on a reconcile path that will be retried.
  - Assuming an Azure resource still exists — it may have been deleted out of band.
  - Treating "not found" as fatal in deletion or garbage collection instead of as success.
  - Early returns that skip finalizer removal, leaving objects stuck terminating.
  - Cancellation or timeout that abandons an in-flight create without recording the instance.

**5. Azure API Semantics**

- **What to check**: That Azure is treated as a remote, rate-limited, eventually consistent API.
- **Breaking signals**:
  - Missing or incorrect retry and throttling handling; retrying a non-retryable error.
  - Dropped `context.Context`, or a context that cannot cancel a long-running poll.
  - Paged list APIs consumed as if they return a single page.
  - Dereferencing pointer fields from the Azure SDK without a nil check.
  - Read-modify-write sequences that ignore concurrency control and can clobber a concurrent update.
  - Assuming a resource is immediately readable after create.
  - Partial success in a batched or multi-resource operation treated as total success or total
    failure.

**6. Scheduling, SKUs, and Capacity**

- **What to check**: That instance type selection stays correct and does not silently produce an
  empty or invalid candidate set.
- **Breaking signals**:
  - Filtering that can eliminate all offerings, yielding no provisioning with an unclear error.
  - Requirements, labels, or taints that no longer match what the node actually reports.
  - Regional and zonal placement handling, including the regional zone value, treated
    inconsistently between offering construction and node labeling.
  - Capacity type, spot, pricing, or quota logic that changes consolidation or replacement
    decisions as a side effect.
  - SKU capability parsing that assumes a capability is always present.

**7. Images, Networking, and Storage**

- **What to check**: Region, architecture, Kubernetes version, and image family assumptions.
- **Breaking signals**:
  - Image resolution that assumes a lineage is published in every region or for every
    architecture or image family.
  - Changes to resolved image identifiers that unintentionally drift existing nodes.
  - Network plugin, dataplane, or policy handling that ignores documented unsupported
    combinations.
  - NIC, subnet, or disk configuration changes without a matching cleanup path.
  - Ephemeral versus managed OS disk assumptions that do not hold for the selected SKU.

**8. Security**

- **What to check**: Credential handling, privilege, and input validation.
- **Breaking signals**:
  - Tokens, keys, bootstrap credentials, or customer identifiers written to logs, events,
    conditions, or Azure resource tags.
  - Broadening identity permissions or resource scope beyond what the operation needs.
  - User-controlled input flowing into Azure resource names, shell commands, or Kubernetes objects
    without validation.
  - New external endpoints not added to `allowed-endpoints`, or actions pinned to a mutable tag.
  - References in code, comments, docs, commit messages, or PR descriptions to systems that
    external contributors cannot access. This repository is public; keep contributions
    self-contained and reviewable from public sources.

**9. Test Coverage**

- **What to check**: Whether the tests would actually catch a regression of this change, and
  whether they are consistent with the existing tests for the files being edited.
- **Breaking signals**:
  - Behavior changes in `pkg/` with no unit or acceptance test that exercises the new path.
  - Tests that assert the implementation rather than the intended behavior.
  - Only the success path covered, when the change is about failure, retry, or cleanup.
  - A new test file or suite created alongside an existing one that already covers the changed
    file, instead of extending it.
  - Tests that diverge from the conventions of their neighbors — different setup or fakes, a
    different table or `DescribeTable` structure, or a different assertion style than the
    surrounding cases.
  - A changed file whose existing tests cover several provisioning modes or deployment models,
    where the new case only covers one of them without saying why.
  - A new `test/suites/` directory not added to `.github/workflows/e2e-matrix.yaml`.
  - Helm chart snapshot updates presented as evidence of runtime behavior — they only verify
    template rendering.
  - Changes that need real Azure behavior to be meaningful, with no E2E coverage and no
    explanation.

### Analysis Approach

**Dependency Tracing**:

1. For each changed file, identify its callers and consumers.
2. Check whether a separate implementation exists for the other provisioning family, and whether it
   needs the same change. Follow the `ATTENTION!!!` comments.
3. Trace changed values through to what is actually sent to Azure and what is set on the Node or
   NodeClaim.
4. For API changes, follow through to defaulting, validation, hashing, drift, and generated CRDs.
5. Check whether Helm chart values, RBAC, or operator options need a matching change.
6. Open the existing tests for each changed file and compare the new tests against them for
   coverage and for consistency of structure and style.

**Historical Context**:

- Look for the established pattern for this kind of change elsewhere in the repository and prefer
  consistency with it.
- Note areas that have required repeated fixes and review changes there more closely.

**Test Coverage Assessment**:

- Note whether the changed code has unit, acceptance, or E2E coverage.
- Flag changes to untested paths as higher risk.
- Say so when new behavior lacks a corresponding test.
- Compare new tests against the existing tests for the same file and flag inconsistency in
  structure, fakes, or assertion style, or a new suite that duplicates one that already exists.

### Review Output

Provide targeted inline comments on the specific lines where you find an issue.

For each finding, include:

- **Severity**: `High` for anything that can break node provisioning, strand Azure resources, break
  an existing cluster on upgrade, or introduce a security vulnerability. `Medium` for issues that
  affect specific configurations, edge cases, or performance. `Low` for correctness nits worth
  noting.
- **Category**: one of `Correctness`, `Compatibility`, `Azure API`, `Lifecycle`, `Scheduling`,
  `Security`, `Performance`, or `Test Coverage`.
- The concrete scenario in which the code misbehaves — not just the rule it violates.
- A specific fix or verification step, such as the counterpart file to update or the case to cover.

**Only comment when you have a substantive finding.** Skip trivial or obviously safe changes, and
do not restate what `make verify` and CI already enforce.

### Review Philosophy

Think like an experienced reviewer of this codebase:

- Understand how the pieces interact before judging a diff.
- Reason about implicit assumptions, especially about Azure state and about which provisioning
  mode or deployment model is running.
- Consider the upgrade path for clusters already running an older version.
- Prefer a small number of high-confidence findings over broad coverage.
- Say plainly when a change looks risky but you cannot prove a defect, and state what would confirm
  it.
