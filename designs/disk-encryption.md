# Disk Encryption Sets with Bring Your Own Key (BYOK) Support in Karpenter

## Overview
Azure automatically encrypts all data in managed disks at rest. By default, this encryption uses Microsoft-managed keys. However, some customers require additional control over encryption keys for compliance or security requirements.

Azure Disk Encryption Sets enable customers to bring their own keys (BYOK) for encryption at rest, applying to both OS and data disks. This design document outlines the implementation of BYOK support in Karpenter for Azure. 

## Goals
- **BYOK Support**: Enable customers to use their own encryption keys for AKS OS and data disks
- **Mutability Coverage**: AKS is considering making these fields mutable Karpenter needs to account for that
- **Comprehensive Testing**: Validate BYOK functionality with:
  - Data disks and Persistent Volume Claims (PVCs)
  - OS disks (both ephemeral and managed)
- **Bootstrapping Mode Compatibility**: Support BYOK across different provisioning modes

## Non-Goals
- **Encryption at Host**: [Host-based encryption](https://learn.microsoft.com/en-us/azure/aks/enable-host-encryption) is out of scope
- **Automatic Key Rotation Detection**: No automatic VM replacement on key expiration. Customers accept the [existing AKS limitations](https://learn.microsoft.com/en-us/azure/aks/azure-disk-customer-managed-keys#limitations) 

## Implementation Requirements
Disk encryption with customer-managed keys must be configured at AKS cluster creation time using the `--node-osdisk-diskencryptionset-id` parameter today. This constraint shapes our implementation approach.

## Phased Delivery Plan
### Phase 1: Global Support 
- Support `--node-osdisk-diskencryptionset-id` from cluster creation
- Propagate the setting from options to the instance provider
- Implement for BootstrappingClient & Scriptless modes
- Remove NAP validation blocking DiskEncryptionSetID

### Phase 2: AKSNodeClass Override Support + Complete Drift Detection
- Add DiskEncryptionSetID field to AKSNodeClass spec
- Implement precedence logic: AKSNodeClass value overrides global setting
- Implement for BootstrappingClient & Scriptless modes
- Field is mutable - supports runtime changes to DiskEncryptionSetID
- **Complete drift detection implementation**:
  - Drift detection for AKSNodeClass DES changes
  - Drift detection for cluster-level DES changes
  - Automatic node replacement when either AKSNodeClass or cluster-level DES value changes
- **All Karpenter drift logic completed in this phase**

### Phase 3: Machine API Integration 
- Machine API integration for NAP mode
- Modify aks-rp machine API code to support specifying DES ID through Machine API
- Support PUT operations on Machine API to set DiskEncryptionSetID
- Support cluster-level DES inheritance in Machine API

### Phase 4: AKS Managed Cluster + AgentPool API Integration 
- **AKS Only Changes**: Karpenters work to support this pattern is done at this stage
  - Enable PUT operations on AKS Managed Cluster `--node-osdisk-diskencryptionset-id`
  - Enable PUT operations on AKS AgentPool API for DiskEncryptionSetID
  - drift detection already implemented in Phase 2, it will start to be affected starting in this phase
  - Only affects nodes without AKSNodeClass overrides are effected 


Phases 1 + 2 can be done in parallel, same with phases 3 + 4. 

## Disk Encryption Set ID Configuration

### Precedence Logic
The system follows this precedence order for determining which DiskEncryptionSetID to use:
1. **AKSNodeClass.Spec.DiskEncryptionSetID** (if specified) - Mutable override with drift detection (Phase 2)
2. **Cluster-level --node-osdisk-diskencryptionset-id** - Global default (Phase 1), Mutable starting Phase 4)

Key behaviors:
- AKSNodeClass DiskEncryptionSetID is mutable with drift detection (Phase 2)
- Cluster-level changes only affect nodes without AKSNodeClass overrides
- **Complete drift detection implemented in Phase 2** (both AKSNodeClass and cluster-level)

## AKSNodeClass Mutability Design Decision

### Phased Approach to Mutability
We will adopt a phased approach to DiskEncryptionSetID mutability:
**Phase 2 (Complete Implementation)**: Field is **mutable with comprehensive drift detection**
- Implement drift detection for AKSNodeClass DiskEncryptionSetID changes
- Implement drift detection for cluster-level DiskEncryptionSetID changes  
- Automatic node replacement when either field value changes
- Enables seamless DES migration at both NodeClass and cluster levels
- **Complete Karpenter functionality delivered**

### Phase 2 Mutability Decision

In Phase 2, we need to decide whether to make AKSNodeClass.DiskEncryptionSetID mutable. This decision has significant implications:

**Important Distinction:**
- Key rotation within same DES: No node replacement needed (handled by Azure automatically)
- Changing DES ID: Requires node replacement (what we're discussing here)

#### Option 1: Keep Field Immutable
We could add a CEL validation rule to constrain karpenter from allowing these fields to mutable, until AKS also adds support for the field being mutable. 
```yaml 
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              name:
                type: string
        x-kubernetes-validations:
        - rule: "self.spec.name == oldSelf.spec.name"
          message: "spec.name is immutable"
```

**Pros:**
- No additional complexity in drift detection
- Clear security boundary - DES changes require explicit NodePool recreation

**Cons:**
- Changing to a different DES requires NodePool recreation
- Cannot easily migrate between encryption sets
- Poor experience for DES migration scenarios
- Cannot respond quickly to DES compromise
- Eventually in Phase 4, AKS will want to make that field immutable for all APIS, this goes backwards against the original goal

#### Option 2: Make Field Mutable with Drift
```go
// Would trigger automatic node replacement when DES changes
if nodeClass.Spec.DiskEncryptionSetID != nil {
    vmDES := getVMDiskEncryptionSetID(nodeClaim)
    if *nodeClass.Spec.DiskEncryptionSetID != vmDES {
        return DiskEncryptionSetDrift, nil
    }
}
```

**Pros:**
- Seamless DES migration experience
- Aligns with Karpenter's drift philosophy
- Automatic migration to new encryption sets
- No manual NodePool management

**Cons:**
- We don't know if this would be a problem with any customers having inconsistent encryption keys in the pool

### Phase 2 Decision Points
When deciding if drift is enough, or if we need some other mechanism, we should evaluate:
- How often do users need to change DES (not just rotate keys)?
- Common reasons: key vault migration, compliance changes? 
- Most key rotation happens within same DES, ephemeral nodes are the only ones needing replacement
- DES changes are rare but high-impact events
- Usually planned migrations, not emergencies
- Emergency scenarios rare (DES compromise vs key compromise)
- Audit requirements for encryption provider changes

### Recommendation
Start with mutability + implement drift early on, so karpenter doesn't have to do additional work to conform with a new AKS pattern.

Note: Node Rotation + Replacement comes from changing the DES, not from the key being rotated. That will still follow the existing AKS limitations. 

**Phase 4 (AKS API Enhancement)**: Enable **cluster-level API mutability**
- AKS API changes to allow cluster-level mutation of DiskEncryptionSetID
- No additional Karpenter work required - leverages existing Phase 2 drift detection
- Pure AKS platform enhancement

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
The AKSMachineAPI automatically inherits the `--node-osdisk-diskencryptionset-id` value from the managed cluster object. The Machine API and AgentPool APIs default this value and explicitly prevent mutation after initial configuration.

In Phase 3, the Machine API will need to:
- Support updates to the cluster-level DiskEncryptionSetID we pass from the AKSNodeClass

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

**Future Enhancement**: Potential integration with Azure Event Grid to trigger drift detection and automatic VM replacement on key rotation events (Phase 5+).

## Drift Detection

### Drift Detection Implementation

**Phase 2**: Complete drift detection implementation (both AKSNodeClass and cluster-level)
**Phase 4**: No additional drift detection work needed, but cluster-level drift will start occurring

```go
// In pkg/cloudprovider/drift.go
const DiskEncryptionSetDrift cloudprovider.DriftReason = "DiskEncryptionSetDrift"

func (c *CloudProvider) isDiskEncryptionSetDrifted(
    ctx context.Context,
    nodeClaim *karpv1.NodeClaim,
    nodeClass *v1beta1.AKSNodeClass,
) (cloudprovider.DriftReason, error) {
    vmDES := getVMDiskEncryptionSetID(nodeClaim) // Get from VM properties
    
    // Phase 2: Check AKSNodeClass override first (has precedence)
    if nodeClass.Spec.DiskEncryptionSetID != nil {
        if *nodeClass.Spec.DiskEncryptionSetID != vmDES {
            return DiskEncryptionSetDrift, nil
        }
        return "", nil // AKSNodeClass matches, no drift
    }
    
    // Phase 4: Check cluster-level setting if no AKSNodeClass override
    clusterDES := options.FromContext(ctx).DiskEncryptionSetID
    if vmDES != clusterDES {
        return DiskEncryptionSetDrift, nil
    }
    
    return "", nil
}
```
This is how we implement drift for AKSNodeClass and CRP APIs, machine IsDrifted could require more work

### Key Principles:
- **Phase 2**: Complete drift detection for both AKSNodeClass and cluster-level DiskEncryptionSetID changes
- **Precedence**: AKSNodeClass overrides cluster-level settings  
- **Selective drift**: Cluster-level changes only affect nodes without AKSNodeClass overrides
- **Phase 4**: No additional Karpenter drift logic needed

**Important**: Read this doc for context as to what happens to disk I/O when the key is either [deleted, disabled or expired](https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#full-control-of-your-keys)

Currently, NAP mode validation blocks DiskEncryptionSetID. 

### Machine API Integration
- **Phase 3 Changes**: Support updates to cluster-level DiskEncryptionSetID
- Both `ManagedCluster` and `AgentPoolProfile` already have `DiskEncryptionSetID` fields in the readonly api
- Machine API inherits correctly from cluster settings

### Validation Updates

**Phase 1**: Remove validation blocking customers from enabling nap when they have disk encryption set id enabled. 
**Phase 3**: Allow PUT operations on cluster DiskEncryptionSetID when NAP is enabled


## RBAC: Azure RBAC Requirements 
1. DES Access: The DES is the azure resource that links the AKS disks to the Key Vault Key. To allow AKS to use CMKs, the user must grant the Cluster Identity permission to use the Disk Encryption Set. 
2. Key Vault Access: The managed identity associated with AKS must be able to access the keys within the key vault, this allows AKS to wrap and unwrap the data encryption keys used for disk encryption with the master key

| Principal | Role Assignment | Scope | 
| - | - | - | 
| Cluster Identity | Disk Encryption Set Reader | diskEncryptionSetID | 
| Cluster Identity | Key Vault Crypto Service Encryption User | Key Vault Resource | 

Karpenter could support marking the AKSNodeClass as unready if we do not have the proper RBAC to do the encryption. Our project needs a larger conversation RE Webhooks VS Readiness Conditions

**Optional/Alternative**
Some Vaults have `rbac-authorization` disabled on the key vault. Instead of leveraging Azure RBAC, Key Vault has its own access policies. Its strongly recommended to use RBAC instead of this legacy model. But if needed user can follow [this doc](https://learn.microsoft.com/en-us/azure/key-vault/general/assign-access-policy?tabs=azure-portal)


## References
- [Key Vault Access Policies](https://learn.microsoft.com/en-us/azure/key-vault/general/assign-access-policy?tabs=azure-portal)
- [Azure Disk Encryption Overview](https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption)
- [Encryption Key Management](https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#about-encryption-key-management)
- [AKS Customer-Managed Keys](https://learn.microsoft.com/en-us/azure/aks/azure-disk-customer-managed-keys)
- [Key Vault Key Rotation Configuration](https://docs.azure.cn/en-us/key-vault/keys/how-to-configure-key-rotation)
- [Full Control of Your Keys - Key Deletion/Expiration Impacts](https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#full-control-of-your-keys)
