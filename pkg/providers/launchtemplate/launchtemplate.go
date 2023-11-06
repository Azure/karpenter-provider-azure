// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package launchtemplate

import (
	"context"
	"strings"

	"github.com/Azure/go-autorest/autorest/to"
	"github.com/Azure/karpenter/pkg/providers/imagefamily"
	"github.com/Azure/karpenter/pkg/providers/launchtemplate/parameters"

	"github.com/samber/lo"

	"github.com/Azure/karpenter/pkg/apis/settings"
	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"

	"github.com/aws/karpenter-core/pkg/cloudprovider"
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
	staticParameters := p.getStaticParameters(ctx, nodeClass, lo.Assign(nodeClaim.Labels, additionalLabels))
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

func (p *Provider) getStaticParameters(ctx context.Context, nodeTemplate *v1alpha2.AKSNodeClass, labels map[string]string) *parameters.StaticParameters {
	return &parameters.StaticParameters{
		ClusterName:     settings.FromContext(ctx).ClusterName,
		ClusterEndpoint: p.clusterEndpoint,
		Tags:            lo.Assign(settings.FromContext(ctx).Tags, nodeTemplate.Spec.Tags),
		Labels:          labels,
		CABundle:        p.caBundle,

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
