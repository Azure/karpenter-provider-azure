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

package clusterdns

import (
	"context"
	"fmt"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ClusterDNSProvider interface {
	ClusterDNS(ctx context.Context) (string, error)
}

type clusterDNSProvider struct {
	kubernetesInterface kubernetes.Interface
}

func NewClusterDNSProvider(kubernetesInterface kubernetes.Interface) *clusterDNSProvider {
	return &clusterDNSProvider{
		kubernetesInterface: kubernetesInterface,
	}
}

func (p *clusterDNSProvider) ClusterDNS(ctx context.Context) (string, error) {
	// First test if DNS IP is present in config, use it if it is.
	dnsServiceIP := options.FromContext(ctx).DNSServiceIP
	if len(dnsServiceIP) > 0 {
		return dnsServiceIP, nil
	}
	// Try to discover DNS service IP address
	dnsService, err := p.kubernetesInterface.CoreV1().Services("kube-system").Get(ctx, "kube-dns", metav1.GetOptions{})
	if err != nil {
		log.FromContext(ctx).Info(fmt.Sprintf("Failed to discover kube-dns in kube-system, error message: %s", err))
		return "", err
	}

	if dnsService != nil && len(dnsService.Spec.ClusterIP) > 0 {
		return dnsService.Spec.ClusterIP, nil
	}
	return "", fmt.Errorf("couldn't find cluster dns service ip address")
}
