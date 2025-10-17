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

package kubernetesversion

import (
	"context"
	"strings"

	"github.com/patrickmn/go-cache"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

const (
	kubernetesVersionCacheKey = "kubernetesVersion"
)

type KubernetesVersionProvider interface {
	KubeServerVersion(ctx context.Context) (string, error)
}

type kubernetesVersionProvider struct {
	kubernetesInterface    kubernetes.Interface
	kubernetesVersionCache *cache.Cache
	cm                     *pretty.ChangeMonitor
}

func NewKubernetesVersionProvider(kubernetesInterface kubernetes.Interface, kubernetesVersionCache *cache.Cache) *kubernetesVersionProvider {
	return &kubernetesVersionProvider{
		kubernetesInterface:    kubernetesInterface,
		kubernetesVersionCache: kubernetesVersionCache,
		cm:                     pretty.NewChangeMonitor(),
	}
}

func (p *kubernetesVersionProvider) KubeServerVersion(ctx context.Context) (string, error) {
	if version, ok := p.kubernetesVersionCache.Get(kubernetesVersionCacheKey); ok {
		return version.(string), nil
	}
	serverVersion, err := p.kubernetesInterface.Discovery().ServerVersion()
	if err != nil {
		return "", err
	}
	version := strings.TrimPrefix(serverVersion.GitVersion, "v") // v1.24.9 -> 1.24.9
	p.kubernetesVersionCache.SetDefault(kubernetesVersionCacheKey, version)
	if p.cm.HasChanged("kubernetes-version", version) {
		log.FromContext(ctx).V(1).Info("discovered kubernetes version", "kubernetesVersion", version)
	}
	return version, nil
}
