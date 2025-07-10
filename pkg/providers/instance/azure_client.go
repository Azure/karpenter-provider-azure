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

	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
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

func CreateAZClient(ctx context.Context, cfg *auth.Config, cred azcore.TokenCredential) (*AZClient, error) {
	env := azclient.EnvironmentFromName(cfg.Cloud)
	azClient, err := NewAZClient(ctx, cfg, env, cred)
	if err != nil {
		return nil, err
	}

	return azClient, nil
}

// nolint: gocyclo
func NewAZClient(ctx context.Context, cfg *auth.Config, env *azclient.Environment, cred azcore.TokenCredential) (*AZClient, error) {
	o := options.FromContext(ctx)
	opts := armopts.DefaultArmOpts(o.EnableAzureSDKLogging)

	extensionsClient, err := armcompute.NewVirtualMachineExtensionsClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}

	interfacesClient, err := armnetwork.NewInterfacesClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}

	// copy the options to avoid modifying the original
	var vmClientOptions = *opts
	var auxiliaryTokenClient auth.AuxiliaryTokenServer
	if o.UseSIG {
		log.FromContext(ctx).Info("using SIG for image versions with auxiliary token policy for creating virtual machines")
		auxiliaryTokenClient = armopts.DefaultHTTPClient()
		auxPolicy := auth.NewAuxiliaryTokenPolicy(auxiliaryTokenClient, o.SIGAccessTokenServerURL, o.SIGAccessTokenScope)
		vmClientOptions.ClientOptions.PerRetryPolicies = append(vmClientOptions.ClientOptions.PerRetryPolicies, auxPolicy)
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

	nodeImageVersionsClient := imagefamily.NewNodeImageVersionsClient(cred)

	loadBalancersClient, err := armnetwork.NewLoadBalancersClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}

	networkSecurityGroupsClient, err := armnetwork.NewSecurityGroupsClient(cfg.SubscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}

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
			cred,
			o.NodeBootstrappingServerURL,
			o.EnableAzureSDKLogging)
		if err != nil {
			return nil, err
		}
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
