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

package customscriptsbootstrap

import (
	"fmt"
	"math"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
)

func TestReverseVMMemoryOverhead(t *testing.T) {
	type Cases struct {
		name                    string
		originalGiB             int64
		vmMemoryOverheadPercent float64
	}
	var cases []Cases

	// Generate test cases from originalGiB 1 to 2000 x vmMemoryOverheadPercent 0 to 0.25 using for loops
	// In fact, mathematically, the reverse won't be perfectly equal, but current accuracy in GiB should be enough
	for i := 1; i <= 2000; i++ {
		for j := 0; j <= 250; j++ {
			cases = append(cases, Cases{
				name:                    fmt.Sprintf("Test %d - %f", i, float64(j)/1000),
				originalGiB:             int64(i),
				vmMemoryOverheadPercent: float64(j) / 1000,
			})
		}
	}
	t.Run("2000 x 0.25", func(t *testing.T) {
		for _, tc := range cases {
			subtracted := instancetype.CalculateMemoryWithoutOverhead(tc.vmMemoryOverheadPercent, float64(tc.originalGiB)).Value()
			reversedGiB := int64(math.Round(reverseVMMemoryOverhead(tc.vmMemoryOverheadPercent, float64(subtracted)) / 1024 / 1024 / 1024))
			if tc.originalGiB != reversedGiB {
				t.Errorf("Expected %d but got %d", tc.originalGiB, reversedGiB)
			}
		}
	})
}
