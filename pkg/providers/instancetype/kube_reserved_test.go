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

package instancetype_test

import (
	"testing"

	v1 "k8s.io/api/core/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
)

// TestKubeReservedResources tests the kubereserved resource calculation formula
// without any provisioning or API calls.
func TestKubeReservedResources(t *testing.T) {
	tests := []struct {
		name           string
		cpus           int64
		memoryGiB      float64
		wantCPU        string
		wantMemory     string
	}{
		{
			name:       "4 cores, 7GiB",
			cpus:       4,
			memoryGiB:  7.0,
			wantCPU:    "140m",
			wantMemory: "1638Mi",
		},
		{
			name:       "2 cores, 8GiB",
			cpus:       2,
			memoryGiB:  8.0,
			wantCPU:    "100m",
			wantMemory: "1843Mi",
		},
		{
			name:       "3 cores, 64GiB",
			cpus:       3,
			memoryGiB:  64.0,
			wantCPU:    "120m",
			wantMemory: "5611Mi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := instancetype.KubeReservedResources(tt.cpus, tt.memoryGiB)

			gotCPU := resources[v1.ResourceCPU]
			gotMemory := resources[v1.ResourceMemory]

			if gotCPU.String() != tt.wantCPU {
				t.Errorf("KubeReservedResources() CPU = %v, want %v", gotCPU.String(), tt.wantCPU)
			}

			if gotMemory.String() != tt.wantMemory {
				t.Errorf("KubeReservedResources() Memory = %v, want %v", gotMemory.String(), tt.wantMemory)
			}
		})
	}
}
