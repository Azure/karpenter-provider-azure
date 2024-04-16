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

	"github.com/Azure/go-autorest/autorest/to"
	core "k8s.io/api/core/v1"
	corev1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"
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

func TestAKS_aksBootstrapScript(t *testing.T) {
	type fields struct {
		Options                        Options
		Arch                           string
		TenantID                       string
		SubscriptionID                 string
		UserAssignedIdentityID         string
		Location                       string
		ResourceGroup                  string
		ClusterID                      string
		APIServerName                  string
		KubeletClientTLSBootstrapToken string
		NetworkPlugin                  string
		NetworkPolicy                  string
		KubernetesVersion              string
	}
	tests := []struct {
		name    string
		fields  fields
		want    string
		wantErr bool
	}{
		{
			name: "Test with all fields and expect no error",
			fields: fields{
				Options: Options{
					ClusterName:     "clustername",
					ClusterEndpoint: "clusterendpoint",
					KubeletConfig:   &corev1beta1.KubeletConfiguration{},
					Taints:          []core.Taint{},
					Labels:          map[string]string{},
					CABundle:        to.StringPtr("cabundle"),
					VMSize:          "vmsize",
				},
				Arch:                           "amd64",
				TenantID:                       "tenantid",
				SubscriptionID:                 "subscriptionid",
				UserAssignedIdentityID:         "userassignedidentityid",
				Location:                       "location",
				ResourceGroup:                  "resourcegroup",
				ClusterID:                      "clusterid",
				APIServerName:                  "apiservername",
				KubeletClientTLSBootstrapToken: "kubeletclienttlsbootstraptoken",
				NetworkPlugin:                  "networkplugin",
				NetworkPolicy:                  "networkpolicy",
				KubernetesVersion:              "1.24.5",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := AKS{
				Options:                        tt.fields.Options,
				Arch:                           tt.fields.Arch,
				TenantID:                       tt.fields.TenantID,
				SubscriptionID:                 tt.fields.SubscriptionID,
				UserAssignedIdentityID:         tt.fields.UserAssignedIdentityID,
				Location:                       tt.fields.Location,
				ResourceGroup:                  tt.fields.ResourceGroup,
				ClusterID:                      tt.fields.ClusterID,
				APIServerName:                  tt.fields.APIServerName,
				KubeletClientTLSBootstrapToken: tt.fields.KubeletClientTLSBootstrapToken,
				NetworkPlugin:                  tt.fields.NetworkPlugin,
				NetworkPolicy:                  tt.fields.NetworkPolicy,
				KubernetesVersion:              tt.fields.KubernetesVersion,
			}
			_, err := a.aksBootstrapScript()
			// Didn't check the actual value of customData here. Only check if there is an error.
			if (err != nil) != tt.wantErr {
				t.Errorf("AKS.aksBootstrapScript() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}
