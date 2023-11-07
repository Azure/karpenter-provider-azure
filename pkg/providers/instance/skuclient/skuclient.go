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

package skuclient

import (
	"context"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/karpenter/pkg/auth"
	"github.com/Azure/skewer"
	klog "k8s.io/klog/v2"
)

const (
	refreshClient = 12 * time.Hour
)

type SkuClient interface {
	GetInstance() skewer.ResourceClient
}

type skuClient struct {
	cfg *auth.Config
	env *azure.Environment

	mu       sync.RWMutex
	instance compute.ResourceSkusClient
}

func (sc *skuClient) updateInstance() {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	authorizer, err := auth.NewAuthorizer(sc.cfg, sc.env)
	if err != nil {
		klog.V(5).Infof("Error creating authorizer for sku client: %s", err)
		return
	}

	azClientConfig := sc.cfg.GetAzureClientConfig(authorizer, sc.env)
	azClientConfig.UserAgent = auth.GetUserAgentExtension()

	skuClient := compute.NewResourceSkusClient(sc.cfg.SubscriptionID)
	skuClient.Authorizer = azClientConfig.Authorizer
	klog.V(5).Infof("Created sku client with authorizer: %v", skuClient)

	sc.instance = skuClient
}

func NewSkuClient(ctx context.Context, cfg *auth.Config, env *azure.Environment) SkuClient {
	sc := &skuClient{
		cfg: cfg,
		env: env,
	}
	sc.updateInstance()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(refreshClient):
				sc.updateInstance()
			}
		}
	}()
	return sc
}

func (sc *skuClient) GetInstance() skewer.ResourceClient {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.instance
}
