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
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type KubeletConfiguration struct {
	v1beta1.KubeletConfiguration

	// MaxPods is the maximum number of pods that can run on a worker node instance.
	MaxPods int32
	// The IP address of cluster DNS (kube-dns service within kube-system namespace) service
	ClusterDNSServiceIP string

	SystemReserved map[string]string
	// KubeReserved contains resources reserved for Kubernetes system components.
	KubeReserved map[string]string
	// EvictionHard is the map of signal names to quantities that define hard eviction thresholds
	EvictionHard map[string]string
	// EvictionSoft is the map of signal names to quantities that define soft eviction thresholds
	EvictionSoft map[string]string
	// EvictionSoftGracePeriod is the map of signal names to quantities that define grace periods for each eviction signal
	EvictionSoftGracePeriod map[string]metav1.Duration
	// EvictionMaxPodGracePeriod is the maximum allowed grace period (in seconds) to use when terminating pods in
	// response to soft eviction thresholds being met.
	EvictionMaxPodGracePeriod *int32
}

// Options is the node bootstrapping parameters passed from Karpenter to the provisioning node
type Options struct {
	ClusterName      string
	ClusterEndpoint  string
	KubeletConfig    *KubeletConfiguration
	Taints           []core.Taint      `hash:"set"`
	Labels           map[string]string `hash:"set"`
	CABundle         *string
	GPUNode          bool
	GPUDriverVersion string
	GPUDriverType    string
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
