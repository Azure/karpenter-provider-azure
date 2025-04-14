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

package launchtemplate

import (
	"context"
	"strconv"
	"strings"

	"github.com/Azure/go-autorest/autorest/to"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

const (
	karpenterManagedTagKey = "karpenter.azure.com/cluster"

	dataplaneLabel       = "kubernetes.azure.com/ebpf-dataplane"
	azureCNIOverlayLabel = "kubernetes.azure.com/azure-cni-overlay"
	subnetNameLabel      = "kubernetes.azure.com/network-subnet"
	vnetGUIDLabel        = "kubernetes.azure.com/nodenetwork-vnetguid"
	podNetworkTypeLabel  = "kubernetes.azure.com/podnetwork-type"
)

type Template struct {
	ScriptlessCustomData    string
	ImageID                 string
	SubnetID                string
	Tags                    map[string]*string
	CustomScriptsCustomData string
	CustomScriptsCSE        string
	IsWindows               bool
	StorageProfile          string
}

type Provider struct {
	imageFamily             imagefamily.Resolver
	imageProvider           *imagefamily.Provider
	caBundle                *string
	clusterEndpoint         string
	tenantID                string
	subscriptionID          string
	kubeletIdentityClientID string
	resourceGroup           string
	clusterResourceGroup    string
	location                string
	vnetGUID                string
	provisionMode           string
}

// TODO: add caching of launch templates

func NewProvider(_ context.Context, imageFamily imagefamily.Resolver, imageProvider *imagefamily.Provider, caBundle *string, clusterEndpoint string,
	tenantID, subscriptionID, clusterResourceGroup string, kubeletIdentityClientID, resourceGroup, location, vnetGUID, provisionMode string,
) *Provider {
	return &Provider{
		imageFamily:             imageFamily,
		imageProvider:           imageProvider,
		caBundle:                caBundle,
		clusterEndpoint:         clusterEndpoint,
		tenantID:                tenantID,
		subscriptionID:          subscriptionID,
		kubeletIdentityClientID: kubeletIdentityClientID,
		resourceGroup:           resourceGroup,
		clusterResourceGroup:    clusterResourceGroup,
		location:                location,
		vnetGUID:                vnetGUID,
		provisionMode:           provisionMode,
	}
}

func (p *Provider) GetTemplate(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass, nodeClaim *karpv1.NodeClaim,
	instanceType *cloudprovider.InstanceType, additionalLabels map[string]string) (*Template, error) {
	staticParameters, err := p.getStaticParameters(ctx, instanceType, nodeClass, lo.Assign(nodeClaim.Labels, additionalLabels))
	if err != nil {
		return nil, err
	}

	// Note: we check ValidateKubernetesVersionReadiness at the start of the Create call. However, to ensure no call paths slip through, also calling it
	// where we use the KubernetesVersion
	if err = nodeClass.ValidateKubernetesVersionReadiness(); err != nil {
		return nil, err
	}
	staticParameters.KubernetesVersion = nodeClass.Status.KubernetesVersion
	templateParameters, err := p.imageFamily.Resolve(ctx, nodeClass, nodeClaim, instanceType, staticParameters)
	if err != nil {
		return nil, err
	}
	launchTemplate, err := p.createLaunchTemplate(ctx, templateParameters)
	if err != nil {
		return nil, err
	}

	return launchTemplate, nil
}

func (p *Provider) getStaticParameters(ctx context.Context, instanceType *cloudprovider.InstanceType, nodeClass *v1alpha2.AKSNodeClass, labels map[string]string) (*parameters.StaticParameters, error) {
	var arch string = karpv1.ArchitectureAmd64
	if err := instanceType.Requirements.Compatible(scheduling.NewRequirements(scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureArm64))); err == nil {
		arch = karpv1.ArchitectureArm64
	}

	subnetID := lo.Ternary(nodeClass.Spec.VNETSubnetID != nil, lo.FromPtr(nodeClass.Spec.VNETSubnetID), options.FromContext(ctx).SubnetID)

	if isAzureCNIOverlay(ctx) {
		// TODO: make conditional on pod subnet
		vnetLabels, err := p.getVnetInfoLabels(subnetID)
		if err != nil {
			return nil, err
		}
		labels = lo.Assign(labels, vnetLabels)
	}

	if options.FromContext(ctx).NetworkDataplane == consts.NetworkDataplaneCilium {
		// This label is required for the cilium agent daemonset because
		// we select the nodes for the daemonset based on this label
		//              - key: kubernetes.azure.com/ebpf-dataplane
		//            operator: In
		//            values:
		//              - cilium

		labels[dataplaneLabel] = consts.NetworkDataplaneCilium
	}

	return &parameters.StaticParameters{
		ClusterName:                    options.FromContext(ctx).ClusterName,
		ClusterEndpoint:                p.clusterEndpoint,
		Tags:                           nodeClass.Spec.Tags,
		Labels:                         labels,
		CABundle:                       p.caBundle,
		Arch:                           arch,
		GPUNode:                        utils.IsNvidiaEnabledSKU(instanceType.Name),
		GPUDriverVersion:               utils.GetGPUDriverVersion(instanceType.Name),
		GPUDriverType:                  utils.GetGPUDriverType(instanceType.Name),
		GPUImageSHA:                    utils.GetAKSGPUImageSHA(instanceType.Name),
		TenantID:                       p.tenantID,
		SubscriptionID:                 p.subscriptionID,
		KubeletIdentityClientID:        p.kubeletIdentityClientID,
		ResourceGroup:                  p.resourceGroup,
		Location:                       p.location,
		ClusterID:                      options.FromContext(ctx).ClusterID,
		APIServerName:                  options.FromContext(ctx).GetAPIServerName(),
		KubeletClientTLSBootstrapToken: options.FromContext(ctx).KubeletClientTLSBootstrapToken,
		NetworkPlugin:                  getAgentbakerNetworkPlugin(ctx),
		NetworkPolicy:                  options.FromContext(ctx).NetworkPolicy,
		SubnetID:                       subnetID,
		ClusterResourceGroup:           p.clusterResourceGroup,
	}, nil
}

