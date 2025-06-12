# Using Customer Managed Keys (CMK) for OS Disk Encryption in AKSNodeClass

Azure supports encrypting managed disks — including AKS node OS disks — with your own Customer Managed Keys (CMK) via [Disk Encryption Sets](https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#customer-managed-keys).
This document explains how to use this feature with Karpenter’s `AKSNodeClass` and provides links to official Azure documentation for further details.

## What is Customer Managed Key (CMK) Encryption?

By default, Azure encrypts managed disks with Microsoft-managed keys.
With CMK, you can use your own keys stored in an Azure Key Vault, giving you greater control over disk encryption and key lifecycle.

- **Default:** Microsoft-managed keys are used for disk encryption.
- **With CMK:** You provide a Disk Encryption Set (DES) resource, which references your Key Vault and key.

See [Azure Disk Encryption documentation](https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#customer-managed-keys) for more details.

## Official AKS Documentation

- [Use customer-managed keys for Azure managed disks](https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#customer-managed-keys)
- [AKS disk encryption options](https://learn.microsoft.com/en-us/azure/aks/azure-disk-customer-managed-keys)
- [Azure Key Vault soft-delete and purge protection](https://learn.microsoft.com/en-us/azure/key-vault/general/soft-delete-overview#purge-protection)

## How it Works in Karpenter

Karpenter’s `AKSNodeClass` supports a field called `osDiskDiskEncryptionSetID`.
If set, the OS disk for nodes provisioned by this class will be encrypted using the specified Disk Encryption Set (and thus your CMK).
If not set, Microsoft-managed keys are used by default.

**Example:**
```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: cmk-enabled
spec:
  imageFamily: Ubuntu2204
  # Replace with your actual Disk Encryption Set resource ID
  osDiskDiskEncryptionSetID: "/subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.Compute/diskEncryptionSets/<des-name>"
```

## Prerequisites

- An Azure Key Vault with a key (RSA, with purge protection enabled).
- A Disk Encryption Set (DES) referencing your Key Vault and key.
- The Karpenter managed identity must have the necessary permissions on the Key Vault and DES.
- [Azure CLI](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli) and required extensions installed.

## Automating Resource Creation

The Makefile in this repository provides automation for creating the required resources:

```bash
# Optionally override the Key Vault name if needed
make az-cmk-all AZURE_KEYVAULT_NAME=mycustomkv
```

If the Key Vault name is unavailable due to purge protection retention, specify a different name using the `AZURE_KEYVAULT_NAME` environment variable.

See [`Makefile-az.mk`](../Makefile-az.mk) for details.

## Usage Notes and Defaults

- If `osDiskDiskEncryptionSetID` is omitted, Microsoft-managed keys are used (the Azure default).
- If you specify a DES, ensure the Karpenter managed identity has `Reader` access to the DES and the appropriate key permissions in Key Vault.
- Due to [purge protection](https://learn.microsoft.com/en-us/azure/key-vault/general/soft-delete-overview#purge-protection), Key Vault names cannot be reused until the retention period expires.
- See [`examples/v1beta1/cmk-enabled.yaml`](../examples/v1beta1/cmk-enabled.yaml) for a complete manifest example.

## Further Reading

- [Azure Disk Encryption documentation](https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#customer-managed-keys)
- [AKS disk encryption options](https://learn.microsoft.com/en-us/azure/aks/azure-disk-customer-managed-keys)
- [Azure Key Vault soft-delete and purge protection](https://learn.microsoft.com/en-us/azure/key-vault/general/soft-delete-overview#purge-protection)
- [Azure Disk Encryption Set documentation](https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#disk-encryption-sets)

---

For questions or troubleshooting, see the [README](../README.md) or open an issue.
