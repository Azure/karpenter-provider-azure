/*
Portions Copyright (c) Microsoft Corporation.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package azure

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

// CreateKeyVaultAndDiskEncryptionSet creates a key vault and disk encryption set for BYOK testing
func (env *Environment) CreateKeyVaultAndDiskEncryptionSet(ctx context.Context) string {
	keyVaultName := fmt.Sprintf("karpentertest%d", time.Now().Unix())
	keyName := "test-key"
	desName := fmt.Sprintf("karpenter-test-des-%d", time.Now().Unix())

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	Expect(err).ToNot(HaveOccurred())

	clusterIdentity := env.GetClusterIdentityPrincipalID(ctx)

	keyVault := armkeyvault.Vault{
		Location: to.Ptr(env.Region),
		Properties: &armkeyvault.VaultProperties{
			TenantID: to.Ptr(env.GetTenantID()),
			SKU: &armkeyvault.SKU{
				Family: to.Ptr(armkeyvault.SKUFamilyA),
				Name:   to.Ptr(armkeyvault.SKUNameStandard),
			},
			AccessPolicies: []*armkeyvault.AccessPolicyEntry{
				{
					TenantID: to.Ptr(env.GetTenantID()),
					ObjectID: to.Ptr(clusterIdentity),
					Permissions: &armkeyvault.Permissions{
						Keys: []*armkeyvault.KeyPermissions{
							to.Ptr(armkeyvault.KeyPermissionsGet),
							to.Ptr(armkeyvault.KeyPermissionsList),
							to.Ptr(armkeyvault.KeyPermissionsCreate),
							to.Ptr(armkeyvault.KeyPermissionsDelete),
							to.Ptr(armkeyvault.KeyPermissionsWrapKey),
							to.Ptr(armkeyvault.KeyPermissionsUnwrapKey),
						},
					},
				},
			},
			EnabledForDiskEncryption:  to.Ptr(true),
			EnablePurgeProtection:     to.Ptr(true),
			SoftDeleteRetentionInDays: to.Ptr(int32(7)),
		},
	}

	poller, err := env.keyVaultClient.BeginCreateOrUpdate(ctx, env.NodeResourceGroup, keyVaultName, armkeyvault.VaultCreateOrUpdateParameters{
		Location:   keyVault.Location,
		Properties: keyVault.Properties,
	}, nil)
	Expect(err).ToNot(HaveOccurred())

	kvResp, err := poller.PollUntilDone(ctx, nil)
	Expect(err).ToNot(HaveOccurred())

	keyClient, err := azkeys.NewClient(fmt.Sprintf("https://%s.vault.azure.net/", keyVaultName), cred, nil)
	Expect(err).ToNot(HaveOccurred())

	keyResp, err := keyClient.CreateKey(ctx, keyName, azkeys.CreateKeyParameters{
		Kty:     to.Ptr(azkeys.KeyTypeRSA),
		KeySize: to.Ptr(int32(2048)),
	}, nil)
	Expect(err).ToNot(HaveOccurred())

	des := armcompute.DiskEncryptionSet{
		Location: to.Ptr(env.Region),
		Identity: &armcompute.EncryptionSetIdentity{
			Type: to.Ptr(armcompute.DiskEncryptionSetIdentityTypeSystemAssigned),
		},
		Properties: &armcompute.EncryptionSetProperties{
			ActiveKey: &armcompute.KeyForDiskEncryptionSet{
				KeyURL: to.Ptr(string(*keyResp.Key.KID)),
				SourceVault: &armcompute.SourceVault{
					ID: kvResp.ID,
				},
			},
		},
	}

	desPoller, err := env.diskEncryptionSetClient.BeginCreateOrUpdate(ctx, env.NodeResourceGroup, desName, des, nil)
	Expect(err).ToNot(HaveOccurred())

	desResp, err := desPoller.PollUntilDone(ctx, nil)
	Expect(err).ToNot(HaveOccurred())

	desIdentity := desResp.Identity
	Expect(desIdentity).ToNot(BeNil())
	Expect(desIdentity.PrincipalID).ToNot(BeNil())

	// Update key vault access policies to include DES
	kvGet, err := env.keyVaultClient.Get(ctx, env.NodeResourceGroup, keyVaultName, nil)
	Expect(err).ToNot(HaveOccurred())

	accessPolicies := kvGet.Properties.AccessPolicies
	accessPolicies = append(accessPolicies, &armkeyvault.AccessPolicyEntry{
		TenantID: to.Ptr(env.GetTenantID()),
		ObjectID: desIdentity.PrincipalID,
		Permissions: &armkeyvault.Permissions{
			Keys: []*armkeyvault.KeyPermissions{
				to.Ptr(armkeyvault.KeyPermissionsGet),
				to.Ptr(armkeyvault.KeyPermissionsWrapKey),
				to.Ptr(armkeyvault.KeyPermissionsUnwrapKey),
			},
		},
	})

	updatePoller, err := env.keyVaultClient.BeginCreateOrUpdate(ctx, env.NodeResourceGroup, keyVaultName, armkeyvault.VaultCreateOrUpdateParameters{
		Location: kvGet.Location,
		Properties: &armkeyvault.VaultProperties{
			TenantID:                  kvGet.Properties.TenantID,
			SKU:                       kvGet.Properties.SKU,
			AccessPolicies:            accessPolicies,
			EnabledForDiskEncryption:  kvGet.Properties.EnabledForDiskEncryption,
			EnablePurgeProtection:     kvGet.Properties.EnablePurgeProtection,
			SoftDeleteRetentionInDays: kvGet.Properties.SoftDeleteRetentionInDays,
		},
	}, nil)
	Expect(err).ToNot(HaveOccurred())

	_, err = updatePoller.PollUntilDone(ctx, nil)
	Expect(err).ToNot(HaveOccurred())

	return lo.FromPtr(desResp.ID)
}
