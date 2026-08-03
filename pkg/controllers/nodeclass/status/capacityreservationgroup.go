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
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
)

const (
	CapacityReservationGroupUnreadyReasonIDInvalid = "CapacityReservationGroupIDInvalid"
	CapacityReservationGroupUnreadyReasonNotFound  = "CapacityReservationGroupNotFound"
	// CapacityReservationGroupUnreadyReasonAccessDenied is expected when the Karpenter
	// identity has not been granted access to the group's scope. Note that this only
	// covers read access; permission to associate an instance with the group cannot be
	// verified until launch.
	CapacityReservationGroupUnreadyReasonAccessDenied             = "CapacityReservationGroupAccessDenied"
	CapacityReservationGroupUnreadyReasonRegionMismatch           = "CapacityReservationGroupRegionMismatch"
	CapacityReservationGroupUnreadyReasonSubscriptionMismatch     = "CapacityReservationGroupSubscriptionMismatch"
	CapacityReservationGroupUnreadyReasonUnsupportedType          = "CapacityReservationGroupUnsupportedReservationType"
	CapacityReservationGroupUnreadyReasonNoReservations           = "CapacityReservationGroupNoReservations"
	CapacityReservationGroupUnreadyReasonUnsupportedProvisionMode = "CapacityReservationGroupUnsupportedProvisionMode"
	CapacityReservationGroupUnreadyReasonUnknownError             = "CapacityReservationGroupUnknownError"
)

const (
	capacityReservationGroupReconcilerName = "nodeclass.capacityreservationgroup"
	capacityReservationGroupResourceType   = "capacityReservationGroups"
)

// CapacityReservationGroupReconciler resolves spec.capacityReservationGroupID into
// status.capacityReservationGroup: the group's placement and the member reservations
// that can back offerings. It deliberately resolves only the static shape of the
// group. Utilization is volatile and must not become a per-node guarantee.
type CapacityReservationGroupReconciler struct {
	groupsClient       azapi.CapacityReservationGroupsAPI
	reservationsClient azapi.CapacityReservationsAPI
	subscriptionID     string
	location           string
}

func NewCapacityReservationGroupReconciler(
	groupsClient azapi.CapacityReservationGroupsAPI,
	reservationsClient azapi.CapacityReservationsAPI,
	subscriptionID string,
	location string,
) *CapacityReservationGroupReconciler {
	return &CapacityReservationGroupReconciler{
		groupsClient:       groupsClient,
		reservationsClient: reservationsClient,
		subscriptionID:     subscriptionID,
		location:           location,
	}
}

