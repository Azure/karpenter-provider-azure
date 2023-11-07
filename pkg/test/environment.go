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

	azurecache "github.com/Azure/karpenter/pkg/cache"
	"github.com/Azure/karpenter/pkg/fake"
	"github.com/Azure/karpenter/pkg/providers/imagefamily"
	"github.com/Azure/karpenter/pkg/providers/instance"
	"github.com/Azure/karpenter/pkg/providers/instancetype"
	"github.com/Azure/karpenter/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter/pkg/providers/pricing"
	"github.com/patrickmn/go-cache"
	"knative.dev/pkg/ptr"

	coretest "github.com/aws/karpenter-core/pkg/test"
)

var resourceGroup = "test-resourceGroup"

type Environment struct {
	// API
	VirtualMachinesAPI          *fake.VirtualMachinesAPI
	AzureResourceGraphAPI       *fake.AzureResourceGraphAPI
	VirtualMachineExtensionsAPI *fake.VirtualMachineExtensionsAPI
	NetworkInterfacesAPI        *fake.NetworkInterfacesAPI
	CommunityImageVersionsAPI   *fake.CommunityGalleryImageVersionsAPI
	MockSkuClientSignalton      *fake.MockSkuClientSingleton
	PricingAPI                  *fake.PricingAPI
	LoadBalancersAPI            *fake.LoadBalancersAPI

	// Cache
	KubernetesVersionCache    *cache.Cache
	InstanceTypeCache         *cache.Cache
	LoadBalancerCache         *cache.Cache
	UnavailableOfferingsCache *azurecache.UnavailableOfferings

	// Providers
	InstanceTypesProvider  *instancetype.Provider
	InstanceProvider       *instance.Provider
	PricingProvider        *pricing.Provider
	ImageProvider          *imagefamily.Provider
	ImageResolver          *imagefamily.Resolver
	LaunchTemplateProvider *launchtemplate.Provider
	LoadBalancerProvider   *loadbalancer.Provider
}

func NewEnvironment(ctx context.Context, env *coretest.Environment) *Environment {
	testSettings := Settings()
	location := fake.Region

	// API
	virtualMachinesAPI := &fake.VirtualMachinesAPI{}
	azureResourceGraphAPI := &fake.AzureResourceGraphAPI{AzureResourceGraphBehavior: fake.AzureResourceGraphBehavior{VirtualMachinesAPI: virtualMachinesAPI, ResourceGroup: resourceGroup}}
	virtualMachinesExtensionsAPI := &fake.VirtualMachineExtensionsAPI{}
	networkInterfacesAPI := &fake.NetworkInterfacesAPI{}
	pricingAPI := &fake.PricingAPI{}
	skuClientSignalton := &fake.MockSkuClientSingleton{SKUClient: &fake.ResourceSKUsAPI{}}
	coummunityImageVersionsAPI := &fake.CommunityGalleryImageVersionsAPI{}
	loadBalancersAPI := &fake.LoadBalancersAPI{}

	// Cache
	kubernetesVersionCache := cache.New(azurecache.KubernetesVersionTTL, azurecache.DefaultCleanupInterval)
	instanceTypeCache := cache.New(instancetype.InstanceTypesCacheTTL, azurecache.DefaultCleanupInterval)
	loadBalancerCache := cache.New(loadbalancer.LoadBalancersCacheTTL, azurecache.DefaultCleanupInterval)
	unavailableOfferingsCache := azurecache.NewUnavailableOfferings()

	// Providers
	pricingProvider := pricing.NewProvider(ctx, pricingAPI, location, make(chan struct{}))
	imageFamilyProvider := imagefamily.NewProvider(env.KubernetesInterface, kubernetesVersionCache, coummunityImageVersionsAPI, location)
	imageFamilyResolver := imagefamily.New(env.Client, imageFamilyProvider)
	instanceTypesProvider := instancetype.NewProvider(location, instanceTypeCache, skuClientSignalton, pricingProvider, unavailableOfferingsCache)
	launchTemplateProvider := launchtemplate.NewProvider(
		ctx,
		imageFamilyResolver,
		imageFamilyProvider,
		ptr.String("ca-bundle"),
		testSettings.ClusterEndpoint,
		"test-tenant",
		"test-subscription",
		"test-userAssignedIdentity",
		resourceGroup,
		location,
	)
	loadBalancerProvider := loadbalancer.NewProvider(
		loadBalancersAPI,
		loadBalancerCache,
		"test-resourceGroup",
	)
	azClient := instance.NewAZClientFromAPI(
		virtualMachinesAPI,
		azureResourceGraphAPI,
		virtualMachinesExtensionsAPI,
		networkInterfacesAPI,
		loadBalancersAPI,
		coummunityImageVersionsAPI,
		skuClientSignalton,
	)
	instanceProvider := instance.NewProvider(
		ctx,
		azClient,
		instanceTypesProvider,
		launchTemplateProvider,
		loadBalancerProvider,
		unavailableOfferingsCache,
		location,      // region
		resourceGroup, // resourceGroup
		"",            // subnet
		"",            // subscriptionID
	)

	return &Environment{
		VirtualMachinesAPI:          virtualMachinesAPI,
		AzureResourceGraphAPI:       azureResourceGraphAPI,
		VirtualMachineExtensionsAPI: virtualMachinesExtensionsAPI,
		NetworkInterfacesAPI:        networkInterfacesAPI,
		LoadBalancersAPI:            loadBalancersAPI,
		MockSkuClientSignalton:      skuClientSignalton,
		PricingAPI:                  pricingAPI,

		KubernetesVersionCache:    kubernetesVersionCache,
		InstanceTypeCache:         instanceTypeCache,
		UnavailableOfferingsCache: unavailableOfferingsCache,
		LoadBalancerCache:         loadBalancerCache,

		InstanceTypesProvider:  instanceTypesProvider,
		InstanceProvider:       instanceProvider,
		PricingProvider:        pricingProvider,
		ImageProvider:          imageFamilyProvider,
		ImageResolver:          imageFamilyResolver,
		LaunchTemplateProvider: launchTemplateProvider,
		LoadBalancerProvider:   loadBalancerProvider,
	}
}

func (env *Environment) Reset() {
	env.VirtualMachinesAPI.Reset()
	env.AzureResourceGraphAPI.Reset()
	env.VirtualMachineExtensionsAPI.Reset()
	env.NetworkInterfacesAPI.Reset()
	env.LoadBalancersAPI.Reset()
	env.CommunityImageVersionsAPI.Reset()
	env.MockSkuClientSignalton.Reset()
	env.PricingAPI.Reset()
	env.PricingProvider.Reset()

	env.KubernetesVersionCache.Flush()
	env.InstanceTypeCache.Flush()
	env.UnavailableOfferingsCache.Flush()
	env.LoadBalancerCache.Flush()
}
