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

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/allocationstrategy"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/fleet"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/networksecuritygroup"
)

// FleetProvider is the interface for fleet-based instance creation.
type FleetProvider interface {
	BeginCreate(ctx context.Context, nodeClass *v1beta1.AKSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType) (*FleetMemberPromise, error)
}

// Ensure DefaultFleetProvider implements FleetProvider.
var _ FleetProvider = (*DefaultFleetProvider)(nil)

// DefaultFleetProvider orchestrates fleet-based VM creation.
type DefaultFleetProvider struct {
	fleetBatchClient             *fleet.Client
	launchTemplateProvider       *launchtemplate.Provider
	allocationStrategyProvider   allocationstrategy.Provider
	loadBalancerProvider         *loadbalancer.Provider
	networkSecurityGroupProvider *networksecuritygroup.Provider
	location                     string
	resourceGroup                string
	subscriptionID               string
	diskEncryptionSetID          string
}

// NewFleetProvider creates a new DefaultFleetProvider.
func NewFleetProvider(
	fleetBatchClient *fleet.Client,
	launchTemplateProvider *launchtemplate.Provider,
	allocationStrategyProvider allocationstrategy.Provider,
	loadBalancerProvider *loadbalancer.Provider,
	networkSecurityGroupProvider *networksecuritygroup.Provider,
	location, resourceGroup, subscriptionID, diskEncryptionSetID string,
) *DefaultFleetProvider {
	return &DefaultFleetProvider{
		fleetBatchClient:             fleetBatchClient,
		launchTemplateProvider:       launchTemplateProvider,
		allocationStrategyProvider:   allocationStrategyProvider,
		loadBalancerProvider:         loadBalancerProvider,
		networkSecurityGroupProvider: networkSecurityGroupProvider,
		location:                     location,
		resourceGroup:                resourceGroup,
		subscriptionID:               subscriptionID,
		diskEncryptionSetID:          diskEncryptionSetID,
	}
}

// BeginCreate starts fleet-based instance creation for a NodeClaim.
// It resolves the launch template, builds the FleetCreateRequest, and submits to the batch client.
func (p *DefaultFleetProvider) BeginCreate(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*cloudprovider.InstanceType,
) (*FleetMemberPromise, error) {
	// TODO:
	// 1. Resolve launch template via launchTemplateProvider
	// 2. Get LB backend pools via loadBalancerProvider
	// 3. Get NSG via networkSecurityGroupProvider
	// 4. Build FleetCreateRequest with all fields
	// 5. Call fleetBatchClient.BeginCreateWithFleet()
	// 6. Return FleetMemberPromise from response.SharedState
	return nil, fmt.Errorf("not implemented")
}
