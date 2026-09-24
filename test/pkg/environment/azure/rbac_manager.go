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
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/google/uuid"
	"github.com/samber/lo"
)

type RBACManager struct {
	client *armauthorization.RoleAssignmentsClient
}

// NewRBACManager builds a client with the provided TokenCredential.
func NewRBACManager(subscriptionID string, cred azcore.TokenCredential) (*RBACManager, error) {
	c, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}
	return &RBACManager{client: c}, nil
}

// EnsureRole assigns roleDefinitionID to principalID at scope if not already present.
// It lists for the scope and returns nil if a matching assignment exists.
func (r *RBACManager) EnsureRole(ctx context.Context, scope, roleDefinitionID, principalID string) error {
	_, err := r.EnsureRoleReportingCreate(ctx, scope, roleDefinitionID, principalID, "")
	return err
}

// EnsureRoleWithPrincipalType assigns roleDefinitionID to principalID at scope with optional principalType.
// Setting principalType helps handle replication delays when creating principals and immediately assigning roles.
// See https://aka.ms/docs-principaltype for more information.
func (r *RBACManager) EnsureRoleWithPrincipalType(ctx context.Context, scope, roleDefinitionID, principalID, principalType string) error {
	_, err := r.EnsureRoleReportingCreate(ctx, scope, roleDefinitionID, principalID, principalType)
	return err
}

// EnsureRoleReportingCreate is EnsureRoleWithPrincipalType, additionally reporting the ID
// of the assignment it created. The ID is empty when a matching assignment already
// existed, so a caller that cleans up removes only what it introduced and leaves a
// standing grant alone.
func (r *RBACManager) EnsureRoleReportingCreate(ctx context.Context, scope, roleDefinitionID, principalID, principalType string) (string, error) {
	// Quick scan to avoid duplicates
	pager := r.client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: lo.ToPtr(fmt.Sprintf("assignedTo('%s')", principalID)),
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", err
		}
		for _, ra := range page.Value {
			if ra.Properties != nil &&
				ra.Properties.PrincipalID != nil &&
				ra.Properties.RoleDefinitionID != nil &&
				*ra.Properties.PrincipalID == principalID &&
				*ra.Properties.RoleDefinitionID == roleDefinitionID {
				// Already assigned
				return "", nil
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

	created, err := r.client.Create(ctx, scope, name, armauthorization.RoleAssignmentCreateParameters{
		Properties: properties,
	}, nil)
	if err != nil {
		return "", err
	}
	return lo.FromPtr(created.ID), nil
}

// DeleteRoleAssignment removes an assignment by ID. One that is already gone is success:
// deleting the scope it lived on takes the assignment with it.
func (r *RBACManager) DeleteRoleAssignment(ctx context.Context, roleAssignmentID string) error {
	if _, err := r.client.DeleteByID(ctx, roleAssignmentID, nil); err != nil {
		var responseErr *azcore.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
			return nil
		}
		return err
	}
	return nil
}
