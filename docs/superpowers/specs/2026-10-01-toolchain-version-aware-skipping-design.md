# Version-aware toolchain installation

## Why this matters

The toolchain cache can contain an executable with the right name but the wrong version. Today,
`SKIP_INSTALLED=true` checks only that a Go tool's file exists, while a cache hit prevents
`make toolchain` from running at all. A stale envtest cache can therefore make a Kubernetes matrix
job run against a different API server version from the one it requested.

PR #1954 repairs that failure with a separate envtest validation subsystem. This design takes a
smaller, more general approach: make the existing skip decision version-aware, run that decision
on every CI job, and reinstall only tools that are missing or stale.

## Goals

- Make `SKIP_INSTALLED=true` mean "skip this tool when the required version is already installed".
- Preserve presence-only skipping for tools that do not expose a safe, meaningful version command.
- Apply the same policy to Go tools, Trivy, and the envtest binaries.
- Repair the complete envtest bundle when any required binary is missing or stale.
- Keep the change in the existing toolchain script rather than adding a separate repair subsystem.
- Add focused regression tests for the skip and repair decisions.

## Non-goals

- Generalising the script into a package manager or declarative tool manifest.
- Discovering versions for tools that report build-time placeholders such as `dev`.
- Adding special-case handling for Kubernetes versions outside the current 1.31-1.37 CI range.
- Retaining the obsolete Kubernetes 1.25.16 CEL workaround.

## Helper contracts

### `should-skip`

`should-skip` accepts a presence-only form and a version-aware form:

```text
should-skip <command>
should-skip <command> <version|--version> <expected-text> [version-arg]
```

Examples:

```bash
should-skip controller-gen --version v0.19.0
should-skip cosign version v2.4.1
should-skip /usr/local/kubebuilder/bin/kubectl version v1.32 --client=true
```

A command containing `/` is treated as a path. A bare command is resolved through `PATH`.
`go-install` passes the full path under `TOOL_DEST`, so it always checks the binary that `go
install` manages rather than a same-named binary elsewhere on `PATH`.

The decision order is:

1. When `SKIP_INSTALLED` is not `true`, return "installation required". This preserves the current
   default behaviour.
2. Require the command to be a regular executable file. A searchable directory must not satisfy a
   presence-only check.
3. For a version-aware call, run the requested `version` or `--version` command, append the optional
   fourth argument when present, and capture combined standard output and standard error.
4. Skip only when the version command succeeds and its output contains `expected-text` as a fixed
   string.
5. For a presence-only call, skip once the executable exists.

The helper returns distinct statuses for "skip", "install required", and invalid usage. Callers
must propagate invalid usage rather than treating it as a cache miss. Accepted version verbs and
argument counts are validated explicitly.

The version-execution and fixed-string matching logic should live in a small internal predicate.
`should-skip` adds the `SKIP_INSTALLED` policy around that predicate. The envtest installer uses the
internal predicate to verify newly installed binaries without applying skip policy.

### `go-install`

`go-install` accepts exactly these forms:

```text
go-install <app> <reference>
go-install <app> <version|--version> <expected-text> <reference>
```

The two-argument form keeps the existing presence-only behaviour. The four-argument form delegates
the version decision to `should-skip` before running `go install`.

The current `go-licenses` call includes `// v2.0.1` as extra shell arguments. It must become a true
two-argument call, with any explanatory text converted to a shell comment, so strict arity
validation does not reinterpret it as a version-aware call.

## Tool coverage

Use version-aware checks for pinned tools with verified, side-effect-free version commands:

| Tool | Version invocation | Expected text source |
|---|---|---|
| `ko` | `version` | `v0.17.1` |
| `yq` | `--version` | `v4.45.1` |
| `controller-gen` | `--version` | `v0.19.0` |
| `cosign` | `version` | `v2.4.1` |
| `govulncheck` | `--version` | `v1.1.4` |
| `ginkgo` | `version` | Ginkgo module version from `go.mod` |
| `actionlint` | `--version` | `v1.7.7` |
| `crane` | `version` | `v0.20.2` |
| `swagger` | `version` | `v0.33.1` |
| `golangci-lint` | `--version` | `2.12.2` |
| `golangci-lint-custom` | `--version` | `v2.12.2-custom` marker |
| `trivy` | `--version` | `0.74.0` |

Resolve the Ginkgo module version from `go.mod` and use the same value for both the expected output
and the `go install` reference. This replaces `@latest`, keeping the CLI aligned with the library
used by the test suite.

Keep presence-only checks for:

- `go-licenses`, which does not expose either supported version form.
- `helm-docs`, where `helm-docs version` performs documentation generation rather than reporting a
  version.
