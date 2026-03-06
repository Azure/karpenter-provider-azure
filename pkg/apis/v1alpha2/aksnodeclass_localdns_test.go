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

package v1alpha2_test

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsLocalDNSEnabled(t *testing.T) {
	tests := []struct {
		name              string
		mode              v1alpha2.LocalDNSMode
		kubernetesVersion string
		expected          bool
	}{
		{"LocalDNS is nil", v1alpha2.LocalDNSMode(""), "", false},
		{"Mode is Required", v1alpha2.LocalDNSModeRequired, "", true},
		{"Mode is Disabled", v1alpha2.LocalDNSModeDisabled, "", false},
		{"Mode is Preferred, no k8s version", v1alpha2.LocalDNSModePreferred, "", false},
		{"Mode is Preferred, k8s 1.34.0", v1alpha2.LocalDNSModePreferred, "1.34.0", false},
		{"Mode is Preferred, k8s 1.35.0", v1alpha2.LocalDNSModePreferred, "1.35.0", true},
		{"Mode is Preferred, k8s v1.35.0", v1alpha2.LocalDNSModePreferred, "v1.35.0", true},
		{"Mode is Preferred, k8s 1.36.0", v1alpha2.LocalDNSModePreferred, "1.36.0", true},
		{"Mode is Preferred, k8s 1.35.5", v1alpha2.LocalDNSModePreferred, "1.35.5", true},
		{"Mode is Preferred, k8s 1.34.99", v1alpha2.LocalDNSModePreferred, "1.34.99", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeClass := &v1alpha2.AKSNodeClass{}
			nodeClass.Status = v1alpha2.AKSNodeClassStatus{
				Conditions: []status.Condition{{
					Type:               v1alpha2.ConditionTypeKubernetesVersionReady,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: nodeClass.Generation,
				}},
			}
			if tt.mode != "" {
				nodeClass.Spec.LocalDNS = &v1alpha2.LocalDNS{Mode: tt.mode}
			}
			if tt.kubernetesVersion != "" {
				nodeClass.Status.KubernetesVersion = lo.ToPtr(tt.kubernetesVersion)
			}
			got := nodeClass.IsLocalDNSEnabled()
			if got != tt.expected {
				t.Errorf("IsLocalDNSEnabled() = %v, want %v", got, tt.expected)
			}
		})
	}
}
