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

package status

import (
	"context"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ReaderRoleID = "acdd72a7-3385-48ef-bd42-f606fba81ae7" // Azure built-in Reader role
)

type ValidationReconciler struct{}

func NewValidationReconciler() *ValidationReconciler {
	return &ValidationReconciler{}
}

func (r *ValidationReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	// TODO: Implement RBAC validation for BYOK (Disk Encryption Set)
	// This will require:
	// 1. Checking if DiskEncryptionSetID is configured
	// 2. Verifying the managed identity has Reader role on the DES
	// 3. Setting ValidationSucceeded to false with appropriate message if RBAC is missing
	//
	// For now, always succeed. Full implementation requires access to Azure RBAC APIs.
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeValidationSucceeded)
	return reconcile.Result{}, nil
}
