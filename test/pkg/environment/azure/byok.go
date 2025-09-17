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
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

const (
	cryptoServiceEncryptionUserRole = "e147488a-f6f5-4113-8e2d-b22465e65bf6"
	readerRole                      = "acdd72a7-3385-48ef-bd42-f606fba81ae7"
	keyVaultAdminRole               = "00482a5a-887f-4fb3-b363-3b7fe8e74483"
	keyVaultCryptoOfficer           = "14b46e9e-c2b7-41b4-b07b-48a6ebf60603"
)

// CreateKeyVaultAndDiskEncryptionSet creates a key vault and disk encryption set for BYOK testing
//
// This function performs the following operations in sequence:
//
// 1. API Calls:
//   - Creates an Azure Key Vault with RBAC authorization enabled
//   - Creates a key in the Key Vault for encryption
//   - Creates a Disk Encryption Set (DES) referencing the Key Vault key
//
// 2. RBAC Assignments:
//   - Assigns Key Vault Crypto Officer and Administrator roles to the Karpenter workload identity
//   - Assigns Key Vault Crypto Officer and Administrator roles to the test user (for key creation)
//   - Assigns Reader role to the Karpenter workload identity for DES access (so it can create VMs)
//   - Assigns Key Vault Crypto Service Encryption User role to the DES managed identity (so DES can access keys)
//
// 3. Retry Logic:
//   - Azure clients are configured with retry logic for 403 (Forbidden) errors caused by RBAC propagation delays
//   - Azure clients also retry on 400 (Bad Request) errors for PrincipalNotFound when identities haven't replicated
//
// Returns the DES resource ID for use in VM disk encryption configuration.
func (env *Environment) CreateKeyVaultAndDiskEncryptionSet(ctx context.Context) string {
	keyVaultName := fmt.Sprintf("karpentertest%d", time.Now().Unix())
	keyName := "test-key"
	desName := fmt.Sprintf("karpenter-test-des-%d", time.Now().Unix())

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	Expect(err).ToNot(HaveOccurred())

	clusterTenant := env.GetTenantID(ctx)
	karpenterIdentity := env.GetKarpenterWorkloadIdentity(ctx)

	keyVault, err := env.createKeyVault(ctx, keyVaultName, clusterTenant)
	Expect(err).ToNot(HaveOccurred())

	// Get the current test user's principal ID from the defaultCredential Token
	testUserPrincipalID := env.GetCurrentUserPrincipalID(ctx, cred)
	Expect(testUserPrincipalID).ToNot(BeEmpty(), "test user authentication failed")

	err = env.assignKeyVaultRBAC(ctx, lo.FromPtr(keyVault.ID), karpenterIdentity, testUserPrincipalID)
	Expect(err).ToNot(HaveOccurred())

	key, err := env.createKeyVaultKey(ctx, keyVaultName, keyName, cred)
	Expect(err).ToNot(HaveOccurred())

	des, err := env.createDiskEncryptionSet(ctx, desName, keyVault, key)
	Expect(err).ToNot(HaveOccurred())

	desIdentity := des.Identity
	Expect(desIdentity).ToNot(BeNil())
	Expect(desIdentity.PrincipalID).ToNot(BeNil())

	err = env.assignDiskEncryptionSetRBAC(ctx, lo.FromPtr(des.ID), karpenterIdentity)
	Expect(err).ToNot(HaveOccurred())

	err = env.updateKeyVaultAccessForDES(ctx, keyVaultName, desIdentity)
	Expect(err).ToNot(HaveOccurred())

	return lo.FromPtr(des.ID)
}

// createKeyVault creates an Azure Key Vault with proper access policies
func (env *Environment) createKeyVault(ctx context.Context, keyVaultName, clusterTenant string) (*armkeyvault.Vault, error) {
	keyVault := armkeyvault.Vault{
		Location: to.Ptr(env.Region),
		Properties: &armkeyvault.VaultProperties{
			TenantID: to.Ptr(clusterTenant),
			SKU: &armkeyvault.SKU{
				Family: to.Ptr(armkeyvault.SKUFamilyA),
				Name:   to.Ptr(armkeyvault.SKUNameStandard),
			},
			EnableRbacAuthorization:   to.Ptr(true),
			AccessPolicies:            []*armkeyvault.AccessPolicyEntry{},
			EnabledForDiskEncryption:  to.Ptr(true),
			EnablePurgeProtection:     to.Ptr(true),
			SoftDeleteRetentionInDays: to.Ptr(int32(7)),
		},
	}

	poller, err := env.keyVaultClient.BeginCreateOrUpdate(ctx, env.NodeResourceGroup, keyVaultName, armkeyvault.VaultCreateOrUpdateParameters{
		Location:   keyVault.Location,
		Properties: keyVault.Properties,
	}, nil)
	if err != nil {
		return nil, err
	}

	kvResp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}

	return &kvResp.Vault, nil
}

