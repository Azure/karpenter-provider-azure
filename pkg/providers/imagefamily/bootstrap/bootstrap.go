// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package bootstrap

import (
	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	core "k8s.io/api/core/v1"
)

// Options is the node bootstrapping parameters passed from Karpenter to the provisioning node
type Options struct {
	ClusterName      string
	ClusterEndpoint  string
	KubeletConfig    *corev1beta1.KubeletConfiguration
	Taints           []core.Taint      `hash:"set"`
	Labels           map[string]string `hash:"set"`
	CABundle         *string
	GPUNode          bool
	GPUDriverVersion string
}

// Bootstrapper can be implemented to generate a bootstrap script
// that uses the params from the Bootstrap type for a specific
// bootstrapping method.
// The only one implemented right now is AKS bootstrap script
type Bootstrapper interface {
	Script() (string, error)
}
