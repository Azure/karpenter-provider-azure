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

package v1alpha1

import (
	"github.com/awslabs/operatorpkg/status"
)

const (
	ConditionTypeValidationSucceeded = "ValidationSucceeded"
	ConditionTypeSubnetsReady        = "SubnetsReady"
)

// AzureNodeClassStatus contains the resolved state of the AzureNodeClass.
// Simpler than AKSNodeClassStatus — no image resolution or K8s version tracking,
// since the user provides the image directly and manages their own cluster.
type AzureNodeClassStatus struct {
	// conditions contains signals for health and readiness
	// +optional
	//nolint:kubeapilinter // conditions: using status.Condition from operatorpkg instead of metav1.Condition for compatibility
	Conditions []status.Condition `json:"conditions,omitempty"`
}

// StatusConditions returns the condition set for the AzureNodeClass.
// Only ValidationSucceeded is tracked — image and K8s version are user-managed.
func (in *AzureNodeClass) StatusConditions() status.ConditionSet {
	return status.NewReadyConditions(
		ConditionTypeValidationSucceeded,
		ConditionTypeSubnetsReady,
	).For(in)
}

// GetConditions returns the status conditions of the AzureNodeClass.
func (in *AzureNodeClass) GetConditions() []status.Condition {
	return in.Status.Conditions
}

// SetConditions sets the status conditions on the AzureNodeClass.
func (in *AzureNodeClass) SetConditions(conditions []status.Condition) {
	in.Status.Conditions = conditions
}