- `goveralls` and `pprof`, which do not expose either supported version form.
- `aks-node-viewer`, whose source-built binary reports `dev` rather than the pinned module version.

## Envtest bundle repair

The `kubebuilder` function continues to own envtest installation.

1. Always download the pinned `setup-envtest` helper before evaluating the bundle. Verify it
   against the SHA-256 digest published in the GitHub release metadata before replacing the
   existing helper.
2. Derive the expected Kubernetes version text from `K8S_VERSION` by removing a trailing `.x` and
   adding the `v` prefix. For example, `1.32.x` expects `v1.32` in client and server output.
3. Use `should-skip` for each top-level binary:
   - `kube-apiserver --version` must contain the expected Kubernetes version.
   - `kubectl version --client=true` must contain the expected Kubernetes version.
   - `etcd` must be a regular executable file. Its version is selected by the envtest bundle and is
     not the Kubernetes version.
4. Resolve each top-level binary to its canonical path. Skip bundle installation only when all
   three checks pass and all three binaries share one canonical parent directory.
5. If any check fails, call `setup-envtest use --force -p path` for the requested selector.
6. Validate the returned asset directory, require all three source binaries to be regular
   executable files, remove any corrupt destination entry, and refresh all three top-level links
   explicitly. Do not use a wildcard link.
7. Verify the linked binaries with the internal presence/version predicate before returning. A
   failed post-install check fails the toolchain instead of leaving a success-shaped broken cache.

Refreshing all three links after any failed check keeps `kube-apiserver`, `kubectl`, and `etcd`
from the same resolved bundle.

The Kubernetes 1.25.16 override is removed. It was introduced while 1.25 was in the CI matrix, but
the supported matrix now starts at 1.31. The corresponding `dl.k8s.io` allow-list entries and their
"1.25 CI only" comments are removed from `.github/workflows/ci.yml` and
`.github/workflows/ci-test.yml`.

## GitHub Actions flow

The install-deps composite action keeps caching `/usr/local/kubebuilder/bin` and `~/go/bin`, with
`hack/toolchain.sh` in the cache key.

Replace the cache-miss-only toolchain step with one unconditional step:

- Set `K8S_VERSION` from the action input.
- Set `SKIP_INSTALLED=true` when the cache hit, and `false` when it did not.
- Run `make toolchain`.

On a cache miss, disabled skipping installs everything. On a healthy cache hit, version-aware
checks skip matching tools. On a stale or partial cache hit, only the affected Go tools are
reinstalled, while any envtest failure refreshes the complete bundle.

## Error handling and logging

- A missing executable, failed version command, or mismatched version means installation is needed.
- Invalid helper arguments stop the script with a usage diagnostic.
- Failed downloads, invalid envtest paths, incomplete bundles, link failures, and failed
  post-install verification stop the script with a concrete error.
- Installation logs state whether work is forced, missing, or caused by a version mismatch.
- Successful skips stay quiet to avoid adding noise to every CI job.
- Temporary downloads are promoted only after a successful transfer; existing destinations are not
  deliberately removed before a replacement is available.
- A `setup-envtest` checksum mismatch fails before installation and preserves the existing helper.

## Test design

Add `hack/toolchain_test.sh` and run it from `make verify`. The production script should guard its
`main` call with `BASH_SOURCE` so the test can source the helpers without performing a real
installation.

The test uses temporary executable stubs and test-specific paths. It covers:

- `SKIP_INSTALLED=false` preserving unconditional installation.
- Presence-only skipping.
- Matching and mismatching `version` and `--version` output.
- Passing `--client=true` to a version command.
- Treating a failed version command as installation required.
- Rejecting unsupported arities and version verbs.
- Both accepted `go-install` forms.
- A healthy envtest bundle skipping resolution.
- Any one missing or mismatched envtest binary causing all three links to refresh.
- A directory-shaped cache entry causing all three links to refresh.
- Individually valid binaries from different canonical directories causing one-bundle refresh.
- Rejecting an incomplete resolved bundle.
- Rejecting a post-install Kubernetes version mismatch.
- Rejecting a `setup-envtest` checksum mismatch without replacing the existing helper.
- Ginkgo using the module version from `go.mod`.

Run the focused shell test first, then `make verify`. `actionlint`, already part of `make verify`,
validates the workflow expression changes.

## Files changed

- `hack/toolchain.sh`
- `hack/toolchain_test.sh`
- `Makefile`
- `.github/actions/install-deps/action.yaml`
- `.github/workflows/ci.yml`
- `.github/workflows/ci-test.yml`

No separate envtest setup script is introduced.
