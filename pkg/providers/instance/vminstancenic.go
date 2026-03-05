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

package instance

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

type createNICOptions struct {
	NICName                string
	BackendPools           *loadbalancer.BackendAddressPools
	InstanceType           *corecloudprovider.InstanceType
	LaunchTemplate         *launchtemplate.Template
	NetworkPlugin          string
	NetworkPluginMode      string
	MaxPods                int32
	NetworkSecurityGroupID string
}

func (p *DefaultVMProvider) newNetworkInterfaceForVM(opts *createNICOptions) armnetwork.Interface {
	var ipv4BackendPools []*armnetwork.BackendAddressPool
	for _, poolID := range opts.BackendPools.IPv4PoolIDs {
		ipv4BackendPools = append(ipv4BackendPools, &armnetwork.BackendAddressPool{
			ID: &poolID,
		})
	}

	skuAcceleratedNetworkingRequirements := scheduling.NewRequirements(
		scheduling.NewRequirement(v1beta1.LabelSKUAcceleratedNetworking, v1.NodeSelectorOpIn, "true"))

	enableAcceleratedNetworking := false
	if err := opts.InstanceType.Requirements.Compatible(skuAcceleratedNetworkingRequirements); err == nil {
		enableAcceleratedNetworking = true
	}

	var nsgRef *armnetwork.SecurityGroup
	if opts.NetworkSecurityGroupID != "" {
		nsgRef = &armnetwork.SecurityGroup{
			ID: &opts.NetworkSecurityGroupID,
		}
	}

	nic := armnetwork.Interface{
		Location: lo.ToPtr(p.location),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
				{
					Name: &opts.NICName,
					Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
						Primary:                   lo.ToPtr(true),
						PrivateIPAllocationMethod: lo.ToPtr(armnetwork.IPAllocationMethodDynamic),

						LoadBalancerBackendAddressPools: ipv4BackendPools,
					},
				},
			},
			NetworkSecurityGroup:        nsgRef,
			EnableAcceleratedNetworking: lo.ToPtr(enableAcceleratedNetworking),
			EnableIPForwarding:          lo.ToPtr(false),
		},
	}
	if opts.NetworkPlugin == consts.NetworkPluginAzure && opts.NetworkPluginMode != consts.NetworkPluginModeOverlay {
		// AzureCNI without overlay requires secondary IPs, for pods. (These IPs are not included in backend address pools.)
		// NOTE: Unlike AKS RP, this logic does not reduce secondary IP count by the number of expected hostNetwork pods, favoring simplicity instead
		for i := 1; i < int(opts.MaxPods); i++ {
			nic.Properties.IPConfigurations = append(
				nic.Properties.IPConfigurations,
				&armnetwork.InterfaceIPConfiguration{
					Name: lo.ToPtr(fmt.Sprintf("ipconfig%d", i)),
					Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
						Primary:                   lo.ToPtr(false),
						PrivateIPAllocationMethod: lo.ToPtr(armnetwork.IPAllocationMethodDynamic),
					},
				},
			)
		}
	}
	return nic
}

func (p *DefaultVMProvider) applyTemplateToNic(nic *armnetwork.Interface, template *launchtemplate.Template) {
	// set tags
	nic.Tags = template.Tags
	for _, ipConfig := range nic.Properties.IPConfigurations {
		ipConfig.Properties.Subnet = &armnetwork.Subnet{ID: &template.SubnetID}
	}
}

func (p *DefaultVMProvider) createNetworkInterface(ctx context.Context, opts *createNICOptions) (string, error) {
	nic := p.newNetworkInterfaceForVM(opts)
	p.applyTemplateToNic(&nic, opts.LaunchTemplate)
	log.FromContext(ctx).V(1).Info("creating network interface", "nicName", opts.NICName)
	res, err := createNic(ctx, p.azClient.NetworkInterfacesClient(), p.resourceGroup, opts.NICName, nic)
	if err != nil {
		return "", err
	}
	log.FromContext(ctx).V(1).Info("successfully created network interface", "nicName", opts.NICName, "nicID", *res.ID)
	return *res.ID, nil
}

// buildAndCreateNIC consolidates NIC creation: fetches backend pools, resolves NSG if needed, builds and creates the NIC.
func (p *DefaultVMProvider) buildAndCreateNIC(
	ctx context.Context,
	resourceName string,
	instanceType *corecloudprovider.InstanceType,
	nodeClass *v1beta1.AKSNodeClass,
	launchTemplate *launchtemplate.Template,
) (string, error) {
	backendPools, err := p.loadBalancerProvider.LoadBalancerBackendPools(ctx)
	if err != nil {
		return "", fmt.Errorf("getting backend pools: %w", err)
	}

	nodeResourceGroup := options.FromContext(ctx).NodeResourceGroup
	networkPlugin := options.FromContext(ctx).NetworkPlugin
	networkPluginMode := options.FromContext(ctx).NetworkPluginMode

	isAKSManagedVNET, err := utils.IsAKSManagedVNET(nodeResourceGroup, launchTemplate.SubnetID)
	if err != nil {
		return "", fmt.Errorf("checking if vnet is managed: %w", err)
	}
	var nsgID string
	if !isAKSManagedVNET {
		nsg, err := p.networkSecurityGroupProvider.ManagedNetworkSecurityGroup(ctx)
		if err != nil {
			return "", fmt.Errorf("getting managed network security group: %w", err)
		}
		nsgID = lo.FromPtr(nsg.ID)
	}

	nicReference, err := p.createNetworkInterface(
		ctx,
		&createNICOptions{
			NICName:                resourceName,
			NetworkPlugin:          networkPlugin,
			NetworkPluginMode:      networkPluginMode,
			MaxPods:                utils.GetMaxPods(nodeClass, networkPlugin, networkPluginMode),
			LaunchTemplate:         launchTemplate,
			BackendPools:           backendPools,
			InstanceType:           instanceType,
			NetworkSecurityGroupID: nsgID,
		},
	)
	if err != nil {
		return "", err
	}

	return nicReference, nil
}
