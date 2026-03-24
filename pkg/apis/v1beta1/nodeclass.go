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

// Compile-time assertion: AKSNodeClass must implement NodeClass.
var _ NodeClass = (*AKSNodeClass)(nil)

// NodeClass is the interface for Azure VM-level properties shared across all
// NodeClass types (AKSNodeClass, AzureNodeClass, and potential future types).
// It captures what the shared VM creation helpers need regardless of which
// Kubernetes distribution manages the cluster.
//
// AKS-specific properties (Kubelet, ImageFamily, LocalDNS, ArtifactStreaming, etc.)
// are NOT on this interface. Code that needs AKS-specific behavior accesses them
// via the concrete *AKSNodeClass type, with the non-AKS behavior (no filtering,
// no AKS bootstrap) being the correct default.
type NodeClass interface {
	client.Object

	// VM resource configuration
	GetVNETSubnetID() *string
	GetOSDiskSizeGB() *int32
	GetImageID() *string
	GetTags() map[string]string
	GetEncryptionAtHost() bool
	GetManagedIdentities() []string

	// Lifecycle — used by hash controllers and drift detection
	Hash() string
	HashAnnotationKey() string
	HashVersionAnnotationKey() string
	HashVersion() string
	StatusConditions() status.ConditionSet
}
