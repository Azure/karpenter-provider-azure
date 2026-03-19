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
	"context"
	"fmt"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

// NodePoolValidator validates NodePool resources against AKS Machine API constraints.
type NodePoolValidator struct {
	decoder admission.Decoder
}

// NewNodePoolValidator creates a new NodePoolValidator webhook handler.
func NewNodePoolValidator(scheme *runtime.Scheme) *NodePoolValidator {
	return &NodePoolValidator{
		decoder: admission.NewDecoder(scheme),
	}
}

// Handle implements admission.Handler. It validates NodePool CREATE and UPDATE operations.
func (v *NodePoolValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := log.FromContext(ctx).WithValues("webhook", "nodepool-validator", "name", req.Name)

	// Only validate CREATE and UPDATE
	if req.Operation != admissionv1.Create && req.Operation != admissionv1.Update {
		return admission.Allowed("")
	}

	nodePool := &karpv1.NodePool{}
	if err := v.decoder.Decode(req, nodePool); err != nil {
		logger.Error(err, "failed to decode NodePool")
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("failed to decode NodePool: %w", err))
	}

	var allErrors []string

	// Validate labels
	if nodePool.Spec.Template.Labels != nil {
		labelErrs := ValidateNodePoolLabels(nodePool.Spec.Template.Labels)
		for _, e := range labelErrs {
			allErrors = append(allErrors, e.Message)
		}
	}

	// Validate taints
	isSystemMode := false
	if nodePool.Spec.Template.Labels != nil {
		if mode, ok := nodePool.Spec.Template.Labels[v1beta1.AKSLabelMode]; ok && mode == v1beta1.ModeSystem {
			isSystemMode = true
		}
	}

	// Convert NodeSelectorRequirementWithMinValues to v1.Taint for validation
	taints := nodePool.Spec.Template.Spec.Taints
	startupTaints := nodePool.Spec.Template.Spec.StartupTaints
	taintErrs := ValidateNodePoolTaints(taints, startupTaints, isSystemMode)
	for _, e := range taintErrs {
		allErrors = append(allErrors, e.Message)
	}

	// Validate requirements
	var k8sReqs []v1.NodeSelectorRequirement
	for _, req := range nodePool.Spec.Template.Spec.Requirements {
		k8sReqs = append(k8sReqs, req.NodeSelectorRequirement)
	}
	reqErrs := ValidateNodePoolRequirements(k8sReqs)
	for _, e := range reqErrs {
		allErrors = append(allErrors, e.Message)
	}

	if len(allErrors) > 0 {
		msg := fmt.Sprintf("NodePool validation failed against AKS Machine API constraints:\n%s", strings.Join(allErrors, "\n"))
		logger.V(1).Info("NodePool validation failed", "errors", allErrors)
		return admission.Denied(msg)
	}

	return admission.Allowed("")
}

// Ensure NodePoolValidator implements admission.Handler
var _ admission.Handler = (*NodePoolValidator)(nil)

// Needed for runtime.Scheme usage
var (
	_ = serializer.NewCodecFactory(runtime.NewScheme())
)
