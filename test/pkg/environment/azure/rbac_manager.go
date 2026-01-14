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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/google/uuid"
)

// ReaderRoleID is the Azure built-in Reader role definition ID
const ReaderRoleID = "acdd72a7-3385-48ef-bd42-f606fba81ae7"

type RBACManager struct {
	subscriptionID string
	client         *armauthorization.RoleAssignmentsClient
}

// NewRBACManager builds a client with the provided TokenCredential.
func NewRBACManager(subscriptionID string, cred azcore.TokenCredential) (*RBACManager, error) {
	c, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}
	return &RBACManager{subscriptionID: subscriptionID, client: c}, nil
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
	properties := &armauthorization.RoleAssignmentProperties{
		PrincipalID:      to.Ptr(principalID),
		RoleDefinitionID: to.Ptr(roleDefinitionID),
	}

	if principalType != "" {
		properties.PrincipalType = to.Ptr(armauthorization.PrincipalType(principalType))
	}

	_, err := r.client.Create(ctx, scope, name, armauthorization.RoleAssignmentCreateParameters{
		Properties: properties,
	}, nil)
	return err
}

// HasRole checks if principalID has roleDefinitionID at scope.
func (r *RBACManager) HasRole(ctx context.Context, scope, principalID, roleDefinitionID string) bool {
	pager := r.client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: to.Ptr(fmt.Sprintf("assignedTo('%s')", principalID)),
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false
		}
		for _, ra := range page.Value {
			if ra.Properties != nil &&
				ra.Properties.PrincipalID != nil &&
				ra.Properties.RoleDefinitionID != nil &&
				*ra.Properties.PrincipalID == principalID &&
				*ra.Properties.RoleDefinitionID == roleDefinitionID {
				return true
			}
		}
	}
	return false
}

// RemoveRole removes all role assignments of principalID at scope.
func (r *RBACManager) RemoveRole(ctx context.Context, scope, principalID string) error {
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
				*ra.Properties.PrincipalID == principalID &&
				ra.Name != nil {
				// Delete this role assignment
				_, err := r.client.Delete(ctx, scope, *ra.Name, nil)
				if err != nil {
					return fmt.Errorf("failed to delete role assignment %s: %w", *ra.Name, err)
				}
			}
		}
	}
	return nil
}
