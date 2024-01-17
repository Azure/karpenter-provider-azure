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

package v1alpha2

import (
	"fmt"

	"github.com/mitchellh/hashstructure/v2"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AKSNodeClassSpec is the top level specification for the AKS Karpenter Provider.
// This will contain configuration necessary to launch instances in AKS.
type AKSNodeClassSpec struct {
	// +kubebuilder:default=128
	// +kubebuilder:validation:Minimum=100
	// osDiskSizeGB is the size of the OS disk in GB.
	OSDiskSizeGB *int32 `json:"osDiskSizeGB,omitempty"`
	// ImageID is the ID of the image that instances use.
	// Not exposed in the API yet
	ImageID *string `json:"-"`
	// ImageFamily is the image family that instances use.
	// +kubebuilder:default=Ubuntu2204
	// +kubebuilder:validation:Enum:={Ubuntu2204,AzureLinux}
	ImageFamily *string `json:"imageFamily,omitempty"`
	// ImageVersion is the image version that instances use.
	// +optional
	ImageVersion *string `json:"imageVersion,omitempty"`
	// Tags to be applied on Azure resources like instances.
	// +optional
	Tags map[string]string `json:"tags,omitempty"`
}

// AKSNodeClass is the Schema for the AKSNodeClass API
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=aksnodeclasses,scope=Cluster,categories=karpenter,shortName={aksnc,aksncs}
// +kubebuilder:subresource:status
type AKSNodeClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AKSNodeClassSpec   `json:"spec,omitempty"`
	Status AKSNodeClassStatus `json:"status,omitempty"`
}

func (in *AKSNodeClass) Hash() string {
	return fmt.Sprint(lo.Must(hashstructure.Hash(in.Spec, hashstructure.FormatV2, &hashstructure.HashOptions{
		SlicesAsSets:    true,
		IgnoreZeroValue: true,
		ZeroNil:         true,
	})))
}

// AKSNodeClassList contains a list of AKSNodeClass
// +kubebuilder:object:root=true
type AKSNodeClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AKSNodeClass `json:"items"`
}
