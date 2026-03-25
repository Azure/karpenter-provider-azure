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

	"github.com/awslabs/operatorpkg/status"
	corev1 "k8s.io/api/core/v1"
)

const (
	ConditionTypeImagesReady            = "ImagesReady"
	ConditionTypeKubernetesVersionReady = "KubernetesVersionReady"
	ConditionTypeSubnetsReady           = "SubnetsReady"
)

// NodeImage contains resolved image selector values utilized for node launch
type NodeImage struct {
	// id is the ID of the image. Examples:
	// - CIG: /CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2containerd/versions/2022.10.03
	// - SIG: /subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03
	// +required
	//nolint:kubeapilinter // requiredfields: validation is intentionally not enforced for this field
	ID string `json:"id"`
	// requirements of the image to be utilized on an instance type
	// +required
	//nolint:kubeapilinter // requiredfields: omitempty is intentionally omitted for this field
	Requirements []corev1.NodeSelectorRequirement `json:"requirements"`
}

// AKSNodeClassStatus contains the resolved state of the AKSNodeClass
type AKSNodeClassStatus struct {
	// images contains the current set of images available to use
	// for the NodeClass
	// +optional
	//nolint:kubeapilinter // ssatags: adding listType marker would be a breaking change
	Images []NodeImage `json:"images,omitempty"`
	// kubernetesVersion contains the current kubernetes version which should be
	// used for nodes provisioned for the NodeClass
	// +optional
	KubernetesVersion *string `json:"kubernetesVersion,omitempty"`
	// conditions contains signals for health and readiness
	// +optional
	//nolint:kubeapilinter // conditions: using status.Condition from operatorpkg instead of metav1.Condition for compatibility
	Conditions []status.Condition `json:"conditions,omitempty"`
}

func (in *AKSNodeClass) StatusConditions() status.ConditionSet {
	conds := []string{
		ConditionTypeImagesReady,
		ConditionTypeKubernetesVersionReady,
		ConditionTypeSubnetsReady,
	}
	return status.NewReadyConditions(conds...).For(in)
}

func (in *AKSNodeClass) GetConditions() []status.Condition {
	return in.Status.Conditions
}

func (in *AKSNodeClass) SetConditions(conditions []status.Condition) {
	in.Status.Conditions = conditions
}

// GetKubernetesVersion returns the Status.KubernetesVersion if its up to date and valid to use, otherwise returns an error.
func (in *AKSNodeClass) GetKubernetesVersion() (string, error) {
	err := in.validateKubernetesVersionReadiness()
	if err != nil {
		return "", err
	}

	if in.Status.KubernetesVersion == nil {
		return "", nil
	}

	return *in.Status.KubernetesVersion, nil
}

// validateKubernetesVersionReadiness will return nil if the the KubernetesVersion is considered valid to use,
// otherwise will return an error detailing the reason of failure.
//
// Ensures
// - The AKSNodeClass is non-nil
// - The AKSNodeClass' KubernetesVersionReady Condition is true
// - The Condition's ObservedGeneration is up to date with the latest Spec Generation
// - The KubernetesVersion is initialized and non-empty
func (in *AKSNodeClass) validateKubernetesVersionReadiness() error {
	if in == nil {
		return fmt.Errorf("NodeClass is nil, condition %s is not true", ConditionTypeKubernetesVersionReady)
	}
	kubernetesVersionCondition := in.StatusConditions().Get(ConditionTypeKubernetesVersionReady)
	if kubernetesVersionCondition.IsFalse() || kubernetesVersionCondition.IsUnknown() {
		return fmt.Errorf("NodeClass condition %s, is in Ready=%s, %s", ConditionTypeKubernetesVersionReady, kubernetesVersionCondition.GetStatus(), kubernetesVersionCondition.Message)
	} else if kubernetesVersionCondition.ObservedGeneration != in.GetGeneration() {
		return fmt.Errorf("NodeClass condition %s ObservedGeneration %d does not match the NodeClass Generation %d", ConditionTypeKubernetesVersionReady, kubernetesVersionCondition.ObservedGeneration, in.GetGeneration())
	} else if in.Status.KubernetesVersion == nil || *in.Status.KubernetesVersion == "" {
		return fmt.Errorf("NodeClass KubernetesVersion is uninitialized")
	}
	return nil
}

// GetImages returns the Status.Images if its up to date and valid to use, otherwise returns an error.
func (in *AKSNodeClass) GetImages() ([]NodeImage, error) {
	err := in.validateImagesReadiness()
	if err != nil {
		return []NodeImage{}, err
	}
	return in.Status.Images, nil
}

// validateImagesReadiness will return nil if the the Images are considered valid to use,
// otherwise will return an error detailing the reason of failure.
//
// Ensures
// - The AKSNodeClass is non-nil
// - The AKSNodeClass' ConditionTypeImagesReady Condition is true
// - The Condition's ObservedGeneration is up to date with the latest Spec Generation
func (in *AKSNodeClass) validateImagesReadiness() error {
	if in == nil {
		return fmt.Errorf("NodeClass is nil, condition %s is not true", ConditionTypeImagesReady)
	}
	imagesCondition := in.StatusConditions().Get(ConditionTypeImagesReady)
	if imagesCondition.IsFalse() || imagesCondition.IsUnknown() {
		return fmt.Errorf("NodeClass condition %s, is in Ready=%s, %s", ConditionTypeImagesReady, imagesCondition.GetStatus(), imagesCondition.Message)
	} else if imagesCondition.ObservedGeneration != in.GetGeneration() {
		return fmt.Errorf("NodeClass condition %s ObservedGeneration %d does not match the NodeClass Generation %d", ConditionTypeImagesReady, imagesCondition.ObservedGeneration, in.GetGeneration())
	}
	return nil
}
