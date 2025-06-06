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
	"net/http"
	"time"

	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	armcomputev5 "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	imagefamilytypes "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/skuclient"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/networksecuritygroup"

	armopts "github.com/Azure/karpenter-provider-azure/pkg/utils/opts"
	klog "k8s.io/klog/v2"
)

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
}

type NetworkInterfacesAPI interface {
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, networkInterfaceName string, parameters armnetwork.Interface, options *armnetwork.InterfacesClientBeginCreateOrUpdateOptions) (*runtime.Poller[armnetwork.InterfacesClientCreateOrUpdateResponse], error)
	BeginDelete(ctx context.Context, resourceGroupName string, networkInterfaceName string, options *armnetwork.InterfacesClientBeginDeleteOptions) (*runtime.Poller[armnetwork.InterfacesClientDeleteResponse], error)
	Get(ctx context.Context, resourceGroupName string, networkInterfaceName string, options *armnetwork.InterfacesClientGetOptions) (armnetwork.InterfacesClientGetResponse, error)
}

// TODO: Move this to another package that more correctly reflects its usage across multiple providers
type AZClient struct {
	azureResourceGraphClient       AzureResourceGraphAPI
	virtualMachinesClient          VirtualMachinesAPI
	virtualMachinesExtensionClient VirtualMachineExtensionsAPI
	networkInterfacesClient        NetworkInterfacesAPI

	NodeImageVersionsClient imagefamilytypes.NodeImageVersionsAPI
	ImageVersionsClient     imagefamilytypes.CommunityGalleryImageVersionsAPI
	NodeBootstrappingClient imagefamilytypes.NodeBootstrappingAPI
	// SKU CLIENT is still using track 1 because skewer does not support the track 2 path. We need to refactor this once skewer supports track 2
	SKUClient                   skuclient.SkuClient
	LoadBalancersClient         loadbalancer.LoadBalancersAPI
	NetworkSecurityGroupsClient networksecuritygroup.API
}

func NewAZClientFromAPI(
	virtualMachinesClient VirtualMachinesAPI,
	azureResourceGraphClient AzureResourceGraphAPI,
	virtualMachinesExtensionClient VirtualMachineExtensionsAPI,
	interfacesClient NetworkInterfacesAPI,
	loadBalancersClient loadbalancer.LoadBalancersAPI,
	networkSecurityGroupsClient networksecuritygroup.API,
	imageVersionsClient imagefamilytypes.CommunityGalleryImageVersionsAPI,
	nodeImageVersionsClient imagefamilytypes.NodeImageVersionsAPI,
	nodeBootstrappingClient imagefamilytypes.NodeBootstrappingAPI,
	skuClient skuclient.SkuClient,
) *AZClient {
	return &AZClient{
		virtualMachinesClient:          virtualMachinesClient,
		azureResourceGraphClient:       azureResourceGraphClient,
		virtualMachinesExtensionClient: virtualMachinesExtensionClient,
		networkInterfacesClient:        interfacesClient,
		ImageVersionsClient:            imageVersionsClient,
		NodeImageVersionsClient:        nodeImageVersionsClient,
		NodeBootstrappingClient:        nodeBootstrappingClient,
		SKUClient:                      skuClient,
		LoadBalancersClient:            loadBalancersClient,
		NetworkSecurityGroupsClient:    networkSecurityGroupsClient,
	}
}

func CreateAZClient(ctx context.Context, cfg *auth.Config) (*AZClient, error) {
	// Defaulting env to Azure Public Cloud.
	env := azclient.PublicCloud
	var err error
	if cfg.Cloud != "" {
		env = azclient.EnvironmentFromName(cfg.Cloud)
	}

	azClient, err := NewAZClient(ctx, cfg, env)
	if err != nil {
		return nil, err
	}

	return azClient, nil
}

func NewAZClient(ctx context.Context, cfg *auth.Config, env *azclient.Environment) (*AZClient, error) {
	defaultAzureCred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	cred := auth.NewTokenWrapper(defaultAzureCred)
	opts := armopts.DefaultArmOpts()
	extensionsClient, err := armcompute.NewVirtualMachineExtensionsClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}

	interfacesClient, err := armnetwork.NewInterfacesClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}
	klog.V(5).Infof("Created network interface client %v using token credential", interfacesClient)

	// copy the options to avoid modifying the original
	var vmClientOptions = *opts
	o := options.FromContext(ctx)
	if o.UseSIG {
		klog.V(1).Info("Using SIG for image versions")
		client := &http.Client{Timeout: 10 * time.Second}
		auxPolicy, err := auth.NewAuxiliaryTokenPolicy(ctx, client, o.SIGAccessTokenServerURL, o.SIGAccessTokenScope)
		if err != nil {
			return nil, err
		}
		vmClientOptions.ClientOptions.PerRetryPolicies = append(vmClientOptions.ClientOptions.PerRetryPolicies, auxPolicy)
	}
	virtualMachinesClient, err := armcompute.NewVirtualMachinesClient(cfg.SubscriptionID, cred, &vmClientOptions)
	if err != nil {
		return nil, err
	}
	klog.V(5).Infof("Created virtual machines client %v, using a token credential", virtualMachinesClient)
	azureResourceGraphClient, err := armresourcegraph.NewClient(cred, opts)
	if err != nil {
		return nil, err
	}
	klog.V(5).Infof("Created azure resource graph client %v, using a token credential", azureResourceGraphClient)

	communityImageVersionsClient, err := armcomputev5.NewCommunityGalleryImageVersionsClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}
	klog.V(5).Infof("Created image versions client %v, using a token credential", communityImageVersionsClient)

	nodeImageVersionsClient := imagefamily.NewNodeImageVersionsClient(cred)

	loadBalancersClient, err := armnetwork.NewLoadBalancersClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}
	klog.V(5).Infof("Created load balancers client %v, using a token credential", loadBalancersClient)

	networkSecurityGroupsClient, err := armnetwork.NewSecurityGroupsClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}
	klog.V(5).Infof("Created nsg client %v, using a token credential", networkSecurityGroupsClient)

	// TODO: this one is not enabled for rate limiting / throttling ...
	// TODO Move this over to track 2 when skewer is migrated
	skuClient := skuclient.NewSkuClient(ctx, cfg, env)

	var nodeBootstrappingClient imagefamilytypes.NodeBootstrappingAPI = nil
	if o.ProvisionMode == consts.ProvisionModeBootstrappingClient {
		nodeBootstrappingClient, err = imagefamily.NewNodeBootstrappingClient(
			ctx,
			cfg.SubscriptionID,
			cfg.ResourceGroup,
			o.ClusterName,
			o.NodeBootstrappingServerURL)
		if err != nil {
			return nil, fmt.Errorf("failed to create node bootstrapping client: %w", err)
		}
		klog.V(5).Infof("Created bootstrapping client %v, using a token credential", nodeBootstrappingClient)
	}

	return NewAZClientFromAPI(virtualMachinesClient,
		azureResourceGraphClient,
		extensionsClient,
		interfacesClient,
		loadBalancersClient,
		networkSecurityGroupsClient,
		communityImageVersionsClient,
		nodeImageVersionsClient,
		nodeBootstrappingClient,
		skuClient), nil
}
