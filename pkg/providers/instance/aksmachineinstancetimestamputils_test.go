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

		stringResult := AKSMachineTimestampToTag(result)
		if stringResult != "1970-01-01T00:00:00.00Z" {
			t.Errorf("Expected string %s, got %s", "1970-01-01T00:00:00.00Z", stringResult)
		}
	})

	t.Run("AKSMachineTimestampToTag", func(t *testing.T) {
		result := AKSMachineTimestampToTag(time.Date(2023, 12, 25, 10, 30, 45, 129456789, time.UTC))
		if result != "2023-12-25T10:30:45.12Z" {
			t.Errorf("Expected %s, got %s", "2023-12-25T10:30:45.12Z", result)
		}
	})

	t.Run("AKSMachineTimestampFromTag", func(t *testing.T) {
		parsed, err := AKSMachineTimestampFromTag("2023-12-25T10:30:45.12Z")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if !parsed.Equal(time.Date(2023, 12, 25, 10, 30, 45, 120000000, time.UTC)) {
			t.Errorf("Expected %v, got %v", time.Date(2023, 12, 25, 10, 30, 45, 120000000, time.UTC), parsed)
		}
	})

	t.Run("RoundTripConsistency", func(t *testing.T) {
		// Test that format -> parse -> format gives the same result
		formatted := AKSMachineTimestampToTag(NewAKSMachineTimestamp())
		parsed, err := AKSMachineTimestampFromTag(formatted)
		if err != nil {
			t.Errorf("Parse error: %v", err)
		}
		reFormatted := AKSMachineTimestampToTag(parsed)
		if formatted != reFormatted {
			t.Errorf("Round trip failed: %s != %s", formatted, reFormatted)
		}
	})

	t.Run("AKSMachineTimestampFromTag_InvalidFormat", func(t *testing.T) {
		_, err := AKSMachineTimestampFromTag("invalid-format")
		if err == nil {
			t.Error("Expected error for invalid format")
		}
	})

	t.Run("TimezoneHandling_NonUTCInput", func(t *testing.T) {
		// Test with a non-UTC timezone to ensure .UTC() conversion works correctly
		est, err := time.LoadLocation("America/New_York")
		if err != nil {
			t.Skipf("Could not load EST timezone: %v", err)
		}

		// Create time in EST (UTC-5) - use centisecond precision
		nonUTCTime := time.Date(2023, 12, 25, 10, 30, 45, 120000000, est)

		// Format should convert to UTC and show 15:30 (10:30 EST + 5 hours)
		formatted := AKSMachineTimestampToTag(nonUTCTime)
		expected := "2023-12-25T15:30:45.12Z"

		if formatted != expected {
			t.Errorf("Timezone conversion failed: expected %s, got %s", expected, formatted)
		}

		// Parse back and verify it equals the truncated UTC time
		parsed, err := AKSMachineTimestampFromTag(formatted)
		if err != nil {
			t.Errorf("Parse error: %v", err)
		}

		expectedTruncatedUTC := time.Date(2023, 12, 25, 15, 30, 45, 120000000, time.UTC)
		if !parsed.Equal(expectedTruncatedUTC) {
			t.Errorf("Round trip with timezone failed: expected %v, got %v", expectedTruncatedUTC, parsed)
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
