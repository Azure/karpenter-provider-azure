# workflows

## E2E

### Making a new E2E test

1. Create your new E2E testing suite `<test-suite-name>` within the `test/suites/` package. See: [test/README.md](../../test/README.md)
2. Update the `workflows/e2e-matrix.yaml` workflow to include your E2E test case: `suite: [Utilization, GPU, ...]` - add in the name of your folder within the `test/suites/` package to the comma separated list. Casing does not matter.

> **Note — suites that need a non-default cluster:** most suites run on the shared CI cluster
> (`ci-mkcluster-all`, Azure CNI overlay + Cilium, in the matrix's `provision_mode`). The `Windows`
> suite is a dedicated matrix over both AKS Machine API modes. Each cluster runs the Windows2022 and
> Windows2025 provisioning specs and uses a non-Cilium cluster (`ci-mkcluster-all-windows`,
> `az-mkaks-windows`) with a machines pool name `<= 6` chars (`winmp`) to satisfy the Windows
> machine-name limit. Follow that pattern if a new suite needs its own cluster shape or provisioning mode.

### Running the test case

(temporary workflow until we re-enable automation)

1. Create a new branch (or make a draft PR)
2. Ensure the identity used to run E2E tests has permission for the new branch
3. Trigger the [E2EMatrixTrigger](https://github.com/Azure/karpenter-provider-azure/actions/workflows/e2e-matrix-trigger.yaml) action manually on your branch
4. Record the results of the test run on the PR as evidence
