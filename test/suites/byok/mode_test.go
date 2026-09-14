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
package byok_test

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
)

func TestShouldSkipDiskEncryptionOverride(t *testing.T) {
	tests := []struct {
		name                string
		provisionMode       string
		inClusterController bool
		want                bool
	}{
		{name: "self-hosted Machine API", provisionMode: consts.ProvisionModeAKSMachineAPI, inClusterController: true, want: true},
		{name: "self-hosted Machine API header batch", provisionMode: consts.ProvisionModeAKSMachineAPIHeaderBatch, inClusterController: true, want: true},
		{name: "NAP Machine API", provisionMode: consts.ProvisionModeAKSMachineAPI, want: false},
		{name: "NAP Machine API header batch", provisionMode: consts.ProvisionModeAKSMachineAPIHeaderBatch, want: false},
		{name: "self-hosted scriptless", provisionMode: consts.ProvisionModeAKSScriptless, inClusterController: true, want: false},
		{name: "NAP bootstrapping client", provisionMode: consts.ProvisionModeBootstrappingClient, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldSkipDiskEncryptionOverride(test.provisionMode, test.inClusterController); got != test.want {
				t.Fatalf("got %t, want %t", got, test.want)
			}
		})
	}
}
