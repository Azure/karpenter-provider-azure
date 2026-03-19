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

package validation

import (
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
)

// labelValidationErrors holds all validation errors for a single label entry.
type labelValidationError struct {
	Key     string
	Message string
}

// ValidateNodePoolLabels checks that labels on a NodePool are compatible with AKS Machine API.
// Labels that are AKS-managed or kubelet-managed will be silently filtered by Karpenter before
// sending to Machine API, which can cause confusion. This validates at admission time instead.
//
// Returns a list of validation errors. An empty list means the labels are valid.
func ValidateNodePoolLabels(nodeLabels map[string]string) []labelValidationError {
	var errs []labelValidationError

	for key, value := range nodeLabels {
		// 1. Reject AKS-managed labels (kubernetes.azure.com/* and legacy AKS labels)
		// These are set by AKS/Karpenter automatically and will be stripped before sending to Machine API.
		// Users should not set them on NodePools as they have no effect and suggest misconfiguration.
		if v1beta1.IsAKSLabel(key) {
			errs = append(errs, labelValidationError{
				Key:     key,
				Message: fmt.Sprintf("label %q is managed by AKS and cannot be set on a NodePool; it will be automatically applied by the system", key),
			})
			continue
		}

		// 2. Reject kubelet-managed labels
		// These are set by kubelet during node registration and will be filtered before sending to Machine API.
		if labels.IsLabelKubeletManaged(key) {
			errs = append(errs, labelValidationError{
				Key:     key,
				Message: fmt.Sprintf("label %q is managed by kubelet and cannot be set on a NodePool", key),
			})
			continue
		}

		// 3. Standard Kubernetes label key validation
		if errMsgs := validation.IsQualifiedName(key); len(errMsgs) > 0 {
			errs = append(errs, labelValidationError{
				Key:     key,
				Message: fmt.Sprintf("invalid label key %q: %s", key, strings.Join(errMsgs, "; ")),
			})
			continue
		}

		// 4. Standard Kubernetes label value validation
		if errMsgs := validation.IsValidLabelValue(value); len(errMsgs) > 0 {
			errs = append(errs, labelValidationError{
				Key:     key,
				Message: fmt.Sprintf("invalid label value %q for key %q: %s", value, key, strings.Join(errMsgs, "; ")),
			})
		}
	}

	return errs
}

// taintValidationError holds a validation error for a taint.
type taintValidationError struct {
	Taint   string
	Message string
}

// ValidateNodePoolTaints checks that taints on a NodePool are compatible with AKS Machine API.
//
// AKS Machine API has restrictions on taints for system mode nodes:
// - System nodes cannot have NoSchedule or NoExecute taints other than CriticalAddonsOnly.
//
// Currently, Karpenter works around this by putting all taints in nodeInitializationTaints
// instead of nodeTaints. This validation catches known incompatibilities early.
//
// Returns a list of validation errors. An empty list means the taints are valid.
func ValidateNodePoolTaints(taints []v1.Taint, startupTaints []v1.Taint, isSystemMode bool) []taintValidationError {
	var errs []taintValidationError

	allTaints := append(append([]v1.Taint{}, taints...), startupTaints...)

	if isSystemMode {
		for _, taint := range allTaints {
			if isHardTaint(taint) && !isCriticalAddonsOnly(taint) {
				errs = append(errs, taintValidationError{
					Taint:   taint.ToString(),
					Message: fmt.Sprintf("system mode nodes cannot have %s taint %q; only CriticalAddonsOnly is allowed for system mode", taint.Effect, taint.ToString()),
				})
			}
		}
	}

	// Validate taint format
	for _, taint := range allTaints {
		if errMsgs := validation.IsQualifiedName(taint.Key); len(errMsgs) > 0 {
			errs = append(errs, taintValidationError{
				Taint:   taint.ToString(),
				Message: fmt.Sprintf("invalid taint key %q: %s", taint.Key, strings.Join(errMsgs, "; ")),
			})
		}
	}

	return errs
}

// isHardTaint returns true if the taint effect prevents scheduling or evicts pods.
func isHardTaint(taint v1.Taint) bool {
	return taint.Effect == v1.TaintEffectNoSchedule || taint.Effect == v1.TaintEffectNoExecute
}

// isCriticalAddonsOnly returns true if the taint is the CriticalAddonsOnly taint.
func isCriticalAddonsOnly(taint v1.Taint) bool {
	return taint.Key == "CriticalAddonsOnly" && taint.Effect == v1.TaintEffectNoSchedule
}

// requirementValidationError holds a validation error for a NodePool requirement.
type requirementValidationError struct {
	Key     string
	Message string
}

// ValidateNodePoolRequirements checks that NodePool requirements don't include labels
// that would be stripped by Karpenter before sending to Machine API.
func ValidateNodePoolRequirements(requirements []v1.NodeSelectorRequirement) []requirementValidationError {
	var errs []requirementValidationError

	for _, req := range requirements {
		// Warn about AKS-managed labels in requirements
		// Note: some AKS labels ARE well-known labels used for scheduling (e.g., sku-cpu, sku-memory).
		// Those are fine in requirements. We only flag truly AKS-managed labels that shouldn't be user-set.
		if v1beta1.IsAKSLabel(req.Key) && !v1beta1.AzureWellKnownLabels.Has(req.Key) {
			errs = append(errs, requirementValidationError{
				Key:     req.Key,
				Message: fmt.Sprintf("requirement key %q is an AKS-managed label and should not be used in NodePool requirements", req.Key),
			})
		}
	}

	return errs
}
