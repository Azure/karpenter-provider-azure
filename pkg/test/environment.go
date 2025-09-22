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

package test

import (
	"context"
	"time"

	gomegaformat "github.com/onsi/gomega/format"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/patrickmn/go-cache"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/kubernetesversion"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/networksecuritygroup"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing"
)

func init() {
	karpv1.NormalizedLabels = lo.Assign(karpv1.NormalizedLabels, map[string]string{"topology.disk.csi.azure.com/zone": corev1.LabelTopologyZone})

	// Configuing this here because it's commonly imported and has an init already
	gomegaformat.CharactersAroundMismatchToInclude = 40
}

const (
	clusterName   = "test-cluster"
	resourceGroup = "test-resourceGroup"
	subscription  = "12345678-1234-1234-1234-123456789012"
)

type Environment struct {
	// API
	VirtualMachinesAPI          *fake.VirtualMachinesAPI
	AzureResourceGraphAPI       *fake.AzureResourceGraphAPI
	VirtualMachineExtensionsAPI *fake.VirtualMachineExtensionsAPI
	NetworkInterfacesAPI        *fake.NetworkInterfacesAPI
	CommunityImageVersionsAPI   *fake.CommunityGalleryImageVersionsAPI
	SKUsAPI                     *fake.ResourceSKUsAPI
	PricingAPI                  *fake.PricingAPI
	LoadBalancersAPI            *fake.LoadBalancersAPI
	NetworkSecurityGroupAPI     *fake.NetworkSecurityGroupAPI
	SubnetsAPI                  *fake.SubnetsAPI
	AuxiliaryTokenServer        *fake.AuxiliaryTokenServer
	SubscriptionAPI             *fake.SubscriptionsAPI
	AKSMachinesAPI              *fake.AKSMachinesAPI
	AKSAgentPoolsAPI            *fake.AKSAgentPoolsAPI

	// Fake data stores for the APIs
	SharedStores *fake.SharedAKSDataStores

	// Cache
	KubernetesVersionCache    *cache.Cache
	NodeImagesCache           *cache.Cache
	InstanceTypeCache         *cache.Cache
	LoadBalancerCache         *cache.Cache
	UnavailableOfferingsCache *azurecache.UnavailableOfferings

	// Providers
	InstanceTypesProvider        instancetype.Provider
	VMInstanceProvider           instance.VMProvider
	AKSMachineProvider           instance.AKSMachineProvider
	PricingProvider              *pricing.Provider
	KubernetesVersionProvider    kubernetesversion.KubernetesVersionProvider
	ImageProvider                imagefamily.NodeImageProvider
	ImageResolver                imagefamily.Resolver
	LaunchTemplateProvider       *launchtemplate.Provider
	LoadBalancerProvider         *loadbalancer.Provider
	NetworkSecurityGroupProvider *networksecuritygroup.Provider

	// Settings
	nonZonal       bool
	SubscriptionID string
	coreEnv        *coretest.Environment
	region         string
}

func NewEnvironment(ctx context.Context, env *coretest.Environment) *Environment {
	return NewRegionalEnvironment(ctx, env, fake.Region, false)
}

func NewEnvironmentNonZonal(ctx context.Context, env *coretest.Environment) *Environment {
	return NewRegionalEnvironment(ctx, env, fake.RegionNonZonal, true)
}

