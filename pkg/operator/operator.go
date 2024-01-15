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

package operator

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
	"knative.dev/pkg/ptr"

	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing"
	"github.com/aws/karpenter-core/pkg/operator"
)

type Operator struct {
	*operator.Operator

	UnavailableOfferingsCache *azurecache.UnavailableOfferings

	ImageProvider          *imagefamily.Provider
	ImageResolver          *imagefamily.Resolver
	LaunchTemplateProvider *launchtemplate.Provider
	PricingProvider        *pricing.Provider
	InstanceTypesProvider  *instancetype.Provider
	InstanceProvider       *instance.Provider
	LoadBalancerProvider   *loadbalancer.Provider
}

func NewOperator(ctx context.Context, operator *operator.Operator) (context.Context, *Operator) {
	azConfig, err := GetAZConfig()
	lo.Must0(err, "creating Azure config") // TODO: I assume we prefer this over the cleaner azConfig := lo.Must(GetAzConfig()), as this has a helpful error message?

	azClient, err := instance.CreateAZClient(ctx, azConfig)
	lo.Must0(err, "creating Azure client")

	unavailableOfferingsCache := azurecache.NewUnavailableOfferings()
	pricingProvider := pricing.NewProvider(
		ctx,
		pricing.NewAPI(),
		azConfig.Location,
		operator.Elected(),
	)
	imageProvider := imagefamily.NewProvider(
		operator.KubernetesInterface,
		cache.New(azurecache.KubernetesVersionTTL,
			azurecache.DefaultCleanupInterval),
		azClient.ImageVersionsClient,
		azConfig.Location,
	)
	imageResolver := imagefamily.New(operator.GetClient(), imageProvider)
	launchTemplateProvider := launchtemplate.NewProvider(
		ctx,
		imageResolver,
		imageProvider,
		lo.Must(getCABundle(operator.GetConfig())),
		options.FromContext(ctx).ClusterEndpoint,
		azConfig.TenantID,
		azConfig.SubscriptionID,
		azConfig.UserAssignedIdentityID,
		azConfig.NodeResourceGroup,
		azConfig.Location,
	)
	instanceTypeProvider := instancetype.NewProvider(
		azConfig.Location,
		cache.New(instancetype.InstanceTypesCacheTTL, azurecache.DefaultCleanupInterval),
		azClient.SKUClient,
		pricingProvider,
		unavailableOfferingsCache,
	)
	loadBalancerProvider := loadbalancer.NewProvider(
		azClient.LoadBalancersClient,
		cache.New(loadbalancer.LoadBalancersCacheTTL, azurecache.DefaultCleanupInterval),
		azConfig.NodeResourceGroup,
	)
	instanceProvider := instance.NewProvider(
		ctx,
		azClient,
		instanceTypeProvider,
		launchTemplateProvider,
		loadBalancerProvider,
		unavailableOfferingsCache,
		azConfig.Location,
		azConfig.NodeResourceGroup,
		azConfig.SubnetID,
		azConfig.SubscriptionID,
	)

	return ctx, &Operator{
		Operator:                  operator,
		UnavailableOfferingsCache: unavailableOfferingsCache,
		ImageProvider:             imageProvider,
		ImageResolver:             imageResolver,
		LaunchTemplateProvider:    launchTemplateProvider,
		PricingProvider:           pricingProvider,
		InstanceTypesProvider:     instanceTypeProvider,
		InstanceProvider:          instanceProvider,
		LoadBalancerProvider:      loadBalancerProvider,
	}
}

func GetAZConfig() (*auth.Config, error) {
	cfg, err := auth.BuildAzureConfig()
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func getCABundle(restConfig *rest.Config) (*string, error) {
	// Discover CA Bundle from the REST client. We could alternatively
	// have used the simpler client-go InClusterConfig() method.
	// However, that only works when Karpenter is running as a Pod
	// within the same cluster it's managing.
	transportConfig, err := restConfig.TransportConfig()
	if err != nil {
		return nil, fmt.Errorf("discovering caBundle, loading transport config, %w", err)
	}
	_, err = transport.TLSConfigFor(transportConfig) // fills in CAData!
	if err != nil {
		return nil, fmt.Errorf("discovering caBundle, loading TLS config, %w", err)
	}
	return ptr.String(base64.StdEncoding.EncodeToString(transportConfig.TLS.CAData)), nil
}
