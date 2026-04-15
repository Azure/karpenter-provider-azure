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

package v1beta1_test

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

func TestParseSpotMaxPrice(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
		expectVal   *int64
	}{
		{name: "-1 sentinel", input: "-1", expectError: false, expectVal: lo.ToPtr(int64(-1))},
		{name: "whole number 1", input: "1", expectError: false, expectVal: lo.ToPtr(int64(100000))},
		{name: "whole number 2", input: "2", expectError: false, expectVal: lo.ToPtr(int64(200000))},
		{name: "one decimal 1.2", input: "1.2", expectError: false, expectVal: lo.ToPtr(int64(120000))},
		{name: "five decimals 0.00001", input: "0.00001", expectError: false, expectVal: lo.ToPtr(int64(1))},
		{name: "five decimals 0.98765", input: "0.98765", expectError: false, expectVal: lo.ToPtr(int64(98765))},
		{name: "large value 100.0", input: "100.0", expectError: false, expectVal: lo.ToPtr(int64(10000000))},
		// Invalid: zero
		{name: "zero integer", input: "0", expectError: true},
		{name: "zero decimal 0.0", input: "0.0", expectError: true},
		{name: "zero 5 decimals 0.00000", input: "0.00000", expectError: true},
		// Invalid: negatives other than -1
		{name: "negative -0.1", input: "-0.1", expectError: true},
		{name: "negative -2", input: "-2", expectError: true},
		// Invalid: more than 5 decimal places
		{name: "6 decimals 1.234567", input: "1.234567", expectError: true},
		// Invalid: non-numeric
		{name: "alpha abc", input: "abc", expectError: true},
		// Invalid: trailing/leading dot
		{name: "trailing dot 1.", input: "1.", expectError: true},
		{name: "leading dot .1", input: ".1", expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := v1beta1.ParseSpotMaxPrice(tt.input)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred(), "expected error for input %q", tt.input)
			} else {
				g.Expect(err).To(BeNil(), "unexpected error for input %q: %v", tt.input, err)
				g.Expect(result).To(Equal(tt.expectVal))
			}
		})
	}
}

func TestSpotMaxPriceFixedMethod(t *testing.T) {
	g := NewWithT(t)

	// nil SpotMaxPrice returns nil
	spec := &v1beta1.AKSNodeClassSpec{}
	result, err := spec.SpotMaxPriceFixed()
	g.Expect(err).To(BeNil())
	g.Expect(result).To(BeNil())

	// "-1" returns -1
	spec.SpotMaxPrice = lo.ToPtr("-1")
	result, err = spec.SpotMaxPriceFixed()
	g.Expect(err).To(BeNil())
	g.Expect(result).ToNot(BeNil())
	g.Expect(*result).To(Equal(int64(-1)))

	// "0.98765" returns 98765
	spec.SpotMaxPrice = lo.ToPtr("0.98765")
	result, err = spec.SpotMaxPriceFixed()
	g.Expect(err).To(BeNil())
	g.Expect(result).ToNot(BeNil())
	g.Expect(*result).To(Equal(int64(98765)))

	// "2" returns 200000
	spec.SpotMaxPrice = lo.ToPtr("2")
	result, err = spec.SpotMaxPriceFixed()
	g.Expect(err).To(BeNil())
	g.Expect(result).ToNot(BeNil())
	g.Expect(*result).To(Equal(int64(200000)))
}
