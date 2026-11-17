# Toolchain version globs

## Goal

Fix envtest patch-version validation and unnecessary custom golangci-lint
reinstallation with minimal changes to `hack/toolchain.sh`.

## Matching contract

The expected-version argument accepted by `go-install`, `should-skip`, and
`_command_is_current` is a Bash glob pattern. Match it against each
whitespace-delimited token in command output. A whole token must match;
substring matches are not sufficient.

Use native Bash pattern matching, without converting patterns to regular
expressions or expanding them against filesystem paths. Callers must quote
patterns containing wildcard characters.

Patterns without wildcard syntax retain exact matching. Thus `v0.19.0`
does not match `v0.19.0-rc.1`. Explicit wildcard patterns intentionally
permit suffixes, including prerelease suffixes.

Preserve argument validation, command resolution, return codes, logging,
CI abort behaviour, and local installation behaviour.

## Call sites

- Convert a Kubernetes selector ending in `.x` to a version pattern ending
  in `.*`, prefixed with `v`. For example, `1.34.x` becomes `v1.34.*` and
  `1.33.x` becomes `v1.33.*`.
- Keep exact Kubernetes patch selectors exact: `1.34.1` becomes `v1.34.1`.
- Both envtest pre-install and post-install checks use the existing shared
  expected-version helper.
- Change the custom golangci-lint expected version to `v2.14.0*`. This
  accepts the custom build's hash suffix. Future version upgrades update
  the version in the pattern without requiring matching-logic changes.
- Keep other tool version arguments unchanged.

Update the directly related argument documentation and matching comments.
Do not refactor installation or envtest bundle management.

## Alternatives

Native Bash token glob matching is preferred because it implements the
requested interface directly. Glob-to-regex conversion adds escaping
complexity. Tool-specific exceptions duplicate policy and do not provide
the requested general interface.

## Verification

Use focused shell checks without installing tools or creating Azure
resources. Validate shell syntax and exercise the real matching functions.

- `v1.34.*` accepts `v1.34.1`, not `v1.33.2` or `v1.340.1`.
- `v1.33.*` accepts `v1.33.2`.
- An exact `v1.34.1` rejects `v1.34.2`.
- `v2.14.0*` accepts
  `v2.14.0-custom-gcl-47DEQpj8HBSaTImW5JCeuQeRkm5NMpJWZG3hSuFU`,
  not `v2.15.0-custom-gcl-hash`.
- Literal `v0.19.0` accepts itself and rejects `v0.19.0-rc.1`.
- Explicit wildcard patterns accept prerelease suffixes.
- Matching works within multiline command output.
- Missing commands, failed version commands, and unmatched versions
  retain return codes 1, 2, and 3 respectively; matches return 0.
- Wildcard and exact Kubernetes selectors produce the specified patterns.

Run repository-required validation after implementation and report any
environmental blockers without bypassing the configured Go proxy.
