# Testing Directory

These test scenarios are designed to be used against a live AKS cluster running karpenter, and validate particular E2E scenarios.

## File Directory
- `/suites`: Ginkgo test suites for particular scenarios live here.
- `/pkg`: Common code re-used across test suites lives here.

## Scale suite

The scale suite provisions up to 200 nodes or 6,600 workload pods per scenario.
Check regional VM quota and subnet capacity before running it.
The simultaneous-deprovisioning scenario configures expiration before provisioning
and waits for the original NodeClaims' ten-minute lifetime without changing their
NodePool template. Replacement nodes have expiration disabled.

Run locally against a dedicated test cluster:

```bash
TEST_SUITE=Scale make az-e2etests
```

Use `FOCUS` to select a scenario, for example:

```bash
TEST_SUITE=Scale FOCUS="node-dense scale-up" make az-e2etests
```

For CI, dispatch the existing [E2E workflow](../.github/workflows/e2e.yaml)
on the test branch with `suite: Scale` and the desired region and provisioning mode.
