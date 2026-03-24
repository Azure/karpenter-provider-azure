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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// SubscriptionClients holds Azure SDK clients scoped to a single subscription.
// These are the per-VM-lifecycle clients needed for NIC and VM creation/deletion.
type SubscriptionClients struct {
	VirtualMachinesClient          VirtualMachinesAPI
	VirtualMachineExtensionsClient VirtualMachineExtensionsAPI
	NetworkInterfacesClient        NetworkInterfacesAPI
	SubnetsClient                  SubnetsAPI
}

// AZClientManager lazily creates and caches per-subscription Azure SDK clients.
// A single TokenCredential (shared across all subscriptions) is used — the identity
// must have RBAC across all target subscriptions.
type AZClientManager struct {
	defaultSubscriptionID string
	defaultClients        *SubscriptionClients
	cred                  azcore.TokenCredential
	armOpts               *arm.ClientOptions
	mu                    sync.RWMutex
	clients               map[string]*SubscriptionClients
}

// NewAZClientManager creates a manager that wraps the default subscription's clients
// and lazily creates clients for other subscriptions on demand.
func NewAZClientManager(
	defaultSubID string,
	defaultAZClient *AZClient,
	cred azcore.TokenCredential,
	armOpts *arm.ClientOptions,
) *AZClientManager {
	return &AZClientManager{
		defaultSubscriptionID: defaultSubID,
		defaultClients: &SubscriptionClients{
			VirtualMachinesClient:          defaultAZClient.VirtualMachinesClient(),
			VirtualMachineExtensionsClient: defaultAZClient.VirtualMachineExtensionsClient(),
			NetworkInterfacesClient:        defaultAZClient.NetworkInterfacesClient(),
			SubnetsClient:                  defaultAZClient.SubnetsClient(),
		},
		cred:    cred,
		armOpts: armOpts,
		clients: make(map[string]*SubscriptionClients),
	}
}

// DefaultSubscriptionID returns the controller-level default subscription ID.
func (m *AZClientManager) DefaultSubscriptionID() string {
	return m.defaultSubscriptionID
}

// GetClients returns the SDK clients for the given subscription. If subscriptionID
// is empty or matches the default, the pre-built default clients are returned.
// Otherwise, clients are lazily created and cached for the lifetime of the manager.
func (m *AZClientManager) GetClients(subscriptionID string) (*SubscriptionClients, error) {
	if subscriptionID == "" || subscriptionID == m.defaultSubscriptionID {
		return m.defaultClients, nil
	}

	// Fast path: check under read lock
	m.mu.RLock()
	if c, ok := m.clients[subscriptionID]; ok {
		m.mu.RUnlock()
		return c, nil
	}
	m.mu.RUnlock()

	// Slow path: create under write lock
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if c, ok := m.clients[subscriptionID]; ok {
		return c, nil
	}

	c, err := m.newSubscriptionClients(subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("creating Azure clients for subscription %s: %w", subscriptionID, err)
	}
	m.clients[subscriptionID] = c
	return c, nil
}

// newSubscriptionClients creates fresh Azure SDK clients for a non-default subscription.
func (m *AZClientManager) newSubscriptionClients(subscriptionID string) (*SubscriptionClients, error) {
	vmClient, err := armcompute.NewVirtualMachinesClient(subscriptionID, m.cred, m.armOpts)
	if err != nil {
		return nil, fmt.Errorf("creating VirtualMachinesClient: %w", err)
	}

	extClient, err := armcompute.NewVirtualMachineExtensionsClient(subscriptionID, m.cred, m.armOpts)
	if err != nil {
		return nil, fmt.Errorf("creating VirtualMachineExtensionsClient: %w", err)
	}

	nicClient, err := armnetwork.NewInterfacesClient(subscriptionID, m.cred, m.armOpts)
	if err != nil {
		return nil, fmt.Errorf("creating NetworkInterfacesClient: %w", err)
	}

	subnetsClient, err := armnetwork.NewSubnetsClient(subscriptionID, m.cred, m.armOpts)
	if err != nil {
		return nil, fmt.Errorf("creating SubnetsClient: %w", err)
	}

	return &SubscriptionClients{
		VirtualMachinesClient:          vmClient,
		VirtualMachineExtensionsClient: extClient,
		NetworkInterfacesClient:        nicClient,
		SubnetsClient:                  subnetsClient,
	}, nil
}
