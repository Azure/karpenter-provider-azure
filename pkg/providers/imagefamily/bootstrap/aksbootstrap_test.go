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

package bootstrap

import (
	_ "embed"
	"fmt"
	"testing"

	nbcontractv1 "github.com/Azure/agentbaker/pkg/proto/nbcontract/v1"
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

func Test_getCustomDataFromNodeBootstrapContract(t *testing.T) {
	type args struct {
		nbcp *nbcontractv1.Configuration
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "Test with staticNodeBootstrapVars and expect no error",
			args: args{
				nbcp: &staticNodeBootstrapVars,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := getCustomDataFromNodeBootstrapContract(tt.args.nbcp)
			if (err != nil) != tt.wantErr {
				t.Errorf("getCustomDataFromNodeBootstrapContract() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			// Didn't check the actual value of customData here. Only check if there is an error.
		})
	}
}
