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
	"github.com/awslabs/operatorpkg/status"
	corev1 "k8s.io/api/core/v1"
)

const (
	ConditionTypeNodeImageReady  = "NodeImageReady"
	ConditionTypeK8sVersionReady = "K8sVersionReady"
)

// Image contains resolved image selector values utilized for node launch
type Image struct {
	// ID of the image
	// +required
	ID string `json:"id"`
	// Requirements of the image to be utilized on an instance type
	// +required
	Requirements []corev1.NodeSelectorRequirement `json:"requirements"`
}

// AKSNodeClassStatus contains the resolved state of the AKSNodeClass
type AKSNodeClassStatus struct {
	// NodeImages contains the current set of images available to use
	// for the NodeClass
	// +optional
	NodeImages []Image `json:"nodeImages,omitempty"`
	// K8sVersion contains the current k8s version which should be
	// used for nodes provisioned for the NodeClass
	K8sVersion string `json:"k8sVersion,omitempty"`
	// Conditions contains signals for health and readiness
	// +optional
	Conditions []status.Condition `json:"conditions,omitempty"`
}

func (in *AKSNodeClass) StatusConditions() status.ConditionSet {
	conds := []string{
		ConditionTypeNodeImageReady,
		ConditionTypeK8sVersionReady,
	}
	return status.NewReadyConditions(conds...).For(in)
}

func (in *AKSNodeClass) GetConditions() []status.Condition {
	return in.Status.Conditions
}

func (in *AKSNodeClass) SetConditions(conditions []status.Condition) {
	in.Status.Conditions = conditions
}
