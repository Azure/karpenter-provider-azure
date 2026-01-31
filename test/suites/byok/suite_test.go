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
	"github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	nodeclassstatus "github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
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
	It("should provision a VM with customer-managed key disk encryption", Label("runner"), func() {
		ctx := context.Background()
		var diskEncryptionSetID string

		By("Phase 1: Setting up DES (Disk Encryption Set)")
		if env.InClusterController {
			diskEncryptionSetID = createKeyVaultAndDiskEncryptionSet(ctx, env)
			env.ExpectSettingsOverridden(corev1.EnvVar{Name: "NODE_OSDISK_DISKENCRYPTIONSET_ID", Value: diskEncryptionSetID})
		}

		By("Phase 2: Creating NodeClass and NodePool")
		nodeClass := env.DefaultAKSNodeClass()
		nodePool := env.DefaultNodePool(nodeClass)

		By("Phase 3: Creating test Pod")
		pod := test.Pod()

		By("Applying resources to Kubernetes")
		env.ExpectCreated(nodeClass, nodePool, pod)

		By("Phase 4: Verifying AKSNodeClass status shows validation success and is Ready")
		Eventually(func(g Gomega) {
			retrieved := &v1beta1.AKSNodeClass{}
			err := env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), retrieved)
			g.Expect(err).ToNot(HaveOccurred())
			condition := retrieved.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			g.Expect(condition.IsTrue()).To(BeTrue())
			readyCondition := retrieved.StatusConditions().Get(status.ConditionReady)
			g.Expect(readyCondition.IsTrue()).To(BeTrue())
		}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second)

		By("Phase 5: Waiting for VM to be created and node to be registered")
		env.EventuallyExpectCreatedNodeCount("==", 1)

		By("Phase 6: Verifying Pod becomes healthy")
		env.EventuallyExpectHealthy(pod)

		By("Phase 7: Verifying VM disk encryption configuration")
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

	It("should provision a VM with ephemeral OS disk and customer-managed key disk encryption", Label("runner"), func() {
		ctx := context.Background()
		var diskEncryptionSetID string

		By("Phase 1: Setting up DES (Disk Encryption Set)")
		// If not InClusterController, assume the test setup will include the creation of the KV, KV-Key + DES
		if env.InClusterController {
			diskEncryptionSetID = createKeyVaultAndDiskEncryptionSet(ctx, env)
			env.ExpectSettingsOverridden(corev1.EnvVar{Name: "NODE_OSDISK_DISKENCRYPTIONSET_ID", Value: diskEncryptionSetID})
		}

		By("Phase 2: Creating NodeClass with ephemeral disk configuration")
		nodeClass := env.DefaultAKSNodeClass()
		nodePool := env.DefaultNodePool(nodeClass)

		By("Phase 3: Configuring ephemeral OS disk requirement")
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
				Operator: corev1.NodeSelectorOpGt,
				Values:   []string{"50"},
			}})
		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](50)

		By("Phase 4: Creating test Pod")
		pod := test.Pod()

		By("Applying resources to Kubernetes")
		env.ExpectCreated(nodeClass, nodePool, pod)

		By("Phase 5: Waiting for VM to be created and node to be registered")
		env.EventuallyExpectCreatedNodeCount("==", 1)

		By("Phase 6: Verifying Pod becomes healthy")
		env.EventuallyExpectHealthy(pod)

		By("Phase 7: Verifying VM disk configuration")
		vm := env.GetVM(pod.Spec.NodeName)
		Expect(vm.Properties).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())

		By("Phase 8: Verifying ephemeral OS disk settings")
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option).ToNot(BeNil())
		Expect(string(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option))).To(Equal("Local"))

		By("Phase 9: Verifying DES is configured on managed disk")
		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).ToNot(BeNil())
		if env.InClusterController {
			Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID)).To(Equal(diskEncryptionSetID))
		}
	})

	It("should mark AKSNodeClass as NotReady when DES ID is set but Reader RBAC is missing", func() {
		if !env.InClusterController {
			Skip("This test requires InClusterController mode to configure DES environment variable")
		}

		ctx := context.Background()

		By("Phase 1: Creating a DES WITHOUT Reader RBAC for the controlling identity")
		// Create a second DES but intentionally skip assigning the Reader role
		// This simulates the scenario where a user configures DES but forgets to grant permissions
		diskEncryptionSetWithoutRBAC := createKeyVaultAndDiskEncryptionSetWithoutReaderRBAC(ctx, env)

		By("Phase 2: Configuring Karpenter to use the DES without RBAC")
		// The validation should detect this and mark the NodeClass as NotReady
		env.ExpectSettingsOverridden(corev1.EnvVar{Name: "NODE_OSDISK_DISKENCRYPTIONSET_ID", Value: diskEncryptionSetWithoutRBAC})

		By("Phase 3: Creating a NodeClass and NodePool")
		nodeClass := env.DefaultAKSNodeClass()
		nodePool := env.DefaultNodePool(nodeClass)

		By("Phase 4: Applying NodeClass and NodePool to cluster")
		env.ExpectCreated(nodeClass, nodePool)

		By("Phase 5: Verifying AKSNodeClass status shows RBAC validation failure")
		Eventually(func(g Gomega) {
			retrieved := &v1beta1.AKSNodeClass{}
			err := env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), retrieved)
			g.Expect(err).ToNot(HaveOccurred())
			condition := retrieved.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			g.Expect(condition.IsFalse()).To(BeTrue(), "expected ValidationSucceeded to be False when DES Reader RBAC is missing")
			g.Expect(condition.Message).To(ContainSubstring(nodeclassstatus.DESRBACErrorMessage))
		}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second)

		By("Phase 6: Verifying the overall Ready condition is False")
		Eventually(func(g Gomega) {
			retrieved := &v1beta1.AKSNodeClass{}
			err := env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), retrieved)
			g.Expect(err).ToNot(HaveOccurred())
			readyCondition := retrieved.StatusConditions().Get(status.ConditionReady)
			g.Expect(readyCondition.IsFalse()).To(BeTrue(), "expected AKSNodeClass to be NotReady when DES Reader role is not assigned")
		}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second)

		By("Phase 7: Verifying no VMs or nodes are created despite NodePool being present")
		// Verify no new nodes are created when validation fails
		env.EventuallyExpectCreatedNodeCount("==", 0)
	})
})

