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
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/operator"

	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	armopts "github.com/Azure/karpenter-provider-azure/pkg/utils/opts"
)

func init() {
	karpv1.NormalizedLabels = lo.Assign(karpv1.NormalizedLabels, map[string]string{"topology.disk.csi.azure.com/zone": corev1.LabelTopologyZone})
}

type Operator struct {
	*operator.Operator

	UnavailableOfferingsCache *azurecache.UnavailableOfferings

	ImageProvider          *imagefamily.Provider
	ImageResolver          imagefamily.Resolver
	LaunchTemplateProvider *launchtemplate.Provider
	PricingProvider        *pricing.Provider
	InstanceTypesProvider  instancetype.Provider
	InstanceProvider       *instance.DefaultProvider
	LoadBalancerProvider   *loadbalancer.Provider
}

func NewOperator(ctx context.Context, operator *operator.Operator) (context.Context, *Operator) {
	azConfig, err := GetAZConfig()
	lo.Must0(err, "creating Azure config") // NOTE: we prefer this over the cleaner azConfig := lo.Must(GetAzConfig()), as when initializing the client there are helpful error messages in initializing clients and the azure config

	azClient, err := instance.CreateAZClient(ctx, azConfig)
	lo.Must0(err, "creating Azure client")
	if options.FromContext(ctx).VnetGUID == "" && options.FromContext(ctx).NetworkPluginMode == consts.NetworkPluginModeOverlay {
		vnetGUID, err := getVnetGUID(azConfig, options.FromContext(ctx).SubnetID)
		lo.Must0(err, "getting VNET GUID")
		options.FromContext(ctx).VnetGUID = vnetGUID
	}

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
		azConfig.SubscriptionID,
		azClient.NodeImageVersionsClient,
	)
	imageResolver := imagefamily.NewDefaultResolver(
		operator.GetClient(),
		imageProvider,
	)
	launchTemplateProvider := launchtemplate.NewProvider(
		ctx,
		imageResolver,
		imageProvider,
		lo.Must(getCABundle(operator.GetConfig())),
		options.FromContext(ctx).ClusterEndpoint,
		azConfig.TenantID,
		azConfig.SubscriptionID,
		azConfig.ResourceGroup,
		azConfig.KubeletIdentityClientID,
		azConfig.NodeResourceGroup,
		azConfig.Location,
		options.FromContext(ctx).VnetGUID,
		options.FromContext(ctx).ProvisionMode,
	)
	instanceTypeProvider := instancetype.NewDefaultProvider(
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
	instanceProvider := instance.NewDefaultProvider(
		azClient,
		instanceTypeProvider,
		launchTemplateProvider,
		loadBalancerProvider,
		unavailableOfferingsCache,
		azConfig.Location,
		azConfig.NodeResourceGroup,
		azConfig.SubscriptionID,
		options.FromContext(ctx).ProvisionMode,
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

func getVnetGUID(cfg *auth.Config, subnetID string) (string, error) {
	creds, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return "", err
	}
	opts := armopts.DefaultArmOpts()
	vnetClient, err := armnetwork.NewVirtualNetworksClient(cfg.SubscriptionID, creds, opts)
	if err != nil {
		return "", err
	}

	subnetParts, err := utils.GetVnetSubnetIDComponents(subnetID)
	if err != nil {
		return "", err
	}
	vnet, err := vnetClient.Get(context.Background(), subnetParts.ResourceGroupName, subnetParts.VNetName, nil)
	if err != nil {
		return "", err
	}
	if vnet.Properties == nil || vnet.Properties.ResourceGUID == nil {
		return "", fmt.Errorf("vnet %s does not have a resource GUID", subnetParts.VNetName)
	}
	return *vnet.Properties.ResourceGUID, nil
}