func NewRegionalEnvironment(ctx context.Context, env *coretest.Environment, region string, nonZonal bool) *Environment {
	testOptions := options.FromContext(ctx)

	azureEnv := lo.Must(auth.EnvironmentFromName("AzurePublicCloud"))

	// API
	var auxTokenPolicy *auth.AuxiliaryTokenPolicy
	var auxiliaryTokenServer *fake.AuxiliaryTokenServer
	if testOptions.UseSIG {
		auxiliaryTokenServer = fake.NewAuxiliaryTokenServer("test-token", time.Now().Add(1*time.Hour), time.Now().Add(5*time.Minute))
		auxTokenPolicy = auth.NewAuxiliaryTokenPolicy(auxiliaryTokenServer, testOptions.SIGAccessTokenServerURL, auth.TokenScope(azureEnv.Cloud))
	}
	virtualMachinesAPI := &fake.VirtualMachinesAPI{AuxiliaryTokenPolicy: auxTokenPolicy}

	networkInterfacesAPI := &fake.NetworkInterfacesAPI{}
	virtualMachinesExtensionsAPI := &fake.VirtualMachineExtensionsAPI{}
	pricingAPI := &fake.PricingAPI{}
	skusAPI := &fake.ResourceSKUsAPI{Location: region}
	communityImageVersionsAPI := &fake.CommunityGalleryImageVersionsAPI{}
	loadBalancersAPI := &fake.LoadBalancersAPI{}
	networkSecurityGroupAPI := &fake.NetworkSecurityGroupAPI{}
	nodeImageVersionsAPI := &fake.NodeImageVersionsAPI{}
	nodeBootstrappingAPI := &fake.NodeBootstrappingAPI{}
	subscriptionAPI := &fake.SubscriptionsAPI{}

	// Create shared data stores for AKS agent pools and machines
	sharedStores := fake.NewSharedAKSDataStores()

	// Create APIs that share the data stores
	aksAgentPoolsAPI := fake.NewAKSAgentPoolsAPI(sharedStores)
	aksMachinesAPI := fake.NewAKSMachinesAPI(sharedStores)

	azureResourceGraphAPI := fake.NewAzureResourceGraphAPI(resourceGroup, virtualMachinesAPI, networkInterfacesAPI)
	// Cache
	kubernetesVersionCache := cache.New(azurecache.KubernetesVersionTTL, azurecache.DefaultCleanupInterval)
	nodeImagesCache := cache.New(imagefamily.ImageExpirationInterval, imagefamily.ImageCacheCleaningInterval)
	instanceTypeCache := cache.New(instancetype.InstanceTypesCacheTTL, azurecache.DefaultCleanupInterval)
	loadBalancerCache := cache.New(loadbalancer.LoadBalancersCacheTTL, azurecache.DefaultCleanupInterval)
	unavailableOfferingsCache := azurecache.NewUnavailableOfferings()

	// Providers
	pricingProvider := pricing.NewProvider(ctx, azureEnv, pricingAPI, region, make(chan struct{}))
	kubernetesVersionProvider := kubernetesversion.NewKubernetesVersionProvider(env.KubernetesInterface, kubernetesVersionCache)
	imageFamilyProvider := imagefamily.NewProvider(communityImageVersionsAPI, region, subscription, nodeImageVersionsAPI, nodeImagesCache)
	instanceTypesProvider := instancetype.NewDefaultProvider(
		region,
		instanceTypeCache,
		skusAPI,
		pricingProvider,
		unavailableOfferingsCache)
	imageFamilyResolver := imagefamily.NewDefaultResolver(env.Client, imageFamilyProvider, instanceTypesProvider, nodeBootstrappingAPI)
	launchTemplateProvider := launchtemplate.NewProvider(
		ctx,
		imageFamilyResolver,
		imageFamilyProvider,
		lo.ToPtr("ca-bundle"),
		testOptions.ClusterEndpoint,
		"test-tenant",
		subscription,
		"test-cluster-resource-group",
		"test-kubelet-identity-client-id",
		testOptions.NodeResourceGroup,
		region,
		testOptions.VnetGUID,
		testOptions.ProvisionMode,
	)
	loadBalancerProvider := loadbalancer.NewProvider(
		loadBalancersAPI,
		loadBalancerCache,
		testOptions.NodeResourceGroup,
	)
	networkSecurityGroupProvider := networksecuritygroup.NewProvider(
		networkSecurityGroupAPI,
		testOptions.NodeResourceGroup,
	)
	subnetsAPI := &fake.SubnetsAPI{}
	azClient := instance.NewAZClientFromAPI(
		virtualMachinesAPI,
		azureResourceGraphAPI,
		aksMachinesAPI,
		aksAgentPoolsAPI,
		virtualMachinesExtensionsAPI,
		networkInterfacesAPI,
		subnetsAPI,
		loadBalancersAPI,
		networkSecurityGroupAPI,
		communityImageVersionsAPI,
		nodeImageVersionsAPI,
		nodeBootstrappingAPI,
		skusAPI,
		subscriptionAPI,
	)
	vmInstanceProvider := instance.NewDefaultVMProvider(
		azClient,
		instanceTypesProvider,
		launchTemplateProvider,
		loadBalancerProvider,
		networkSecurityGroupProvider,
		unavailableOfferingsCache,
		region,
		testOptions.NodeResourceGroup,
		subscription,
		testOptions.ProvisionMode,
	)

	if testOptions.ProvisionMode == consts.ProvisionModeAKSMachineAPI && testOptions.AKSMachinesPoolName != "" {
		// For this configuration, we assume the AKS machines pool already exists
		sharedStores.AgentPools.Store(
			fake.MkAgentPoolID(testOptions.NodeResourceGroup, clusterName, testOptions.AKSMachinesPoolName),
			armcontainerservice.AgentPool{
				Name: lo.ToPtr(testOptions.AKSMachinesPoolName),
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					Mode: lo.ToPtr(armcontainerservice.AgentPoolModeMachines),
				},
			},
		)
	}

	aksMachineInstanceProvider := instance.NewAKSMachineProvider(
		ctx,
		azClient,
		instanceTypesProvider,
		imageFamilyResolver,
		unavailableOfferingsCache,
		subscription,
		testOptions.NodeResourceGroup,
		clusterName,
		testOptions.AKSMachinesPoolName,
		region,
	)

	return &Environment{
		VirtualMachinesAPI:          virtualMachinesAPI,
		AuxiliaryTokenServer:        auxiliaryTokenServer,
		AzureResourceGraphAPI:       azureResourceGraphAPI,
		VirtualMachineExtensionsAPI: virtualMachinesExtensionsAPI,
		NetworkInterfacesAPI:        networkInterfacesAPI,
		CommunityImageVersionsAPI:   communityImageVersionsAPI,
		LoadBalancersAPI:            loadBalancersAPI,
		NetworkSecurityGroupAPI:     networkSecurityGroupAPI,
		SubnetsAPI:                  subnetsAPI,
		SKUsAPI:                     skusAPI,
		PricingAPI:                  pricingAPI,
		SubscriptionAPI:             subscriptionAPI,
		AKSMachinesAPI:              aksMachinesAPI,
		AKSAgentPoolsAPI:            aksAgentPoolsAPI,

		SharedStores: sharedStores,

		KubernetesVersionCache:    kubernetesVersionCache,
		NodeImagesCache:           nodeImagesCache,
		InstanceTypeCache:         instanceTypeCache,
		UnavailableOfferingsCache: unavailableOfferingsCache,
		LoadBalancerCache:         loadBalancerCache,

		InstanceTypesProvider:        instanceTypesProvider,
		VMInstanceProvider:           vmInstanceProvider,
		AKSMachineProvider:           aksMachineInstanceProvider,
		PricingProvider:              pricingProvider,
		KubernetesVersionProvider:    kubernetesVersionProvider,
		ImageProvider:                imageFamilyProvider,
		ImageResolver:                imageFamilyResolver,
		LaunchTemplateProvider:       launchTemplateProvider,
		LoadBalancerProvider:         loadBalancerProvider,
		NetworkSecurityGroupProvider: networkSecurityGroupProvider,

		nonZonal:       nonZonal,
		SubscriptionID: subscription,
		region:         region,
		coreEnv:        env,
	}
}

