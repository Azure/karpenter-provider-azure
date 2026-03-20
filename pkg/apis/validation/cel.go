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

// Package validation provides CEL rule generation from the authoritative
// AKS validation rules.
package validation

import (
	"fmt"
	"strings"
)

// GenerateLabelsCELRule generates the CEL expression for validating labels on a map
// (e.g., NodePool .spec.template.metadata.labels).
// The rule blocks kubernetes.azure.com/* labels unless they are in the allowlist.
//
// The generated rule uses `self.all(x, ...)` since it validates map keys.
func GenerateLabelsCELRule() string {
	return fmt.Sprintf(
		`self.all(x, x in [%s] || !x.find("^([^/]+)").endsWith("%s"))`,
		quotedList(AllowedAKSUserLabels),
		AKSLabelDomain,
	)
}

// GenerateRequirementKeyCELRule generates the CEL expression for validating a single
// requirement key (e.g., NodePool .spec.template.spec.requirements[].key).
// The rule blocks kubernetes.azure.com/* keys unless they are in the allowlist.
//
// The generated rule uses `self` since it validates a single string value.
func GenerateRequirementKeyCELRule() string {
	return fmt.Sprintf(
		`self in [%s] || !self.find("^([^/]+)").endsWith("%s")`,
		quotedList(AllowedAKSUserLabels),
		AKSLabelDomain,
	)
}

// GenerateTaintsCELRule generates the CEL expression for validating taints on an array
// (e.g., NodePool .spec.template.spec.taints or .spec.template.spec.startupTaints).
// The rule blocks kubernetes.azure.com/* taint keys unless they are in the allowlist.
//
// Uses startsWith instead of find+endsWith for taint keys because:
// 1. CEL cost estimation is much cheaper for startsWith (no regex)
// 2. AKS RP checks exact domain match (not subdomains), so startsWith is equivalent
// 3. The find+endsWith pattern on array items exceeds CEL cost budget
func GenerateTaintsCELRule() string {
	return fmt.Sprintf(
		`self.all(x, x.key in [%s] || !x.key.startsWith("%s/"))`,
		quotedList(AllowedAKSUserTaintKeys),
		AKSLabelDomain,
	)
}

// quotedList returns a comma-separated list of double-quoted strings.
func quotedList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = fmt.Sprintf(`"%s"`, item)
	}
	return strings.Join(quoted, ", ")
}
