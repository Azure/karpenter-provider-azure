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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/go-logr/logr"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
	"k8s.io/client-go/util/flowcontrol"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/operator"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"

	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/kubernetesversion"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/networksecuritygroup"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	armopts "github.com/Azure/karpenter-provider-azure/pkg/utils/opts"
)

func init() {
	karpv1.NormalizedLabels = lo.Assign(karpv1.NormalizedLabels, map[string]string{"topology.disk.csi.azure.com/zone": corev1.LabelTopologyZone})
}

type Operator struct {
	*operator.Operator

	// InClusterKubernetesInterface is a Kubernetes client that can be used to talk to the APIServer
	// of the cluster where the Karpenter pod is running. This is usually the same as operator.KubernetesInterface,
	// but may be different if Karpenter is running in a different cluster than the one it manages.
	InClusterKubernetesInterface kubernetes.Interface

	UnavailableOfferingsCache *azurecache.UnavailableOfferings

	KubernetesVersionProvider kubernetesversion.KubernetesVersionProvider
	ImageProvider             imagefamily.NodeImageProvider
	ImageResolver             imagefamily.Resolver
	LaunchTemplateProvider    *launchtemplate.Provider
	PricingProvider           *pricing.Provider
	InstanceTypesProvider     instancetype.Provider
	VMInstanceProvider        *instance.DefaultVMProvider
	LoadBalancerProvider      *loadbalancer.Provider
	AZClient                  *instance.AZClient
}

