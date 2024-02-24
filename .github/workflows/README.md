# workflows

## E2E

### Making a new E2E test
1. Create your new E2E testing suite `<test-suite-name>` within the `test/suites/` package. See: `test/README.md`
2. Update the `workflows/e2e-matrix.yaml` workflow to include your E2E test case on line 24: `suite: [Nonbehavioral, Utilization]`.
    - Add in the name of your folder within the `test/suites/` package to the comma seperated list. Casing does not matter.

### Running the test case
1. Make a draft PR
2. Anytime you want to run the E2E test suite submit a review comment `/test`
3. Each time a the given review comment is submited it will trigger the [E2EMatrixTrigger](https://github.com/Azure/karpenter-provider-azure/actions/workflows/e2e-matrix-trigger.yaml) workflow which will contain your test suite.



## E2E NAP
We want to create a github action, that allows us to run the e2e-matrix-trigger with a specified set of cluster create params and install self hosted or not install self hosted karpenter
