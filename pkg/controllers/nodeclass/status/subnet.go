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
	"net/http"
	"time"

	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
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

// We can share some of the validation reasons between vnetSubnetID + podSubnetID
const (
	SubnetUnreadyReasonNotFound = "SubnetNotFound"

	SubnetUnreadyReasonIDInvalid = "SubnetIDInvalid"

	// TODO(bsoghigian): Support SubnetFull readiness reason
	SubnetUnreadyReasonFull = "SubnetFull"

	// TODO(bsoghigian): check if the cidr of a subnet-id is overlapping with any of the static agentpools,
	// AKSNodeClass subnets, or any defaulting reserved networking addresses for AKS (--dns-service-ip)
	SubnetUnreadyReasonCIDROverlapping = "SubnetCIDROverlapping"

	// TODO(bsoghigian): check cluster identity has rbac for subnet/read subnet/join for a given vnetSubnetID
	SubnetUnreadyReasonRBACInvalid = "SubnetRBACInvalid"
)

const (
	// TODO: Use this in SubnetFull logic
	// Azure reserves the first four addresses and the last address, for a total of 5 ips for each subnet.
	// For example 192.168.1.0/24 has the following reserved addresses
	// 192.168.1.0 Network Address
	// 192.168.1.1 Reserved by azure for the default gateway
	// 192.168.1.2, 192.168.1.3: Reserved by Azure to map the Azure DNS IP addresses to the virtual network space.
	// 192.168.1.255: Network broadcast address
	AzureReservedIPs = 5
)

func (r *SubnetReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	// TODO: Handle podSubnetID readiness here as well
	result, err := r.validateVNETSubnetID(ctx, nodeClass)
	return result, err
}

func (r *SubnetReconciler) validateVNETSubnetID(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	clusterSubnetID := options.FromContext(ctx).SubnetID
	subnetID := lo.Ternary(nodeClass.Spec.VNETSubnetID != nil, lo.FromPtr(nodeClass.Spec.VNETSubnetID), options.FromContext(ctx).SubnetID)
	logger.V(2).Info("validating os.spec.vnetSubnetID: %s", subnetID)

	nodeClassSubnetComponents, err := utils.GetVnetSubnetIDComponents(subnetID)
	if err != nil {
		nodeClass.StatusConditions().SetFalse(
			v1beta1.ConditionTypeSubnetReady,
			SubnetUnreadyReasonIDInvalid,
			fmt.Sprintf("Failed to parse vnetSubnetID %s", subnetID),
		)
		return reconcile.Result{}, err
	}
	if subnetID != clusterSubnetID {
		clusterSubnetIDParts, _ := utils.GetVnetSubnetIDComponents(clusterSubnetID) // Assume valid cluster subnet id
		if !clusterSubnetIDParts.IsSameVNET(nodeClassSubnetComponents) {
			nodeClass.StatusConditions().SetFalse(
				v1beta1.ConditionTypeSubnetReady,
				SubnetUnreadyReasonIDInvalid,
				fmt.Sprintf("vnetSubnetID does not match the cluster subscription, resource group, or virtual network: %s", subnetID),
			)
			return reconcile.Result{}, fmt.Errorf("subnet %s does not match cluster subscription, resource group, or virtual network", subnetID)
		}
	}

	_, err = r.subnetClient.Get(ctx, nodeClassSubnetComponents.ResourceGroupName, nodeClassSubnetComponents.VNetName, nodeClassSubnetComponents.SubnetName, nil)
	if err != nil {
		azErr := sdkerrors.IsResponseError(err)
		if azErr != nil && (azErr.StatusCode == http.StatusNotFound) {
			nodeClass.StatusConditions().SetFalse(
				v1beta1.ConditionTypeSubnetReady,
				SubnetUnreadyReasonNotFound,
				fmt.Sprintf("resource not found: %s", subnetID),
			)
			return reconcile.Result{RequeueAfter: time.Minute}, err
		}
		// Log generic error since we don't have any reason + message for it
		logger.Error(err, "subnet reconciliation failed")
		return reconcile.Result{}, err
	}

	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeSubnetReady)
	// Periodically check the subnet health conditions haven't been violated
	const healthyRequeueInterval = time.Minute * 3
	return reconcile.Result{RequeueAfter: healthyRequeueInterval}, nil
}
