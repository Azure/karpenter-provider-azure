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

// AzureNodeClassSpec is the top level specification for the Azure Karpenter Provider
// for non-AKS Kubernetes clusters. It configures VM provisioning directly against Azure.
type AzureNodeClassSpec struct {
	// subscriptionID is the Azure subscription to create VMs in.
	// If not specified, defaults to the subscription from the controller's Azure credentials.
	// +kubebuilder:validation:Pattern=`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
	// +optional
	SubscriptionID *string `json:"subscriptionID,omitempty"`

	// resourceGroup is the Azure resource group to create VMs in.
	// If not specified, defaults to the node resource group from the controller options.
	// +optional
	ResourceGroup *string `json:"resourceGroup,omitempty"`

	// location is the Azure region to create VMs in.
	// If not specified, defaults to the location from the controller's Azure config.
	// +optional
	Location *string `json:"location,omitempty"`

	// imageID is the ARM resource ID of the image to use for VMs.
	// Supports community gallery, shared image gallery, and platform image references.
	// +required
	//nolint:kubeapilinter // requiredfields: validation is intentionally not enforced via struct tag
	ImageID string `json:"imageID"`

	// userData is the plain-text user data to pass to the VM via CustomData.
	// The system will base64-encode this value before passing it to the Azure API.
	// +optional
	UserData *string `json:"userData,omitempty"`

	// managedIdentities is a list of user-assigned managed identity ARM resource IDs
	// to attach to provisioned VMs.
	// +optional
	//nolint:kubeapilinter // ssatags: listType marker omitted for simplicity in alpha API
	ManagedIdentities []string `json:"managedIdentities,omitempty"`

	// vnetSubnetID is the subnet used by NICs provisioned with this node class.
	// If not specified, falls back to the default --vnet-subnet-id from controller options.
	// +kubebuilder:validation:Pattern=`(?i)^\/subscriptions\/[^\/]+\/resourceGroups\/[a-zA-Z0-9_\-().]{0,89}[a-zA-Z0-9_\-()]\/providers\/Microsoft\.Network\/virtualNetworks\/[^\/]+\/subnets\/[^\/]+$`
	// +optional
	VNETSubnetID *string `json:"vnetSubnetID,omitempty"`

	// osDiskSizeGB is the size of the OS disk in GB.
	// +kubebuilder:validation:Minimum=30
	// +kubebuilder:validation:Maximum=4096
	// +optional
	OSDiskSizeGB *int32 `json:"osDiskSizeGB,omitempty"`

	// dataDiskSizeGB is the size of an additional data disk in GB.
	// +kubebuilder:validation:Minimum=10
	// +kubebuilder:validation:Maximum=4096
	// +optional
	DataDiskSizeGB *int32 `json:"dataDiskSizeGB,omitempty"`

	// instanceTypes is an optional list of specific VM SKU names to restrict provisioning to.
	// When specified, only these SKUs will be considered for scheduling, even if they are
	// not in the standard supported set.
	// +optional
	//nolint:kubeapilinter // ssatags: listType marker omitted for simplicity in alpha API
	InstanceTypes []string `json:"instanceTypes,omitempty"`

	// tags to be applied on Azure resources like instances.
	// +optional
	Tags map[string]string `json:"tags,omitempty" hash:"ignore"`

	// security is a collection of security related fields.
	// +optional
	Security *AzureNodeClassSecurity `json:"security,omitempty"`
}

// AzureNodeClassSecurity defines security settings for provisioned VMs.
type AzureNodeClassSecurity struct {
	// encryptionAtHost specifies whether host-level encryption is enabled for provisioned nodes.
	// +optional
	EncryptionAtHost *bool `json:"encryptionAtHost,omitempty"`
}

// AzureNodeClass is the Schema for the AzureNodeClass API
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=azurenodeclasses,scope=Cluster,categories={karpenter},shortName={azurenc,azurencs}
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="ImageID",type=string,JSONPath=".spec.imageID",priority=1
// +kubebuilder:subresource:status
type AzureNodeClass struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the specification for the AzureNodeClass.
	// +optional
	//nolint:kubeapilinter // optionalfields: changing to pointer would be a breaking change
	Spec AzureNodeClassSpec `json:"spec,omitempty"`
	// status contains the resolved state of the AzureNodeClass.
	// +optional
	//nolint:kubeapilinter // optionalfields: changing to pointer would be a breaking change
	Status AzureNodeClassStatus `json:"status,omitempty"`
}

const AzureNodeClassHashVersion = "v1"

func (in *AzureNodeClass) Hash() string {
	return fmt.Sprint(lo.Must(hashstructure.Hash(in.Spec, hashstructure.FormatV2, &hashstructure.HashOptions{
		SlicesAsSets:    true,
		IgnoreZeroValue: true,
		ZeroNil:         true,
	})))
}

// GetEncryptionAtHost returns whether encryption at host is enabled for the node class.
// Returns false if Security or EncryptionAtHost is nil.
func (in *AzureNodeClass) GetEncryptionAtHost() bool {
	if in.Spec.Security != nil && in.Spec.Security.EncryptionAtHost != nil {
		return *in.Spec.Security.EncryptionAtHost
	}
	return false
}

// AzureNodeClassList contains a list of AzureNodeClass
// +kubebuilder:object:root=true
type AzureNodeClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AzureNodeClass `json:"items"`
}
