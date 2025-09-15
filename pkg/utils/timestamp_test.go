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

package utils

import (
	"testing"
	"time"
)

func TestCreationTimestampUtilities(t *testing.T) {
	// Test time for consistency
	testTime := time.Date(2023, 12, 25, 10, 30, 45, 123000000, time.UTC)
	expectedFormat := "2023-12-25T10:30:45.123Z"

	t.Run("GetStringFromCreationTimestamp", func(t *testing.T) {
		result := GetStringFromCreationTimestamp(testTime)
		if result != expectedFormat {
			t.Errorf("Expected %s, got %s", expectedFormat, result)
		}
	})

	t.Run("GetCreationTimestampFromString", func(t *testing.T) {
		parsed, err := GetCreationTimestampFromString(expectedFormat)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if !parsed.Equal(testTime) {
			t.Errorf("Expected %v, got %v", testTime, parsed)
		}
	})

	t.Run("RoundTripConsistency", func(t *testing.T) {
		// Test that format -> parse -> format gives the same result
		formatted := GetStringFromCreationTimestamp(testTime)
		parsed, err := GetCreationTimestampFromString(formatted)
		if err != nil {
			t.Errorf("Parse error: %v", err)
		}
		reFormatted := GetStringFromCreationTimestamp(parsed)
		if formatted != reFormatted {
			t.Errorf("Round trip failed: %s != %s", formatted, reFormatted)
		}
	})

	t.Run("GetCreationTimestampFromString_InvalidFormat", func(t *testing.T) {
		_, err := GetCreationTimestampFromString("invalid-format")
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
		
		// Create time in EST (UTC-5)
		nonUTCTime := time.Date(2023, 12, 25, 10, 30, 45, 123000000, est)
		
		// Format should convert to UTC and show 15:30 (10:30 EST + 5 hours)
		formatted := GetStringFromCreationTimestamp(nonUTCTime)
		expected := "2023-12-25T15:30:45.123Z"
		
		if formatted != expected {
			t.Errorf("Timezone conversion failed: expected %s, got %s", expected, formatted)
		}
		
		// Parse back and verify it equals the original time
		parsed, err := GetCreationTimestampFromString(formatted)
		if err != nil {
			t.Errorf("Parse error: %v", err)
		}
		
		if !parsed.Equal(nonUTCTime) {
			t.Errorf("Round trip with timezone failed: original %v != parsed %v", nonUTCTime, parsed)
		}
	})
}
