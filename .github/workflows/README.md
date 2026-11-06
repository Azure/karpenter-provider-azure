# workflows

## E2E

### Making a new E2E test

1. Create your new E2E testing suite `<test-suite-name>` within the `test/suites/` package. See: [test/README.md](../../test/README.md)
2. Update the `workflows/e2e-matrix.yaml` workflow to include your E2E test case: add the name of your folder within the `test/suites/` package to the `suite` matrix. Casing does not matter.

> **Note — provisioning modes and non-default clusters:** the matrix runs each suite in both
> `aksscriptless` and `aksmachineapi` modes. Most combinations use the shared CI cluster shape
> (`ci-mkcluster-all`, Azure CNI overlay + Cilium). The `Windows` suite's scriptless combination is
> skipped because Windows is only provisionable via the AKS Machine API. Its Machine API combination
> uses a dedicated cluster (`ci-mkcluster-all-windows`, `az-mkaks-windows`) because Windows does not
> support the Cilium dataplane, and a machines pool name `<= 6` chars (`mpool`) to satisfy the Windows
> machine-name limit. Follow that pattern if a new suite needs its own cluster shape or provisioning
> mode.

### Running the test case

(temporary workflow until we re-enable automation)

1. Create a new branch (or make a draft PR)
2. Ensure the identity used to run E2E tests has permission for the new branch
3. Trigger the [E2EMatrixTrigger](https://github.com/Azure/karpenter-provider-azure/actions/workflows/e2e-matrix-trigger.yaml) action manually on your branch
4. Record the results of the test run on the PR as evidence
