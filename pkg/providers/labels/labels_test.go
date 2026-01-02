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

package labels_test

import (
	"context"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/awslabs/operatorpkg/status"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

func TestGetAllSingleValuedRequirementLabels(t *testing.T) {
	cases := []struct {
		name           string
		requirements   scheduling.Requirements
		expectedLabels map[string]string
	}{
		{
			name:           "Nil instance type",
			requirements:   nil,
			expectedLabels: map[string]string{},
		},
		{
			name: "Single-valued requirements",
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(corev1.LabelInstanceTypeStable, corev1.NodeSelectorOpIn, "Standard_D2s_v3"),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
			),
			expectedLabels: map[string]string{
				corev1.LabelInstanceTypeStable: "Standard_D2s_v3",
				corev1.LabelTopologyZone:       "westus-1",
			},
		},
		{
			name: "Mixed single and multi-valued requirements",
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(corev1.LabelInstanceTypeStable, corev1.NodeSelectorOpIn, "Standard_D2s_v3"),
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
			),
			expectedLabels: map[string]string{
				corev1.LabelInstanceTypeStable: "Standard_D2s_v3",
				corev1.LabelTopologyZone:       "westus-1",
				// karpv1.CapacityTypeLabelKey should be excluded because it has multiple values
			},
		},
		{
			name: "No single-valued requirements",
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1", "westus-2"),
			),
			expectedLabels: map[string]string{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			labels := labels.GetAllSingleValuedRequirementLabels(c.requirements)
			assert.Equal(t, c.expectedLabels, labels)
		})
	}
}

func TestGetWellKnownSingleValuedRequirementLabels(t *testing.T) {
	cases := []struct {
		name           string
		requirements   scheduling.Requirements
		expectedLabels map[string]string
	}{
		{
			name:           "Nil requirements",
			requirements:   nil,
			expectedLabels: map[string]string{},
		},
		{
			name: "Single-valued well-known Azure label",
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1beta1.LabelSKUName, corev1.NodeSelectorOpIn, "Standard_D2s_v3"),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
			),
			expectedLabels: map[string]string{
				v1beta1.LabelSKUName: "Standard_D2s_v3",
				// corev1.LabelTopologyZone should be excluded because it's not in AzureWellKnownLabels
			},
		},
		{
			name: "Mixed well-known and non-well-known labels",
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1beta1.LabelSKUCPU, corev1.NodeSelectorOpIn, "2"),
				scheduling.NewRequirement(v1beta1.LabelSKUMemory, corev1.NodeSelectorOpIn, "8"),
				scheduling.NewRequirement(corev1.LabelInstanceTypeStable, corev1.NodeSelectorOpIn, "Standard_D2s_v3"),
				scheduling.NewRequirement("custom.domain.com/label", corev1.NodeSelectorOpIn, "custom-value"),
			),
			expectedLabels: map[string]string{
				v1beta1.LabelSKUCPU:    "2",
				v1beta1.LabelSKUMemory: "8",
				// "node.kubernetes.io/instance-type" and "custom.domain.com/label" should be excluded
			},
		},
		{
			name: "Multi-valued well-known label should be excluded",
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1beta1.LabelSKUName, corev1.NodeSelectorOpIn, "Standard_D2s_v3", "Standard_D4s_v3"),
				scheduling.NewRequirement(v1beta1.LabelSKUFamily, corev1.NodeSelectorOpIn, "D"),
			),
			expectedLabels: map[string]string{
				v1beta1.LabelSKUFamily: "D",
				// v1beta1.LabelSKUName should be excluded because it has multiple values
			},
		},
		{
			name: "GPU labels",
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1beta1.LabelSKUGPUName, corev1.NodeSelectorOpIn, "V100"),
				scheduling.NewRequirement(v1beta1.LabelSKUGPUCount, corev1.NodeSelectorOpIn, "1"),
				scheduling.NewRequirement(v1beta1.LabelSKUGPUManufacturer, corev1.NodeSelectorOpIn, "nvidia"),
			),
			expectedLabels: map[string]string{
				v1beta1.LabelSKUGPUName:         "V100",
				v1beta1.LabelSKUGPUCount:        "1",
				v1beta1.LabelSKUGPUManufacturer: "nvidia",
			},
		},
		{
			name: "No well-known single-valued requirements",
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(corev1.LabelInstanceTypeStable, corev1.NodeSelectorOpIn, "Standard_D2s_v3"),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1", "westus-2"),
			),
			expectedLabels: map[string]string{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			labels := labels.GetWellKnownSingleValuedRequirementLabels(c.requirements)
			assert.Equal(t, c.expectedLabels, labels)
		})
	}
}

