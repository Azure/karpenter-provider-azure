# workflows
test/README.md
## E2E

### Making a new E2E test

1. Create your new E2E testing suite `<test-suite-name>` within the `test/suites/` package. See: `test/README.md`
2. Update the `workflows/e2e-matrix.yaml` workflow to include your E2E test case: `suite: [Utilization, GPU, ...]` - add in the name of your folder within the `test/suites/` package to the comma separated list. Casing does not matter.

### Running the test case

(temporary workflow until we re-enable automation)

1. Create a new branch (or make a draft PR)
2. Ensure the identity used to run E2E tests has permission for the new branch
3. Trigger the [E2EMatrixTrigger](https://github.com/Azure/karpenter-provider-azure/actions/workflows/e2e-matrix-trigger.yaml) action manually on your branch
4. Record the results of the test run on the PR as evidence
