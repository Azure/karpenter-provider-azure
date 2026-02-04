---
name: run-e2e-tests
description: Run end-to-end (E2E) tests - provider test suites or upstream Karpenter tests, with focus filtering, background execution, and monitoring
---

# E2E Tests

## Running Tests

- **Provider tests**: `TEST_SUITE=<suite> make az-e2etests` (suites in `test/suites/`)
- **Upstream tests**: `FOCUS=<focus> make az-upstream-e2etests` (source in Go module cache or [upstream repo](https://github.com/kubernetes-sigs/karpenter/tree/main/test/suites))

Use `FOCUS` to filter further (or `FDescribe`/`FIt` in code).

## Background Execution

Run with `nohup` so tests continue even if you run other commands or the terminal disconnects:

```bash
TEST_SUITE=Consolidation nohup make az-e2etests > /workspaces/karpenter/logs/e2e.log 2>&1 &
```

## Codespace Considerations

Idle suspension kills all processes (default 30min, can be increased under https://github.com/settings/codespaces; only affects new codespaces). Logs in `/workspaces` (not `/tmp`) survive for checking after resume.

## Monitoring

- Logs: `tail -f <logfile>` (or `tail -n` for spot check)
- Cluster: `kubectl get nodeclaims,nodes,pods -w`
- Events: `make az-kevents`
- Azure resources: `make az-res`
- Metrics: port-forward to Karpenter pod, `/metrics` endpoint

## GitHub Actions

Run via `.github/workflows/e2e.yaml` using `gh workflow run`.
