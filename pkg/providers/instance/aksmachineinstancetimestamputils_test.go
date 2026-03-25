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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// nolint: gocyclo
func TestCreationTimestampUtilities(t *testing.T) {
	t.Run("ZeroAKSMachineTimestamp", func(t *testing.T) {
		result := ZeroAKSMachineTimestamp()
		expected := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
		if !result.Equal(expected) {
			t.Errorf("Expected %v, got %v", expected, result)
		}
	})

	t.Run("AKSMachineTimestampToMeta", func(t *testing.T) {
		testTime := NewAKSMachineTimestamp()
		var expected = metav1.Time{Time: testTime}
		result := AKSMachineTimestampToMeta(testTime)
		if !result.Time.Equal(expected.Time) {
			t.Errorf("Expected %v, got %v", expected.Time, result.Time)
		}
	})
}
