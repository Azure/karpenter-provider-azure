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

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/samber/lo"
	"go.uber.org/multierr"
	"k8s.io/apimachinery/pkg/api/equality"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
	"sigs.k8s.io/karpenter/pkg/utils/result"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/awslabs/operatorpkg/reasonable"
)

type AzureNodeClassController struct {
	kubeClient      client.Client
	subnetsClient   azclient.SubnetsAPI
	azClientManager *azclient.AZClientManager
}

func NewAzureNodeClassController(kubeClient client.Client, subnetsClient azclient.SubnetsAPI, azClientManager *azclient.AZClientManager) *AzureNodeClassController {
	return &AzureNodeClassController{
		kubeClient:      kubeClient,
		subnetsClient:   subnetsClient,
		azClientManager: azClientManager,
	}
}

func (c *AzureNodeClassController) Reconcile(ctx context.Context, nodeClass *v1alpha1.AzureNodeClass) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, "azurenodeclass.status")

	if !controllerutil.ContainsFinalizer(nodeClass, v1alpha1.TerminationFinalizer) {
		stored := nodeClass.DeepCopy()
		controllerutil.AddFinalizer(nodeClass, v1alpha1.TerminationFinalizer)
		if err := c.kubeClient.Patch(ctx, nodeClass, client.MergeFrom(stored)); err != nil {
			return reconcile.Result{}, err
		}
	}
	stored := nodeClass.DeepCopy()

	var results []reconcile.Result
	var errs error

	// Validate ImageID
	if nodeClass.Spec.ImageID == nil || *nodeClass.Spec.ImageID == "" {
		nodeClass.StatusConditions().SetFalse(
			v1alpha1.ConditionTypeValidationSucceeded,
			"ImageIDRequired",
			"spec.imageID must be set",
		)
	} else {
		nodeClass.StatusConditions().SetTrue(v1alpha1.ConditionTypeValidationSucceeded)
	}

	// Validate subnet
	subnetResult, subnetErr := c.validateSubnet(ctx, nodeClass)
	errs = multierr.Append(errs, subnetErr)
	results = append(results, subnetResult)

	if !equality.Semantic.DeepEqual(stored, nodeClass) {
		// We use client.MergeFromWithOptimisticLock because patching a list with a JSON merge patch
		// can cause races due to the fact that it fully replaces the list on a change
		// Here, we are updating the status condition list
		if err := c.kubeClient.Status().Patch(ctx, nodeClass, client.MergeFromWithOptions(stored, client.MergeFromWithOptimisticLock{})); err != nil {
			errs = multierr.Append(errs, client.IgnoreNotFound(err))
		}
	}
	if errs != nil {
		return reconcile.Result{}, errs
	}
	return result.Min(results...), nil
}

// validateSubnet checks that the subnet ARM resource exists. If the NodeClass
// specifies a cross-subscription subnet (via subscriptionID override or embedded
// in the VNETSubnetID), the per-sub client from AZClientManager is used.
func (c *AzureNodeClassController) validateSubnet(ctx context.Context, nodeClass *v1alpha1.AzureNodeClass) (reconcile.Result, error) {
	// Determine the effective subnet ID: spec override or global flag
	subnetID := lo.FromPtr(nodeClass.Spec.VNETSubnetID)
	if subnetID == "" {
		subnetID = options.FromContext(ctx).SubnetID
	}
	if subnetID == "" {
		// No subnet configured at all — nothing to validate. Mark ready since
		// the VM creation path will fail separately if no subnet is available.
		nodeClass.StatusConditions().SetTrue(v1alpha1.ConditionTypeSubnetsReady)
		return reconcile.Result{}, nil
	}

	logger := log.FromContext(ctx).WithName("azurenodeclass.subnet").WithValues("subnetID", subnetID)

	// Parse the subnet ARM resource ID
	subnetComponents, err := utils.GetVnetSubnetIDComponents(subnetID)
	if err != nil {
		logger.Error(err, "failed to parse vnetSubnetID")
		nodeClass.StatusConditions().SetFalse(
			v1alpha1.ConditionTypeSubnetsReady,
			SubnetUnreadyReasonIDInvalid,
			fmt.Sprintf("Failed to parse vnetSubnetID %s", subnetID),
		)
		return reconcile.Result{}, nil
	}

	// Determine which subnets client to use based on the subnet's subscription.
	// If the NodeClass has a subscriptionID override, the subnet might be in that sub.
	// Or the subnet ARM ID itself may encode a different subscription.
	subnetsClient := c.subnetsClient
	subnetSubID := subnetComponents.SubscriptionID
	defaultSubID := c.azClientManager.DefaultSubscriptionID()
	if subnetSubID != "" && subnetSubID != defaultSubID {
		subClients, clientErr := c.azClientManager.GetClients(subnetSubID)
		if clientErr != nil {
			logger.Error(clientErr, "failed to get Azure clients for subnet subscription", "subscriptionID", subnetSubID)
			nodeClass.StatusConditions().SetFalse(
				v1alpha1.ConditionTypeSubnetsReady,
				SubnetUnreadyReasonUnknownError,
				fmt.Sprintf("failed to create client for subnet subscription %s: %s", subnetSubID, clientErr.Error()),
			)
			return reconcile.Result{}, nil
		}
		subnetsClient = subClients.SubnetsClient
	}

	// Validate the subnet exists
	_, err = subnetsClient.Get(ctx, subnetComponents.ResourceGroupName, subnetComponents.VNetName, subnetComponents.SubnetName, nil)
	if err != nil {
		azErr := sdkerrors.IsResponseError(err)
		if azErr != nil && azErr.StatusCode == http.StatusNotFound {
			nodeClass.StatusConditions().SetFalse(
				v1alpha1.ConditionTypeSubnetsReady,
				SubnetUnreadyReasonNotFound,
				fmt.Sprintf("resource not found: %s", subnetID),
			)
			return reconcile.Result{RequeueAfter: time.Minute}, err
		}
		nodeClass.StatusConditions().SetFalse(
			v1alpha1.ConditionTypeSubnetsReady,
			SubnetUnreadyReasonUnknownError,
			fmt.Sprintf("unknown error getting subnet: %s", err.Error()),
		)
		logger.Error(err, "getting subnet failed during reconciliation with unknown error")
		return reconcile.Result{}, err
	}

	nodeClass.StatusConditions().SetTrue(v1alpha1.ConditionTypeSubnetsReady)
	return reconcile.Result{RequeueAfter: healthyRequeueInterval}, nil
}

func (c *AzureNodeClassController) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("azurenodeclass.status").
		For(&v1alpha1.AzureNodeClass{}).
		WithOptions(controller.Options{
			RateLimiter:             reasonable.RateLimiter(),
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}
