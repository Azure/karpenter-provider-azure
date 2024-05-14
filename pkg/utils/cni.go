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
package utils

import (
	"github.com/Azure/karpenter-provider-azure/pkg/apis/consts"
)

const (
	// defaultKubernetesMaxPodsAzureOverlay is the maximum number of pods to run on a node for Azure CNI Overlay.
	defaultKubernetesMaxPodsAzureOverlay = 250

	// defaultKubernetesMaxPodsAzure is the maximum number of pods to run on a node for Azure CNI
	defaultKubernetesMaxPodsAzure = 30

	// defaultKubernetesMaxPodsKubenet is the maximum number of pods to run on a node for Kubenet.
	defaultKubernetesMaxPodsKubenet = 100
	// defaultKubernetesMaxPods is the maximum number of pods on a node.
	defaultKubernetesMaxPods = 110
)

// DefaultMaxPods returns for a given network plugin the default value for pods per node
func DefaultMaxPods(networkPlugin, networkPluginMode string) int {
	if networkPlugin == consts.NetworkPluginAzure && networkPluginMode == consts.PodNetworkTypeOverlay {
		return defaultKubernetesMaxPodsAzureOverlay
	}
	// Pod
	if networkPlugin == consts.NetworkPluginAzure {
		return defaultKubernetesMaxPodsAzure
	}
	if networkPlugin == consts.NetworkPluginKubenet {
		return defaultKubernetesMaxPodsKubenet
	}
	return defaultKubernetesMaxPods
}
