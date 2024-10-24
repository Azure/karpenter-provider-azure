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
	"sync"

	"github.com/awslabs/operatorpkg/controller"
	"github.com/awslabs/operatorpkg/object"
	"github.com/awslabs/operatorpkg/status"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
	knativeinjection "knative.dev/pkg/injection"
	"knative.dev/pkg/ptr"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	karpv1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
	karpenteroptions "sigs.k8s.io/karpenter/pkg/operator/options"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	webhooksalt "github.com/Azure/karpenter-provider-azure/pkg/alt/karpenter-core/pkg/webhooks"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	armopts "github.com/Azure/karpenter-provider-azure/pkg/utils/opts"
	"sigs.k8s.io/karpenter/pkg/operator"
)

func init() {
	karpv1.NormalizedLabels = lo.Assign(karpv1.NormalizedLabels, map[string]string{"topology.disk.csi.azure.com/zone": corev1.LabelTopologyZone})
	karpv1beta1.NormalizedLabels = lo.Assign(karpv1.NormalizedLabels, map[string]string{"topology.disk.csi.azure.com/zone": corev1.LabelTopologyZone})
}

type Operator struct {
	*operator.Operator

	UnavailableOfferingsCache *azurecache.UnavailableOfferings

	ImageProvider          *imagefamily.Provider
	ImageResolver          *imagefamily.Resolver
	LaunchTemplateProvider *launchtemplate.Provider
	PricingProvider        *pricing.Provider
	InstanceTypesProvider  instancetype.Provider
	InstanceProvider       *instance.DefaultProvider
	LoadBalancerProvider   *loadbalancer.Provider

	// Copied from the core Operator because we control our own webhooks
	webhooks []knativeinjection.ControllerConstructor
}

func NewOperator(ctx context.Context, operator *operator.Operator) (context.Context, *Operator) {
	azConfig, err := GetAZConfig()
	lo.Must0(err, "creating Azure config") // NOTE: we prefer this over the cleaner azConfig := lo.Must(GetAzConfig()), as when initializing the client there are helpful error messages in initializing clients and the azure config

	azClient, err := instance.CreateAZClient(ctx, azConfig)
	lo.Must0(err, "creating Azure client")

	vnetGUID, err := getVnetGUID(azConfig, options.FromContext(ctx).SubnetID)
	lo.Must0(err, "getting VNET GUID")

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
	imageResolver := imagefamily.New(
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
		vnetGUID,
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

// Copied from karpenter-core pkg/operator/operator.go, needed for webhooks
func (o *Operator) WithControllers(ctx context.Context, controllers ...controller.Controller) *Operator {
	for _, c := range controllers {
		lo.Must0(c.Register(ctx, o.Manager))
	}
	return o
}

// Copied from karpenter-core pkg/operator/operator.go, needed for webhooks
func (o *Operator) WithWebhooks(ctx context.Context, ctors ...knativeinjection.ControllerConstructor) *Operator {
	if !karpenteroptions.FromContext(ctx).DisableWebhook {
		o.webhooks = append(o.webhooks, ctors...)
		lo.Must0(o.Manager.AddReadyzCheck("webhooks", webhooksalt.HealthProbe(ctx)))
		lo.Must0(o.Manager.AddHealthzCheck("webhooks", webhooksalt.HealthProbe(ctx)))
	}
	return o
}

// Copied from karpenter-core pkg/operator/operator.go, needed for webhooks
func (o *Operator) Start(ctx context.Context, cp cloudprovider.CloudProvider) {
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		lo.Must0(o.Manager.Start(ctx))
	}()
	if karpenteroptions.FromContext(ctx).DisableWebhook {
		log.FromContext(ctx).Info("conversion webhooks are disabled")
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Taking the first supported NodeClass to be the default NodeClass
			gvk := lo.Map(cp.GetSupportedNodeClasses(), func(nc status.Object, _ int) schema.GroupVersionKind {
				return object.GVK(nc)
			})
			ctx = injection.WithNodeClasses(ctx, gvk)
			ctx = injection.WithClient(ctx, o.GetClient())
			webhooksalt.Start(ctx, o.GetConfig(), o.webhooks...) // This is our alt copy of webhooks that can support multiple apiservers
		}()
	}
	wg.Wait()
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
