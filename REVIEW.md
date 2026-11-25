# Pull Request Review Guidelines

<!-- Note: This guideline is designed for a generic agent reviewing the PR, not specifically for the GitHub Copilot reviewer. Some instructions may not be applicable to some reviewer agents. E.g., GitHub Copilot doesn't seem to care about PR description/quality. -->

You own this codebase and are accountable for its business outcomes in the short or long term. There are a lot of users relying on this around the world, many on critical workloads. Still, you want to allow innovation and efficient improvements through the changes you are going to review.

For each rule below (or elsewhere), also consider its applicability. E.g., rules targeting code changes may not apply to a small documentation change.

## Existing Rules

- Re-read [AGENTS.md](AGENTS.md). Pay attention to each area, its instructions and requirements. Verify whether the PR has followed them, especially but not limited to the **bold** parts. **The most important considerations live here.**
  - Be mindful of what's applicable and what's not. Do not over-apply rules when their purpose is lost.
- Re-read [CONTRIBUTING.md](CONTRIBUTING.md). Verify whether the PR violates them. Pay attention to recommendations (do not call out if violated) and strict requirements (call out if violated). For more specifics:
  - Suspected but unproven violations will each receive a `Medium`-severity comment, including but not limited to "Direct AI writing with more than 150 words" in the PR description.
  - PRs with a severely malformed description structure or that violate multiple requirements will receive a `High`-severity comment describing the issue and warning that the PR is at risk of being ignored.
    - However, extra sections are allowed and not considered malformed.

## PR Quality

Also read the PR description/design doc and see whether it makes sense. Only call out necessary things, not "nice-to-have" items. Do not nitpick. For example:

- Are the tests really enough?
- Do the described behavior changes match reality?
- Is it unnecessarily lengthy, or does it state what's obvious in the code?
- Is the change large/significant enough, or does the author seem to underestimate its implications? Should this be in a design doc rather than the PR description?

Apart from correctness, think about things from the human code owner's perspective when they come and check this PR after you do. Again, do not nitpick. Only leave comments on these matters when you have relatively high confidence.

- Is the review effort needed low enough? Is there an easy opportunity for the PR author to contribute that would save the reviewer a lot of effort?
- Is the existing presentation (e.g., description or design review) easy to understand, and does it avoid lacking detail or including too many sloppy/distracting details?
- Can you be confident that this PR is safe? Did the author present enough evidence, especially on testing/validation, if needed?
- If the PR involves multiple intended changes that are not tightly coupled, can the development be iterative, and can this PR be split into multiple PRs that may or may not depend on one another? Or is it better to keep this unified PR?

Watch for some common anti-patterns:

- In the "Testing" section of the PR description, listing tests already gated by CI, such as `make verify`, `make presubmit`, `git diff`, or `go test`, is not useful. The PR author must remove them to prevent distraction.
- A PR description with a structure similar to the template but a different heading style (e.g., it may use a Markdown heading instead of bold text as a header), combined with an overly lengthy description, indicates that the description is AI-written and usually more than 150 words. Scrutinize it carefully against the other PR description guidelines.

## Review Strategy

- Use the PR description/docs as context. They may already address some of the issues you are going to ask about, in which case you may not need to ask about them after reading.
- Research more context if needed, especially if the PR has dependencies on other services/libraries/codebases.
  - Historical context (discussions, code changes, incidents, etc.) is usually useful and gives you necessary context to adjust your expectations and the effort needed.
  - Understand how the pieces interact before judging a diff.
  - Reason about implicit assumptions, especially about Azure state and about which provisioning mode or deployment model is running.
- Try to review the PR layer-by-layer and keep what you learned in mind. E.g., intention → design / architecture / failure modes → implementation → testing → merging / releasing. Do not just pattern-match.
- Always trace the behavioral consequences of the change deeply, even if changes are not expected, and call out the issues explicitly.
- For any issue, investigate its origin, which may not be the current PR but rather existing code/behavior. If so, clarify how the current PR interacts with the issue (e.g., merely touches it, makes it worse, or assumes that it does not exist even though it must be fixed first). This affects the severity of the issue.

## Commenting

For each applicable comment (**only if applicable**):

- Every comment must have an intended or expected good consequence, whether implicit or explicit. If there is no clear benefit to commenting, don't do it.
- State the confidence level.
  - Be honest if you think the change looks risky but you cannot prove a defect, or suggest what would confirm it.
  - Likewise, state any implicit assumptions/dependencies that are made, violated, or impossible for you to verify.
  - Higher-level things like design trade-offs should generally have lower confidence (and human reviewers will take a look later). Still, do your best to state your opinions in that way.
  - It is the PR author's responsibility to help you prove its safety when you cannot do so yourself.
- Prefer a small number of high-confidence findings over broad coverage.
- Prefer inline comments where the issue originates, or at least close to it.
- Categorize comments using the categories below, only when applicable. Context matters when categorizing them (e.g., the PR's intention determines what is considered a side effect):
  - `Correctness`; Incl. backward compatibility by nature
  - `Security`
  - `Performance`
  - `Clarity`
  - `Maintainability`
  - `Test coverage`
  - `Trade-off`; E.g., if the PR is correct, but you disagree with the intended trade-off
- Clarify the domain to which the comment applies, e.g., `Scheduling`, `Provisioning`, `NodeClass Validation`, or `NodeClaim Lifecycle`.
- Categorize severity. For example (but not limited to the following):
  - `High`:
    - Anything that breaks the contract or documented behavior, with a decent number of users potentially relying on it. E.g., breaking provisioning, stranding Azure resources, breaking an existing cluster on upgrade, or introducing a security vulnerability.
  - `Medium`:
    - Issues that break only on rare, likely user-unintentional edge cases.
    - Minor performance / QoL degradation for issues that affect specific configurations
    - Decent technical debt introduced
  - `Low`:
    - Correctness nits worth noting
    - Advisory on the existing issue/work that relates to the area of the PR, but the PR doesn't make it worse
- Only comment when you have a substantive finding. Skip trivial or obviously safe changes, and do not restate what `make verify` and CI already enforce.
- Also do not comment if the issue is already called out (well enough) as intentional in PR description, design doc, etc.
- For a known, concrete problem or behavior, demonstrate, walk through, or visualize it clearly and concisely. Make sure the reader understands it easily. Use visualization techniques such as tables, bullet points, comparisons and contrasts, symbols such as `→`, or descriptions of a graph or chain of consequences. Use them only when worthwhile, though (e.g., minimal problems do not require elaborate explanations).
  - Always use ASD-STE100 Simplified Technical English. Avoid walls of text.
  - Suggest the specific solution whenever applicable.

## Specifics

In addition to the general guidelines, consider the items below whenever applicable. Note that these could be outdated, and do not blindly follow them to the point that you ignore other paths encouraged by the other guidelines. Consider [AGENTS.md](AGENTS.md) more up to date if they conflict.

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

- **Context**: `v1beta1` is current and `v1alpha2` is deprecated and planned for removal, but need to be kept in parity. CRDs are
  generated, and the nodeclass hash feeds drift detection.
- **What to check**: Generated artifacts and upgrade impact on objects that already exist.
- **Breaking signals**:
  - New fields or behavior added to only one of `v1alpha2` and `v1beta1`.
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
