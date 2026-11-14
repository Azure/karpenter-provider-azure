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
	"errors"
	"fmt"
	"reflect"
	"strings"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/util/sets"
)

const publicIPOwnerTag = "karpenter.azure.com_public-ip-owner"

func (p *DefaultVMProvider) attachPublicIP(ctx context.Context, nic *armnetwork.Interface, opts *createNICOptions) error {
	if nic.Properties == nil || nic.Properties.NetworkSecurityGroup == nil {
		return fmt.Errorf("a network security group is required before attaching a node public IP")
	}
	configuration, ok := lo.Find(nic.Properties.IPConfigurations, func(config *armnetwork.InterfaceIPConfiguration) bool {
		return config != nil && config.Properties != nil && lo.FromPtr(config.Properties.Primary)
	})
	if !ok {
		return fmt.Errorf("primary NIC IP configuration is missing")
	}
	address, err := p.ensurePublicIP(ctx, opts.NICName, opts.Zone, opts.LaunchTemplate.Tags)
	if err != nil {
		return err
	}
	if lo.FromPtr(address.ID) == "" {
		return fmt.Errorf("created node public IP has no resource ID")
	}
	backendPools := lo.Map(opts.BackendPools.IPv4PoolIDs, func(poolID string, _ int) *armnetwork.BackendAddressPool {
		return &armnetwork.BackendAddressPool{ID: lo.ToPtr(poolID)}
	})
	if publicIPBindingMatches(configuration, address, backendPools) {
		return nil
	}
	configuration.Properties.LoadBalancerBackendAddressPools = backendPools
	configuration.Properties.PublicIPAddress = &armnetwork.PublicIPAddress{ID: address.ID}
	_, err = createNic(ctx, p.azClient.NetworkInterfacesClient(), p.resourceGroup, opts.NICName, *nic)
	if err != nil {
		return fmt.Errorf("attaching public IP to NIC %q: %w", opts.NICName, err)
	}
	return nil
}

func publicIPBindingMatches(configuration *armnetwork.InterfaceIPConfiguration, address *armnetwork.PublicIPAddress, backendPools []*armnetwork.BackendAddressPool) bool {
	current := configuration.Properties.PublicIPAddress
	return current != nil && strings.EqualFold(lo.FromPtr(current.ID), lo.FromPtr(address.ID)) &&
		reflect.DeepEqual(configuration.Properties.LoadBalancerBackendAddressPools, backendPools)
}

func ownedPublicIP(address *armnetwork.PublicIPAddress, name, cluster string) bool {
	return address != nil && lo.FromPtr(address.Tags[publicIPOwnerTag]) == name &&
		lo.FromPtr(address.Tags[launchtemplate.KarpenterManagedTagKey]) == cluster
}

func (p *DefaultVMProvider) ensurePublicIP(ctx context.Context, name, zone string, tags map[string]*string) (*armnetwork.PublicIPAddress, error) {
	client := p.azClient.PublicIPAddressesClient
	if client == nil {
		return nil, fmt.Errorf("public IP client is not configured")
	}
	existing, err := client.Get(ctx, p.resourceGroup, name, nil)
	if err == nil {
		if !ownedPublicIP(&existing.PublicIPAddress, name, options.FromContext(ctx).ClusterName) {
			return nil, fmt.Errorf("refusing to reuse public IP %q not owned by this node", name)
		}
		return &existing.PublicIPAddress, nil
	}
	if !sdkerrors.IsNotFoundErr(err) {
		return nil, fmt.Errorf("getting public IP %q: %w", name, err)
	}
	address := armnetwork.PublicIPAddress{
		Location: lo.ToPtr(p.location),
		SKU: &armnetwork.PublicIPAddressSKU{
			Name: lo.ToPtr(armnetwork.PublicIPAddressSKUNameStandard),
			Tier: lo.ToPtr(armnetwork.PublicIPAddressSKUTierRegional),
		},
		Zones: zones.MakeARMZonesFromAKSLabelZone(zone),
		Tags: lo.Assign(tags, map[string]*string{
			publicIPOwnerTag:                      &name,
			launchtemplate.KarpenterManagedTagKey: lo.ToPtr(options.FromContext(ctx).ClusterName),
		}),
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: lo.ToPtr(armnetwork.IPAllocationMethodStatic),
			PublicIPAddressVersion:   lo.ToPtr(armnetwork.IPVersionIPv4),
		},
	}
	poller, err := client.BeginCreateOrUpdate(ctx, p.resourceGroup, name, address, nil)
	if err != nil {
		return nil, fmt.Errorf("creating public IP %q: %w", name, err)
	}
	result, err := poller.PollUntilDone(ctx, defaultPollerOptions())
	if err != nil {
		return nil, fmt.Errorf("waiting for public IP %q: %w", name, err)
	}
	return &result.PublicIPAddress, nil
}

func (p *DefaultVMProvider) deletePublicIP(ctx context.Context, name string) error {
	client := p.azClient.PublicIPAddressesClient
	if client == nil {
		return nil
	}
	address, err := client.Get(ctx, p.resourceGroup, name, nil)
	if sdkerrors.IsNotFoundErr(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("getting public IP for deletion: %w", err)
	}
	if !ownedPublicIP(&address.PublicIPAddress, name, options.FromContext(ctx).ClusterName) {
		return nil
	}
	poller, err := client.BeginDelete(ctx, p.resourceGroup, name, nil)
	if sdkerrors.IsNotFoundErr(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("deleting public IP %q: %w", name, err)
	}
	_, err = poller.PollUntilDone(ctx, defaultPollerOptions())
	if sdkerrors.IsNotFoundErr(err) {
		return nil
	}
	return err
}

func (p *DefaultVMProvider) CleanupPublicIPs(ctx context.Context, protected sets.Set[string]) error {
	client := p.azClient.PublicIPAddressesClient
	if client == nil {
		return nil
	}
	var failures []error
	pager := client.NewListPager(p.resourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return errors.Join(append(failures, fmt.Errorf("listing node public IPs: %w", err))...)
		}
		for _, address := range page.Value {
			orphan, err := p.publicIPIsOrphan(ctx, address, protected)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if orphan {
				if err := p.deletePublicIP(ctx, lo.FromPtr(address.Name)); err != nil {
					failures = append(failures, err)
				}
			}
		}
	}
	return errors.Join(failures...)
}

func (p *DefaultVMProvider) publicIPIsOrphan(ctx context.Context, address *armnetwork.PublicIPAddress, protected sets.Set[string]) (bool, error) {
	if address == nil || lo.FromPtr(address.Name) == "" {
		return false, nil
	}
	name := *address.Name
	if protected.Has(name) || !ownedPublicIP(address, name, options.FromContext(ctx).ClusterName) {
		return false, nil
	}
	if address.Properties != nil && address.Properties.IPConfiguration != nil {
		return false, nil
	}
	_, err := p.GetNic(ctx, p.resourceGroup, name)
	if sdkerrors.IsNotFoundErr(err) {
		return true, nil
	}
	return false, err
}
