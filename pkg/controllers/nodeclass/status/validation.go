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

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

type ValidationReconciler struct{}

func NewValidationReconciler() *ValidationReconciler {
	return &ValidationReconciler{}
}

func (r *ValidationReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	// This is currently a skeleton for future runtime validations that cannot be expressed
	// declaratively in the CRD schema (e.g., validations requiring external API calls,
	// cross-resource checks, or complex contextual business logic).

	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeValidationSucceeded)
	return reconcile.Result{}, nil
}
