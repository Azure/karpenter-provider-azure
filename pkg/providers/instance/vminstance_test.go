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

	. "github.com/onsi/gomega"
	"github.com/samber/lo"

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
