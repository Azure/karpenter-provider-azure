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
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	core "k8s.io/api/core/v1"
)

// Options is the node bootstrapping parameters passed from Karpenter to the provisioning node
type Options struct {
	ClusterName      string
	ClusterEndpoint  string
	KubeletConfig    *v1alpha2.KubeletConfiguration
	Taints           []core.Taint      `hash:"set"`
	Labels           map[string]string `hash:"set"`
	CABundle         *string
	GPUNode          bool
	GPUDriverVersion string
	GPUImageSHA      string
	SubnetID         string
}

// Bootstrapper can be implemented to generate a bootstrap script
// that uses the params from the Bootstrap type for a specific
// bootstrapping method.
// The only one implemented right now is AKS bootstrap script
type Bootstrapper interface {
	Script() (string, error)
}
