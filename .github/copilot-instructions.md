# Copilot Instructions

This repository is the Azure cloud provider for [Karpenter](https://karpenter.sh/). It provisions
real Azure infrastructure for AKS clusters, so defects surface as failed node provisioning,
stranded Azure resources, or unschedulable workloads.

Read [AGENTS.md](../AGENTS.md) for the architecture, provisioning modes, code layout, validation
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
