# `cloudprovider` module acceptance tests

This package contains acceptance tests (i.e., end-to-end unit tests, integration unit tests) targeting executions from core controllers through provider-level `cloudprovider` interface implementation.

## What about non-`cloudprovider`?

On the other hand, acceptance tests targeting provider-level reconcilers should be in its respective package (e.g., `pkg/controllers/nodeclaim/inplaceupdate`, `pkg/controllers/nodeclaim/garbagecollection`). The rule is to place acceptance tests with their closest responsible controller/reconciler.

## Files and categorization

### `suite_features_test.go`

Use for observable provisioning features, such as:

- Fields from NodeClass to API/provisioning payloads (e.g., LocalDNS, KubeletConfig)
- Karpenter-configured provisioning payloads (e.g., Scriptless bootstrapping config)
- Labels and taints written to created resources

### `suite_offerings_test.go`

Use for offering (e.g., VM size, zones) selection and quota/capacity provisioning errors.

### `suite_integration_test.go`

Use for common operational correctness, such as:

- `Create`, `List`, `Get`, and `Delete` flows
- Basic lifecycle behavior
- Unexpected errors handling

### `suite_drift_test.go`

Use for drift detection and handling behavior.

### `suite_test.go`

Use for helpers/infrastructure. Avoid adding actual tests here.

## Shared vs. mode-specific tests

Many tests are being "shared" across multiple configurations manifested as contexts, mostly provision mode at the time of writing. This is so that we can compare the differences between modes clearly.
