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

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Readiness struct {
}

func (n Readiness) Reconcile(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass) (reconcile.Result, error) {
	// nodeClass.StatusConditions().SetTrue(status.ConditionReady)
	return reconcile.Result{}, nil
}
