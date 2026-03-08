# AzureVM Provision Mode

**Author:** @comtalyst

**Last updated:** March 7, 2026

**Status:** Proposed

## Overview

AzureVM provision mode enables Karpenter to provision standalone Azure Virtual Machines that are **not** part of an AKS cluster. This opens Karpenter as a general-purpose VM autoscaler for any Kubernetes distribution running on Azure (e.g., self-managed k8s, Rancher, OpenShift, Talos).

In existing AKS modes (`AKSMachineAPI`, `AKSScriptless`, `BootstrappingClient`), Karpenter relies heavily on AKS-specific infrastructure: image family resolution via AKS VHD build system, node bootstrapping via AKS's cloud-init/CSE pipeline, AKS billing extensions, and AKS load balancer backend pool management. AzureVM mode bypasses all of these, giving the user full control over image selection and node bootstrapping.

### Goals

* Allow Karpenter to provision VMs outside of AKS clusters
* Support user-provided VM images (Compute Gallery, SIG, or any ARM image resource)
* Support user-provided bootstrap data (cloud-init / custom scripts via `userData`)
* Support per-NodeClass subscription, resource group, and location overrides for multi-subscription deployments
* Support per-NodeClass managed identity assignment
* Support optional data disk attachment
* Maintain backward compatibility — existing AKS modes are unaffected

### Non-Goals

* Windows VM support (Linux only for now)
* Automatic image updates or OS patching
* Karpenter-managed node bootstrapping (the user provides their own)
* AKS billing extension or AKS identifying extension in AzureVM mode
* Node auto-join to AKS clusters (use AKS modes for that)

## Architecture

### New CRD: AzureNodeClass (karpenter.azure.com/v1alpha1)

A new CRD `AzureNodeClass` is introduced alongside the existing `AKSNodeClass`. It contains fields relevant to generic Azure VM provisioning:

```yaml
apiVersion: karpenter.azure.com/v1alpha1
kind: AzureNodeClass
metadata:
  name: my-nodeclass
spec:
  imageID: "/subscriptions/.../Microsoft.Compute/galleries/.../versions/1.0.0"
  userData: "#!/bin/bash\nkubeadm join ..."
  vnetSubnetID: "/subscriptions/.../subnets/worker-subnet"
  osDiskSizeGB: 128
  dataDiskSizeGB: 256
  subscriptionID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
  resourceGroup: "my-custom-rg"
  location: "westus2"
  managedIdentities:
    - "/subscriptions/.../userAssignedIdentities/my-identity"
  tags:
    environment: production
  security:
    encryptionAtHost: true
```

### Adapter Pattern

Internally, `AzureNodeClass` is converted to `AKSNodeClass` via an adapter function (`AKSNodeClassFromAzureNodeClass`). The VM provider always operates on `AKSNodeClass`, with AzureVM-specific fields carried via `json:"-"` fields that don't appear in the AKSNodeClass CRD schema:

```
AzureNodeClass → adapter → AKSNodeClass (with hidden fields) → VM Provider
```

This avoids duplicating the entire VM provider and keeps the code path unified.

### Provider Behavior by Mode

| Behavior | AKS Modes | AzureVM Mode |
|---|---|---|
| Image resolution | AKS VHD image families | User-provided `imageID` |
| Node bootstrap | AKS cloud-init / CSE | User-provided `userData` |
| LB backend pools | Configured from AKS LB | Skipped |
| NSG lookup | AKS-managed NSG | Skipped |
| Billing extension | Installed | Skipped |
| Identifying extension | Installed | Skipped |
| CSE extension | Installed (bootstrappingclient) | Skipped |
| K8s version validation | Required | Skipped |
| Data disks | Not supported | Optional via `dataDiskSizeGB` |
| Multi-subscription | Not supported | Optional via `subscriptionID` |

## Decisions

### Decision 1: Separate CRD vs. extending AKSNodeClass

#### Option A: Add all fields to AKSNodeClass
Pro: Single CRD. Con: Pollutes the AKSNodeClass with non-AKS fields; confusing UX for AKS users.