const (
	cryptoServiceEncryptionUserRole = "e147488a-f6f5-4113-8e2d-b22465e65bf6"
	readerRole                      = "acdd72a7-3385-48ef-bd42-f606fba81ae7"
	keyVaultAdminRole               = "00482a5a-887f-4fb3-b363-3b7fe8e74483"
	keyVaultCryptoOfficer           = "14b46e9e-c2b7-41b4-b07b-48a6ebf60603"
)

// createKeyVaultAndDiskEncryptionSet creates a key vault and disk encryption set for BYOK testing
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
func createKeyVaultAndDiskEncryptionSet(ctx context.Context, env *azure.Environment) string {
	keyVaultName := fmt.Sprintf("karpentertest%d", time.Now().Unix())
	keyName := "test-key"
	desName := fmt.Sprintf("karpenter-test-des-%d", time.Now().Unix())

	keyVaultID, desID, desPrincipalID, karpenterIdentity := createKeyVaultDESResources(ctx, env, keyVaultName, keyName, desName)

	// Assign Reader role on DES to Karpenter identity (so it can create VMs with this DES)
	err := assignDiskEncryptionSetRBAC(ctx, env, desID, karpenterIdentity)
	Expect(err).ToNot(HaveOccurred())

	// Assign Key Vault Crypto Service Encryption User role to DES identity
	err = assignKeyVaultAccessForDES(ctx, env, keyVaultID, desPrincipalID)
	Expect(err).ToNot(HaveOccurred())

	return desID
}

// createKeyVaultAndDiskEncryptionSetWithoutReaderRBAC creates a DES without assigning Reader role
// to the Karpenter workload identity. This is used to test the validation logic that detects missing RBAC.
//
// Note: This function is only used in Test 3, which runs in InClusterController mode only.
// The function creates all necessary resources (Key Vault, key, DES) and assigns all roles
// EXCEPT the Reader role on the DES to the Karpenter workload identity.
// This simulates a misconfiguration where the user forgets to grant the necessary permissions.
func createKeyVaultAndDiskEncryptionSetWithoutReaderRBAC(ctx context.Context, env *azure.Environment) string {
	keyVaultName := fmt.Sprintf("karpentertest%d", time.Now().Unix())
	keyName := "test-key-no-rbac"
	desName := fmt.Sprintf("karpenter-test-des-no-rbac-%d", time.Now().Unix())

	keyVaultID, desID, desPrincipalID, _ := createKeyVaultDESResources(ctx, env, keyVaultName, keyName, desName)

	// INTENTIONALLY SKIP assignDiskEncryptionSetRBAC - this is the missing permission we're testing

	// Still assign Key Vault access to the DES identity so the DES itself can work
	// The issue is only that Karpenter can't READ the DES resource
	err := assignKeyVaultAccessForDES(ctx, env, keyVaultID, desPrincipalID)
	Expect(err).ToNot(HaveOccurred())

	return desID
}

