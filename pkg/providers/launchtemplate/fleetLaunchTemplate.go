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
	"fmt"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	karplabels "github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/networksecuritygroup"
	"github.com/samber/lo"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

// GetFleetTemplate is the Fleet counterpart of GetTemplate. It builds one template shared by all Fleet
// candidate SKUs: labels come from the NodeClaim requirements rather than a single SKU, and the
// instance type is only used to resolve the shared image and OS disk profile.
func (p *Provider) GetFleetTemplate(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceType *cloudprovider.InstanceType,
	architecture string,
	additionalLabels map[string]string,
) (*Template, error) {
	if p.provisionMode != consts.ProvisionModeAKSScriptless {
		return nil, fmt.Errorf("fleet templates require provision mode %q, got %q", consts.ProvisionModeAKSScriptless, p.provisionMode)
	}
	resolver, ok := p.imageFamily.(imagefamily.FleetResolver)
	if !ok {
		return nil, fmt.Errorf("image family resolver %T does not support Fleet templates", p.imageFamily)
	}

	staticParameters, err := p.getFleetStaticParameters(ctx, nodeClass, nodeClaim, architecture, lo.Assign(nodeClaim.Labels, additionalLabels))
	if err != nil {
		return nil, err
	}

	kubernetesVersion, err := nodeClass.GetKubernetesVersion()
	if err != nil {
		return nil, err
	}
	staticParameters.KubernetesVersion = kubernetesVersion
	templateParameters, err := resolver.ResolveFleet(ctx, nodeClass, nodeClaim, instanceType, staticParameters)
	if err != nil {
		return nil, err
	}
	launchTemplate, err := p.createLaunchTemplate(ctx, templateParameters)
	if err != nil {
		return nil, err
	}

	launchTemplate.Tags = Tags(options.FromContext(ctx), nodeClass, nodeClaim)
	if nodeClass.Spec.Security != nil {
		launchTemplate.EncryptionAtHost = nodeClass.Spec.Security.EncryptionAtHost
	}

	return launchTemplate, nil
}

// getFleetStaticParameters is the Fleet counterpart of getStaticParameters. Labels are derived from the
// NodeClaim requirements, and GPU settings are left unset because the template is shared across SKUs.
func (p *Provider) getFleetStaticParameters(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	architecture string,
	labels map[string]string,
) (*parameters.StaticParameters, error) {
	requirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)

	subnetID := lo.Ternary(nodeClass.Spec.VNETSubnetID != nil, lo.FromPtr(nodeClass.Spec.VNETSubnetID), options.FromContext(ctx).SubnetID)
	baseLabels, err := karplabels.Get(ctx, nodeClass, architecture, requirements)
	if err != nil {
		return nil, err
	}
	labels = lo.Assign(baseLabels, labels)

	// Remove labels kubelet can't set (e.g. kubernetes.io/*, k8s.io/* outside allowed namespaces)
	labels = lo.OmitBy(labels, func(key string, _ string) bool {
		return !karplabels.CanKubeletSetLabel(key)
	})

	nsg, err := p.nsgProvider.ManagedNetworkSecurityGroup(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting managed network security group: %w", err)
	}
	nsgName := lo.FromPtr(nsg.Name)
	clusterID := networksecuritygroup.GetClusterIDFromNSGName(nsgName)
	routeTableName := fmt.Sprintf("aks-agentpool-%s-routetable", clusterID)

	return &parameters.StaticParameters{
		ClusterName:                    options.FromContext(ctx).ClusterName,
		ClusterEndpoint:                p.clusterEndpoint,
		Labels:                         labels,
		CABundle:                       p.caBundle,
		Arch:                           architecture,
		GPUNode:                        false,
		GPUDriverInstallationEnabled:   nodeClass.IsGPUDriverInstallationEnabled(),
		TenantID:                       p.tenantID,
		SubscriptionID:                 p.subscriptionID,
		KubeletIdentityClientID:        p.kubeletIdentityClientID,
		ResourceGroup:                  p.resourceGroup,
		Location:                       p.location,
		NetworkSecurityGroupName:       nsgName,
		RouteTableName:                 routeTableName,
		APIServerName:                  options.FromContext(ctx).GetAPIServerName(),
		KubeletClientTLSBootstrapToken: options.FromContext(ctx).KubeletClientTLSBootstrapToken,
		NetworkPlugin:                  getAgentbakerNetworkPlugin(ctx),
		NetworkPolicy:                  options.FromContext(ctx).NetworkPolicy,
		SubnetID:                       subnetID,
		ClusterResourceGroup:           p.clusterResourceGroup,
	}, nil
}
