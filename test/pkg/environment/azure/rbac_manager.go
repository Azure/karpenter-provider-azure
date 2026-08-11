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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/google/uuid"
	"github.com/samber/lo"
)

type RBACManager struct {
	subscriptionID string
	client         *armauthorization.RoleAssignmentsClient
	definitions    *armauthorization.RoleDefinitionsClient
}

// NewRBACManager builds a client with the provided TokenCredential.
func NewRBACManager(subscriptionID string, cred azcore.TokenCredential) (*RBACManager, error) {
	c, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}
	d, err := armauthorization.NewRoleDefinitionsClient(cred, nil)
	if err != nil {
		return nil, err
	}
	return &RBACManager{subscriptionID: subscriptionID, client: c, definitions: d}, nil
}

// EnsureCustomRole creates or updates a subscription-scoped custom role granting exactly
// actions, and returns its role definition ID. The definition ID is derived from roleName
// so repeated runs converge on one definition rather than accumulating them; it is left
// behind deliberately, since deleting it would break a concurrent run.
func (r *RBACManager) EnsureCustomRole(ctx context.Context, roleName string, actions []string) (string, error) {
	scope := fmt.Sprintf("/subscriptions/%s", r.subscriptionID)
	definitionID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(roleName)).String()

	resp, err := r.definitions.CreateOrUpdate(ctx, scope, definitionID, armauthorization.RoleDefinition{
		Properties: &armauthorization.RoleDefinitionProperties{
			RoleName:         lo.ToPtr(roleName),
			Description:      lo.ToPtr("Least-privilege role used by Karpenter E2E tests."),
			RoleType:         lo.ToPtr("CustomRole"),
			AssignableScopes: []*string{lo.ToPtr(scope)},
			Permissions: []*armauthorization.Permission{{
				Actions: lo.ToSlicePtr(actions),
			}},
		},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("creating custom role %q: %w", roleName, err)
	}
	return lo.FromPtr(resp.ID), nil
}

// EnsureRole assigns roleDefinitionID to principalID at scope if not already present.
// It lists for the scope and returns nil if a matching assignment exists.
func (r *RBACManager) EnsureRole(ctx context.Context, scope, roleDefinitionID, principalID string) error {
	return r.EnsureRoleWithPrincipalType(ctx, scope, roleDefinitionID, principalID, "")
}

// EnsureRoleWithPrincipalType assigns roleDefinitionID to principalID at scope with optional principalType.
// Setting principalType helps handle replication delays when creating principals and immediately assigning roles.
// See https://aka.ms/docs-principaltype for more information.
func (r *RBACManager) EnsureRoleWithPrincipalType(ctx context.Context, scope, roleDefinitionID, principalID, principalType string) error {
	// Quick scan to avoid duplicates
	pager := r.client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: lo.ToPtr(fmt.Sprintf("assignedTo('%s')", principalID)),
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
	properties := &armauthorization.RoleAssignmentProperties{
		PrincipalID:      lo.ToPtr(principalID),
		RoleDefinitionID: lo.ToPtr(roleDefinitionID),
	}

	if principalType != "" {
		properties.PrincipalType = lo.ToPtr(armauthorization.PrincipalType(principalType))
	}

	_, err := r.client.Create(ctx, scope, name, armauthorization.RoleAssignmentCreateParameters{
		Properties: properties,
	}, nil)
	return err
}