func TestIsKubeletLabel(t *testing.T) {
	cases := []struct {
		name            string
		label           string
		expectedKubelet bool
	}{
		{
			name:            "Non-kubernetes label should be kubelet label",
			label:           "example.com/custom-label",
			expectedKubelet: true,
		},
		{
			name:            "Well-known kubelet label - hostname",
			label:           corev1.LabelHostname,
			expectedKubelet: true,
		},
		{
			name:            "Well-known kubelet label - instance type",
			label:           corev1.LabelInstanceType,
			expectedKubelet: true,
		},
		{
			name:            "Well-known kubelet label - topology zone",
			label:           corev1.LabelTopologyZone,
			expectedKubelet: true,
		},
		{
			name:            "Kubelet namespace - direct match",
			label:           "kubelet.kubernetes.io/custom",
			expectedKubelet: true,
		},
		{
			name:            "Kubelet namespace - subdomain match",
			label:           "my-component.kubelet.kubernetes.io/custom",
			expectedKubelet: true,
		},
		{
			name:            "Node namespace - direct match",
			label:           "node.kubernetes.io/custom",
			expectedKubelet: true,
		},
		{
			name:            "Node namespace - subdomain match",
			label:           "my-component.node.kubernetes.io/custom",
			expectedKubelet: true,
		},
		{
			name:            "Node restriction label should NOT be kubelet label",
			label:           "node-restriction.kubernetes.io/test",
			expectedKubelet: false,
		},
		{
			name:            "Node restriction subdomain should NOT be kubelet label",
			label:           "custom.node-restriction.kubernetes.io/test",
			expectedKubelet: false,
		},
		{
			name:            "Other kubernetes.io label should NOT be kubelet label",
			label:           "scheduler.kubernetes.io/custom",
			expectedKubelet: false,
		},
		{
			name:            "Other k8s.io label should NOT be kubelet label",
			label:           "example.k8s.io/custom",
			expectedKubelet: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := labels.IsKubeletLabel(c.label)
			assert.Equal(t, c.expectedKubelet, result)
		})
	}
}

func TestLocalDNSLabels(t *testing.T) {
	testCases := []struct {
		name              string
		localDNS          *v1beta1.LocalDNS
		kubernetesVersion string
		expectedLabel     string
	}{
		{
			name: "LocalDNS mode is Required",
			localDNS: &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModeRequired,
			},
			kubernetesVersion: "1.35.0",
			expectedLabel:     "enabled",
		},
		{
			name: "LocalDNS mode is Disabled",
			localDNS: &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModeDisabled,
			},
			kubernetesVersion: "1.35.0",
			expectedLabel:     "disabled",
		},
		{
			name: "LocalDNS mode is Preferred with k8s >= 1.36",
			localDNS: &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModePreferred,
			},
			kubernetesVersion: "1.36.0",
			expectedLabel:     "enabled",
		},
		{
			name: "LocalDNS mode is Preferred with k8s 1.37",
			localDNS: &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModePreferred,
			},
			kubernetesVersion: "1.37.0",
			expectedLabel:     "enabled",
		},
		{
			name: "LocalDNS mode is Preferred with k8s < 1.36",
			localDNS: &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModePreferred,
			},
			kubernetesVersion: "1.35.0",
			expectedLabel:     "disabled",
		},
		{
			name: "LocalDNS mode is Preferred with k8s 1.35.9",
			localDNS: &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModePreferred,
			},
			kubernetesVersion: "1.35.9",
			expectedLabel:     "disabled",
		},
		{
			name:              "LocalDNS is nil",
			localDNS:          nil,
			kubernetesVersion: "1.36.0",
			expectedLabel:     "disabled",
		},
		{
			name: "LocalDNS mode is empty",
			localDNS: &v1beta1.LocalDNS{
				Mode: "",
			},
			kubernetesVersion: "1.36.0",
			expectedLabel:     "disabled",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := options.ToContext(context.Background(), &options.Options{
				NodeResourceGroup:       "test-rg",
				KubeletIdentityClientID: "test-client-id",
				SubnetID:                "/subscriptions/test/resourceGroups/test/providers/Microsoft.Network/virtualNetworks/test/subnets/test",
			})

			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nodeclass",
				},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: tc.localDNS,
				},
				Status: v1beta1.AKSNodeClassStatus{
					KubernetesVersion: tc.kubernetesVersion,
					Conditions: []status.Condition{
						{
							Type:   v1beta1.ConditionTypeKubernetesVersionReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			labelMap, err := labels.Get(ctx, nodeClass)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedLabel, labelMap[labels.AKSLocalDNSStateLabelKey])
		})
	}
}
