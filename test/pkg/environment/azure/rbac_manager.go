package azure

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/google/uuid"
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

// WaitForPrincipalAvailability polls Azure AD to ensure a principal exists before assigning roles
func (r *RBACManager) WaitForPrincipalAvailability(ctx context.Context, principalID string, maxWait time.Duration) error {
	fmt.Printf("Waiting for principal to be available in Azure AD...\n")

	start := time.Now()
	for {
		pager := r.client.NewListForSubscriptionPager(&armauthorization.RoleAssignmentsClientListForSubscriptionOptions{
			Filter: to.Ptr(fmt.Sprintf("assignedTo('%s')", principalID)),
		})

		_, err := pager.NextPage(ctx)
		if err == nil {
			fmt.Printf("Principal is now available in Azure AD (waited %v)\n", time.Since(start))
			// Small additional wait to ensure principal is fully ready for role assignment operations
			// There is still a bit of a delay after it shows up
			time.Sleep(10 * time.Second)
			return nil
		}

		if time.Since(start) >= maxWait {
			return fmt.Errorf("principal not available after %v: %w", maxWait, err)
		}

		time.Sleep(5 * time.Second)
	}
}

// EnsureRoleWithRetry assigns roleDefinitionID to principalID at scope with retry logic for principal availability
// In some cases, like when creating a Disk Encryption Set, we want to assign roles to an identity being created by an RP,
// and that identity may have not propagated yet
func (r *RBACManager) EnsureRoleWithRetry(ctx context.Context, scope, roleDefinitionID, principalID string, maxWait time.Duration) error {
	if err := r.WaitForPrincipalAvailability(ctx, principalID, maxWait); err != nil {
		return err
	}

	return r.EnsureRole(ctx, scope, roleDefinitionID, principalID)
}

// WaitForRoleAssignmentPropagation polls to verify that a role assignment has propagated and is effective
func (r *RBACManager) WaitForRoleAssignmentPropagation(ctx context.Context, scope, principalID string, maxWait time.Duration) error {
	fmt.Printf("Waiting for RBAC role assignment to propagate...\n")
	start := time.Now()
	for {
		pager := r.client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
			Filter: to.Ptr(fmt.Sprintf("assignedTo('%s')", principalID)),
		})

		page, err := pager.NextPage(ctx)
		if err == nil && len(page.Value) > 0 {
			fmt.Printf("RBAC role assignment propagated successfully (waited %v)\n", time.Since(start))
			// TODO: Remove additional wait after rbac role assignment propagating successfully
			// Key Vault RBAC propagation requies additional time for RBAC propagation beyond the ARM
			// Role Assignment Propagation
			time.Sleep(10 * time.Second)
			return nil
		}

		// Check if we've exceeded the max wait time
		if time.Since(start) >= maxWait {
			fmt.Printf("RBAC role assignment propagation timeout after %v, proceeding anyway\n", maxWait)
			return nil
		}

		time.Sleep(3 * time.Second)
	}
}
