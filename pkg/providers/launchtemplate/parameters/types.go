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

package parameters

import (
	"github.com/Azure/karpenter/pkg/providers/imagefamily/bootstrap"
)

// StaticParameters define the static launch template parameters
type StaticParameters struct {
	ClusterName                    string
	ClusterEndpoint                string
	CABundle                       *string
	Arch                           string
	GPUNode                        bool
	GPUDriverVersion               string
	GPUImageSHA                    string
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

	Tags   map[string]string
	Labels map[string]string
}

// Parameters adds the dynamically generated launch template parameters
type Parameters struct {
	*StaticParameters
	UserData bootstrap.Bootstrapper
	ImageID  string
}
