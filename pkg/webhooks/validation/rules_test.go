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
	"testing"

	v1 "k8s.io/api/core/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

func TestValidateNodePoolLabels_AKSManagedLabels(t *testing.T) {
	tests := []struct {
		name        string
		labels      map[string]string
		expectError bool
		errorSubstr string
	}{
		{
			name:        "valid user label",
			labels:      map[string]string{"app": "web", "team": "platform"},
			expectError: false,
		},
		{
			name:        "AKS managed label kubernetes.azure.com/cluster",
			labels:      map[string]string{v1beta1.AKSLabelCluster: "my-cluster"},
			expectError: true,
			errorSubstr: "managed by AKS",
		},
		{
			name:        "AKS managed label kubernetes.azure.com/mode",
			labels:      map[string]string{v1beta1.AKSLabelMode: "user"},
			expectError: true,
			errorSubstr: "managed by AKS",
		},
		{
			name:        "AKS managed label kubernetes.azure.com/os-sku",
			labels:      map[string]string{v1beta1.AKSLabelOSSKU: "Ubuntu"},
			expectError: true,
			errorSubstr: "managed by AKS",
		},
		{
			name:        "AKS managed label kubernetes.azure.com/scalesetpriority",
			labels:      map[string]string{v1beta1.AKSLabelScaleSetPriority: "spot"},
			expectError: true,
			errorSubstr: "managed by AKS",
		},
		{
			name:        "legacy AKS managed label agentpool",
			labels:      map[string]string{v1beta1.AKSLabelLegacyAgentPool: "pool1"},
			expectError: true,
			errorSubstr: "managed by AKS",
		},
		{
			name:        "legacy AKS managed label storageprofile",
			labels:      map[string]string{v1beta1.AKSLabelLegacyStorageProfile: "managed"},
			expectError: true,
			errorSubstr: "managed by AKS",
		},
		{
			name:        "kubelet managed label node.kubernetes.io/instance-type",
			labels:      map[string]string{v1.LabelInstanceTypeStable: "Standard_D2s_v3"},
			expectError: true,
			errorSubstr: "managed by kubelet",
		},
		{
			name:        "kubelet managed label topology.kubernetes.io/zone",
			labels:      map[string]string{v1.LabelTopologyZone: "eastus-1"},
			expectError: true,
			errorSubstr: "managed by kubelet",
		},
		{
			name:        "valid non-AKS domain label",
			labels:      map[string]string{"custom.example.com/role": "worker"},
			expectError: false,
		},
		{
			name:        "empty labels",
			labels:      map[string]string{},
			expectError: false,
		},
		{
			name: "mix of valid and invalid labels",
			labels: map[string]string{
				"app":                      "web",
				v1beta1.AKSLabelCluster:    "cluster",
				"custom.example.com/valid": "true",
			},
			expectError: true,
			errorSubstr: "managed by AKS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateNodePoolLabels(tt.labels)
			if tt.expectError && len(errs) == 0 {
				t.Errorf("expected validation error but got none")
			}
			if !tt.expectError && len(errs) > 0 {
				t.Errorf("expected no validation errors but got: %v", errs)
			}
			if tt.expectError && len(errs) > 0 {
				found := false
				for _, e := range errs {
					if contains(e.Message, tt.errorSubstr) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing %q but got: %v", tt.errorSubstr, errs)
				}
			}
		})
	}
}

func TestValidateNodePoolTaints_SystemMode(t *testing.T) {
	tests := []struct {
		name         string
		taints       []v1.Taint
		startupTaint []v1.Taint
		isSystemMode bool
		expectError  bool
		errorSubstr  string
	}{
		{
			name:         "user mode with NoSchedule taint is valid",
			taints:       []v1.Taint{{Key: "dedicated", Value: "gpu", Effect: v1.TaintEffectNoSchedule}},
			isSystemMode: false,
			expectError:  false,
		},
		{
			name:         "system mode with CriticalAddonsOnly is valid",
			taints:       []v1.Taint{{Key: "CriticalAddonsOnly", Effect: v1.TaintEffectNoSchedule}},
			isSystemMode: true,
			expectError:  false,
		},
		{
			name:         "system mode with NoSchedule non-CriticalAddonsOnly taint is invalid",
			taints:       []v1.Taint{{Key: "dedicated", Value: "gpu", Effect: v1.TaintEffectNoSchedule}},
			isSystemMode: true,
			expectError:  true,
			errorSubstr:  "system mode",
		},
		{
			name:         "system mode with NoExecute taint is invalid",
			taints:       []v1.Taint{{Key: "node.kubernetes.io/unreachable", Effect: v1.TaintEffectNoExecute}},
			isSystemMode: true,
			expectError:  true,
			errorSubstr:  "system mode",
		},
		{
			name:         "system mode with PreferNoSchedule is valid",
			taints:       []v1.Taint{{Key: "prefer-no", Value: "true", Effect: v1.TaintEffectPreferNoSchedule}},
			isSystemMode: true,
			expectError:  false,
		},
		{
			name:         "system mode with startup taint NoSchedule is invalid",
			startupTaint: []v1.Taint{{Key: "startup-only", Value: "true", Effect: v1.TaintEffectNoSchedule}},
			isSystemMode: true,
			expectError:  true,
			errorSubstr:  "system mode",
		},
		{
			name:         "empty taints are valid",
			taints:       []v1.Taint{},
			isSystemMode: true,
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateNodePoolTaints(tt.taints, tt.startupTaint, tt.isSystemMode)
			if tt.expectError && len(errs) == 0 {
				t.Errorf("expected validation error but got none")
			}
			if !tt.expectError && len(errs) > 0 {
				t.Errorf("expected no validation errors but got: %v", errs)
			}
			if tt.expectError && len(errs) > 0 {
				found := false
				for _, e := range errs {
					if contains(e.Message, tt.errorSubstr) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing %q but got: %v", tt.errorSubstr, errs)
				}
			}
		})
	}
}

func TestValidateNodePoolRequirements_AKSLabels(t *testing.T) {
	tests := []struct {
		name        string
		reqs        []v1.NodeSelectorRequirement
		expectError bool
	}{
		{
			name: "well-known AKS label in requirements is valid",
			reqs: []v1.NodeSelectorRequirement{
				{Key: v1beta1.LabelSKUCPU, Operator: v1.NodeSelectorOpIn, Values: []string{"4"}},
			},
			expectError: false,
		},
		{
			name: "AKS managed (non-well-known) label in requirements is invalid",
			reqs: []v1.NodeSelectorRequirement{
				{Key: v1beta1.AKSLabelCluster, Operator: v1.NodeSelectorOpIn, Values: []string{"my-cluster"}},
			},
			expectError: true,
		},
		{
			name: "standard k8s label is valid",
			reqs: []v1.NodeSelectorRequirement{
				{Key: v1.LabelTopologyZone, Operator: v1.NodeSelectorOpIn, Values: []string{"eastus-1"}},
			},
			expectError: false,
		},
		{
			name:        "empty requirements are valid",
			reqs:        []v1.NodeSelectorRequirement{},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateNodePoolRequirements(tt.reqs)
			if tt.expectError && len(errs) == 0 {
				t.Errorf("expected validation error but got none")
			}
			if !tt.expectError && len(errs) > 0 {
				t.Errorf("expected no validation errors but got: %v", errs)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsImpl(s, substr)
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
