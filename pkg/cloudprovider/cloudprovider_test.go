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

package cloudprovider

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestGenerateNodeClaimName(t *testing.T) {
	tests := []struct {
		name     string
		vmName   string
		expected string
	}{
		{
			name:     "basic",
			vmName:   "aks-default-a1b2c",
			expected: "default-a1b2c",
		},
		{
			name:     "dashes nodepool name",
			vmName:   "aks-node-pool-name-a1b2c",
			expected: "node-pool-name-a1b2c",
		},
		{
			name:     "aks",
			vmName:   "aks-aks-default-a1b2c",
			expected: "aks-default-a1b2c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result := GetNodeClaimNameFromVMName(tt.vmName)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}
