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
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
)

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

func TestSetVMPropertiesBillingProfile(t *testing.T) {
	tests := []struct {
		name             string
		capacityType     string
		spotMaxPrice     *string
		expectBilling    bool
		expectedMaxPrice float64
		expectEviction   bool
	}{
		{
			name:           "on-demand: no billing profile set",
			capacityType:   karpv1.CapacityTypeOnDemand,
			spotMaxPrice:   nil,
			expectBilling:  false,
			expectEviction: false,
		},
		{
			name:             "spot with nil SpotMaxPrice defaults to -1",
			capacityType:     karpv1.CapacityTypeSpot,
			spotMaxPrice:     nil,
			expectBilling:    true,
			expectedMaxPrice: -1,
			expectEviction:   true,
		},
		{
			name:             "spot with SpotMaxPrice=-1 sets -1",
			capacityType:     karpv1.CapacityTypeSpot,
			spotMaxPrice:     lo.ToPtr("-1"),
			expectBilling:    true,
			expectedMaxPrice: -1,
			expectEviction:   true,
		},
		{
			name:             "spot with SpotMaxPrice=0.5",
			capacityType:     karpv1.CapacityTypeSpot,
			spotMaxPrice:     lo.ToPtr("0.5"),
			expectBilling:    true,
			expectedMaxPrice: 0.5,
			expectEviction:   true,
		},
		{
			name:             "spot with SpotMaxPrice=0.98765",
			capacityType:     karpv1.CapacityTypeSpot,
			spotMaxPrice:     lo.ToPtr("0.98765"),
			expectBilling:    true,
			expectedMaxPrice: 0.98765,
			expectEviction:   true,
		},
		{
			name:             "spot with SpotMaxPrice=100.0",
			capacityType:     karpv1.CapacityTypeSpot,
			spotMaxPrice:     lo.ToPtr("100.0"),
			expectBilling:    true,
			expectedMaxPrice: 100.0,
			expectEviction:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			nodeClass := &v1beta1.AKSNodeClass{}
			nodeClass.Spec.SpotMaxPrice = tt.spotMaxPrice

			vmProperties := &armcompute.VirtualMachineProperties{}
			setVMPropertiesBillingProfile(vmProperties, tt.capacityType, nodeClass)

			if tt.expectBilling {
				g.Expect(vmProperties.BillingProfile).ToNot(BeNil())
				g.Expect(vmProperties.BillingProfile.MaxPrice).ToNot(BeNil())
				g.Expect(*vmProperties.BillingProfile.MaxPrice).To(Equal(tt.expectedMaxPrice))
			} else {
				g.Expect(vmProperties.BillingProfile).To(BeNil())
			}

			if tt.expectEviction {
				g.Expect(vmProperties.EvictionPolicy).ToNot(BeNil())
				g.Expect(*vmProperties.EvictionPolicy).To(Equal(armcompute.VirtualMachineEvictionPolicyTypesDelete))
			} else {
				g.Expect(vmProperties.EvictionPolicy).To(BeNil())
			}
		})
	}
}
