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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
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
	readerRole            = "acdd72a7-3385-48ef-bd42-f606fba81ae7"
	keyVaultAdminRole     = "00482a5a-887f-4fb3-b363-3b7fe8e74483"
	keyVaultCryptoOfficer = "14b46e9e-c2b7-41b4-b07b-48a6ebf60603"
)

// CreateKeyVaultAndDiskEncryptionSet creates a key vault and disk encryption set for BYOK testing
func (env *Environment) CreateKeyVaultAndDiskEncryptionSet(ctx context.Context) string {
	keyVaultName := fmt.Sprintf("karpentertest%d", time.Now().Unix())
	keyName := "test-key"
	desName := fmt.Sprintf("karpenter-test-des-%d", time.Now().Unix())

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	Expect(err).ToNot(HaveOccurred())

	clusterIdentity := env.GetClusterIdentityPrincipalID(ctx)
	clusterTenant := env.GetTenantID(ctx)
	karpenterIdentity := env.getKarpenterWorkloadIdentity(ctx)

	fmt.Println(clusterIdentity, clusterTenant)

	keyVault, err := env.createKeyVault(ctx, keyVaultName, clusterTenant)
	Expect(err).ToNot(HaveOccurred())

	// Get the current test user's principal ID from the credential
	testUserPrincipalID := env.getCurrentUserPrincipalID(ctx, cred)

	err = env.assignKeyVaultRBAC(ctx, lo.FromPtr(keyVault.ID), karpenterIdentity, testUserPrincipalID)
	Expect(err).ToNot(HaveOccurred())

	key, err := env.createKeyVaultKey(ctx, keyVaultName, keyName, cred)
	Expect(err).ToNot(HaveOccurred())

	des, err := env.createDiskEncryptionSet(ctx, desName, keyVault, key)
	Expect(err).ToNot(HaveOccurred())

	desIdentity := des.Identity
	Expect(desIdentity).ToNot(BeNil())
	Expect(desIdentity.PrincipalID).ToNot(BeNil())

	// Wait for DES managed identity to be available in Azure AD before assigning roles
	fmt.Printf("Waiting for DES managed identity %s to be available in Azure AD...\n", lo.FromPtr(desIdentity.PrincipalID))
	time.Sleep(30 * time.Second)

	err = env.assignDiskEncryptionSetRBAC(ctx, lo.FromPtr(des.ID), karpenterIdentity, cred)
	Expect(err).ToNot(HaveOccurred())

	err = env.updateKeyVaultAccessForDES(ctx, keyVaultName, desIdentity, clusterTenant)
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
	env.RBACManager.EnsureRole(ctx, keyVaultID, cryptoOfficerRoleDefinitionID, karpenterIdentity)
	env.RBACManager.EnsureRole(ctx, keyVaultID, keyVaultAdminRoleDefinitionID, karpenterIdentity)

	// Also assign roles to test user (for test setup operations like creating keys)
	if testUserPrincipalID != "" {
		fmt.Printf("Assigning Key Vault roles to test user: %s\n", testUserPrincipalID)
		err := env.RBACManager.EnsureRole(ctx, keyVaultID, cryptoOfficerRoleDefinitionID, testUserPrincipalID)
		if err != nil {
			fmt.Printf("Failed to assign Crypto Officer role to test user: %v\n", err)
		}
		err = env.RBACManager.EnsureRole(ctx, keyVaultID, keyVaultAdminRoleDefinitionID, testUserPrincipalID)
		if err != nil {
			fmt.Printf("Failed to assign Administrator role to test user: %v\n", err)
		}
	} else {
		fmt.Printf("WARNING: testUserPrincipalID is empty, skipping test user RBAC assignments\n")
	}

	// Wait a bit for RBAC propagation
	time.Sleep(10 * time.Second)

	return nil
}

// createKeyVaultKey creates a key in the Key Vault
func (env *Environment) createKeyVaultKey(ctx context.Context, keyVaultName, keyName string, cred *azidentity.DefaultAzureCredential) (*azkeys.KeyBundle, error) {
	keyClient, err := azkeys.NewClient(fmt.Sprintf("https://%s.vault.azure.net/", keyVaultName), cred, nil)
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
func (env *Environment) assignDiskEncryptionSetRBAC(ctx context.Context, desID, clusterIdentity string, cred *azidentity.DefaultAzureCredential) error {
	readerRoleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", env.SubscriptionID, readerRole)

	fmt.Printf("Assigning Reader role to cluster identity %s for DES %s\n", clusterIdentity, desID)
	// Use the RBACManager for consistency with other RBAC operations
	err := env.RBACManager.EnsureRole(ctx, desID, readerRoleDefinitionID, clusterIdentity)
	if err != nil {
		return fmt.Errorf("failed to assign RBAC role: %w", err)
	}

	// Wait for RBAC propagation
	time.Sleep(10 * time.Second)

	return nil
}

// updateKeyVaultAccessForDES assigns RBAC roles to DES identity for Key Vault access
func (env *Environment) updateKeyVaultAccessForDES(ctx context.Context, keyVaultName string, desIdentity *armcompute.EncryptionSetIdentity, clusterTenant string) error {
	kvGet, err := env.keyVaultClient.Get(ctx, env.NodeResourceGroup, keyVaultName, nil)
	if err != nil {
		return err
	}

	// Since the Key Vault uses RBAC authorization, assign the Key Vault Crypto Service Encryption User role
	// This role provides get, wrapKey, and unwrapKey permissions needed for DES
	cryptoServiceEncryptionUserRole := "e147488a-f6f5-4113-8e2d-b22465e65bf6"
	cryptoServiceRoleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", env.SubscriptionID, cryptoServiceEncryptionUserRole)

	desIdentityPrincipalID := lo.FromPtr(desIdentity.PrincipalID)
	keyVaultScope := lo.FromPtr(kvGet.ID)

	fmt.Printf("Assigning Key Vault Crypto Service Encryption User role to DES identity %s for Key Vault %s\n", desIdentityPrincipalID, keyVaultScope)

	err = env.RBACManager.EnsureRole(ctx, keyVaultScope, cryptoServiceRoleDefinitionID, desIdentityPrincipalID)
	if err != nil {
		return fmt.Errorf("failed to assign Key Vault RBAC role: %w", err)
	}

	// Wait for RBAC propagation
	time.Sleep(10 * time.Second)

	return nil
}

// getCurrentUserPrincipalID gets the principal ID of the current authenticated user
func (env *Environment) getCurrentUserPrincipalID(ctx context.Context, cred *azidentity.DefaultAzureCredential) string {
	// Get a token to extract the principal ID from
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		fmt.Printf("Warning: Could not get token to determine current user: %v\n", err)
		return ""
	}

	// Parse the JWT token to extract the oid (object ID) claim
	// JWT tokens have three parts separated by dots: header.payload.signature
	parts := strings.Split(token.Token, ".")
	if len(parts) != 3 {
		fmt.Printf("Warning: Invalid token format\n")
		return ""
	}

	// Decode the payload (second part)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		fmt.Printf("Warning: Could not decode token payload: %v\n", err)
		return ""
	}

	// Parse the JSON payload
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		fmt.Printf("Warning: Could not parse token claims: %v\n", err)
		return ""
	}

	// Extract the oid (object ID) claim which is the principal ID
	if oid, ok := claims["oid"].(string); ok {
		fmt.Printf("Current test user principal ID: %s\n", oid)
		return oid
	}

	fmt.Printf("Warning: Could not find oid claim in token\n")
	return ""
}