#### Option B: New AzureNodeClass CRD with adapter pattern
Pro: Clean separation of concerns; each CRD has only the fields relevant to its use case. Con: Slight code complexity from the adapter.

#### Conclusion: Option B
The adapter pattern keeps the AKSNodeClass API clean and focused on AKS, while AzureNodeClass serves the standalone VM use case. The adapter is a simple mapping function, not a complex abstraction layer.

### Decision 2: Multi-subscription client management

#### Option A: Create new SDK clients per-request
Pro: Simple. Con: Expensive — Azure SDK client creation involves HTTP transport setup.

#### Option B: Lazy, cached per-subscription client pool (AZClientManager)
Pro: Clients are created once per subscription and reused. Thread-safe via double-checked locking. Con: Slight memory overhead for cached clients.

#### Conclusion: Option B
`AZClientManager` provides `GetClients(subscriptionID)` which returns cached `SubscriptionClients` (containing VirtualMachinesClient, VirtualMachineExtensionsClient, NetworkInterfacesClient, SubnetsClient). Default subscription returns the existing AZClient's clients directly.

### Decision 3: Data disk configuration

Data disks are configured as Premium_LRS managed disks attached at LUN 0 with auto-delete on VM termination. This is a simple, opinionated default suitable for container runtime storage. Future iterations may support multiple disks, custom storage account types, or per-disk configuration.

## PR Chain

The feature is delivered as a chain of incremental PRs:

1. **PR 1487 — AzureNodeClass CRD** (`dd8cb731`): Defines the new CRD and adapter
2. **PR 1488 — AzureVM provision mode** (`ad6c5a2d`): Adds `--provision-mode=azurevm` flag with relaxed validation
3. **PR 1489 — Azure VM provider** (`3bfa8942`): Core VM provider changes for AzureVM mode
4. **PR 1497 — Multi-subscription + data disk** (`d0963558`): Per-NodeClass overrides, AZClientManager, data disk

## Testing

* Unit tests for all new helper functions (configureStorageProfile, configureOSProfile, buildVMIdentity, configureDataDisk, resolveEffectiveClients)
* Unit tests for AZClientManager (default subscription, lazy creation)
* Unit tests for AKSNodeClassFromAzureNodeClass adapter (all field mappings)
* Unit tests for GetManagedExtensionNames (AzureVM mode returns no extensions)
* E2E tests planned with self-managed k8s cluster using custom images

## Production Readiness

* **RBAC**: The controller's managed identity / service principal must have VM Contributor and Network Contributor roles in any target subscription
* **Quotas**: Standard Azure VM quotas apply per-subscription
* **Observability**: Existing Karpenter metrics (vm_create_start, vm_create_failure) apply. Error codes are extracted via `ErrorCodeForMetrics`
* **Upgrade path**: AzureNodeClass is v1alpha1; field changes before GA are expected

## Known Limitations

### Multi-Subscription Support (v1alpha1)

The `subscriptionID`, `resourceGroup`, and `location` override fields are defined on the AzureNodeClass CRD, and the adapter maps them through to the VM provider. However, the following limitations exist:

1. **Create path**: `resolveEffectiveClients()` is implemented but not yet wired into `beginLaunchInstance()`. VMs are currently always created in the controller's default subscription/RG. The wiring requires refactoring `createNetworkInterface()` and `createVirtualMachine()` to accept overridden clients.

2. **List/Get/Delete paths**: These operations (used for garbage collection, drift detection, and lifecycle management) are hardcoded to the default subscription/RG via `p.azClient` and `p.resourceGroup`. VMs created in a non-default subscription or resource group would not be discoverable by Karpenter for lifecycle management.

3. **Error handling**: When a non-default subscription is specified but `AZClientManager` is not injected, `resolveEffectiveClients()` returns a clear error instead of silently using default-subscription clients.

These limitations will be addressed in a follow-up PR. For the initial release, AzureVM mode operates exclusively within the controller's default subscription and resource group.

### userData Encoding

The `userData` field must contain pre-base64-encoded data. The Azure Compute SDK does NOT auto-encode `CustomData`. If raw (non-base64) data is provided, the VM will receive corrupted bootstrap data.
