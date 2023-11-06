// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package bootstrap

import (
	"fmt"
	"testing"
)

func TestKubeBinaryURL(t *testing.T) {
	cases := []struct {
		name     string
		version  string
		expected string
	}{
		{
			name:     "Test version 1.24.x",
			version:  "1.24.5",
			expected: fmt.Sprintf("%s/kubernetes/v1.24.5/binaries/kubernetes-node-linux-amd64.tar.gz", globalAKSMirror),
		},
		{
			name:     "Test version 1.25.x",
			version:  "1.25.2",
			expected: fmt.Sprintf("%s/kubernetes/v1.25.2/binaries/kubernetes-node-linux-amd64.tar.gz", globalAKSMirror),
		},
		{
			name:     "Test version 1.26.x",
			version:  "1.26.0",
			expected: fmt.Sprintf("%s/kubernetes/v1.26.0/binaries/kubernetes-node-linux-amd64.tar.gz", globalAKSMirror),
		},
		{
			name:     "Test version 1.27.x",
			version:  "1.27.1",
			expected: fmt.Sprintf("%s/kubernetes/v1.27.1/binaries/kubernetes-node-linux-amd64.tar.gz", globalAKSMirror),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual := kubeBinaryURL(tc.version, "amd64")
			if actual != tc.expected {
				t.Errorf("Expected %s but got %s", tc.expected, actual)
			}
		})
	}
}
