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

	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	"github.com/Azure/skewer"
	"github.com/jongio/azidext/go/azidext"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	refreshClient = 12 * time.Hour
)

type SkuClient interface {
	GetInstance() skewer.ResourceClient
}

type skuClient struct {
	cfg *auth.Config
	env *azclient.Environment

	mu       sync.RWMutex
	instance compute.ResourceSkusClient
}

func (sc *skuClient) updateInstance(ctx context.Context) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	// Create a new authorizer for the sku client
	// TODO (charliedmcb): need to get track 2 support for the skewer API
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		log.FromContext(ctx).Error(err, "error creating authorizer for sku client", "credentialType", "default")
		return
	}
	authorizer := azidext.NewTokenCredentialAdapter(cred, []string{azidext.DefaultManagementScope})

	azClientConfig := sc.cfg.GetAzureClientConfig(authorizer, sc.env)

	skuClient := compute.NewResourceSkusClient(sc.cfg.SubscriptionID)
	skuClient.Authorizer = azClientConfig.Authorizer

	sc.instance = skuClient
}

func NewSkuClient(ctx context.Context, cfg *auth.Config, env *azclient.Environment) SkuClient {
	sc := &skuClient{
		cfg: cfg,
		env: env,
	}
	sc.updateInstance(ctx)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(refreshClient):
				sc.updateInstance(ctx)
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
