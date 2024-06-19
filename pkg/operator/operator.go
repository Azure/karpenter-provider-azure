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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
	"knative.dev/pkg/ptr"
	corev1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"
	"sigs.k8s.io/karpenter/pkg/operator/scheme"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing"
	"sigs.k8s.io/karpenter/pkg/operator"
)

func init() {
	lo.Must0(apis.AddToScheme(scheme.Scheme))
	corev1beta1.NormalizedLabels = lo.Assign(corev1beta1.NormalizedLabels, map[string]string{"topology.disk.csi.azure.com/zone": corev1.LabelTopologyZone})
}

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
	opts := options.FromContext(ctx)

	var authManager *auth.AuthManager
	if opts.ArmAuthMethod == auth.AuthMethodWorkloadIdentity {
		authManager = auth.NewAuthManagerWorkloadIdentity(opts.Location)
	} else if opts.ArmAuthMethod == auth.AuthMethodSysMSI {
		authManager = auth.NewAuthManagerSystemAssignedMSI(opts.Location)
	}

	azClient, err := instance.CreateAZClient(ctx, opts.SubscriptionID, authManager)
	lo.Must0(err, "creating Azure client")

	unavailableOfferingsCache := azurecache.NewUnavailableOfferings()
	pricingProvider := pricing.NewProvider(
		ctx,
		opts,
		pricing.NewAPI(),
		operator.Elected(),
	)
	imageProvider := imagefamily.NewProvider(
		opts,
		operator.KubernetesInterface,
		cache.New(azurecache.KubernetesVersionTTL,
			azurecache.DefaultCleanupInterval),
		azClient.ImageVersionsClient,
	)
	imageResolver := imagefamily.New(
		operator.GetClient(),
		imageProvider,
	)
	launchTemplateProvider := launchtemplate.NewProvider(
		ctx,
		opts,
		imageResolver,
		imageProvider,
		lo.Must(getCABundle(operator.GetConfig())),
	)
	instanceTypeProvider := instancetype.NewProvider(
		opts,
		cache.New(instancetype.InstanceTypesCacheTTL, azurecache.DefaultCleanupInterval),
		azClient.SKUClient,
		pricingProvider,
		unavailableOfferingsCache,
	)
	loadBalancerProvider := loadbalancer.NewProvider(
		opts,
		azClient.LoadBalancersClient,
		cache.New(loadbalancer.LoadBalancersCacheTTL, azurecache.DefaultCleanupInterval),
	)
	instanceProvider := instance.NewProvider(
		opts,
		azClient,
		instanceTypeProvider,
		launchTemplateProvider,
		loadBalancerProvider,
		unavailableOfferingsCache,
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
