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

package validation

import (
	"testing"
)

func TestGenerateLabelsCELRule(t *testing.T) {
	rule := GenerateLabelsCELRule()
	if rule == "" {
		t.Fatal("GenerateLabelsCELRule returned empty string")
	}
	// Verify it contains the domain
	if !contains(rule, AKSLabelDomain) {
		t.Errorf("expected rule to contain %q, got: %s", AKSLabelDomain, rule)
	}
	// Verify it contains all allowed labels
	for _, label := range AllowedAKSUserLabels {
		if !contains(rule, label) {
			t.Errorf("expected rule to contain allowed label %q, got: %s", label, rule)
		}
	}
	// Verify it uses self.all pattern
	if !contains(rule, "self.all(x,") {
		t.Errorf("expected rule to use self.all pattern, got: %s", rule)
	}
}

func TestGenerateRequirementKeyCELRule(t *testing.T) {
	rule := GenerateRequirementKeyCELRule()
	if rule == "" {
		t.Fatal("GenerateRequirementKeyCELRule returned empty string")
	}
	// Verify it uses self pattern (not self.all)
	if !contains(rule, "self in [") {
		t.Errorf("expected rule to use 'self in' pattern, got: %s", rule)
	}
	// Verify it contains the domain
	if !contains(rule, AKSLabelDomain) {
		t.Errorf("expected rule to contain %q, got: %s", AKSLabelDomain, rule)
	}
}

func TestGenerateTaintsCELRule(t *testing.T) {
	rule := GenerateTaintsCELRule()
	if rule == "" {
		t.Fatal("GenerateTaintsCELRule returned empty string")
	}
	// Verify it contains taint allowlist
	for _, key := range AllowedAKSUserTaintKeys {
		if !contains(rule, key) {
			t.Errorf("expected rule to contain allowed taint key %q, got: %s", key, rule)
		}
	}
	// Verify it checks x.key (taint key field)
	if !contains(rule, "x.key") {
		t.Errorf("expected rule to check x.key, got: %s", rule)
	}
}

func TestAllowedAKSUserLabelsNotEmpty(t *testing.T) {
	if len(AllowedAKSUserLabels) == 0 {
		t.Fatal("AllowedAKSUserLabels is empty")
	}
	for _, label := range AllowedAKSUserLabels {
		if label == "" {
			t.Error("AllowedAKSUserLabels contains empty string")
		}
		if !contains(label, AKSLabelDomain+"/") {
			t.Errorf("AllowedAKSUserLabels entry %q does not start with %s/", label, AKSLabelDomain)
		}
	}
}

func TestAllowedAKSUserTaintKeysNotEmpty(t *testing.T) {
	if len(AllowedAKSUserTaintKeys) == 0 {
		t.Fatal("AllowedAKSUserTaintKeys is empty")
	}
	for _, key := range AllowedAKSUserTaintKeys {
		if key == "" {
			t.Error("AllowedAKSUserTaintKeys contains empty string")
		}
		if !contains(key, AKSLabelDomain+"/") {
			t.Errorf("AllowedAKSUserTaintKeys entry %q does not start with %s/", key, AKSLabelDomain)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
