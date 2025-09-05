# Disk Encryption Sets with Bring Your Own Key (BYOK) Support in Karpenter

## Overview
Azure automatically encrypts all data in managed disks at rest. By default, this encryption uses Microsoft-managed keys. However, some customers require additional control over encryption keys for compliance or security requirements.

Azure Disk Encryption Sets enable customers to bring their own keys (BYOK) for encryption at rest, applying to both OS and data disks. This design document outlines the implementation of BYOK support in Karpenter for Azure. 

## Goals
- **BYOK Support**: Enable customers to use their own encryption keys for AKS OS and data disks
- **Comprehensive Testing**: Validate BYOK functionality with:
  - Data disks and Persistent Volume Claims (PVCs)
  - OS disks (both ephemeral and managed)
- **Bootstrapping Mode Compatibility**: Support BYOK across different provisioning modes

## Non-Goals
- **Encryption at Host**: [Host-based encryption](https://learn.microsoft.com/en-us/azure/aks/enable-host-encryption) is out of scope
- **Automatic Key Rotation Drift Detection**: No automatic VM replacement on key expiration. Customers accept the [existing AKS limitations](https://learn.microsoft.com/en-us/azure/aks/azure-disk-customer-managed-keys#limitations) 

## Implementation Requirements

Disk encryption with customer-managed keys must be configured at AKS cluster creation time using the `--node-osdisk-diskencryptionset-id` parameter. This constraint shapes our implementation approach. 

## Disk Encryption Set ID Configuration

### Bootstrapping Mode Support

#### BootstrappingClient & Scriptless Modes
For these modes, Karpenter must:
1. Accept the `--node-osdisk-diskencryptionset-id` parameter via options
2. Pass the DiskEncryptionSetID directly to the instance provider
3. Apply the encryption configuration during VM creation:

```go
if provider.diskEncryptionSetID != "" {
    osDisk.ManagedDisk = &armcompute.ManagedDiskParameters{
        DiskEncryptionSet: &armcompute.DiskEncryptionSetParameters{
            ID: to.StringPtr(provider.diskEncryptionSetID),
        },
    }
}
```

#### AKSMachineAPI Mode
The AKSMachineAPI automatically inherits the `--node-osdisk-diskencryptionset-id` value from the managed cluster object. The Machine API and AgentPool APIs default this value and explicitly prevent mutation after initial configuration. No additional implementation is required for this mode.

#### Implementation Rationale
While AKSMachineAPI will eventually handle this configuration automatically, supporting BootstrappingClient and Scriptless modes is essential to meet immediate business requirements for BYOK support. 

## Customer-Managed Key (CMK) Rotation

### Rotation Types

Azure Key Vault supports two rotation mechanisms:

1. **Automatic Key Rotation**: Scheduled rotation based on configured policies
2. **Manual Key Rotation**: User-initiated rotation on demand


### Configuration Steps

#### 1. Set Up Rotation Policy
```bash
# Create rotation policy (rotate after 18 months)
cat > rotation-policy.json <<EOF
{
  "lifetimeActions": [
    {
      "trigger": {
        "timeAfterCreate": "P18M"
      },
      "action": {
        "type": "Rotate"
      }
    }
  ],
  "attributes": {
    "expiryTime": null
  }
}
EOF

# Apply policy to key
az keyvault key rotation-policy update \
  --vault-name <vault-name> \
  --name <key-name> \
  --value @rotation-policy.json
```

#### 2. Enable Auto-Rotation on Disk Encryption Set
Use `--enable-auto-key-rotation true` when creating the DES to automatically adopt the newest key version from Azure Key Vault.

### Disk Re-Encryption Behavior

#### Managed OS Disks
Azure automatically updates managed disks to use the latest rotated key version without requiring VM recreation.

#### Ephemeral OS Disks
Ephemeral disks have immutable encryption settings. The only way to apply a new encryption key is to:
1. Delete the existing VM
2. Create a new VM (which will use the latest key version) 

### AKS Limitations and Rotation Strategies

[AKS requires manual intervention for key rotation with ephemeral disks](https://learn.microsoft.com/en-us/azure/aks/azure-disk-customer-managed-keys#limitations). Two strategies are available:

#### Strategy 1: Immediate Key Adoption
1. Scale node pool to 0 replicas
2. Rotate the encryption key
3. Scale node pool back to original count

#### Strategy 2: Gradual Key Adoption
1. Allow natural node replacement through:
   - AKS node image upgrades
   - Kubernetes version upgrades
2. Configure rotation policies to maintain old key validity until replacement completes
3. New CMK takes effect as nodes are progressively replaced

### Karpenter Implementation Approach

Karpenter will initially adopt the same limitations as AKS:
- No automatic VM replacement on key rotation
- Rely on natural node lifecycle events for key adoption

**Future Enhancement**: Potential integration with Azure Event Grid to trigger drift detection and automatic VM replacement on key rotation events.

**Important**: Read this doc for context as to what happens to disk I/O when the key is either [deleted, disabled or expired](https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#full-control-of-your-keys)


## End-to-End Testing Strategy

We will be introducing tests that ensure OS Disks for ephemeral disk + Network Attached Disks are properly encrypted with BYOK.

### Test Organization Goals

We need to solve two distinct testing challenges:

#### 1. BYOK Test Suite
Create a dedicated test suite for BYOK encryption scenarios that requires additional infrastructure setup before cluster creation. BYOK tests need Key Vault creation, disk encryption set setup, and additional RBAC permissions that are expensive and only relevant for encryption scenarios. These tests require fundamentally different cluster creation steps (using `az-mkaks-cmk` instead of standard cluster creation) and should only run when specifically testing encryption functionality.

#### 2. Storage Test Suite 
Move existing storage-related tests into their own organized suite. We have storage scenarios scattered in integration and some ephemeral OS disk testing in nodeclaim. As we have more scenarios relating to storage that are cloudprovider specific (our ephemeral disk with v6 SKU testing), we risk polluting other test suites. Storage is a clearly defined domain that warrants its own organization. Since this work touches the storage area, its worth revisiting this test organization inside of this effort. 

### Tradeoffs
- Moving tests like the storage scenarios for PVC into their own suite rather than integration breaks the alignment we have with the EKS provider and their testing directory structure, which is a valuable reference for coverage our provider may be missing
- However, Azure-specific storage features justify the separate organization and prevent test suite pollution

### Implementation: Dual Suite Approach
While ginkgo labeling is nice for keeping things filtered and allowing us to pick up a subset of scenarios to run in cases where an e2e may belong to multiple domains, we have enough cases for storage related to just our cloudprovider that dumping them all into integration for parity, or in nodeclaim doesn't make a ton of sense.

We will implement a dual suite approach:

#### Directory Structure
```
test/suites/
├── storage/           # General storage tests (moved from integration + nodeclaim)
│   ├── suite_test.go
│   ├── storage_test.go         # Moved from integration/storage_test.go  
│   ├── ephemeral_os_disk_test.go # Moved from nodeclaim/eph_osdisk_test.go
├── byok/             # BYOK-specific encryption tests only
│   ├── suite_test.go
│   └── disk_encryption_byok_test.go
```

#### Test Suite Responsibilities

**Storage Suite (`test/suites/storage/`)**
- **Migration only**: Move existing storage-related tests from integration and nodeclaim suites
- PVC functionality with standard Azure disks
- Ephemeral OS disk functionality (without encryption)
- Managed OS disk functionality (without encryption)
- No new test development required - pure reorganization
- Note: While ginkgo label filtering could keep tests co-located, the volume of Azure-specific storage scenarios justifies dedicated organization

**BYOK Suite (`test/suites/byok/`)**
- OS disk encryption with ephemeral disks + BYOK
- OS disk encryption with managed disks + BYOK  
- Data disk encryption with PVCs + BYOK
- Error handling for invalid DiskEncryptionSetID
- each test should handle key value access revocation gracefully 
- should recover when DES access is restored
- Uses BYOK-enabled cluster creation (`az-mkaks-cmk`)

### Implementation Benefits

This dual suite approach ensures we don't add expensive BYOK infrastructure to all test runs while providing comprehensive coverage of both general storage scenarios and encryption-specific functionality. 


## References

- [Azure Disk Encryption Overview](https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption)
- [Encryption Key Management](https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#about-encryption-key-management)
- [AKS Customer-Managed Keys](https://learn.microsoft.com/en-us/azure/aks/azure-disk-customer-managed-keys)
- [Key Vault Key Rotation Configuration](https://docs.azure.cn/en-us/key-vault/keys/how-to-configure-key-rotation)
- [Full Control of Your Keys - Key Deletion/Expiration Impacts](https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#full-control-of-your-keys)
