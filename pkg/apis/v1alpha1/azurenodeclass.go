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

	"github.com/mitchellh/hashstructure/v2"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AzureNodeClassSpec is the top level specification for the Azure Karpenter Provider.
// This will contain configuration necessary to launch instances in Azure,
// independent of a specific managed control plane (e.g. AKS).
type AzureNodeClassSpec struct {
	// imageID is the ARM resource ID of the image that instances use.
	// This can be a Compute Gallery image, Shared Image Gallery image, Community Gallery image,
	// or any valid Azure image resource ID.
	// When set, imageFamily-based image resolution is bypassed entirely.
	// The user is responsible for ensuring the image is compatible with the selected instance types.
	// Examples:
	//   /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Compute/galleries/{gallery}/images/{image}/versions/{version}
	//   /CommunityGalleries/{gallery}/images/{image}/versions/{version}
	// +kubebuilder:validation:Pattern=`(?i)^(\/subscriptions\/[^\/]+\/resourceGroups\/[^\/]+\/providers\/Microsoft\.Compute\/.*|\/CommunityGalleries\/[^\/]+\/images\/[^\/]+\/versions\/[^\/]+)$`
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	ImageID *string `json:"imageID,omitempty"`
	// userData is the base64-encoded custom data that will be passed to the VM at creation time.
	// The caller must pre-encode their cloud-init or bootstrap script to base64, as the Azure API
	// expects this field to contain a base64-encoded string.
	// The user is fully responsible for providing valid bootstrap/cloud-init data.
	// When this field is set, no Karpenter-managed bootstrapping is performed.
	// +kubebuilder:validation:MaxLength=87380
	// +optional
	UserData *string `json:"userData,omitempty"`
	// managedIdentities is a list of user-assigned managed identity resource IDs
	// to attach to provisioned VMs. These are merged with any global identities
	// configured via the --node-identities flag.
	// +kubebuilder:validation:MaxItems=10
	// +optional
	//nolint:kubeapilinter // ssatags: adding listType marker is unnecessary for this new field
	ManagedIdentities []string `json:"managedIdentities,omitempty"`
	// vnetSubnetID is the subnet used by nics provisioned with this nodeclass.
	// If not specified, we will use the default --vnet-subnet-id specified in karpenter's options config.
	// +kubebuilder:validation:Pattern=`(?i)^\/subscriptions\/[^\/]+\/resourceGroups\/[a-zA-Z0-9_\-().]{0,89}[a-zA-Z0-9_\-()]\/providers\/Microsoft\.Network\/virtualNetworks\/[^\/]+\/subnets\/[^\/]+$`
	// +optional
	VNETSubnetID *string `json:"vnetSubnetID,omitempty"`
	// osDiskSizeGB is the size of the OS disk in GB.
	// +kubebuilder:validation:Minimum=30
	// +kubebuilder:validation:Maximum=4096
	// +optional
	OSDiskSizeGB *int32 `json:"osDiskSizeGB,omitempty"`
	// tags to be applied on Azure resources like instances.
	// +kubebuilder:validation:XValidation:message="tags keys must be less than 512 characters",rule="self.all(k, size(k) <= 512)"
	// +kubebuilder:validation:XValidation:message="tags keys must not contain '<', '>', '%', '&', or '?'",rule="self.all(k, !k.matches('[<>%&?]'))"
	// +kubebuilder:validation:XValidation:message="tags keys must not contain '\\'",rule="self.all(k, !k.contains('\\\\'))"
	// +kubebuilder:validation:XValidation:message="tags values must be less than 256 characters",rule="self.all(k, size(self[k]) <= 256)"
	// +optional
	Tags map[string]string `json:"tags,omitempty" hash:"ignore"`
	// security is a collection of security related karpenter fields.
	// +optional
	Security *AzureNodeClassSecurity `json:"security,omitempty"`
}

// AzureNodeClassSecurity contains security-related configuration for provisioned nodes.
type AzureNodeClassSecurity struct {
	// encryptionAtHost specifies whether host-level encryption is enabled for provisioned nodes.
	// For more information, see:
	// https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#encryption-at-host---end-to-end-encryption-for-your-vm-data
	// +optional
	EncryptionAtHost *bool `json:"encryptionAtHost,omitempty"`
}

// AzureNodeClass is the Schema for the AzureNodeClass API.
// AzureNodeClass is a more generic node class for provisioning Azure VMs
// that are not necessarily managed by AKS. It supports custom images,
// custom bootstrap data (userData), and per-NodeClass identity configuration.
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=azurenodeclasses,scope=Cluster,categories={karpenter},shortName={aznc,azncs}
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="ImageID",type=string,JSONPath=".spec.imageID",priority=1
// +kubebuilder:subresource:status
type AzureNodeClass struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the top level specification for the Azure Karpenter Provider.
	// This will contain configuration necessary to launch instances in Azure.
	// +optional
	//nolint:kubeapilinter // optionalfields: using value type for consistency with AKSNodeClass pattern
	Spec AzureNodeClassSpec `json:"spec,omitempty"`
	// status contains the resolved state of the AzureNodeClass.
	// +optional
	//nolint:kubeapilinter // optionalfields: using value type for consistency with AKSNodeClass pattern
	Status AzureNodeClassStatus `json:"status,omitempty"`
}

// AzureNodeClassHashVersion tracks the hash version for AzureNodeClass.
// Bump this when:
// 1. A field changes its default value for an existing field that is already hashed
// 2. A field is added to the hash calculation with an already-set value
// 3. A field is removed from the hash calculations
const AzureNodeClassHashVersion = "v1"

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
