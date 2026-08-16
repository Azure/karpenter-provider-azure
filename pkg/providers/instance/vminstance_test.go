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

package instance

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
)

func TestResolveUltraSSDRequested(t *testing.T) {
	t.Parallel()

	requirement := func(operator v1.NodeSelectorOperator, values ...string) karpv1.NodeSelectorRequirementWithMinValues {
		return karpv1.NodeSelectorRequirementWithMinValues{
			Key:      v1beta1.LabelUltraSSD,
			Operator: operator,
			Values:   values,
		}
	}

	tests := []struct {
		name         string
		requirements []karpv1.NodeSelectorRequirementWithMinValues
		expected     bool
	}{
		{
			name:         "true",
			requirements: []karpv1.NodeSelectorRequirementWithMinValues{requirement(v1.NodeSelectorOpIn, "true")},
			expected:     true,
		},
		{
			name:         "false",
			requirements: []karpv1.NodeSelectorRequirementWithMinValues{requirement(v1.NodeSelectorOpIn, "false")},
			expected:     false,
		},
		{
			name:     "empty",
			expected: false,
		},
		{
			name:         "true and false",
			requirements: []karpv1.NodeSelectorRequirementWithMinValues{requirement(v1.NodeSelectorOpIn, "true", "false")},
			expected:     false,
		},
		{
			name:         "not in false",
			requirements: []karpv1.NodeSelectorRequirementWithMinValues{requirement(v1.NodeSelectorOpNotIn, "false")},
			expected:     true,
		},
		{
			name:         "not in true",
			requirements: []karpv1.NodeSelectorRequirementWithMinValues{requirement(v1.NodeSelectorOpNotIn, "true")},
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			result := resolveUltraSSDRequested(&karpv1.NodeClaim{
				Spec: karpv1.NodeClaimSpec{Requirements: tt.requirements},
			})

			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestGetManagedExtensionNames(t *testing.T) {
	publicCloudEnv := lo.Must(auth.EnvironmentFromName("AzurePublicCloud"))
	chinaCloudEnv := lo.Must(auth.EnvironmentFromName("AzureChinaCloud"))
	usGovCloudEnv := lo.Must(auth.EnvironmentFromName("AzureUSGovernmentCloud"))
	baseEnv := lo.Must(auth.EnvironmentFromName("AzurePublicCloud"))
	copiedInnerEnv := *baseEnv.Environment
	copiedInnerEnv.Name = "AzureStackCloud"
	noBillingExtensionEnv := &auth.Environment{
		Environment: &copiedInnerEnv,
		Cloud:       baseEnv.Cloud,
	}

	tests := []struct {
		name          string
		provisionMode string
		env           *auth.Environment
		expected      []string
	}{
		{
			name:          "PublicCloud with BootstrappingClient mode returns billing extension and CSE",
			provisionMode: consts.ProvisionModeBootstrappingClient,
			env:           publicCloudEnv,
			expected:      []string{"computeAksLinuxBilling", "cse-agent-karpenter"},
		},
		{
			name:          "PublicCloud with AKSScriptless mode returns only billing extension",
			provisionMode: consts.ProvisionModeAKSScriptless,
			env:           publicCloudEnv,
			expected:      []string{"computeAksLinuxBilling"},
		},
		{
			name:          "ChinaCloud with BootstrappingClient mode returns billing extension and CSE",
			provisionMode: consts.ProvisionModeBootstrappingClient,
			env:           chinaCloudEnv,
			expected:      []string{"computeAksLinuxBilling", "cse-agent-karpenter"},
		},
		{
			name:          "ChinaCloud with AKSScriptless mode returns only billing extension",
			provisionMode: consts.ProvisionModeAKSScriptless,
			env:           chinaCloudEnv,
			expected:      []string{"computeAksLinuxBilling"},
		},
		{
			name:          "USGovernmentCloud with BootstrappingClient mode returns billing extension and CSE",
			provisionMode: consts.ProvisionModeBootstrappingClient,
			env:           usGovCloudEnv,
			expected:      []string{"computeAksLinuxBilling", "cse-agent-karpenter"},
		},
		{
			name:          "USGovernmentCloud with AKSScriptless mode returns only billing extension",
			provisionMode: consts.ProvisionModeAKSScriptless,
			env:           usGovCloudEnv,
			expected:      []string{"computeAksLinuxBilling"},
		},
		{
			name:          "Nonstandard cloud with BootstrappingClient mode returns only CSE",
			provisionMode: consts.ProvisionModeBootstrappingClient,
			env:           noBillingExtensionEnv,
			expected:      []string{"cse-agent-karpenter"},
		},
		{
			name:          "Nonstandard cloud with AKSScriptless mode returns empty",
			provisionMode: consts.ProvisionModeAKSScriptless,
			env:           noBillingExtensionEnv,
			expected:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result := GetManagedExtensionNames(tt.provisionMode, tt.env)

			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestSetVMPropertiesCapacityReservation(t *testing.T) {
	t.Parallel()

	const groupID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/crg-rg/providers/Microsoft.Compute/capacityReservationGroups/crg"

	tests := []struct {
		name      string
		nodeClass *v1beta1.AKSNodeClass
		expected  *string
	}{
		{
			name:      "no group configured leaves the VM unassociated",
			nodeClass: &v1beta1.AKSNodeClass{},
			expected:  nil,
		},
		{
			name: "configured group is passed through to ARM",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{CapacityReservationGroupID: lo.ToPtr(groupID)},
			},
			expected: lo.ToPtr(groupID),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			properties := &armcompute.VirtualMachineProperties{}
			setVMPropertiesCapacityReservation(properties, tt.nodeClass)

			if tt.expected == nil {
				g.Expect(properties.CapacityReservation).To(BeNil())
				return
			}
			g.Expect(properties.CapacityReservation.CapacityReservationGroup.ID).To(Equal(tt.expected))
		})
	}
}

func TestValidateExistingCapacityReservation(t *testing.T) {
	t.Parallel()

	const groupID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/crg-rg/providers/Microsoft.Compute/capacityReservationGroups/crg"
	const otherGroupID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/crg-rg/providers/Microsoft.Compute/capacityReservationGroups/other"

	vmInGroup := func(id string) *armcompute.VirtualMachine {
		vm := &armcompute.VirtualMachine{Name: lo.ToPtr("aks-test"), Properties: &armcompute.VirtualMachineProperties{}}
		if id != "" {
			vm.Properties.CapacityReservation = &armcompute.CapacityReservationProfile{
				CapacityReservationGroup: &armcompute.SubResource{ID: lo.ToPtr(id)},
			}
		}
		return vm
	}

	tests := []struct {
		name      string
		vm        *armcompute.VirtualMachine
		nodeClass *v1beta1.AKSNodeClass
		wantErr   bool
	}{
		{
			name:      "neither is reserved",
			vm:        vmInGroup(""),
			nodeClass: &v1beta1.AKSNodeClass{},
		},
		{
			name:      "same group",
			vm:        vmInGroup(groupID),
			nodeClass: &v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{CapacityReservationGroupID: lo.ToPtr(groupID)}},
		},
		{
			name:      "same group, different casing as ARM echoes it",
			vm:        vmInGroup(strings.ToUpper(groupID)),
			nodeClass: &v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{CapacityReservationGroupID: lo.ToPtr(groupID)}},
		},
		{
			name:      "the NodeClass changed groups since the VM was created",
			vm:        vmInGroup(groupID),
			nodeClass: &v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{CapacityReservationGroupID: lo.ToPtr(otherGroupID)}},
			wantErr:   true,
		},
		{
			name:      "the NodeClass gained a group since the VM was created",
			vm:        vmInGroup(""),
			nodeClass: &v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{CapacityReservationGroupID: lo.ToPtr(groupID)}},
			wantErr:   true,
		},
		{
			name:      "the NodeClass dropped its group since the VM was created",
			vm:        vmInGroup(groupID),
			nodeClass: &v1beta1.AKSNodeClass{},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			err := validateExistingCapacityReservation(tt.vm, tt.nodeClass)

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
		})
	}
}
