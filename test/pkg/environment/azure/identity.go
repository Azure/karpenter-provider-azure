package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/google/uuid"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

type RBACManager struct {
	subscriptionID string
	client         *armauthorization.RoleAssignmentsClient
}

// NewRBACManager builds a client with DefaultAzureCredential.
func NewRBACManager(subscriptionID string) (*RBACManager, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	c, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}
	return &RBACManager{subscriptionID: subscriptionID, client: c}, nil
}

// EnsureRole assigns roleDefinitionID to principalID at scope if not already present.
// It lists for the scope and returns nil if a matching assignment exists.
func (r *RBACManager) EnsureRole(ctx context.Context, scope, roleDefinitionID, principalID string) error {
	// Quick scan to avoid duplicates
	pager := r.client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: to.Ptr(fmt.Sprintf("assignedTo('%s')", principalID)),
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, ra := range page.Value {
			if ra.Properties != nil &&
				ra.Properties.PrincipalID != nil &&
				ra.Properties.RoleDefinitionID != nil &&
				*ra.Properties.PrincipalID == principalID &&
				*ra.Properties.RoleDefinitionID == roleDefinitionID {
				// Already assigned
				return nil
			}
		}
	}
	name := uuid.New().String()
	_, err := r.client.Create(ctx, scope, name, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID:      to.Ptr(principalID),
			RoleDefinitionID: to.Ptr(roleDefinitionID),
		},
	}, nil)
	return err
}

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

func (env *Environment) getKarpenterWorkloadIdentity(ctx context.Context) string {
	// Get the Karpenter User Assigned Identity principal ID
	// This matches the logic in Makefile-az.mk line 165
	karpenterMSIName := "karpentermsi" // matches AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	Expect(err).ToNot(HaveOccurred())

	msiClient, err := armmsi.NewUserAssignedIdentitiesClient(env.SubscriptionID, cred, nil)
	Expect(err).ToNot(HaveOccurred())

	identity, err := msiClient.Get(ctx, env.ClusterResourceGroup, karpenterMSIName, nil)
	Expect(err).ToNot(HaveOccurred())

	principalID := lo.FromPtr(identity.Properties.PrincipalID)
	fmt.Printf("Karpenter workload identity principal ID: %s\n", principalID)
	return principalID
}
