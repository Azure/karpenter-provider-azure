# Pull Request Review Guidelines

<!-- Note: This guideline is designed for a generic agent reviewing the PR, not specifically for the GitHub Copilot reviewer. Some instructions may not be applicable to some reviewer agents. E.g., GitHub Copilot doesn't seem to care about PR description/quality. -->

Review as a code owner: protect users and critical workloads without blocking justified improvements.

## Start With Repository Rules

- Read [AGENTS.md](AGENTS.md). It contains the most important architecture, development, and review
  requirements. Pay particular attention to **bold** requirements, but apply each rule only when relevant.
- Read [CONTRIBUTING.md](CONTRIBUTING.md).
  - Do not comment on violations of recommendations. Comment on violations of strict requirements.
  - Give each suspected but unproven violation a `Medium`-severity comment. This includes suspected direct AI writing over 150 words in the PR description.
  - Give a severely malformed description or multiple requirement violations one `High`-severity
    comment. Explain the problem and warn that the PR may be ignored.
  - Extra sections are allowed and are not malformed by themselves.

## Review PR Logistics Quality

Read the description and any design document before reviewing the diff. Do not repeat an issue that they already describe as intentional.

Check whether:

- the described behavior matches the implementation;
- the tests are sufficient and would fail if the intended behavior regressed;
- the explanation has enough detail without restating obvious code;
- the change is large enough to require a design document;
- the evidence is sufficient to judge the change safe;
- the author can reduce maintainer review effort by providing missing context or validation; and
- loosely coupled changes should be split into dependent or independent PRs.

Avoid these PR-description anti-patterns:

- Routine CI-gated commands such as `make verify`, `make presubmit`,
  `git diff`, or `go test` should not be called out at all. They usually distract from meaningful validation.
- A template-like description with different heading styles and excessive length is a signal of direct AI writing
  over 150 words (which violates a rule). Check it carefully against the disclosure and description requirements.

## Review Strategy

- Use the PR description and design documents as context. Do not ask questions they already answer.
- Research dependencies and historical context when needed. Understand how the components interact before judging the diff.
- Review in layers: intention → design and failure modes → implementation → tests → merge and release concerns.
- Trace behavioral consequences even when none are expected. Do not rely on pattern matching alone.
- Identify the origin of each issue. State whether the PR introduces it, worsens it, merely touches it,
  or depends on an existing issue being fixed first. This affects severity.
- Reason explicitly about Azure state and the active provisioning mode and deployment model.

## Write Useful Comments

Comment only when there is a clear benefit. Prefer a small number of substantive, high-confidence findings over broad or speculative coverage.

For each finding:

- Comment inline at, or close to, the issue's origin.
- State the category when useful: `Correctness` (including backward compatibility), `Security`,
  `Performance`, `Clarity`, `Maintainability`, `Test coverage`, or `Trade-off`.
- State the domain when useful, such as `Scheduling`, `Provisioning`, `NodeClass Validation`, or `NodeClaim Lifecycle`.
- State the severity:
  - `High`: can break a contract or documented behavior, disrupt node provisioning or resource
    cleanup, break an existing cluster on upgrade, or introduce a security vulnerability for many users.
  - `Medium`: affects a specific configuration or rare edge case, causes a minor performance or quality-of-life regression, or adds significant technical debt.
  - `Low`: a worthwhile correctness nit or advisory about a related existing issue that the PR does not worsen.
- State confidence. If you cannot prove the concern, identify the assumption and the evidence needed to
  confirm it. The PR author must help establish safety when the reviewer cannot.
- Describe a concrete failing scenario and suggest a specific fix or verification step when possible.

Do not comment on trivial issues, obviously safe changes, intentional behavior already explained by the PR, or anything enforced by `make verify` and CI.

Explain concrete problems with a short walkthrough, comparison, table, or causal chain when that makes
the issue easier to understand. If needed, you may use symbols to visualize/animate the problem. Use ASD-STE100 Simplified Technical English and avoid walls of text. You may also use bullet points or split the text into multiple paragraph, whenever applicable.

## Specifics

Use the checks below when relevant. They may become stale; verify them against the code. [AGENTS.md](AGENTS.md) takes precedence if the files conflict.

### 1. Provisioning Mode Compatibility

The provider has separate VM-based and AKS Machine API-based implementations under
`pkg/providers/instance/`. `ATTENTION!!!` comments mark counterpart files. The two Machine API modes
share the same path and differ only in create dispatch.

