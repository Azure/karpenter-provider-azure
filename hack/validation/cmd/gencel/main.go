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

// gencel generates CEL validation rules from the authoritative AKS validation
// rules defined in pkg/apis/validation/aksrules.go.
//
// Usage:
//
//	go run ./hack/validation/cmd/gencel -type labels
//	go run ./hack/validation/cmd/gencel -type requirement-key
//	go run ./hack/validation/cmd/gencel -type taints
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/validation"
)

func main() {
	ruleType := flag.String("type", "", "Type of CEL rule to generate: labels, requirement-key, taints")
	flag.Parse()

	var rule string
	switch *ruleType {
	case "labels":
		rule = validation.GenerateLabelsCELRule()
	case "requirement-key":
		rule = validation.GenerateRequirementKeyCELRule()
	case "taints":
		rule = validation.GenerateTaintsCELRule()
	default:
		fmt.Fprintf(os.Stderr, "unknown rule type %q; expected: labels, requirement-key, taints\n", *ruleType)
		os.Exit(1)
	}

	fmt.Print(rule)
}
