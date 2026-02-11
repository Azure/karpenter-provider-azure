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

package instance

import (
	"context"
	"fmt"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	imagefamilytypes "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/skuclient"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/networksecuritygroup"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/zone"
	"github.com/Azure/skewer"

	armopts "github.com/Azure/karpenter-provider-azure/pkg/utils/opts"
)

type AKSMachinesAPI interface {
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, parameters armcontainerservice.Machine, options *armcontainerservice.MachinesClientBeginCreateOrUpdateOptions) (*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse], error)
	Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, options *armcontainerservice.MachinesClientGetOptions) (armcontainerservice.MachinesClientGetResponse, error)
	NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse]
}

type AKSAgentPoolsAPI interface {
	Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.AgentPoolsClientGetOptions) (armcontainerservice.AgentPoolsClientGetResponse, error)
	BeginDeleteMachines(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachines armcontainerservice.AgentPoolDeleteMachinesParameter, options *armcontainerservice.AgentPoolsClientBeginDeleteMachinesOptions) (*runtime.Poller[armcontainerservice.AgentPoolsClientDeleteMachinesResponse], error)
}

type VirtualMachinesAPI interface {
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, vmName string, parameters armcompute.VirtualMachine, options *armcompute.VirtualMachinesClientBeginCreateOrUpdateOptions) (*runtime.Poller[armcompute.VirtualMachinesClientCreateOrUpdateResponse], error)
	Get(ctx context.Context, resourceGroupName string, vmName string, options *armcompute.VirtualMachinesClientGetOptions) (armcompute.VirtualMachinesClientGetResponse, error)
	BeginUpdate(ctx context.Context, resourceGroupName string, vmName string, parameters armcompute.VirtualMachineUpdate, options *armcompute.VirtualMachinesClientBeginUpdateOptions) (*runtime.Poller[armcompute.VirtualMachinesClientUpdateResponse], error)
	BeginDelete(ctx context.Context, resourceGroupName string, vmName string, options *armcompute.VirtualMachinesClientBeginDeleteOptions) (*runtime.Poller[armcompute.VirtualMachinesClientDeleteResponse], error)
}

type AzureResourceGraphAPI interface {
	Resources(ctx context.Context, query armresourcegraph.QueryRequest, options *armresourcegraph.ClientResourcesOptions) (armresourcegraph.ClientResourcesResponse, error)
}

type VirtualMachineExtensionsAPI interface {
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, vmName string, vmExtensionName string, extensionParameters armcompute.VirtualMachineExtension, options *armcompute.VirtualMachineExtensionsClientBeginCreateOrUpdateOptions) (*runtime.Poller[armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse], error)
	BeginUpdate(ctx context.Context, resourceGroupName string, vmName string, vmExtensionName string, extensionParameters armcompute.VirtualMachineExtensionUpdate, options *armcompute.VirtualMachineExtensionsClientBeginUpdateOptions) (*runtime.Poller[armcompute.VirtualMachineExtensionsClientUpdateResponse], error)
}

type NetworkInterfacesAPI interface {
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, networkInterfaceName string, parameters armnetwork.Interface, options *armnetwork.InterfacesClientBeginCreateOrUpdateOptions) (*runtime.Poller[armnetwork.InterfacesClientCreateOrUpdateResponse], error)
	BeginDelete(ctx context.Context, resourceGroupName string, networkInterfaceName string, options *armnetwork.InterfacesClientBeginDeleteOptions) (*runtime.Poller[armnetwork.InterfacesClientDeleteResponse], error)
	Get(ctx context.Context, resourceGroupName string, networkInterfaceName string, options *armnetwork.InterfacesClientGetOptions) (armnetwork.InterfacesClientGetResponse, error)
	UpdateTags(ctx context.Context, resourceGroupName string, networkInterfaceName string, tags armnetwork.TagsObject, options *armnetwork.InterfacesClientUpdateTagsOptions) (armnetwork.InterfacesClientUpdateTagsResponse, error)
}

type SubnetsAPI interface {
	Get(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error)
}

