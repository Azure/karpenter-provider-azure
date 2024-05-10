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
func DefaultMaxPods(networkPlugin string, networkPluginMode string) int32 {
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
