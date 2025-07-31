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

package status

import (
	"context"
	"fmt"
	"net"

	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

type SubnetReconciler struct {
	subnetClient instance.SubnetsAPI
}

func NewSubnetReconciler(subnetClient instance.SubnetsAPI) *SubnetReconciler {
	return &SubnetReconciler{
		subnetClient: subnetClient,
	}
}

const ConditionTypeSubnetReady = "SubnetReady"

const (
	SubnetReadyReasonNotFound = "SubnetNotFound"
	SubnetReadyReasonFull     = "SubnetFull"
	SubnetReadyReasonInvalid  = "SubnetIDInvalid"
)

// Azure reserves the first four addresses and the last address, for a total of 5 ips for each subnet.
// For example 192.168.1.0/24 has the following reserved addresses
// 192.168.1.0 Network Address
// 192.168.1.1 Reserved by azure for the default gateway
// 192.168.1.2, 192.168.1.3: Reserved by Azure to map the Azure DNS IP addresses to the virtual network space.
// 192.168.1.255: Network broadcast address
const azureReservedIPs = 5

func (r *SubnetReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	subnetID := lo.Ternary(nodeClass.Spec.VNETSubnetID != nil, lo.FromPtr(nodeClass.Spec.VNETSubnetID), options.FromContext(ctx).SubnetID)
	subnetComponents, err := utils.GetVnetSubnetIDComponents(subnetID)
	if err != nil {
		nodeClass.StatusConditions().SetFalse(
			ConditionTypeSubnetReady,
			SubnetReadyReasonInvalid,
			fmt.Sprintf("Failed to parse subnet ID: %s", err.Error()),
		)
		return reconcile.Result{}, nil
	}

	subnet, err := r.subnetClient.Get(ctx, subnetComponents.ResourceGroupName, subnetComponents.VNetName, subnetComponents.SubnetName, nil)
	if err != nil {
		nodeClass.StatusConditions().SetFalse(
			ConditionTypeSubnetReady,
			SubnetReadyReasonNotFound,
			fmt.Sprintf("Subnet does not exist: %s", err.Error()),
		)
		return reconcile.Result{}, nil
	}

	if err := r.validateSubnetCapacity(&subnet.Subnet); err != nil {
		nodeClass.StatusConditions().SetFalse(
			ConditionTypeSubnetReady,
			SubnetReadyReasonFull,
			fmt.Sprintf("Subnet capacity issue: %s", err.Error()),
		)
		return reconcile.Result{}, nil
	}

	nodeClass.StatusConditions().SetTrue(ConditionTypeSubnetReady)
	return reconcile.Result{}, nil
}

func (r *SubnetReconciler) validateSubnetCapacity(subnet *armnetwork.Subnet) error {
	if subnet.Properties == nil || subnet.Properties.AddressPrefix == nil {
		return fmt.Errorf("subnet has no address prefix configured")
	}

	_, network, err := net.ParseCIDR(*subnet.Properties.AddressPrefix)
	if err != nil {
		return fmt.Errorf("invalid subnet address prefix: %w", err)
	}

	ones, bits := network.Mask.Size()
	totalIPs := 1 << (bits - ones)

	availableIPs := totalIPs - azureReservedIPs

	if subnet.Properties.IPConfigurations != nil {
		usedIPs := len(subnet.Properties.IPConfigurations)
		remainingIPs := availableIPs - usedIPs

		if remainingIPs <= 0 {
			return fmt.Errorf("insufficient IPs available: %d remaining out of %d total", remainingIPs, availableIPs)
		}
	}

	return nil
}
