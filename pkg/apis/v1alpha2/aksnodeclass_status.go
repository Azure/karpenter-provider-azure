// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package v1alpha2

import v1 "k8s.io/api/core/v1"

// Image contains resolved image selector values utilized for node launch
type Image struct {
	// ID of the image
	// +required
	ID string `json:"id"`
	// Requirements of the image to be utilized on an instance type
	// +required
	Requirements []v1.NodeSelectorRequirement `json:"requirements"`
}

// AKSNodeClassStatus contains the resolved state of the AKSNodeClass
type AKSNodeClassStatus struct {
}
