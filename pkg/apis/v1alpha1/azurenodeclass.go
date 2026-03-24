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
	"fmt"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Compile-time assertion: AzureNodeClass must implement NodeClass.
var _ v1beta1.NodeClass = (*AzureNodeClass)(nil)

var (
	AnnotationAzureNodeClassHash        = apis.Group + "/azurenodeclass-hash"
	AnnotationAzureNodeClassHashVersion = apis.Group + "/azurenodeclass-hash-version"

	// TerminationFinalizer is the finalizer applied to AzureNodeClass resources.
	// Uses the same group as AKSNodeClass since both are under karpenter.azure.com.
	TerminationFinalizer = apis.Group + "/termination"
)

// AzureNodeClassHashVersion should be bumped when:
// 1. A field changes its default value for an existing field that is already hashed
// 2. A field is added to the hash calculation with an already-set value
// 3. A field is removed from the hash calculations
const AzureNodeClassHashVersion = "v1"

// AzureNodeClassSpec is the top level specification for the Azure Karpenter Provider.
// This contains configuration for launching standalone Azure VMs (non-AKS).
type AzureNodeClassSpec struct {
	// imageID is the full ARM resource ID of the image to use for VMs.
	// Required for AzureNodeClass — the user must provide their own image.
	// +kubebuilder:validation:Pattern=`(?i)^\/subscriptions\/[^\/]+\/.*`
	// +required
	ImageID *string `json:"imageID"`
	// userData is the raw cloud-init or bootstrap script passed as CustomData to the VM.
	// The provider base64-encodes this internally — provide raw text, not pre-encoded.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	UserData *string `json:"userData,omitempty"`
	// vnetSubnetID is the subnet used by NICs provisioned with this NodeClass.
	// If not specified, falls back to the global --vnet-subnet-id flag.
	// +kubebuilder:validation:Pattern=`(?i)^\/subscriptions\/[^\/]+\/resourceGroups\/[a-zA-Z0-9_\-().]{0,89}[a-zA-Z0-9_\-()]\/providers\/Microsoft\.Network\/virtualNetworks\/[^\/]+\/subnets\/[^\/]+$`
	// +optional
	VNETSubnetID *string `json:"vnetSubnetID,omitempty"`
	// osDiskSizeGB is the size of the OS disk in GB.
	// +kubebuilder:default=128
	// +kubebuilder:validation:Minimum=30
	// +kubebuilder:validation:Maximum=4096
	// +optional
	OSDiskSizeGB *int32 `json:"osDiskSizeGB,omitempty"`
	// tags to be applied on Azure resources like instances.
	// +optional
	Tags map[string]string `json:"tags,omitempty" hash:"ignore"`
	// security contains security-related configuration for provisioned VMs.
	// +optional
	Security *AzureNodeClassSecurity `json:"security,omitempty"`
}

// AzureNodeClassSecurity contains security-related configuration for provisioned VMs.
type AzureNodeClassSecurity struct {
	// encryptionAtHost specifies whether host-level encryption is enabled.
	// +optional
	EncryptionAtHost *bool `json:"encryptionAtHost,omitempty"`
}

// AzureNodeClass is the Schema for the AzureNodeClass API
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=azurenodeclasses,scope=Cluster,shortName={aznc,azncs}
// +kubebuilder:subresource:status
type AzureNodeClass struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the top level specification for the Azure Karpenter Provider.
	// This contains configuration for launching standalone Azure VMs (non-AKS).
	// +optional
	//nolint:kubeapilinter // optionalfields: changing to pointer would be a breaking change
	Spec AzureNodeClassSpec `json:"spec,omitempty"`
	// status contains the resolved state of the AzureNodeClass.
	// +optional
	//nolint:kubeapilinter // optionalfields: changing to pointer would be a breaking change
	Status AzureNodeClassStatus `json:"status,omitempty"`
}

// Hash returns a hash of the AzureNodeClassSpec, used for drift detection.
func (in *AzureNodeClass) Hash() string {
	return fmt.Sprint(lo.Must(hashstructure.Hash(in.Spec, hashstructure.FormatV2, &hashstructure.HashOptions{
		SlicesAsSets:    true,
		IgnoreZeroValue: true,
		ZeroNil:         true,
	})))
}

// AzureNodeClassList contains a list of AzureNodeClass
// +kubebuilder:object:root=true
type AzureNodeClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AzureNodeClass `json:"items"`
}

// GetEncryptionAtHost returns whether encryption at host is enabled for the node class.
// Returns false if Security or EncryptionAtHost is nil.
func (in *AzureNodeClass) GetEncryptionAtHost() bool {
	if in.Spec.Security != nil && in.Spec.Security.EncryptionAtHost != nil {
		return *in.Spec.Security.EncryptionAtHost
	}
	return false
}

// GetVNETSubnetID returns the VNETSubnetID from the spec.
func (in *AzureNodeClass) GetVNETSubnetID() *string {
	return in.Spec.VNETSubnetID
}

// GetOSDiskSizeGB returns the OSDiskSizeGB from the spec.
func (in *AzureNodeClass) GetOSDiskSizeGB() *int32 {
	return in.Spec.OSDiskSizeGB
}

// GetImageID returns the ImageID from the spec.
func (in *AzureNodeClass) GetImageID() *string {
	return in.Spec.ImageID
}

// GetUserData returns the UserData from the spec.
func (in *AzureNodeClass) GetUserData() *string {
	return in.Spec.UserData
}

// GetTags returns the Tags from the spec.
func (in *AzureNodeClass) GetTags() map[string]string {
	return in.Spec.Tags
}

// GetManagedIdentities returns nil — AzureNodeClass doesn't have managed identities yet.
// This field will be added in a future PR to allow users to assign identities to VMs.
func (in *AzureNodeClass) GetManagedIdentities() []string {
	return nil
}

// HashAnnotationKey returns the annotation key for the AzureNodeClass hash.
func (in *AzureNodeClass) HashAnnotationKey() string {
	return AnnotationAzureNodeClassHash
}

// HashVersionAnnotationKey returns the annotation key for the AzureNodeClass hash version.
func (in *AzureNodeClass) HashVersionAnnotationKey() string {
	return AnnotationAzureNodeClassHashVersion
}

// HashVersion returns the current hash version for the AzureNodeClass.
func (in *AzureNodeClass) HashVersion() string {
	return AzureNodeClassHashVersion
}
