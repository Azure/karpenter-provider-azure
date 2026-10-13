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

package azure

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
)

func TestSetProvisionModeFromEnvironment(t *testing.T) {
	tests := []struct {
		name                  string
		value                 string
		expectedMode          string
		expectedExplicitlySet bool
	}{
		{name: "unset defaults to unresolved AKS Scriptless", expectedMode: consts.ProvisionModeAKSScriptless},
		{name: "empty value defaults to unresolved AKS Scriptless", value: "", expectedMode: consts.ProvisionModeAKSScriptless},
		{name: "explicit AKS Scriptless preserves provenance", value: consts.ProvisionModeAKSScriptless, expectedMode: consts.ProvisionModeAKSScriptless, expectedExplicitlySet: true},
		{name: "explicit Machine API preserves provenance", value: consts.ProvisionModeAKSMachineAPI, expectedMode: consts.ProvisionModeAKSMachineAPI, expectedExplicitlySet: true},
		{name: "explicit Machine API header batch preserves provenance", value: consts.ProvisionModeAKSMachineAPIHeaderBatch, expectedMode: consts.ProvisionModeAKSMachineAPIHeaderBatch, expectedExplicitlySet: true},
		{name: "explicit BootstrappingClient preserves provenance", value: consts.ProvisionModeBootstrappingClient, expectedMode: consts.ProvisionModeBootstrappingClient, expectedExplicitlySet: true},
		{name: "explicit unknown mode preserves provenance", value: "unknown", expectedMode: "unknown", expectedExplicitlySet: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testEnv := &Environment{}
			testEnv.setProvisionModeFromEnvironment(test.value)
			if testEnv.ProvisionMode != test.expectedMode {
				t.Fatalf("expected provision mode %q, got %q", test.expectedMode, testEnv.ProvisionMode)
			}
			if testEnv.ProvisionModeExplicitlySet != test.expectedExplicitlySet {
				t.Fatalf("expected explicitly set to be %t, got %t", test.expectedExplicitlySet, testEnv.ProvisionModeExplicitlySet)
			}
		})
	}
}
