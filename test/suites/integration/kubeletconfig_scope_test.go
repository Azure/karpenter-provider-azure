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
	const unsupportedModeReason = "this E2E requires aksmachineapi or aksmachineapiheaderbatch"

	tests := []struct {
		provisionMode  string
		expectedReason string
	}{
		{provisionMode: consts.ProvisionModeAKSMachineAPI, expectedReason: ""},
		{provisionMode: consts.ProvisionModeAKSMachineAPIHeaderBatch, expectedReason: ""},
		{provisionMode: consts.ProvisionModeBootstrappingClient, expectedReason: unsupportedModeReason},
		{provisionMode: consts.ProvisionModeAKSScriptless, expectedReason: unsupportedModeReason},
	}

	for _, test := range tests {
		t.Run(test.provisionMode, func(t *testing.T) {
			testEnv := &azure.Environment{ProvisionMode: test.provisionMode}
			if reason := kubeletOverrideE2ESkipReason(testEnv); reason != test.expectedReason {
				t.Fatalf("expected skip reason %q, got %q", test.expectedReason, reason)
			}
		})
	}
}