// TODO: Move this to another package that more correctly reflects its usage across multiple providers
type AZClient struct {
	azureResourceGraphClient       AzureResourceGraphAPI
	virtualMachinesClient          VirtualMachinesAPI
	aksMachinesClient              AKSMachinesAPI
	agentPoolsClient               AKSAgentPoolsAPI
	virtualMachinesExtensionClient VirtualMachineExtensionsAPI
	networkInterfacesClient        NetworkInterfacesAPI
	subnetsClient                  SubnetsAPI

	NodeImageVersionsClient imagefamilytypes.NodeImageVersionsAPI
	ImageVersionsClient     imagefamilytypes.CommunityGalleryImageVersionsAPI
	NodeBootstrappingClient imagefamilytypes.NodeBootstrappingAPI
	// SKU CLIENT is still using track 1 because skewer does not support the track 2 path. We need to refactor this once skewer supports track 2
	SKUClient                   skewer.ResourceClient
	LoadBalancersClient         loadbalancer.LoadBalancersAPI
	NetworkSecurityGroupsClient networksecuritygroup.API
	SubscriptionsClient         zone.SubscriptionsAPI
}

func (c *AZClient) SubnetsClient() SubnetsAPI {
	return c.subnetsClient
}

// AZClientManager manages per-subscription AZClient instances. It lazily creates
// and caches AZClient instances keyed by subscription ID. All clients share
// the same azcore.TokenCredential (one Azure identity with RBAC across all subs).
type AZClientManager struct {
	mu           sync.RWMutex
	clients      map[string]*AZClient // keyed on subscription ID
	cred         azcore.TokenCredential
	opts         *arm.ClientOptions
	defaultSubID string
}

// NewAZClientManager creates a new AZClientManager with the given default AZClient.
func NewAZClientManager(defaultSubID string, defaultClient *AZClient, cred azcore.TokenCredential, opts *arm.ClientOptions) *AZClientManager {
	clients := map[string]*AZClient{
		defaultSubID: defaultClient,
	}
	return &AZClientManager{
		clients:      clients,
		cred:         cred,
		opts:         opts,
		defaultSubID: defaultSubID,
	}
}

// GetClient returns the AZClient for the given subscription ID, creating one if necessary.
// If subscriptionID is empty, the default subscription's client is returned.
func (m *AZClientManager) GetClient(subscriptionID string) (*AZClient, error) {
	if subscriptionID == "" {
		subscriptionID = m.defaultSubID
	}

	// Fast path: check read lock first
	m.mu.RLock()
	c, ok := m.clients[subscriptionID]
	m.mu.RUnlock()
	if ok {
		return c, nil
	}

	// Slow path: create client under write lock
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if c, ok := m.clients[subscriptionID]; ok {
		return c, nil
	}

	c, err := m.newClientForSubscription(subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("creating Azure clients for subscription %s: %w", subscriptionID, err)
	}
	m.clients[subscriptionID] = c
	return c, nil
}

// DefaultSubscriptionID returns the default subscription ID.
func (m *AZClientManager) DefaultSubscriptionID() string {
	return m.defaultSubID
}

// KnownSubscriptionIDs returns all subscription IDs that have cached clients.
func (m *AZClientManager) KnownSubscriptionIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.clients))
	for id := range m.clients {
		ids = append(ids, id)
	}
	return ids
}

