# Pull Request Review Guidelines

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
  - Assuming AKS system manages something (tokens, node image upgrades, chart versions) in a path that
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

---

(The section below is migrated from copilot-instructions.md. Both has always been read at the same time. TODO: Need cleanup and integration with the above.)

Read the repository-root file `AGENTS.md`. for the architecture, provisioning modes, code layout, validation
commands, and the full pull request review guidelines. This file is the short form; `AGENTS.md` is
the source of truth.

## Priorities

Review for correctness, security, and backward compatibility, in that order. Focus on
architecture, logic bugs, and failure modes. Reason about the change's intent and its dependencies
rather than pattern matching.

## Always Consider

- **Provisioning modes** — the provider has separate VM-based and AKS Machine API-based
  implementations in `pkg/providers/instance/`. Determine which modes a change affects and whether
  the counterpart implementation needs the same change. Files with `ATTENTION!!!` comments mark
  exactly where a one-sided change silently breaks a mode. The two AKS Machine API modes
  (`aksmachineapi` and `aksmachineapiheaderbatch`) are the same provisioning path and differ only
  in how creates are dispatched, so a Machine API behavior change normally applies to both; batch
  concerns are grouping, per-machine header fields, batch limits, and partial failure.
- **Deployment models** — the same code runs as the AKS-managed addon (NAP) and as a self-hosted
  deployment. Do not assume configuration, defaults, or AKS-managed behavior that only holds for
  one of them.
- **Backward compatibility** — existing clusters upgrade in place. `AKSNodeClass` fields, node
  labels, Helm values, and defaults must keep working for objects that already exist.
- **API versions** — `v1beta1` is the current `AKSNodeClass` version. `v1alpha2` is deprecated and
  planned for removal, so new fields and behavior belong in `v1beta1`; do not ask for `v1alpha2`
  changes purely for parity. Spec changes usually affect defaulting, CEL validation, the nodeclass
  hash, drift, and the generated CRDs.
- **Generated files** — `zz_generated.deepcopy.go`, CRD YAML, and `charts/karpenter-crd/templates`
  are produced by `make verify`. Flag hand-edited generated output.
- **Azure API semantics** — retries, throttling, pagination, long-running operation polling,
  cancellation, nil SDK pointers, concurrency control, and eventual consistency. Never assume a
  resource is readable right after it is created.
- **Cleanup and idempotency** — every created Azure resource needs a cleanup path, including on
  partial failure. Reconcile paths are retried and must be safe to run repeatedly. "Not found"
  during deletion or garbage collection is success, not an error.
- **Security** — no credentials, tokens, or customer identifiers in logs, events, conditions, or
  resource tags. No unvalidated user input flowing into Azure resource names, shell commands, or
  Kubernetes objects. Actions pinned to a full commit SHA, and new external endpoints added to the
  `harden-runner` `allowed-endpoints` list.
- **Public repository hygiene** — flag references in code, comments, docs, commit messages, or PR
  descriptions to systems that external contributors cannot access. Contributions must be
  reviewable from public sources alone.
- **Test coverage and consistency** — ask whether the tests would fail if this behavior regressed.
  Failure, retry, and cleanup paths need coverage too, not just the success path. Also check the
  new tests against the existing tests for each edited file: they should extend the established
  `_test.go` file or package suite rather than start a parallel one, and should match its setup,
  fakes, table structure, naming, and assertion style, covering the same provisioning modes and
  deployment models the neighboring cases cover. A new `test/suites/` directory must be added to
  `.github/workflows/e2e-matrix.yaml`. Helm chart snapshots verify template rendering only, not
  runtime behavior.

## Output

Comment inline on the specific line, and state the concrete scenario in which the code misbehaves
plus a specific fix or verification step. Label each finding with a severity (`High`, `Medium`,
`Low`) and a category (`Correctness`, `Compatibility`, `Azure API`, `Lifecycle`, `Scheduling`,
`Security`, `Performance`, `Test Coverage`).

Only comment when you have a substantive finding. Do not restate lint, formatting, or anything
`make verify` and CI already enforce.