func NewOperator(ctx context.Context, operator *operator.Operator) (context.Context, *Operator) {
	azConfig, err := GetAZConfig()
	lo.Must0(err, "creating Azure config") // NOTE: we prefer this over the cleaner azConfig := lo.Must(GetAzConfig()), as when initializing the client there are helpful error messages in initializing clients and the azure config

	log.FromContext(ctx).V(0).Info("Initial AZConfig", "azConfig", azConfig.String())

	cred, err := getCredential()
	lo.Must0(err, "getting Azure credential")

	env, err := auth.ResolveCloudEnvironment(azConfig)
	lo.Must0(err, "resolving cloud environment")

	// Get a token to ensure we can
	lo.Must0(ensureToken(cred, env), "ensuring Azure token can be retrieved")

	azClient, err := instance.NewAZClient(ctx, azConfig, env, cred)
	lo.Must0(err, "creating Azure client")
	if options.FromContext(ctx).VnetGUID == "" && options.FromContext(ctx).NetworkPluginMode == consts.NetworkPluginModeOverlay {
		vnetGUID, err := getVnetGUID(ctx, cred, azConfig, options.FromContext(ctx).SubnetID)
		lo.Must0(err, "getting VNET GUID")
		options.FromContext(ctx).VnetGUID = vnetGUID
	}

	// These options are set similarly to those used by operator.KubernetesInterface
	inClusterConfig := lo.Must(rest.InClusterConfig())
	inClusterConfig.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(float32(coreoptions.FromContext(ctx).KubeClientQPS), coreoptions.FromContext(ctx).KubeClientBurst)
	inClusterConfig.UserAgent = auth.GetUserAgentExtension()
	inClusterClient := kubernetes.NewForConfigOrDie(inClusterConfig)

	unavailableOfferingsCache := azurecache.NewUnavailableOfferings()
	pricingProvider := pricing.NewProvider(
		ctx,
		env,
		pricing.NewAPI(env.Cloud),
		azConfig.Location,
		operator.Elected(),
	)

	kubernetesVersionProvider := kubernetesversion.NewKubernetesVersionProvider(
		operator.KubernetesInterface,
		cache.New(azurecache.KubernetesVersionTTL,
			azurecache.DefaultCleanupInterval),
	)
	imageProvider := imagefamily.NewProvider(
		azClient.ImageVersionsClient,
		azConfig.Location,
		azConfig.SubscriptionID,
		azClient.NodeImageVersionsClient,
		cache.New(imagefamily.ImageExpirationInterval,
			imagefamily.ImageCacheCleaningInterval),
	)
	instanceTypeProvider := instancetype.NewDefaultProvider(
		azConfig.Location,
		cache.New(instancetype.InstanceTypesCacheTTL, azurecache.DefaultCleanupInterval),
		azClient.SKUClient,
		pricingProvider,
		unavailableOfferingsCache,
	)
	imageResolver := imagefamily.NewDefaultResolver(
		operator.GetClient(),
		imageProvider,
		instanceTypeProvider,
		azClient.NodeBootstrappingClient,
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
		options.FromContext(ctx).KubeletIdentityClientID,
		options.FromContext(ctx).NodeResourceGroup,
		azConfig.Location,
		options.FromContext(ctx).VnetGUID,
		options.FromContext(ctx).ProvisionMode,
	)
	loadBalancerProvider := loadbalancer.NewProvider(
		azClient.LoadBalancersClient,
		cache.New(loadbalancer.LoadBalancersCacheTTL, azurecache.DefaultCleanupInterval),
		options.FromContext(ctx).NodeResourceGroup,
	)
	networkSecurityGroupProvider := networksecuritygroup.NewProvider(
		azClient.NetworkSecurityGroupsClient,
		options.FromContext(ctx).NodeResourceGroup,
	)
	vmInstanceProvider := instance.NewDefaultVMProvider(
		azClient,
		instanceTypeProvider,
		launchTemplateProvider,
		loadBalancerProvider,
		networkSecurityGroupProvider,
		unavailableOfferingsCache,
		azConfig.Location,
		options.FromContext(ctx).NodeResourceGroup,
		azConfig.SubscriptionID,
		options.FromContext(ctx).ProvisionMode,
		options.FromContext(ctx).DiskEncryptionSetID,
	)

	return ctx, &Operator{
		Operator:                     operator,
		InClusterKubernetesInterface: inClusterClient,
		UnavailableOfferingsCache:    unavailableOfferingsCache,
		KubernetesVersionProvider:    kubernetesVersionProvider,
		ImageProvider:                imageProvider,
		ImageResolver:                imageResolver,
		LaunchTemplateProvider:       launchTemplateProvider,
		PricingProvider:              pricingProvider,
		InstanceTypesProvider:        instanceTypeProvider,
		VMInstanceProvider:           vmInstanceProvider,
		LoadBalancerProvider:         loadBalancerProvider,
		AZClient:                     azClient,
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
	return lo.ToPtr(base64.StdEncoding.EncodeToString(transportConfig.TLS.CAData)), nil
}

func getVnetGUID(ctx context.Context, creds azcore.TokenCredential, cfg *auth.Config, subnetID string) (string, error) {
	// TODO: Current the VNET client isn't used anywhere but this method. As such, it is not
	// held on azclient like the other clients.
	// We should possibly just put the vnet client on azclient, and then pass azclient in here, rather than
	// constructing the VNET client here separate from all the other Azure clients.
	env, err := auth.ResolveCloudEnvironment(cfg)
	if err != nil {
		return "", err
	}

	o := options.FromContext(ctx)
	opts := armopts.DefaultARMOpts(env.Cloud, o.EnableAzureSDKLogging)
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

// WaitForCRDs waits for the required CRDs to be available with a timeout
func WaitForCRDs(ctx context.Context, timeout time.Duration, config *rest.Config, log logr.Logger) error {
	gvk := func(obj runtime.Object) schema.GroupVersionKind {
		return lo.Must(apiutil.GVKForObject(obj, scheme.Scheme))
	}
	var requiredGVKs = []schema.GroupVersionKind{
		gvk(&karpv1.NodePool{}),
		gvk(&karpv1.NodeClaim{}),
		gvk(&v1beta1.AKSNodeClass{}),
	}

	client, err := rest.HTTPClientFor(config)
	if err != nil {
		return fmt.Errorf("creating kubernetes client, %w", err)
	}
	restMapper, err := apiutil.NewDynamicRESTMapper(config, client)
	if err != nil {
		return fmt.Errorf("creating dynamic rest mapper, %w", err)
	}

	log.Info("waiting for required CRDs to be available", "timeout", timeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for _, gvk := range requiredGVKs {
		err := wait.PollUntilContextCancel(ctx, 10*time.Second, true, func(ctx context.Context) (bool, error) {
			if _, err := restMapper.RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
				if meta.IsNoMatchError(err) {
					log.V(1).Info("waiting for CRD to be available", "gvk", gvk)
					return false, nil
				}
				return false, err
			}
			log.V(1).Info("CRD is available", "gvk", gvk)
			return true, nil
		})
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("timed out waiting for CRD %s to be available", gvk)
			}
			return fmt.Errorf("failed to wait for CRD %s: %w", gvk, err)
		}
	}

	log.Info("all required CRDs are available")
	return nil
}

// ensureToken ensures we can get a token for the Azure environment. Note that this doesn't actually
// use the token for anything, it just checks that we can get one.
func ensureToken(cred azcore.TokenCredential, env *auth.Environment) error {
	// Short timeout to avoid hanging forever if something bad happens
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{auth.TokenScope(env.Cloud)},
	})
	if err != nil {
		return err
	}

	return nil
}

func getCredential() (azcore.TokenCredential, error) {
	// TODO: Don't use NewDefaultAzureCredential
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}

	return auth.NewTokenWrapper(cred), nil
}