Check for:

- behavior added to `vminstance.go`, `launchtemplate/`, `imagefamily/resolver.go`, or
  `customscriptsbootstrap/` without a required counterpart change in `aksmachineinstance.go` or
  `aksmachineinstancehelpers.go`;
- inconsistent `switch` or `if` handling of provision modes;
- behavior that silently does nothing in a claimed mode;
- template changes that break batch grouping or omit a per-machine header field;
- batch handling that ignores size limits, idle/max accumulation durations, or partial failures; and
- unhandled migration paths where resources created in one mode are managed after a mode switch.

### 2. NAP and Self-Hosted Compatibility

The same source supports the AKS-managed addon and self-hosted deployments, but their builds, configuration sources, and upgrade cadences differ.

Check for:

- settings, environment variables, or Helm values assumed to exist in both deployments;
- AKS-managed behavior, such as token, image, or chart management, assumed in self-hosted paths;
- default changes that alter existing clusters during upgrade; and
- AKS-facing labels, taints, or field values asserted without verification against AKS behavior.

### 3. AKSNodeClass API and CRD Compatibility

`v1beta1` is current. `v1alpha2` is deprecated and planned for removal, but it is still served and must
maintain parity. CRDs are generated, and the NodeClass hash controls drift.

Check for:

- behavior or fields added to only one served version;
- hand-edited `zz_generated.deepcopy.go`, CRD YAML, or `charts/karpenter-crd/templates` output;
- removed or narrowed fields, stricter CEL validation, or changed defaults that invalidate stored objects;
- hash-relevant changes without the required hash-version bump and tests; and
- status-condition changes that break consumers.

### 4. NodeClaim Lifecycle and Resource Cleanup

Create, register, initialize, drift, disrupt, in-place update, garbage collection, and delete are separate
retryable paths. Any of them can fail after modifying Azure resources.

Check for:

- resources created before a failure with no cleanup path;
- non-idempotent reconcile behavior;
- assumptions that Azure resources still exist after out-of-band deletion;
- deletion or garbage collection treating “not found” as an error;
- early returns that leave finalizers or objects stuck; and
- cancellation or timeout that abandons an in-flight create without enough state to recover it.

### 5. Azure API Semantics

Treat Azure as a remote, rate-limited, eventually consistent API.

Check for:

- incorrect retry or throttling handling, including retries of non-retryable errors;
- dropped `context.Context` values or calls that cannot be canceled;
- paged APIs consumed as one page;
- unchecked nil Azure SDK pointers;
- read-modify-write sequences without concurrency control;
- assumptions that a new resource is immediately readable; and
- partial batch or multi-resource success treated as total success or failure.

### 6. Scheduling, SKUs, and Capacity

Instance-type selection must not silently return an empty or invalid candidate set.

Check for:

- filters that remove every offering without a clear error;
- requirements, labels, or taints that differ from what the node reports;
- inconsistent regional or zonal placement between offerings and node labels;
- capacity type, Spot, price, or quota changes that alter consolidation or replacement; and
- SKU parsing that assumes every capability exists.

### 7. Images, Networking, and Storage

Check for:

- image assumptions that do not hold for every relevant region, architecture, Kubernetes version, or image family;
- resolved image changes that unintentionally drift existing nodes;
- network plugin, dataplane, or policy handling that ignores unsupported combinations;
- NIC, subnet, or disk changes without matching cleanup; and
- ephemeral or managed OS disk assumptions that do not hold for the selected SKU.

### 8. Security

Check for:

- credentials, tokens, bootstrap secrets, or customer identifiers written to logs, events, conditions, or Azure tags;
- identity permissions or resource scope broader than required;
- unvalidated user input used in Azure resource names, shell commands, or Kubernetes objects;
- new external endpoints missing from `allowed-endpoints`, or actions pinned to mutable tags; and
- private-system references in public code, comments, docs, commits, or PR descriptions. Public contributions must be reviewable from public sources.

### Analysis Approach

For each changed area:

1. Identify callers and consumers.
2. Find counterpart implementations and follow relevant `ATTENTION!!!` comments.
3. Trace changed values to Azure requests and Node or NodeClaim state.
4. For API changes, trace defaulting, validation, hashing, drift, and generated CRDs.
5. Check whether Helm values, RBAC, or operator options need matching changes.
6. Compare new tests with existing tests for the same files, including structure, fakes, coverage, and style.