// newClientForSubscription creates a minimal AZClient for VM operations in a non-default subscription.
// It creates only the clients needed for VM lifecycle: VMs, NICs, extensions, and Resource Graph.
// The Resource Graph client is subscription-agnostic (subscription is specified per-query), so we
// reuse the default client's instance.
func (m *AZClientManager) newClientForSubscription(subscriptionID string) (*AZClient, error) {
	virtualMachinesClient, err := armcompute.NewVirtualMachinesClient(subscriptionID, m.cred, m.opts)
	if err != nil {
		return nil, fmt.Errorf("creating VirtualMachinesClient: %w", err)
	}

	extensionsClient, err := armcompute.NewVirtualMachineExtensionsClient(subscriptionID, m.cred, m.opts)
	if err != nil {
		return nil, fmt.Errorf("creating VirtualMachineExtensionsClient: %w", err)
	}

	interfacesClient, err := armnetwork.NewInterfacesClient(subscriptionID, m.cred, m.opts)
	if err != nil {
		return nil, fmt.Errorf("creating InterfacesClient: %w", err)
	}

	subnetsClient, err := armnetwork.NewSubnetsClient(subscriptionID, m.cred, m.opts)
	if err != nil {
		return nil, fmt.Errorf("creating SubnetsClient: %w", err)
	}

	// Resource Graph client is not subscription-scoped (subscription is passed per query),
	// so we reuse it from the default client.
	defaultClient := m.clients[m.defaultSubID]

	return &AZClient{
		virtualMachinesClient:          virtualMachinesClient,
		virtualMachinesExtensionClient: extensionsClient,
		networkInterfacesClient:        interfacesClient,
		subnetsClient:                  subnetsClient,
		azureResourceGraphClient:       defaultClient.azureResourceGraphClient,
		// AKS-specific clients are not needed for non-default subscriptions
		// in EKS hybrid mode.
		aksMachinesClient: NewNoAKSMachinesClient(),
		agentPoolsClient:  NewNoAKSAgentPoolsClient(),
	}, nil
}

func NewAZClientFromAPI(
	virtualMachinesClient VirtualMachinesAPI,
	azureResourceGraphClient AzureResourceGraphAPI,
	aksMachinesClient AKSMachinesAPI,
	agentPoolsClient AKSAgentPoolsAPI,
	virtualMachinesExtensionClient VirtualMachineExtensionsAPI,
	interfacesClient NetworkInterfacesAPI,
	subnetsClient SubnetsAPI,
	loadBalancersClient loadbalancer.LoadBalancersAPI,
	networkSecurityGroupsClient networksecuritygroup.API,
	imageVersionsClient imagefamilytypes.CommunityGalleryImageVersionsAPI,
	nodeImageVersionsClient imagefamilytypes.NodeImageVersionsAPI,
	nodeBootstrappingClient imagefamilytypes.NodeBootstrappingAPI,
	skuClient skewer.ResourceClient,
	subscriptionsClient zone.SubscriptionsAPI,
) *AZClient {
	return &AZClient{
		virtualMachinesClient:          virtualMachinesClient,
		azureResourceGraphClient:       azureResourceGraphClient,
		aksMachinesClient:              aksMachinesClient,
		agentPoolsClient:               agentPoolsClient,
		virtualMachinesExtensionClient: virtualMachinesExtensionClient,
		networkInterfacesClient:        interfacesClient,
		subnetsClient:                  subnetsClient,
		ImageVersionsClient:            imageVersionsClient,
		NodeImageVersionsClient:        nodeImageVersionsClient,
		NodeBootstrappingClient:        nodeBootstrappingClient,
		SKUClient:                      skuClient,
		LoadBalancersClient:            loadBalancersClient,
		NetworkSecurityGroupsClient:    networkSecurityGroupsClient,
		SubscriptionsClient:            subscriptionsClient,
	}
}

