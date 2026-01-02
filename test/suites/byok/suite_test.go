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

package byok_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/samber/lo"

	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *azure.Environment

func TestBYOK(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	AfterSuite(func() {
		env.Stop()
	})

	RunSpecs(t, "BYOK Suite")
}

var _ = BeforeEach(func() { env.BeforeEach() })
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("BYOK", func() {
	BeforeEach(func() {

	})
	FIt("should provision a VM with customer-managed key disk encryption", func() {
		ctx := context.Background()
		var diskEncryptionSetID string
		// If not InClusterController, assume the test setup will include the creation of the KV, KV-Key + DES
		if env.InClusterController {
			diskEncryptionSetID = CreateKeyVaultAndDiskEncryptionSet(ctx, env)
			env.ExpectSettingsOverridden(corev1.EnvVar{Name: "NODE_OSDISK_DISKENCRYPTIONSET_ID", Value: diskEncryptionSetID})
		}

		nodeClass := env.DefaultAKSNodeClass()
		nodePool := env.DefaultNodePool(nodeClass)

		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.ExpectCreatedNodeCount("==", 1)
		env.EventuallyExpectHealthy(pod)

		vm := env.GetVM(pod.Spec.NodeName)
		Expect(vm.Properties).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).ToNot(BeNil())
		if env.InClusterController {
			Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID)).To(Equal(diskEncryptionSetID))
		}
	})

	It("should provision a VM with ephemeral OS disk and customer-managed key disk encryption", func() {
		ctx := context.Background()
		var diskEncryptionSetID string
		// If not InClusterController, assume the test setup will include the creation of the KV, KV-Key + DES
		if env.InClusterController {
			diskEncryptionSetID = CreateKeyVaultAndDiskEncryptionSet(ctx, env)
			env.ExpectSettingsOverridden(corev1.EnvVar{Name: "NODE_OSDISK_DISKENCRYPTIONSET_ID", Value: diskEncryptionSetID})
		}

		nodeClass := env.DefaultAKSNodeClass()
		nodePool := env.DefaultNodePool(nodeClass)

		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
				Operator: corev1.NodeSelectorOpGt,
				Values:   []string{"50"},
			}})

		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](50)

		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthyWithTimeout(pod, time.Minute*15)
		env.ExpectCreatedNodeCount("==", 1)

		vm := env.GetVM(pod.Spec.NodeName)
		Expect(vm.Properties).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())

		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option).ToNot(BeNil())
		Expect(string(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option))).To(Equal("Local"))

		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).ToNot(BeNil())
		if env.InClusterController {
			Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID)).To(Equal(diskEncryptionSetID))
		}
	})
})

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
func CreateKeyVaultAndDiskEncryptionSet(ctx context.Context, env *azure.Environment) string {
	keyVaultName := fmt.Sprintf("karpentertest%d", time.Now().Unix())
	keyName := "test-key"
	desName := fmt.Sprintf("karpenter-test-des-%d", time.Now().Unix())

	cred := env.GetDefaultCredential()

	clusterIdentity := env.GetClusterIdentity(ctx)
	clusterTenant := lo.FromPtr(clusterIdentity.TenantID)
	Expect(clusterTenant).ToNot(BeEmpty())

	karpenterIdentity := env.GetKarpenterWorkloadIdentity(ctx)

	keyVault, err := createKeyVault(ctx, env, keyVaultName, clusterTenant)
	Expect(err).ToNot(HaveOccurred())

	// Get the current test user's principal ID from the defaultCredential Token
	testUserPrincipalID := env.GetCurrentUserPrincipalID(ctx, cred)
	Expect(testUserPrincipalID).ToNot(BeEmpty(), "test user authentication failed")

	err = assignKeyVaultRBAC(ctx, env, lo.FromPtr(keyVault.ID), karpenterIdentity, testUserPrincipalID)
	Expect(err).ToNot(HaveOccurred())

	key, err := createKeyVaultKey(ctx, keyVaultName, keyName, cred)
	Expect(err).ToNot(HaveOccurred())

	des, err := createDiskEncryptionSet(ctx, env, desName, keyVault, key)
	Expect(err).ToNot(HaveOccurred())

	desIdentity := des.Identity
	Expect(desIdentity).ToNot(BeNil())
	Expect(desIdentity.PrincipalID).ToNot(BeNil())

	err = assignDiskEncryptionSetRBAC(ctx, env, lo.FromPtr(des.ID), karpenterIdentity)
	Expect(err).ToNot(HaveOccurred())

	err = updateKeyVaultAccessForDES(ctx, env, keyVaultName, desIdentity)
	Expect(err).ToNot(HaveOccurred())

	return lo.FromPtr(des.ID)
}

// createKeyVault creates an Azure Key Vault
func createKeyVault(ctx context.Context, env *azure.Environment, keyVaultName, clusterTenant string) (*armkeyvault.Vault, error) {
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

	poller, err := env.KeyVaultClient.BeginCreateOrUpdate(ctx, env.NodeResourceGroup, keyVaultName, armkeyvault.VaultCreateOrUpdateParameters{
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
func assignKeyVaultRBAC(ctx context.Context, env *azure.Environment, keyVaultID, karpenterIdentity, testUserPrincipalID string) error {
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
func createKeyVaultKey(ctx context.Context, keyVaultName, keyName string, cred azcore.TokenCredential) (*azkeys.KeyBundle, error) {
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
func createDiskEncryptionSet(ctx context.Context, env *azure.Environment, desName string, keyVault *armkeyvault.Vault, key *azkeys.KeyBundle) (*armcompute.DiskEncryptionSet, error) {
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

	desPoller, err := env.DiskEncryptionSetClient.BeginCreateOrUpdate(ctx, env.NodeResourceGroup, desName, des, nil)
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
func assignDiskEncryptionSetRBAC(ctx context.Context, env *azure.Environment, desID, karpenterIdentity string) error {
	readerRoleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", env.SubscriptionID, readerRole)

	err := env.RBACManager.EnsureRole(ctx, desID, readerRoleDefinitionID, karpenterIdentity)
	if err != nil {
		return fmt.Errorf("failed to assign RBAC role: %w", err)
	}
	// RBAC propagation is handled by retry logic in the clients
	return nil
}

// updateKeyVaultAccessForDES assigns RBAC roles to DES identity for Key Vault access
func updateKeyVaultAccessForDES(ctx context.Context, env *azure.Environment, keyVaultName string, desIdentity *armcompute.EncryptionSetIdentity) error {
	kvGet, err := env.KeyVaultClient.Get(ctx, env.NodeResourceGroup, keyVaultName, nil)
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
