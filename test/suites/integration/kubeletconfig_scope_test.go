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

package integration_test

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
)

func TestKubeletOverrideE2ESkipReason(t *testing.T) {
	const unsupportedModeReason = "this E2E requires an explicitly resolved provision mode that supports kubelet resource and eviction overrides"

	tests := []struct {
		name                       string
		provisionMode              string
		provisionModeExplicitlySet bool
		expectedReason             string
	}{
		{name: "explicit AKS Scriptless", provisionMode: consts.ProvisionModeAKSScriptless, provisionModeExplicitlySet: true},
		{name: "AKS Machine API", provisionMode: consts.ProvisionModeAKSMachineAPI, provisionModeExplicitlySet: true},
		{name: "AKS Machine API header batch", provisionMode: consts.ProvisionModeAKSMachineAPIHeaderBatch, provisionModeExplicitlySet: true},
		{name: "BootstrappingClient", provisionMode: consts.ProvisionModeBootstrappingClient, provisionModeExplicitlySet: true, expectedReason: unsupportedModeReason},
		{name: "unresolved defaulted AKS Scriptless", provisionMode: consts.ProvisionModeAKSScriptless, expectedReason: unsupportedModeReason},
		{name: "unresolved AKS Machine API", provisionMode: consts.ProvisionModeAKSMachineAPI, expectedReason: unsupportedModeReason},
		{name: "unresolved AKS Machine API header batch", provisionMode: consts.ProvisionModeAKSMachineAPIHeaderBatch, expectedReason: unsupportedModeReason},
		{name: "empty", provisionMode: "", expectedReason: unsupportedModeReason},
		{name: "unknown", provisionMode: "unknown", provisionModeExplicitlySet: true, expectedReason: unsupportedModeReason},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testEnv := &azure.Environment{
				ProvisionMode:              test.provisionMode,
				ProvisionModeExplicitlySet: test.provisionModeExplicitlySet,
			}
			if reason := kubeletOverrideE2ESkipReason(testEnv); reason != test.expectedReason {
				t.Fatalf("expected skip reason %q, got %q", test.expectedReason, reason)
			}
		})
	}
}
