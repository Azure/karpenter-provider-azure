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

// Extracted from suite_test.go — KubeReservedResources table-driven tests.
package instancetype_test

import (
	"testing"

	v1 "k8s.io/api/core/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
)

func TestKubeReservedResources(t *testing.T) {
	tests := []struct {
		name           string
		cpus           int64
		memory         float64
		expectedCPU    string
		expectedMemory string
	}{
		{
			name:           "4 cores, 7GiB",
			cpus:           4,
			memory:         7.0,
			expectedCPU:    "140m",
			expectedMemory: "1638Mi",
		},
		{
			name:           "2 cores, 8GiB",
			cpus:           2,
			memory:         8.0,
			expectedCPU:    "100m",
			expectedMemory: "1843Mi",
		},
		{
			name:           "3 cores, 64GiB",
			cpus:           3,
			memory:         64.0,
			expectedCPU:    "120m",
			expectedMemory: "5611Mi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := instancetype.KubeReservedResources(tt.cpus, tt.memory)
			gotCPU := resources[v1.ResourceCPU]
			gotMemory := resources[v1.ResourceMemory]

			if gotCPU.String() != tt.expectedCPU {
				t.Errorf("CPU = %q, want %q", gotCPU.String(), tt.expectedCPU)
			}
			if gotMemory.String() != tt.expectedMemory {
				t.Errorf("Memory = %q, want %q", gotMemory.String(), tt.expectedMemory)
			}
		})
	}
}
