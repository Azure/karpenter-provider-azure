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

package options_test

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	. "github.com/onsi/gomega"
)

func TestIsIPv6Enabled(t *testing.T) {
	cases := []struct {
		name       string
		ipFamilies []string
		expected   bool
	}{
		{name: "nil families", ipFamilies: nil, expected: false},
		{name: "empty families", ipFamilies: []string{}, expected: false},
		{name: "IPv4 only", ipFamilies: []string{"IPv4"}, expected: false},
		{name: "dual-stack IPv4,IPv6", ipFamilies: []string{"IPv4", "IPv6"}, expected: true},
		{name: "IPv6 only", ipFamilies: []string{"IPv6"}, expected: true},
		{name: "case-insensitive ipv6", ipFamilies: []string{"ipv4", "ipv6"}, expected: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			opts := &options.Options{NodeIPFamilies: c.ipFamilies}
			g.Expect(opts.IsIPv6Enabled()).To(Equal(c.expected))
		})
	}
}
