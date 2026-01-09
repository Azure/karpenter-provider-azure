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

const (
	SubnetUnreadyReasonNotFound     = "SubnetNotFound"
	SubnetUnreadyReasonIDInvalid    = "SubnetIDInvalid"
	SubnetUnreadyReasonUnknownError = "SubnetUnknownError"
)

const (
	subnetReconcilerName = "nodeclass.subnet"
	// we set 3 minutes for a healthy requeue interval because NRP reserves NICs for 180 seconds.
	// which means that we will not be able to free a given NIC for up to 3 minutes, for now setting it as
	// the default requeue interval at that timestamp, we may choose to redesign as we implement subnet fullness
	healthyRequeueInterval = time.Minute * 3
)

func (r *SubnetReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	// TODO: Handle podSubnetID readiness here as well
	return r.validateVNETSubnetID(ctx, nodeClass)
}

func (r *SubnetReconciler) validateVNETSubnetID(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	clusterSubnetID := options.FromContext(ctx).SubnetID
	subnetID := lo.Ternary(nodeClass.Spec.VNETSubnetID != nil, lo.FromPtr(nodeClass.Spec.VNETSubnetID), options.FromContext(ctx).SubnetID)
	logger := log.FromContext(ctx).WithName(subnetReconcilerName).WithValues("subnetID", subnetID)

	nodeClassSubnetComponents, err := utils.GetVnetSubnetIDComponents(subnetID)
	if err != nil {
		logger.Error(err, "failed to parse vnetSubnetID", "subnetID", subnetID)
		nodeClass.StatusConditions().SetFalse(
			v1beta1.ConditionTypeSubnetsReady,
			SubnetUnreadyReasonIDInvalid,
			fmt.Sprintf("Failed to parse vnetSubnetID %s", subnetID),
		)
		return reconcile.Result{}, nil
	}
	if subnetID != clusterSubnetID {
		clusterSubnetIDParts, err := utils.GetVnetSubnetIDComponents(clusterSubnetID) // Assume valid cluster subnet id
		if err != nil {                                                               // Highly unlikely case but putting it in nonetheless
			logger.Error(err, "failed to parse cluster subnet ID", "clusterSubnetID", clusterSubnetID)
			return reconcile.Result{}, nil
		}

		isClusterManagedVNET, err := utils.IsAKSManagedVNET(options.FromContext(ctx).NodeResourceGroup, clusterSubnetID)
		if err != nil {
			logger.Error(err, "failed to determine if cluster VNet is managed", "clusterSubnetID", clusterSubnetID)
			return reconcile.Result{}, nil
		}

		if isClusterManagedVNET && clusterSubnetIDParts.IsSameVNET(nodeClassSubnetComponents) {
			logger.Error(nil, "custom subnet cannot be in the same VNet as cluster managed VNet", "subnetID", subnetID)
			nodeClass.StatusConditions().SetFalse(
				v1beta1.ConditionTypeSubnetsReady,
				SubnetUnreadyReasonIDInvalid,
				fmt.Sprintf("custom subnet cannot be in the same VNet as cluster managed VNet: %s", subnetID),
			)
			return reconcile.Result{}, nil
		}

		if !clusterSubnetIDParts.IsSameVNET(nodeClassSubnetComponents) {
			logger.Error(nil, "subnet does not match cluster subscription, resource group, or virtual network", "subnetID", subnetID)
			nodeClass.StatusConditions().SetFalse(
				v1beta1.ConditionTypeSubnetsReady,
				SubnetUnreadyReasonIDInvalid,
				fmt.Sprintf("vnetSubnetID does not match the cluster subscription, resource group, or virtual network: %s", subnetID),
			)
			return reconcile.Result{}, nil
		}
	}

	_, err = r.subnetClient.Get(ctx, nodeClassSubnetComponents.ResourceGroupName, nodeClassSubnetComponents.VNetName, nodeClassSubnetComponents.SubnetName, nil)
	if err != nil {
		azErr := sdkerrors.IsResponseError(err)
		if azErr != nil && (azErr.StatusCode == http.StatusNotFound) {
			nodeClass.StatusConditions().SetFalse(
				v1beta1.ConditionTypeSubnetsReady,
				SubnetUnreadyReasonNotFound,
				fmt.Sprintf("resource not found: %s", subnetID),
			)
			return reconcile.Result{RequeueAfter: time.Minute}, err
		}
		nodeClass.StatusConditions().SetFalse(
			v1beta1.ConditionTypeSubnetsReady,
			SubnetUnreadyReasonUnknownError,
			fmt.Sprintf("unknown error getting subnet: %s", err.Error()),
		)
		logger.Error(err, "getting subnet failed during reconciliation with unknown error", "error", err.Error())
		return reconcile.Result{}, err
	}

	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeSubnetsReady)

	// Periodically requeue just in case subnet has been removed or later revalidating things like fullness etc
	return reconcile.Result{RequeueAfter: healthyRequeueInterval}, nil
}
