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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

func (env *Environment) GetClusterIdentity(ctx context.Context) *containerservice.ManagedClusterIdentity {
	cluster, err := env.managedClusterClient.Get(ctx, env.ClusterResourceGroup, env.ClusterName, nil)
	Expect(err).ToNot(HaveOccurred())
	Expect(cluster.Identity).ToNot(BeNil())
	return cluster.Identity
}

func (env *Environment) GetKarpenterWorkloadIdentity(ctx context.Context) string {
	karpenterMSIName := "karpentermsi" // matches AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME
	msiClient, err := armmsi.NewUserAssignedIdentitiesClient(env.SubscriptionID, env.GetDefaultCredential(), nil)
	Expect(err).ToNot(HaveOccurred())

	identity, err := msiClient.Get(ctx, env.ClusterResourceGroup, karpenterMSIName, nil)
	Expect(err).ToNot(HaveOccurred())
	return lo.FromPtr(identity.Properties.PrincipalID)
}

// getCurrentUserPrincipalID gets the principal ID of the current authenticated identity
func (env *Environment) GetCurrentUserPrincipalID(ctx context.Context, cred azcore.TokenCredential) string {
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	Expect(err).ToNot(HaveOccurred(), "failed to get token from Azure credential")

	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	parsedToken, _, err := parser.ParseUnverified(token.Token, jwt.MapClaims{})
	Expect(err).ToNot(HaveOccurred(), "failed to parse JWT token")

	claims, ok := parsedToken.Claims.(jwt.MapClaims)
	Expect(ok).To(BeTrue(), "failed to extract claims from JWT token")

	oid, ok := claims["oid"].(string)
	Expect(ok).To(BeTrue(), "oid claim not found or not a string in JWT token")
	Expect(oid).ToNot(BeEmpty(), "oid claim is empty")

	return oid
}

// ExpectCreatedManagedIdentity creates a new user-assigned managed identity
func (env *Environment) ExpectCreatedManagedIdentity(ctx context.Context, identityName string) *armmsi.Identity {
	GinkgoHelper()
	msiClient, err := armmsi.NewUserAssignedIdentitiesClient(env.SubscriptionID, env.GetDefaultCredential(), nil)
	Expect(err).ToNot(HaveOccurred())

	By(fmt.Sprintf("creating managed identity %s in node resource group %s", identityName, env.NodeResourceGroup))

	identity := armmsi.Identity{
		Location: to.Ptr(env.Region),
		Tags: map[string]*string{
			"test": to.Ptr("karpenter-e2e"),
		},
	}

	resp, err := msiClient.CreateOrUpdate(ctx, env.NodeResourceGroup, identityName, identity, nil)
	Expect(err).ToNot(HaveOccurred())

	// Note: we don't register for cleanup in the env.tracker, in case there are more tests to run. We don't want to break the cluster by deleting the kubelet identity
	return &resp.Identity
}

// ExpectUpdatedManagedClusterKubeletIdentityAsync updates the kubelet identity of a managed cluster asynchronously
// Returns the poller so the caller can control when to wait for completion
func (env *Environment) ExpectUpdatedManagedClusterKubeletIdentityAsync(ctx context.Context, newIdentity *armmsi.Identity) *runtime.Poller[containerservice.ManagedClustersClientCreateOrUpdateResponse] {
	GinkgoHelper()

	By("getting current managed cluster configuration")
	mc := env.ExpectGetManagedCluster()

	By("updating kubelet identity")

	// Update the kubelet identity in the identity profile
	if mc.Properties.IdentityProfile == nil {
		mc.Properties.IdentityProfile = make(map[string]*containerservice.UserAssignedIdentity)
	}

	mc.Properties.IdentityProfile["kubeletidentity"] = &containerservice.UserAssignedIdentity{
		ClientID:   newIdentity.Properties.ClientID,
		ObjectID:   newIdentity.Properties.PrincipalID,
		ResourceID: newIdentity.ID,
	}

	// Update the cluster and return the poller
	poller, err := env.managedClusterClient.BeginCreateOrUpdate(ctx, env.ClusterResourceGroup, env.ClusterName, *mc, nil)
	Expect(err).ToNot(HaveOccurred())

	return poller
}

// ExpectGrantedACRAccess grants the specified identity access to pull from the ACR
func (env *Environment) ExpectGrantedACRAccess(ctx context.Context, identity *armmsi.Identity) {
	GinkgoHelper()

	By("granting ACR pull access to identity")

	// Get the ACR resource ID
	acrResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerRegistry/registries/%s",
		env.SubscriptionID, env.ClusterResourceGroup, env.ACRName)

	// AcrPull role definition ID: 7f951dda-4ed3-4680-a7ca-43fe172d538d
	acrPullRoleID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/7f951dda-4ed3-4680-a7ca-43fe172d538d", env.SubscriptionID)

	identityPrincipalID := lo.FromPtr(identity.Properties.PrincipalID)

	err := env.RBACManager.EnsureRoleWithPrincipalType(ctx, acrResourceID, acrPullRoleID, identityPrincipalID, "ServicePrincipal")
	Expect(err).ToNot(HaveOccurred())
}

// CheckClusterIdentityType returns the type of managed identity used by the cluster
func (env *Environment) CheckClusterIdentityType(ctx context.Context) string {
	mc := env.ExpectGetManagedCluster()
	if mc.Identity == nil {
		return "none"
	}
	if mc.Identity.Type == nil {
		return "unknown"
	}
	return string(*mc.Identity.Type)
}

// IsClusterUserAssignedIdentity checks if the cluster uses user-assigned managed identity
func (env *Environment) IsClusterUserAssignedIdentity(ctx context.Context) bool {
	identityType := env.CheckClusterIdentityType(ctx)
	return identityType == "UserAssigned"
}

// GetKubeletIdentity returns the current kubelet identity
func (env *Environment) GetKubeletIdentity(ctx context.Context) *containerservice.UserAssignedIdentity {
	mc := env.ExpectGetManagedCluster()
	Expect(mc.Properties.IdentityProfile).ToNot(BeNil())
	Expect(mc.Properties.IdentityProfile["kubeletidentity"]).ToNot(BeNil())
	return mc.Properties.IdentityProfile["kubeletidentity"]
}