//nolint:gocyclo
func NewAZClient(ctx context.Context, cfg *auth.Config, env *auth.Environment, cred azcore.TokenCredential) (*AZClient, error) {
	o := options.FromContext(ctx)
	opts := armopts.DefaultARMOpts(env.Cloud, o.EnableAzureSDKLogging)
	extensionsClient, err := armcompute.NewVirtualMachineExtensionsClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}

	interfacesClient, err := armnetwork.NewInterfacesClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}

	subnetsClient, err := armnetwork.NewSubnetsClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}

	// copy the options to avoid modifying the original
	var vmClientOptions = *opts
	var auxiliaryTokenClient auth.AuxiliaryTokenServer
	if o.UseSIG {
		log.FromContext(ctx).Info("using SIG for image versions with auxiliary token policy for creating virtual machines")
		auxiliaryTokenClient = armopts.DefaultHTTPClient()
		auxPolicy := auth.NewAuxiliaryTokenPolicy(auxiliaryTokenClient, o.SIGAccessTokenServerURL, auth.TokenScope(env.Cloud))
		vmClientOptions.PerRetryPolicies = append(vmClientOptions.PerRetryPolicies, auxPolicy)
	}
	virtualMachinesClient, err := armcompute.NewVirtualMachinesClient(cfg.SubscriptionID, cred, &vmClientOptions)
	if err != nil {
		return nil, err
	}

	azureResourceGraphClient, err := armresourcegraph.NewClient(cred, opts)
	if err != nil {
		return nil, err
	}

	communityImageVersionsClient, err := armcompute.NewCommunityGalleryImageVersionsClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}

	nodeImageVersionsClient, err := imagefamily.NewNodeImageVersionsClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}

	loadBalancersClient, err := armnetwork.NewLoadBalancersClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}

	networkSecurityGroupsClient, err := armnetwork.NewSecurityGroupsClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}

	subscriptionsClient, err := armsubscriptions.NewClient(cred, opts)
	if err != nil {
		return nil, err
	}

	// TODO: this one is not enabled for rate limiting / throttling ...
	// TODO Move this over to track 2 when skewer is migrated
	skuClient := skuclient.NewSkuClient(cfg.SubscriptionID, cred, env.Cloud)

	// These clients are used for Azure instance management.
	var nodeBootstrappingClient imagefamilytypes.NodeBootstrappingAPI
	var aksMachinesClient AKSMachinesAPI
	var agentPoolsClient AKSAgentPoolsAPI

	// Only create the bootstrapping client if we need to use it.
	if o.ProvisionMode == consts.ProvisionModeBootstrappingClient {
		nodeBootstrappingClient, err = imagefamily.NewNodeBootstrappingClient(
			ctx,
			env.Cloud,
			cfg.SubscriptionID,
			cfg.ResourceGroup,
			o.ClusterName,
			cred,
			o.NodeBootstrappingServerURL,
			o.EnableAzureSDKLogging)
		if err != nil {
			return nil, err
		}
	}

	// Only create AKS machine clients if we need to use them.
	// Otherwise, use the no-op dry clients, which will act like there are no AKS machines present.
	if o.ManageExistingAKSMachines {
		aksMachinesClient, err = armcontainerservice.NewMachinesClient(cfg.SubscriptionID, cred, opts)
		if err != nil {
			return nil, err
		}
		agentPoolsClient, err = armcontainerservice.NewAgentPoolsClient(cfg.SubscriptionID, cred, opts)
		if err != nil {
			return nil, err
		}
	} else {
		aksMachinesClient = NewNoAKSMachinesClient()
		agentPoolsClient = NewNoAKSAgentPoolsClient()

		// Try create true clients. This is just for diagnostic purposes and serves no real functionality.
		// This portion of code can be removed once we are confident that this works reliably.
		_, err = armcontainerservice.NewMachinesClient(cfg.SubscriptionID, cred, opts)
		if err != nil {
			log.FromContext(ctx).Info("failed to create true AKS machines client, but tolerated due to currently on no-client", "error", err)
		}
		_, err = armcontainerservice.NewAgentPoolsClient(cfg.SubscriptionID, cred, opts)
		if err != nil {
			log.FromContext(ctx).Info("failed to create true AKS agent pools client, but tolerated due to currently on no-client", "error", err)
		}
	}

	return NewAZClientFromAPI(
		virtualMachinesClient,
		azureResourceGraphClient,
		aksMachinesClient,
		agentPoolsClient,
		extensionsClient,
		interfacesClient,
		subnetsClient,
		loadBalancersClient,
		networkSecurityGroupsClient,
		communityImageVersionsClient,
		nodeImageVersionsClient,
		nodeBootstrappingClient,
		skuClient,
		subscriptionsClient,
	), nil
}