// Reinitialize the environment, but keep the state from data stores
func (env *Environment) ReapplyContextWithOptions(ctx context.Context) {
	testOptions := options.FromContext(ctx)

	azureEnv := lo.Must(auth.EnvironmentFromName("AzurePublicCloud"))

	// API
	var auxTokenPolicy *auth.AuxiliaryTokenPolicy
	var auxiliaryTokenServer *fake.AuxiliaryTokenServer
	if testOptions.UseSIG {
		auxiliaryTokenServer = fake.NewAuxiliaryTokenServer("test-token", time.Now().Add(1*time.Hour), time.Now().Add(5*time.Minute))
		auxTokenPolicy = auth.NewAuxiliaryTokenPolicy(auxiliaryTokenServer, testOptions.SIGAccessTokenServerURL, auth.TokenScope(azureEnv.Cloud))
	}
	virtualMachinesAPI := &fake.VirtualMachinesAPI{AuxiliaryTokenPolicy: auxTokenPolicy}

	networkInterfacesAPI := &fake.NetworkInterfacesAPI{}
	virtualMachinesExtensionsAPI := &fake.VirtualMachineExtensionsAPI{}
	pricingAPI := &fake.PricingAPI{}
	skusAPI := &fake.ResourceSKUsAPI{Location: env.region}
	communityImageVersionsAPI := &fake.CommunityGalleryImageVersionsAPI{}
	loadBalancersAPI := &fake.LoadBalancersAPI{}
	networkSecurityGroupAPI := &fake.NetworkSecurityGroupAPI{}
	nodeImageVersionsAPI := &fake.NodeImageVersionsAPI{}
	nodeBootstrappingAPI := &fake.NodeBootstrappingAPI{}
	subscriptionAPI := &fake.SubscriptionsAPI{}

	// Create APIs that share the data stores
	aksAgentPoolsAPI := fake.NewAKSAgentPoolsAPI(env.SharedStores)
	aksMachinesAPI := fake.NewAKSMachinesAPI(env.SharedStores)

	azureResourceGraphAPI := fake.NewAzureResourceGraphAPI(resourceGroup, virtualMachinesAPI, networkInterfacesAPI)
	// Cache
	kubernetesVersionCache := cache.New(azurecache.KubernetesVersionTTL, azurecache.DefaultCleanupInterval)
	nodeImagesCache := cache.New(imagefamily.ImageExpirationInterval, imagefamily.ImageCacheCleaningInterval)
	instanceTypeCache := cache.New(instancetype.InstanceTypesCacheTTL, azurecache.DefaultCleanupInterval)
	loadBalancerCache := cache.New(loadbalancer.LoadBalancersCacheTTL, azurecache.DefaultCleanupInterval)
	unavailableOfferingsCache := azurecache.NewUnavailableOfferings()

	// Providers
	pricingProvider := pricing.NewProvider(ctx, azureEnv, pricingAPI, env.region, make(chan struct{}))
	kubernetesVersionProvider := kubernetesversion.NewKubernetesVersionProvider(env.coreEnv.KubernetesInterface, kubernetesVersionCache)
	imageFamilyProvider := imagefamily.NewProvider(communityImageVersionsAPI, env.region, subscription, nodeImageVersionsAPI, nodeImagesCache)
	instanceTypesProvider := instancetype.NewDefaultProvider(env.region, instanceTypeCache, skusAPI, pricingProvider, unavailableOfferingsCache)
	imageFamilyResolver := imagefamily.NewDefaultResolver(env.coreEnv.Client, imageFamilyProvider, instanceTypesProvider, nodeBootstrappingAPI)
	launchTemplateProvider := launchtemplate.NewProvider(
		ctx,
		imageFamilyResolver,
		imageFamilyProvider,
		lo.ToPtr("ca-bundle"),
		testOptions.ClusterEndpoint,
		"test-tenant",
		subscription,
		"test-cluster-resource-group",
		"test-kubelet-identity-client-id",
		testOptions.NodeResourceGroup,
		env.region,
		testOptions.VnetGUID,
		testOptions.ProvisionMode,
	)
	loadBalancerProvider := loadbalancer.NewProvider(
		loadBalancersAPI,
		loadBalancerCache,
		testOptions.NodeResourceGroup,
	)
	networkSecurityGroupProvider := networksecuritygroup.NewProvider(
		networkSecurityGroupAPI,
		testOptions.NodeResourceGroup,
	)
	subnetsAPI := &fake.SubnetsAPI{}
	azClient := instance.NewAZClientFromAPI(
		env.VirtualMachinesAPI,
		azureResourceGraphAPI,
		aksMachinesAPI,
		aksAgentPoolsAPI,
		virtualMachinesExtensionsAPI,
		networkInterfacesAPI,
		subnetsAPI,
		loadBalancersAPI,
		networkSecurityGroupAPI,
		communityImageVersionsAPI,
		nodeImageVersionsAPI,
		nodeBootstrappingAPI,
		skusAPI,
		subscriptionAPI,
	)
	vmInstanceProvider := instance.NewDefaultVMProvider(
		azClient,
		instanceTypesProvider,
		launchTemplateProvider,
		loadBalancerProvider,
		networkSecurityGroupProvider,
		unavailableOfferingsCache,
		env.region,
		testOptions.NodeResourceGroup,
		subscription,
		testOptions.ProvisionMode,
	)

	aksMachineInstanceProvider := instance.NewAKSMachineProvider(
		ctx,
		azClient,
		instanceTypesProvider,
		imageFamilyResolver,
		unavailableOfferingsCache,
		subscription,
		testOptions.NodeResourceGroup,
		clusterName,
		testOptions.AKSMachinesPoolName,
		env.region,
	)

	env.VirtualMachinesAPI = virtualMachinesAPI
	env.AuxiliaryTokenServer = auxiliaryTokenServer
	env.AzureResourceGraphAPI = azureResourceGraphAPI
	env.VirtualMachineExtensionsAPI = virtualMachinesExtensionsAPI
	env.NetworkInterfacesAPI = networkInterfacesAPI
	env.CommunityImageVersionsAPI = communityImageVersionsAPI
	env.LoadBalancersAPI = loadBalancersAPI
	env.NetworkSecurityGroupAPI = networkSecurityGroupAPI
	env.SKUsAPI = skusAPI
	env.PricingAPI = pricingAPI
	env.AKSMachinesAPI = aksMachinesAPI
	env.AKSAgentPoolsAPI = aksAgentPoolsAPI

	env.KubernetesVersionCache = kubernetesVersionCache
	env.NodeImagesCache = nodeImagesCache
	env.InstanceTypeCache = instanceTypeCache
	env.UnavailableOfferingsCache = unavailableOfferingsCache
	env.LoadBalancerCache = loadBalancerCache

	env.InstanceTypesProvider = instanceTypesProvider
	env.VMInstanceProvider = vmInstanceProvider
	env.AKSMachineProvider = aksMachineInstanceProvider
	env.PricingProvider = pricingProvider
	env.KubernetesVersionProvider = kubernetesVersionProvider
	env.ImageProvider = imageFamilyProvider
	env.ImageResolver = imageFamilyResolver
	env.LaunchTemplateProvider = launchTemplateProvider
	env.LoadBalancerProvider = loadBalancerProvider
	env.NetworkSecurityGroupProvider = networkSecurityGroupProvider

	env.SubscriptionID = subscription
}

func (env *Environment) Reset() {
	env.VirtualMachinesAPI.Reset()
	if env.AuxiliaryTokenServer != nil {
		env.AuxiliaryTokenServer.Reset()
	}
	env.AzureResourceGraphAPI.Reset()
	env.VirtualMachineExtensionsAPI.Reset()
	env.NetworkInterfacesAPI.Reset()
	env.LoadBalancersAPI.Reset()
	env.NetworkSecurityGroupAPI.Reset()
	env.SubnetsAPI.Reset()
	env.CommunityImageVersionsAPI.Reset()
	env.SKUsAPI.Reset()
	env.PricingAPI.Reset()
	env.PricingProvider.Reset()
	env.AKSMachinesAPI.Reset()
	env.AKSAgentPoolsAPI.Reset()

	env.KubernetesVersionCache.Flush()
	env.NodeImagesCache.Flush()
	env.InstanceTypeCache.Flush()
	env.UnavailableOfferingsCache.Flush()
	env.LoadBalancerCache.Flush()
}

func (env *Environment) Zones() []string {
	if env.nonZonal {
		return []string{""}
	} else {
		return []string{fake.Region + "-1", fake.Region + "-2", fake.Region + "-3"}
	}
}