// assignKeyVaultRBAC assigns necessary RBAC roles for Key Vault access
func (env *Environment) assignKeyVaultRBAC(ctx context.Context, keyVaultID, karpenterIdentity, testUserPrincipalID string) error {
	cryptoOfficerRoleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", env.SubscriptionID, keyVaultCryptoOfficer)
	keyVaultAdminRoleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", env.SubscriptionID, keyVaultAdminRole)

	// Assign roles to karpenter workload identity so that karpenter can access keys for disk encryption
	err := env.RBACManager.EnsureRole(ctx, keyVaultID, cryptoOfficerRoleDefinitionID, karpenterIdentity)
	if err != nil {
		return fmt.Errorf("failed to assign Crypto Officer role to karpenter identity: %w", err)
	}
	err = env.RBACManager.EnsureRole(ctx, keyVaultID, keyVaultAdminRoleDefinitionID, karpenterIdentity)
	if err != nil {
		return fmt.Errorf("failed to assign Administrator role to karpenter identity: %w", err)
	}

	// User from az.DefaultCred needs rbac to create keys and manage the vault (wrap, unwrap etc)
	err = env.RBACManager.EnsureRole(ctx, keyVaultID, cryptoOfficerRoleDefinitionID, testUserPrincipalID)
	if err != nil {
		return fmt.Errorf("failed to assign Crypto Officer role to test user: %w", err)
	}
	err = env.RBACManager.EnsureRole(ctx, keyVaultID, keyVaultAdminRoleDefinitionID, testUserPrincipalID)
	if err != nil {
		return fmt.Errorf("failed to assign Administrator role to test user: %w", err)
	}

	// RBAC propagation is handled by retry logic in the clients (403 errors are retried)

	return nil
}

// createKeyVaultKey creates a key in the Key Vault
func (env *Environment) createKeyVaultKey(ctx context.Context, keyVaultName, keyName string, cred *azidentity.DefaultAzureCredential) (*azkeys.KeyBundle, error) {
	// Add retry options for Key Vault operations that may encounter RBAC propagation delays
	// RBAC assignments can take time to propagate, resulting in 403 Forbidden errors
	// With 15 retries at 5 second intervals = 75 seconds total retry time
	keyClientOptions := &azkeys.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Retry: policy.RetryOptions{
				MaxRetries: 15,
				RetryDelay: time.Second * 5,
				StatusCodes: []int{
					http.StatusForbidden, // RBAC assignments haven't propagated to Key Vault yet
				},
			},
		},
	}

	keyClient, err := azkeys.NewClient(fmt.Sprintf("https://%s.vault.azure.net/", keyVaultName), cred, keyClientOptions)
	if err != nil {
		return nil, err
	}

	keyResp, err := keyClient.CreateKey(ctx, keyName, azkeys.CreateKeyParameters{
		Kty:     to.Ptr(azkeys.KeyTypeRSA),
		KeySize: to.Ptr(int32(2048)),
	}, nil)
	if err != nil {
		return nil, err
	}

	return &keyResp.KeyBundle, nil
}

// createDiskEncryptionSet creates a Disk Encryption Set
func (env *Environment) createDiskEncryptionSet(ctx context.Context, desName string, keyVault *armkeyvault.Vault, key *azkeys.KeyBundle) (*armcompute.DiskEncryptionSet, error) {
	des := armcompute.DiskEncryptionSet{
		Location: to.Ptr(env.Region),
		Identity: &armcompute.EncryptionSetIdentity{
			Type: to.Ptr(armcompute.DiskEncryptionSetIdentityTypeSystemAssigned),
		},
		Properties: &armcompute.EncryptionSetProperties{
			ActiveKey: &armcompute.KeyForDiskEncryptionSet{
				KeyURL: to.Ptr(string(*key.Key.KID)),
				SourceVault: &armcompute.SourceVault{
					ID: keyVault.ID,
				},
			},
		},
	}

	desPoller, err := env.diskEncryptionSetClient.BeginCreateOrUpdate(ctx, env.NodeResourceGroup, desName, des, nil)
	if err != nil {
		return nil, err
	}

	desResp, err := desPoller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}

	return &desResp.DiskEncryptionSet, nil
}

// assignDiskEncryptionSetRBAC assigns reader role to cluster identity for DES
func (env *Environment) assignDiskEncryptionSetRBAC(ctx context.Context, desID, karpenterIdentity string) error {
	readerRoleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", env.SubscriptionID, readerRole)

	err := env.RBACManager.EnsureRole(ctx, desID, readerRoleDefinitionID, karpenterIdentity)
	if err != nil {
		return fmt.Errorf("failed to assign RBAC role: %w", err)
	}
	// RBAC propagation is handled by retry logic in the clients
	return nil
}

// updateKeyVaultAccessForDES assigns RBAC roles to DES identity for Key Vault access
func (env *Environment) updateKeyVaultAccessForDES(ctx context.Context, keyVaultName string, desIdentity *armcompute.EncryptionSetIdentity) error {
	kvGet, err := env.keyVaultClient.Get(ctx, env.NodeResourceGroup, keyVaultName, nil)
	if err != nil {
		return err
	}

	cryptoServiceRoleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", env.SubscriptionID, cryptoServiceEncryptionUserRole)

	desIdentityPrincipalID := lo.FromPtr(desIdentity.PrincipalID)
	keyVaultScope := lo.FromPtr(kvGet.ID)

	// Assign the Key Vault Crypto Service Encryption User role to the DES managed identity
	// Using EnsureRoleWithPrincipalType to handle replication delays when creating DES identity
	err = env.RBACManager.EnsureRoleWithPrincipalType(ctx, keyVaultScope, cryptoServiceRoleDefinitionID, desIdentityPrincipalID, "ServicePrincipal")
	if err != nil {
		return fmt.Errorf("failed to assign Key Vault RBAC role to DES identity: %w", err)
	}
	// RBAC propagation is handled by retry logic in the clients

	return nil
}
