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

package azclient

import (
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	armnetwork "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// SubscriptionClients holds Azure SDK clients scoped to a specific subscription.
// These are the clients needed for creating and managing VMs and NICs in azurevm mode.
type SubscriptionClients struct {
	VirtualMachinesClient          VirtualMachinesAPI
	VirtualMachineExtensionsClient VirtualMachineExtensionsAPI
	NetworkInterfacesClient        NetworkInterfacesAPI
	SubnetsClient                  SubnetsAPI
}

// AZClientManager provides lazy, cached Azure SDK clients per subscription ID.
// The default subscription's clients come from the main AZClient. Additional subscriptions
// have clients created on-demand using the shared credential and ARM options.
//
// This enables multi-subscription support in azurevm mode: each AzureNodeClass can specify
// a different subscriptionID, and the VM provider uses AZClientManager to get the correct
// clients for that subscription.
type AZClientManager struct {
	defaultSubscriptionID string
	defaultClients        *SubscriptionClients
	cred                  azcore.TokenCredential
	armOpts               *arm.ClientOptions

	mu      sync.RWMutex
	clients map[string]*SubscriptionClients // keyed by subscription ID
}

// NewAZClientManager creates a new client manager. The defaultAZClient provides clients
// for the controller's own subscription; additional subscriptions are created on demand.
func NewAZClientManager(
	defaultSubscriptionID string,
	defaultAZClient *AZClient,
	cred azcore.TokenCredential,
	armOpts *arm.ClientOptions,
) *AZClientManager {
	defaultClients := &SubscriptionClients{
		VirtualMachinesClient:          defaultAZClient.VirtualMachinesClient(),
		VirtualMachineExtensionsClient: defaultAZClient.VirtualMachineExtensionsClient(),
		NetworkInterfacesClient:        defaultAZClient.NetworkInterfacesClient(),
		SubnetsClient:                  defaultAZClient.SubnetsClient(),
	}
	return &AZClientManager{
		defaultSubscriptionID: defaultSubscriptionID,
		defaultClients:        defaultClients,
		cred:                  cred,
		armOpts:               armOpts,
		clients:               make(map[string]*SubscriptionClients),
	}
}

// GetClients returns the subscription-scoped clients for the given subscription ID.
// If subscriptionID is empty or matches the default, the default clients are returned.
// Otherwise, clients are lazily created and cached.
func (m *AZClientManager) GetClients(subscriptionID string) (*SubscriptionClients, error) {
	if subscriptionID == "" || subscriptionID == m.defaultSubscriptionID {
		return m.defaultClients, nil
	}

	// Fast path: check the read cache
	m.mu.RLock()
	if clients, ok := m.clients[subscriptionID]; ok {
		m.mu.RUnlock()
		return clients, nil
	}
	m.mu.RUnlock()

	// Slow path: create clients for this subscription
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if clients, ok := m.clients[subscriptionID]; ok {
		return clients, nil
	}

	clients, err := m.newClientsForSubscription(subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("creating Azure clients for subscription %s: %w", subscriptionID, err)
	}
	m.clients[subscriptionID] = clients
	return clients, nil
}

// DefaultSubscriptionID returns the controller's default subscription ID.
func (m *AZClientManager) DefaultSubscriptionID() string {
	return m.defaultSubscriptionID
}

func (m *AZClientManager) newClientsForSubscription(subscriptionID string) (*SubscriptionClients, error) {
	vmClient, err := armcompute.NewVirtualMachinesClient(subscriptionID, m.cred, m.armOpts)
	if err != nil {
		return nil, fmt.Errorf("creating VirtualMachinesClient: %w", err)
	}

	extensionsClient, err := armcompute.NewVirtualMachineExtensionsClient(subscriptionID, m.cred, m.armOpts)
	if err != nil {
		return nil, fmt.Errorf("creating VirtualMachineExtensionsClient: %w", err)
	}

	nicClient, err := armnetwork.NewInterfacesClient(subscriptionID, m.cred, m.armOpts)
	if err != nil {
		return nil, fmt.Errorf("creating InterfacesClient: %w", err)
	}

	subnetsClient, err := armnetwork.NewSubnetsClient(subscriptionID, m.cred, m.armOpts)
	if err != nil {
		return nil, fmt.Errorf("creating SubnetsClient: %w", err)
	}

	return &SubscriptionClients{
		VirtualMachinesClient:          vmClient,
		VirtualMachineExtensionsClient: extensionsClient,
		NetworkInterfacesClient:        nicClient,
		SubnetsClient:                  subnetsClient,
	}, nil
}
