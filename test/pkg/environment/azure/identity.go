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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/golang-jwt/jwt/v5"
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
