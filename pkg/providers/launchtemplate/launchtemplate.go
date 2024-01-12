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
	"strings"

	"github.com/Azure/go-autorest/autorest/to"
	"github.com/Azure/karpenter/pkg/providers/imagefamily"
	"github.com/Azure/karpenter/pkg/providers/launchtemplate/parameters"
	"github.com/Azure/karpenter/pkg/utils"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"

	"github.com/Azure/karpenter/pkg/apis/settings"
	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"github.com/aws/karpenter-core/pkg/cloudprovider"
	"github.com/aws/karpenter-core/pkg/scheduling"
)

const (
	karpenterManagedTagKey = "karpenter.azure.com/cluster"
)

type Template struct {
	UserData string
	ImageID  string
	Tags     map[string]*string
}

type Provider struct {
	imageFamily            *imagefamily.Resolver
	imageProvider          *imagefamily.Provider
	caBundle               *string
	clusterEndpoint        string
	tenantID               string
	subscriptionID         string
	userAssignedIdentityID string
	resourceGroup          string
	location               string
}

// TODO: add caching of launch templates

func NewProvider(_ context.Context, imageFamily *imagefamily.Resolver, imageProvider *imagefamily.Provider, caBundle *string, clusterEndpoint string,
	tenantID, subscriptionID, userAssignedIdentityID, resourceGroup, location string,
) *Provider {
	return &Provider{
		imageFamily:            imageFamily,
		imageProvider:          imageProvider,
		caBundle:               caBundle,
		clusterEndpoint:        clusterEndpoint,
		tenantID:               tenantID,
		subscriptionID:         subscriptionID,
		userAssignedIdentityID: userAssignedIdentityID,
		resourceGroup:          resourceGroup,
		location:               location,
	}
}

func (p *Provider) GetTemplate(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass, nodeClaim *corev1beta1.NodeClaim,
	instanceType *cloudprovider.InstanceType, additionalLabels map[string]string) (*Template, error) {
	staticParameters := p.getStaticParameters(ctx, instanceType, nodeClass, lo.Assign(nodeClaim.Labels, additionalLabels))
	kubeServerVersion, err := p.imageProvider.KubeServerVersion(ctx)
	if err != nil {
		return nil, err
	}
	staticParameters.KubernetesVersion = kubeServerVersion
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

func (p *Provider) getStaticParameters(ctx context.Context, instanceType *cloudprovider.InstanceType, nodeTemplate *v1alpha2.AKSNodeClass, labels map[string]string) *parameters.StaticParameters {
	var arch string = corev1beta1.ArchitectureAmd64
	if err := instanceType.Requirements.Compatible(scheduling.NewRequirements(scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, corev1beta1.ArchitectureArm64))); err == nil {
		arch = corev1beta1.ArchitectureArm64
	}

	return &parameters.StaticParameters{
		ClusterName:                    settings.FromContext(ctx).ClusterName,
		ClusterEndpoint:                p.clusterEndpoint,
		Tags:                           lo.Assign(settings.FromContext(ctx).Tags, nodeTemplate.Spec.Tags),
		Labels:                         labels,
		CABundle:                       p.caBundle,
		Arch:                           arch,
		GPUNode:                        utils.IsNvidiaEnabledSKU(instanceType.Name),
		GPUDriverVersion:               utils.GetGPUDriverVersion(instanceType.Name),
		GPUImageSHA:                    utils.GetAKSGPUImageSHA(instanceType.Name),
		TenantID:                       p.tenantID,
		SubscriptionID:                 p.subscriptionID,
		UserAssignedIdentityID:         p.userAssignedIdentityID,
		ResourceGroup:                  p.resourceGroup,
		Location:                       p.location,
		ClusterID:                      settings.FromContext(ctx).ClusterID,
		APIServerName:                  settings.FromContext(ctx).GetAPIServerName(),
		KubeletClientTLSBootstrapToken: settings.FromContext(ctx).KubeletClientTLSBootstrapToken,
		NetworkPlugin:                  settings.FromContext(ctx).NetworkPlugin,
		NetworkPolicy:                  settings.FromContext(ctx).NetworkPolicy,
	}
}

func (p *Provider) createLaunchTemplate(_ context.Context, options *parameters.Parameters) (*Template, error) {
	// render user data
	userData, err := options.UserData.Script()
	if err != nil {
		return nil, err
	}

	// merge and convert to ARM tags
	azureTags := mergeTags(options.Tags, map[string]string{karpenterManagedTagKey: options.ClusterName})
	template := &Template{
		UserData: userData,
		ImageID:  options.ImageID,
		Tags:     azureTags,
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
