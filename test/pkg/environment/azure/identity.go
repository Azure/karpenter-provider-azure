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
	"strings"

	"encoding/base64"
	"encoding/json"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/samber/lo"

	. "github.com/onsi/gomega"
)

func (env *Environment) GetTenantID(ctx context.Context) string {
	cluster, err := env.managedClusterClient.Get(ctx, env.ClusterResourceGroup, env.ClusterName, nil)
	Expect(err).ToNot(HaveOccurred())
	Expect(cluster.Identity).ToNot(BeNil())
	Expect(cluster.Identity.PrincipalID).ToNot(BeNil())
	return lo.FromPtr(cluster.Identity.TenantID)
}

func (env *Environment) GetClusterIdentityPrincipalID(ctx context.Context) string {
	cluster, err := env.managedClusterClient.Get(ctx, env.ClusterResourceGroup, env.ClusterName, nil)
	Expect(err).ToNot(HaveOccurred())
	Expect(cluster.Identity).ToNot(BeNil())
	Expect(cluster.Identity.PrincipalID).ToNot(BeNil())
	return lo.FromPtr(cluster.Identity.PrincipalID)
}

func (env *Environment) GetKarpenterWorkloadIdentity(ctx context.Context) string {
	karpenterMSIName := "karpentermsi" // matches AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	Expect(err).ToNot(HaveOccurred())

	msiClient, err := armmsi.NewUserAssignedIdentitiesClient(env.SubscriptionID, cred, nil)
	Expect(err).ToNot(HaveOccurred())

	identity, err := msiClient.Get(ctx, env.ClusterResourceGroup, karpenterMSIName, nil)
	Expect(err).ToNot(HaveOccurred())

	return lo.FromPtr(identity.Properties.PrincipalID)
}

// getCurrentUserPrincipalID gets the principal ID of the current authenticated user
func (env *Environment) GetCurrentUserPrincipalID(ctx context.Context, cred *azidentity.DefaultAzureCredential) string {
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

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		fmt.Printf("Warning: Could not decode token payload: %v\n", err)
		return ""
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		fmt.Printf("Warning: Could not parse token claims: %v\n", err)
		return ""
	}

	// Extract the oid (object ID) claim which is the principal ID
	if oid, ok := claims["oid"].(string); ok {
		return oid
	}

	fmt.Printf("Warning: Could not find oid claim in token\n")
	return ""
}