func getAgentbakerNetworkPlugin(ctx context.Context) string {
	if isAzureCNIOverlay(ctx) || isCiliumNodeSubnet(ctx) || isNetworkPluginNone(ctx) {
		return consts.NetworkPluginNone
	}
	return consts.NetworkPluginAzure
}

func isNetworkPluginNone(ctx context.Context) bool {
	return options.FromContext(ctx).NetworkPlugin == consts.NetworkPluginNone
}

func isCiliumNodeSubnet(ctx context.Context) bool {
	return options.FromContext(ctx).NetworkPlugin == consts.NetworkPluginAzure && options.FromContext(ctx).NetworkPluginMode == consts.NetworkPluginModeNone && options.FromContext(ctx).NetworkDataplane == consts.NetworkDataplaneCilium
}

func isAzureCNIOverlay(ctx context.Context) bool {
	return options.FromContext(ctx).NetworkPlugin == consts.NetworkPluginAzure && options.FromContext(ctx).NetworkPluginMode == consts.NetworkPluginModeOverlay
}

func (p *Provider) createLaunchTemplate(ctx context.Context, params *parameters.Parameters) (*Template, error) {
	// merge and convert to ARM tags
	azureTags := mergeTags(params.Tags, map[string]string{karpenterManagedTagKey: params.ClusterName})
	template := &Template{
		ImageID:        params.ImageID,
		Tags:           azureTags,
		SubnetID:       params.SubnetID,
		IsWindows:      params.IsWindows,
		StorageProfile: params.StorageProfile,
	}

	if p.provisionMode == consts.ProvisionModeBootstrappingClient {
		customData, cse, err := params.CustomScriptsNodeBootstrapping.GetCustomDataAndCSE(ctx)
		if err != nil {
			return nil, err
		}
		template.CustomScriptsCustomData = customData
		template.CustomScriptsCSE = cse
	} else {
		// render user data
		userData, err := params.ScriptlessCustomData.Script()
		if err != nil {
			return nil, err
		}
		template.ScriptlessCustomData = userData
	}

	return template, nil
}

// MergeTags takes a variadic list of maps and merges them together
// with format acceptable to ARM (no / in keys, pointer to strings as values)
func mergeTags(tags ...map[string]string) (result map[string]*string) {
	return lo.MapEntries(lo.Assign(tags...), func(key string, value string) (string, *string) {
		return strings.ReplaceAll(key, "/", "_"), to.StringPtr(value)
	})
}

func (p *Provider) getVnetInfoLabels(subnetID string) (map[string]string, error) {
	vnetSubnetComponents, err := utils.GetVnetSubnetIDComponents(subnetID)
	if err != nil {
		return nil, err
	}
	vnetLabels := map[string]string{
		subnetNameLabel:      vnetSubnetComponents.SubnetName,
		vnetGUIDLabel:        p.vnetGUID,
		azureCNIOverlayLabel: strconv.FormatBool(true),
		podNetworkTypeLabel:  consts.NetworkPluginModeOverlay,
	}
	return vnetLabels, nil
}
