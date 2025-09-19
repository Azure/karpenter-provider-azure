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

	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"
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
	NodeImageVersionsAPI        *fake.NodeImageVersionsAPI
	SKUsAPI                     *fake.ResourceSKUsAPI
	PricingAPI                  *fake.PricingAPI
	LoadBalancersAPI            *fake.LoadBalancersAPI
	NetworkSecurityGroupAPI     *fake.NetworkSecurityGroupAPI
	SubnetsAPI                  *fake.SubnetsAPI
	AuxiliaryTokenServer        *fake.AuxiliaryTokenServer
	SubscriptionAPI             *fake.SubscriptionsAPI

	// Cache
	KubernetesVersionCache    *cache.Cache
	NodeImagesCache           *cache.Cache
	InstanceTypeCache         *cache.Cache
	LoadBalancerCache         *cache.Cache
	UnavailableOfferingsCache *azurecache.UnavailableOfferings

	// Providers
	InstanceTypesProvider        instancetype.Provider
	InstanceProvider             instance.Provider
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
	instanceProvider := instance.NewDefaultProvider(
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

	return &Environment{
		VirtualMachinesAPI:          virtualMachinesAPI,
		AuxiliaryTokenServer:        auxiliaryTokenServer,
		AzureResourceGraphAPI:       azureResourceGraphAPI,
		VirtualMachineExtensionsAPI: virtualMachinesExtensionsAPI,
		NetworkInterfacesAPI:        networkInterfacesAPI,
		CommunityImageVersionsAPI:   communityImageVersionsAPI,
		NodeImageVersionsAPI:        nodeImageVersionsAPI,
		LoadBalancersAPI:            loadBalancersAPI,
		NetworkSecurityGroupAPI:     networkSecurityGroupAPI,
		SubnetsAPI:                  subnetsAPI,
		SKUsAPI:                     skusAPI,
		PricingAPI:                  pricingAPI,
		SubscriptionAPI:             subscriptionAPI,

		KubernetesVersionCache:    kubernetesVersionCache,
		NodeImagesCache:           nodeImagesCache,
		InstanceTypeCache:         instanceTypeCache,
		UnavailableOfferingsCache: unavailableOfferingsCache,
		LoadBalancerCache:         loadBalancerCache,

		InstanceTypesProvider:        instanceTypesProvider,
		InstanceProvider:             instanceProvider,
		PricingProvider:              pricingProvider,
		KubernetesVersionProvider:    kubernetesVersionProvider,
		ImageProvider:                imageFamilyProvider,
		ImageResolver:                imageFamilyResolver,
		LaunchTemplateProvider:       launchTemplateProvider,
		LoadBalancerProvider:         loadBalancerProvider,
		NetworkSecurityGroupProvider: networkSecurityGroupProvider,

		nonZonal:       nonZonal,
		SubscriptionID: subscription,
	}
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
	env.NodeImageVersionsAPI.Reset()
	env.SKUsAPI.Reset()
	env.PricingAPI.Reset()
	env.PricingProvider.Reset()

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
