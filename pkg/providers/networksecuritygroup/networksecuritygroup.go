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

package networksecuritygroup

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type API interface {
	Get(ctx context.Context, resourceGroupName string, securityGroupName string, options *armnetwork.SecurityGroupsClientGetOptions) (armnetwork.SecurityGroupsClientGetResponse, error)
	NewListPager(resourceGroupName string, options *armnetwork.SecurityGroupsClientListOptions) *runtime.Pager[armnetwork.SecurityGroupsClientListResponse]
}

var managedNSGRegex = regexp.MustCompile(`(?i)^aks-agentpool-\d{8}-nsg$`)

type Provider struct {
	nsgAPI        API
	resourceGroup string

	nsg *armnetwork.SecurityGroup
	mu  sync.Mutex
}

// NewProvider creates a new LoadBalancer provider
func NewProvider(nsgAPI API, resourceGroup string) *Provider {
	return &Provider{
		nsgAPI:        nsgAPI,
		resourceGroup: resourceGroup,
	}
}

func (p *Provider) ManagedNetworkSecurityGroup(ctx context.Context) (*armnetwork.SecurityGroup, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// If we've already found the managed NSG, returned the cached result
	if p.nsg != nil {
		return p.nsg, nil
	}

	nsgs, err := p.loadFromAzure(ctx)
	if err != nil {
		return nil, err
	}

	// Only consider the NSGs we actually care about
	managedNSGs := lo.Filter(nsgs, isClusterNSG)
	log.FromContext(ctx).Info("found NSGs of interest", "nsgCount", len(managedNSGs))

	if len(managedNSGs) == 0 {
		return nil, fmt.Errorf("couldn't find managed NSG")
	}
	if len(managedNSGs) > 1 {
		return nil, fmt.Errorf("found multiple NSGs: %s", strings.Join(lo.Map(managedNSGs, func(nsg *armnetwork.SecurityGroup, _ int) string { return lo.FromPtr(nsg.Name) }), ","))
	}

	p.nsg = managedNSGs[0]
	return p.nsg, nil
}

func (p *Provider) loadFromAzure(ctx context.Context) ([]*armnetwork.SecurityGroup, error) {
	log.FromContext(ctx).Info("querying nsgs in resource group", "resourceGroup", p.resourceGroup)

	pager := p.nsgAPI.NewListPager(p.resourceGroup, nil)

	var nsgs []*armnetwork.SecurityGroup
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get next nsg page: %w", err)
		}
		nsgs = append(nsgs, page.Value...)
	}

	return nsgs, nil
}

func isClusterNSG(nsg *armnetwork.SecurityGroup, _ int) bool {
	name := lo.FromPtr(nsg.Name)
	return managedNSGRegex.MatchString(name)
}