// createKeyVaultDESResources creates the Key Vault, Key, and DES resources
// Returns the resource IDs and identities needed for RBAC assignment
func createKeyVaultDESResources(ctx context.Context, env *azure.Environment, keyVaultName, keyName, desName string) (keyVaultID, desID, desPrincipalID, karpenterIdentity string) {
	cred := env.GetDefaultCredential()

	clusterIdentity := env.GetClusterIdentity(ctx)
	clusterTenant := lo.FromPtr(clusterIdentity.TenantID)
	Expect(clusterTenant).ToNot(BeEmpty())

	karpenterIdentity = env.GetKarpenterWorkloadIdentity(ctx)

	keyVault, err := createKeyVault(ctx, env, keyVaultName, clusterTenant)
	Expect(err).ToNot(HaveOccurred())
	keyVaultID = lo.FromPtr(keyVault.ID)

	// Get the current test user's principal ID from the defaultCredential Token
	testUserPrincipalID := env.GetCurrentUserPrincipalID(ctx, cred)
	Expect(testUserPrincipalID).ToNot(BeEmpty(), "test user authentication failed")

	err = assignKeyVaultRBAC(ctx, env, keyVaultID, karpenterIdentity, testUserPrincipalID)
	Expect(err).ToNot(HaveOccurred())

	key, err := createKeyVaultKey(ctx, keyVaultName, keyName, cred)
	Expect(err).ToNot(HaveOccurred())

	des, err := createDiskEncryptionSet(ctx, env, desName, keyVault, key)
	Expect(err).ToNot(HaveOccurred())

	desIdentity := des.Identity
	Expect(desIdentity).ToNot(BeNil())
	Expect(desIdentity.PrincipalID).ToNot(BeNil())

	desID = lo.FromPtr(des.ID)
	desPrincipalID = lo.FromPtr(desIdentity.PrincipalID)

	return keyVaultID, desID, desPrincipalID, karpenterIdentity
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

// assignKeyVaultRBAC assigns necessary RBAC roles for Key Vault access to both
// the Karpenter workload identity and test user identity
func assignKeyVaultRBAC(ctx context.Context, env *azure.Environment, keyVaultID, karpenterIdentity, testUserPrincipalID string) error {
	cryptoOfficerRoleID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", env.SubscriptionID, keyVaultCryptoOfficer)
	adminRoleID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", env.SubscriptionID, keyVaultAdminRole)

	// Assign roles to both identities
	for _, identity := range []string{karpenterIdentity, testUserPrincipalID} {
		if err := env.RBACManager.EnsureRole(ctx, keyVaultID, cryptoOfficerRoleID, identity); err != nil {
			return fmt.Errorf("failed to assign Crypto Officer role: %w", err)
		}
		if err := env.RBACManager.EnsureRole(ctx, keyVaultID, adminRoleID, identity); err != nil {
			return fmt.Errorf("failed to assign Administrator role: %w", err)
		}
	}

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

// assignDiskEncryptionSetRBAC assigns Reader role on the DES to the controlling identity
func assignDiskEncryptionSetRBAC(ctx context.Context, env *azure.Environment, desID, controllingIdentityPrincipalID string) error {
	readerRoleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", env.SubscriptionID, readerRole)
	return env.RBACManager.EnsureRole(ctx, desID, readerRoleDefinitionID, controllingIdentityPrincipalID)
}

// assignKeyVaultAccessForDES assigns Crypto Service Encryption User role on Key Vault to DES identity
func assignKeyVaultAccessForDES(ctx context.Context, env *azure.Environment, keyVaultID, desPrincipalID string) error {
	cryptoServiceRoleID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", env.SubscriptionID, cryptoServiceEncryptionUserRole)
	return env.RBACManager.EnsureRoleWithPrincipalType(ctx, keyVaultID, cryptoServiceRoleID, desPrincipalID, "ServicePrincipal")
}