//nolint:gocyclo
func (r *CapacityReservationGroupReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	if nodeClass.Spec.CapacityReservationGroupID == nil {
		nodeClass.Status.CapacityReservationGroup = nil
		// The condition is not a dependent of Ready while no group is configured, but
		// clear any stale value left behind by a previous configuration.
		_ = nodeClass.StatusConditions().Clear(v1beta1.ConditionTypeCapacityReservationGroupReady)
		return reconcile.Result{}, nil
	}

	crgID := lo.FromPtr(nodeClass.Spec.CapacityReservationGroupID)
	logger := log.FromContext(ctx).WithName(capacityReservationGroupReconcilerName).WithValues("capacityReservationGroupID", crgID)

	// The AKS Machine API does not yet expose a per-Machine capacity reservation
	// field, so association cannot be honored in that provisioning mode. Fail closed
	// rather than silently dropping the association.
	if options.FromContext(ctx).IsAKSMachineAPIMode() {
		r.setFalse(nodeClass, CapacityReservationGroupUnreadyReasonUnsupportedProvisionMode,
			"capacityReservationGroupID is not supported in AKS Machine API provisioning mode")
		return reconcile.Result{}, nil
	}

	resourceID, err := arm.ParseResourceID(crgID)
	if err != nil || !strings.EqualFold(resourceID.ResourceType.Type, capacityReservationGroupResourceType) {
		logger.Error(err, "failed to parse capacityReservationGroupID")
		r.setFalse(nodeClass, CapacityReservationGroupUnreadyReasonIDInvalid,
			fmt.Sprintf("Failed to parse capacityReservationGroupID %s", crgID))
		return reconcile.Result{}, nil
	}

	// ARM returns resource IDs with inconsistent casing, so every comparison against
	// an ID or one of its segments is case-insensitive.
	if !strings.EqualFold(resourceID.SubscriptionID, r.subscriptionID) {
		r.setFalse(nodeClass, CapacityReservationGroupUnreadyReasonSubscriptionMismatch,
			fmt.Sprintf("capacityReservationGroupID must be in subscription %s", r.subscriptionID))
		return reconcile.Result{}, nil
	}

	group, err := r.groupsClient.Get(ctx, resourceID.ResourceGroupName, resourceID.Name, nil)
	if err != nil {
		if azErr := sdkerrors.IsResponseError(err); azErr != nil {
			switch azErr.StatusCode {
			case http.StatusNotFound:
				r.setFalse(nodeClass, CapacityReservationGroupUnreadyReasonNotFound,
					fmt.Sprintf("resource not found: %s", crgID))
				return reconcile.Result{RequeueAfter: time.Minute}, nil
			case http.StatusForbidden, http.StatusUnauthorized:
				r.setFalse(nodeClass, CapacityReservationGroupUnreadyReasonAccessDenied,
					fmt.Sprintf("access denied reading capacity reservation group: %s", crgID))
				return reconcile.Result{RequeueAfter: time.Minute}, nil
			}
		}
		r.setFalse(nodeClass, CapacityReservationGroupUnreadyReasonUnknownError,
			fmt.Sprintf("unknown error getting capacity reservation group: %s", err.Error()))
		logger.Error(err, "getting capacity reservation group failed during reconciliation with unknown error")
		return reconcile.Result{}, err
	}

	if !strings.EqualFold(lo.FromPtr(group.Location), r.location) {
		r.setFalse(nodeClass, CapacityReservationGroupUnreadyReasonRegionMismatch,
			fmt.Sprintf("capacity reservation group is in region %s, expected %s", lo.FromPtr(group.Location), r.location))
		return reconcile.Result{}, nil
	}

	// Azure omits reservationType for Targeted groups, so only an explicit
	// non-Targeted value is a rejection.
	if group.Properties != nil && group.Properties.ReservationType != nil &&
		*group.Properties.ReservationType != armcompute.ReservationTypeTargeted {
		r.setFalse(nodeClass, CapacityReservationGroupUnreadyReasonUnsupportedType,
			fmt.Sprintf("capacity reservation group has unsupported reservation type %s, only Targeted is supported",
				*group.Properties.ReservationType))
		return reconcile.Result{}, nil
	}

	reservations, err := r.listReservations(ctx, resourceID.ResourceGroupName, resourceID.Name)
	if err != nil {
		if azErr := sdkerrors.IsResponseError(err); azErr != nil &&
			(azErr.StatusCode == http.StatusForbidden || azErr.StatusCode == http.StatusUnauthorized) {
			r.setFalse(nodeClass, CapacityReservationGroupUnreadyReasonAccessDenied,
				fmt.Sprintf("access denied listing capacity reservations in group: %s", crgID))
			return reconcile.Result{RequeueAfter: time.Minute}, nil
		}
		r.setFalse(nodeClass, CapacityReservationGroupUnreadyReasonUnknownError,
			fmt.Sprintf("unknown error listing capacity reservations: %s", err.Error()))
		logger.Error(err, "listing capacity reservations failed during reconciliation with unknown error")
		return reconcile.Result{}, err
	}

	// A group with no members can never back an offering. AKS associates a node pool
	// with a warning in this case; we fail closed instead.
	if len(reservations) == 0 {
		nodeClass.Status.CapacityReservationGroup = nil
		r.setFalse(nodeClass, CapacityReservationGroupUnreadyReasonNoReservations,
			fmt.Sprintf("capacity reservation group has no capacity reservations: %s", crgID))
		return reconcile.Result{RequeueAfter: healthyRequeueInterval}, nil
	}

	nodeClass.Status.CapacityReservationGroup = &v1beta1.CapacityReservationGroup{
		ID:                   lo.FromPtr(group.ID),
		Location:             lo.FromPtr(group.Location),
		Zones:                lo.FilterMap(group.Zones, func(z *string, _ int) (string, bool) { return lo.FromPtr(z), z != nil }),
		CapacityReservations: reservations,
	}
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeCapacityReservationGroupReady)

	// Periodically revalidate: members and quantities are user-managed and can change
	// without any change to the NodeClass.
	return reconcile.Result{RequeueAfter: healthyRequeueInterval}, nil
}

func (r *CapacityReservationGroupReconciler) listReservations(ctx context.Context, resourceGroupName, groupName string) ([]v1beta1.CapacityReservation, error) {
	var reservations []v1beta1.CapacityReservation
	pager := r.reservationsClient.NewListByCapacityReservationGroupPager(resourceGroupName, groupName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, cr := range page.Value {
			if cr == nil || cr.SKU == nil || lo.FromPtr(cr.SKU.Name) == "" {
				continue
			}
			// Reserved quantities are small, but the SDK types the field as int64.
			quantity := lo.FromPtr(cr.SKU.Capacity)
			if quantity < 0 {
				quantity = 0
			}
			if quantity > math.MaxInt32 {
				quantity = math.MaxInt32
			}
			reservations = append(reservations, v1beta1.CapacityReservation{
				ID:       lo.FromPtr(cr.ID),
				Name:     lo.FromPtr(cr.Name),
				VMSize:   lo.FromPtr(cr.SKU.Name),
				Zones:    lo.FilterMap(cr.Zones, func(z *string, _ int) (string, bool) { return lo.FromPtr(z), z != nil }),
				Quantity: lo.ToPtr(int32(quantity)),
			})
		}
	}
	return reservations, nil
}

func (r *CapacityReservationGroupReconciler) setFalse(nodeClass *v1beta1.AKSNodeClass, reason, message string) {
	nodeClass.Status.CapacityReservationGroup = nil
	nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeCapacityReservationGroupReady, reason, message)
}
