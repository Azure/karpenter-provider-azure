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
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
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
	subscriptionID string
	authManager    *auth.AuthManager

	mu       sync.RWMutex
	instance compute.ResourceSkusClient
}

func NewSkuClient(ctx context.Context, subscriptionID string, authManager *auth.AuthManager) (SkuClient, error) {
	sc := &skuClient{
		subscriptionID: subscriptionID,
		authManager:    authManager,
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

	return sc, nil
}

func (sc *skuClient) updateInstance() {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	updatedSkuClient := compute.NewResourceSkusClient(sc.subscriptionID)

	authorizer, err := sc.authManager.NewAutorestAuthorizer()
	if err != nil {
		klog.V(5).Infof("Error creating authorizer for sku client: %s", err)
		return
	}
	updatedSkuClient.Authorizer = authorizer

	klog.V(5).Infof("Created sku client with authorizer: %v", updatedSkuClient)
	sc.instance = updatedSkuClient
}

func (sc *skuClient) GetInstance() skewer.ResourceClient {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.instance
}
