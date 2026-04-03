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

package v1beta1

import (
	"github.com/awslabs/operatorpkg/status"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NodeClass is the interface for Azure VM-level properties shared across all
// NodeClass types (AKSNodeClass, AzureNodeClass, and potential future types).
//
// This interface captures VM-level properties and lifecycle methods that any
// NodeClass implementation must provide. It includes both methods currently
// consumed through the interface by shared helpers AND methods that set the
// contract for future NodeClass types to implement.
type NodeClass interface {
	client.Object

	GetKind() string

	// VM resource configuration
	GetVNETSubnetID() *string
	GetOSDiskSizeGB() *int32
	GetMaxPods() *int32
	GetTags() map[string]string
	GetEncryptionAtHost() bool

	// Lifecycle
	Hash() string
	StatusConditions() status.ConditionSet
	GetConditions() []status.Condition
	SetConditions([]status.Condition)
}
