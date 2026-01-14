# Running E2E Tests for Karpenter Provider Azure

This guide explains how to run end-to-end (e2e) tests for the Karpenter Provider Azure project in different environments: locally, on a pull request (PR), and via GitHub Actions. It also covers how to run all suites, a single suite, or a single test case.

---

## 1. Running E2E Tests Locally

### Prerequisites
- Azure CLI (`az`) installed and authenticated (`az login`)
- Access to an Azure subscription with sufficient permissions
- Docker, kubectl, and other project dependencies installed
- A valid AKS cluster, or use the provided Makefile targets to provision one

### Cluster Setup
To provision a test AKS cluster and all required resources:

```sh
make az-all
```

This will:
- Create a resource group, ACR, and AKS cluster
- Set up managed identities and permissions
- Build and deploy Karpenter
- Deploy a sample workload

### Running All E2E Test Suites

```sh
AZURE_SUBSCRIPTION_ID=$(az account show --query id -o tsv) make az-e2etests
```

### Running a Single Suite
You can run a specific suite using the `TEST_SUITE` variable. For example, to run only the BYOK suite:

```sh
AZURE_SUBSCRIPTION_ID=$(az account show --query id -o tsv) make az-e2etests TEST_SUITE=byok
```

The `TEST_SUITE` variable should match the suite directory name under `test/suites/` (lowercase):
- `TEST_SUITE=byok` → runs tests in `test/suites/byok/`
- `TEST_SUITE=acr` → runs tests in `test/suites/acr/`
- `TEST_SUITE=chaos` → runs tests in `test/suites/chaos/`
- etc.

### Running a Single Test Case
You can narrow down to a specific test case within a suite using the `FOCUS` variable with a regex pattern:

```sh
AZURE_SUBSCRIPTION_ID=$(az account show --query id -o tsv) make az-e2etests TEST_SUITE=byok FOCUS='provision a VM with customer-managed key disk encryption'
```

**Note:** The `FOCUS` variable uses Ginkgo's `--ginkgo.focus` flag to filter tests by their description.

---

## 2. Running E2E Tests on a Pull Request (PR)


When you open or update a PR, GitHub Actions will automatically run the e2e test workflow. This will:
- Provision a temporary AKS cluster and resources in Azure
- Build and deploy the code under test
- Run all e2e test suites
- Report results in the PR checks


### Slash Commands
You can trigger or re-run e2e tests on your PR using GitHub "slash" commands in a PR comment:

- `/test` — Runs the default e2e suite (usually the most important or basic suite).
- `/test <suite>` — Runs only the specified suite. For example, `/test byok` runs the BYOK suite. Suite names match the folder names in `test/suites/` (case-insensitive):
  - `/test acr`
  - `/test byok`
  - `/test chaos`
  - etc.

To run all suites, use the **E2EMatrixTrigger** workflow manually from the Actions tab (see next section). To run just one, use `/test` (for the default) or `/test <suite>` for a specific suite.

---

## 3. Running E2E Tests via GitHub Actions (Manually)



There are multiple workflows available:
- **E2EMatrix**: Runs all e2e suites across multiple configurations. Trigger this with `/test e2ematrix` or by running the **E2EMatrixTrigger** workflow manually from the Actions tab.
- **E2E**: Runs a single selected test suite. Trigger this with `/test <suite>` or by running the **E2E** workflow manually from the Actions tab and selecting a suite.

To run all suites manually:
1. Go to the **Actions** tab in your repository.
2. Select the **E2EMatrixTrigger** workflow.
3. Click **Run workflow** and fill in any required parameters (e.g., branch, location).

To run a single suite manually:
1. Go to the **Actions** tab in your repository.
2. Select the **E2E** workflow.
3. Click **Run workflow** and select the suite you want to run.

---

## 4. Troubleshooting & Tips

### Setting the Azure Subscription ID
The `AZURE_SUBSCRIPTION_ID` environment variable is required for all e2e test commands. You can set it inline with each command:

```sh
AZURE_SUBSCRIPTION_ID=$(az account show --query id -o tsv) make az-e2etests
```

Or export it for your session:

```sh
export AZURE_SUBSCRIPTION_ID=$(az account show --query id -o tsv)
make az-e2etests
```

You can verify it with:

```sh
echo $AZURE_SUBSCRIPTION_ID
```

If you see an error like `"Environment variable AZURE_SUBSCRIPTION_ID is set to an empty string"`, ensure you've run `az login` and have a valid subscription selected.

### Federated Identity Issues
- If your e2e test fails due to federated identity or authentication errors, tag a project maintainer or someone from the CODEOWNERS file on your PR for help.
- Common symptoms: authentication failures, permission denied, or identity not found errors.

### General Tips
- Ensure your `kubectl` context is set to the correct AKS cluster before running tests locally:
  ```sh
  az aks get-credentials --name <cluster-name> --resource-group <resource-group> --overwrite-existing
  ```
- If you encounter permission or quota errors, check your Azure subscription and resource limits.
- Clean up resources after testing with:
  ```sh
  make az-rmrg
  ```
- For more advanced scenarios (custom VNet, performance tests, etc.), see the Makefile targets and `/docs/workshops/`.

---

## 5. Reference
- [Makefile-az.mk](../Makefile-az.mk)
- [test/README.md](../test/README.md)
- [docs/workshops/](./workshops/)

---

For further help, contact the maintainers or open an issue.
