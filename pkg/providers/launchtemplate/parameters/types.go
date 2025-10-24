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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
)

// StaticParameters define the static launch template parameters
type StaticParameters struct {
	ClusterName                    string
	ClusterEndpoint                string
	CABundle                       *string
	Arch                           string
	GPUNode                        bool
	GPUDriverVersion               string
	GPUDriverType                  string
	GPUImageSHA                    string
	TenantID                       string
	SubscriptionID                 string
	KubeletIdentityClientID        string
	Location                       string
	ResourceGroup                  string
	ClusterID                      string
	APIServerName                  string
	KubeletClientTLSBootstrapToken string
	EnableSecureTLSBootstrapping   bool
	NetworkPlugin                  string
	NetworkPolicy                  string
	KubernetesVersion              string
	SubnetID                       string
	ClusterResourceGroup           string

	Labels map[string]string
}

// Parameters adds the dynamically generated launch template parameters
type Parameters struct {
	*StaticParameters
	ScriptlessCustomData           bootstrap.Bootstrapper
	CustomScriptsNodeBootstrapping customscriptsbootstrap.Bootstrapper
	ImageID                        string
	StorageProfileDiskType         string
	StorageProfileIsEphemeral      bool
	StorageProfilePlacement        armcompute.DiffDiskPlacement
	StorageProfileSizeGB           int32
	IsWindows                      bool
}
