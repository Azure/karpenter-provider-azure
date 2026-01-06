# Copilot Instructions for karpenter-provider-azure

This repository contains the Azure provider implementation for [Karpenter](https://karpenter.sh/), an open-source Kubernetes node autoscaler.

## Project Overview

- **Language**: Go (version 1.24+)
- **Framework**: Kubernetes controller built on controller-runtime
- **Testing**: Ginkgo/Gomega for BDD-style tests

## Build and Test Commands

```bash
# Install developer toolchain (required before first build/test)
make toolchain

# Run unit and acceptance tests
make test

# Run verification (linting, formatting, code generation)
make verify

# Run all presubmit checks (verify + test)
make presubmit

# Download dependencies
make download

# Tidy go modules
make tidy
```

## Code Style Guidelines

- Follow Go best practices and idiomatic Go patterns
- Use `goimports` for import formatting with local prefix `github.com/Azure/karpenter-provider-azure`
- All files must include the Apache 2.0 license header (see `.golangci.yaml` for template)
- Use US English spelling (configured in misspell linter)
- Avoid dot imports except for Ginkgo and Gomega test packages
- Maximum cyclomatic complexity is 11

## Logging

- Use `sigs.k8s.io/controller-runtime/pkg/log` for logging throughout the codebase
- Use `klog` only when creating clients or authorizers
- Use `zapr` only in the debug package

## Testing Guidelines

- **Unit Tests**: Located in `pkg/*` next to related components, use Go standard testing
- **Acceptance Tests**: Located in `pkg/*`, use Ginkgo framework, test behavior starting from pod pressure
- **E2E Tests**: Located in `test/suites/*`, use Ginkgo framework, run against actual clusters

When adding new functionality:
1. Write unit tests for individual functions
2. Add acceptance tests for integration behavior
3. E2E tests are required for user-facing features

## Pull Request Guidelines

- Follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) for PR titles:
  - `feat:` - New features (MINOR version)
  - `fix:` - Bug fixes (PATCH version)
  - `docs:` - Documentation changes
  - `chore:` - Metadata/dependency updates
  - `test:` - Test changes
  - `perf:` - Performance improvements
- Include a release note in the PR description
- Link related issues using `Fixes #<issue-number>`

## Project Structure

- `pkg/apis/` - Kubernetes API types and CRDs
- `pkg/cloudprovider/` - Azure cloud provider implementation
- `pkg/controllers/` - Kubernetes controllers
- `pkg/providers/` - Azure resource providers (VMs, networking, etc.)
- `cmd/` - Application entrypoints
- `hack/` - Build and development scripts
- `charts/` - Helm charts
- `test/` - E2E test suites

## Azure-Specific Notes

- This is a fork/adaptation of [Karpenter](https://github.com/kubernetes-sigs/karpenter) for Azure
- Uses Azure SDK for Go for cloud operations
- Supports AKS (Azure Kubernetes Service) clusters
- Requires workload identity for authentication
