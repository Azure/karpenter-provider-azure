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
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"

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
	CapacityReservationGroupUnreadyReasonAccessDenied         = "CapacityReservationGroupAccessDenied"
	CapacityReservationGroupUnreadyReasonRegionMismatch       = "CapacityReservationGroupRegionMismatch"
	CapacityReservationGroupUnreadyReasonSubscriptionMismatch = "CapacityReservationGroupSubscriptionMismatch"
	CapacityReservationGroupUnreadyReasonUnsupportedType      = "CapacityReservationGroupUnsupportedReservationType"
	CapacityReservationGroupUnreadyReasonNoReservations       = "CapacityReservationGroupNoReservations"
	// CapacityReservationGroupUnreadyReasonNoEligibleReservations means the group has
	// members but none is provisioned, so none can back an offering yet.
	CapacityReservationGroupUnreadyReasonNoEligibleReservations = "CapacityReservationGroupNoEligibleReservations"
	// CapacityReservationGroupUnreadyReasonNoCompatibleReservations means every eligible
	// member reserves something this NodeClass cannot use: a size absent from the region,
	// or one its own filters exclude.
	CapacityReservationGroupUnreadyReasonNoCompatibleReservations = "CapacityReservationGroupNoCompatibleReservations"
	CapacityReservationGroupUnreadyReasonUnsupportedProvisionMode = "CapacityReservationGroupUnsupportedProvisionMode"
	CapacityReservationGroupUnreadyReasonUnsupportedCloud         = "CapacityReservationGroupUnsupportedCloud"
	CapacityReservationGroupUnreadyReasonUnknownError             = "CapacityReservationGroupUnknownError"
)

const (
	capacityReservationGroupReconcilerName = "nodeclass.capacityreservationgroup"
	capacityReservationGroupResourceType   = "capacityReservationGroups"

	// unreadyRequeueInterval retries states that a user action can clear at once, rather
	// than leaving the NodeClass unready for a full healthy revalidation period.
	unreadyRequeueInterval = time.Minute
)

// cloudSupportsCapacityReservations reports whether the resolved cloud is one Azure
// documents capacity reservations for. Compared by Resource Manager endpoint rather than
// by name, because a file-based environment supplies both, and only the endpoint is
// meaningful -- the same reason auth.IsPublic compares endpoints.
func cloudSupportsCapacityReservations(cfg cloud.Configuration) bool {
	return lo.ContainsBy([]cloud.Configuration{cloud.AzurePublic, cloud.AzureGovernment, cloud.AzureChina},
		func(supported cloud.Configuration) bool {
			return strings.EqualFold(
				strings.TrimRight(cfg.Services[cloud.ResourceManager].Endpoint, "/"),
				strings.TrimRight(supported.Services[cloud.ResourceManager].Endpoint, "/"))
		})
}

// instanceTypeLister is the projection side of the CRG feature, narrowed to what
// readiness needs. Reusing it keeps a single definition of which SKUs a NodeClass can
// actually use, rather than a second copy that can drift.
type instanceTypeLister interface {
	List(context.Context, *v1beta1.AKSNodeClass) ([]*cloudprovider.InstanceType, error)
}

// CapacityReservationGroupReconciler resolves spec.capacityReservationGroupID into
// status.capacityReservationGroup: the group's placement and the member reservations
// that can back offerings. It deliberately resolves only the static shape of the
// group. Utilization is volatile and must not become a per-node guarantee.
type CapacityReservationGroupReconciler struct {
	groupsClient       azapi.CapacityReservationGroupsAPI
	reservationsClient azapi.CapacityReservationsAPI
	instanceTypes      instanceTypeLister
	subscriptionID     string
	location           string
	cloud              cloud.Configuration
}

func NewCapacityReservationGroupReconciler(
	groupsClient azapi.CapacityReservationGroupsAPI,
	reservationsClient azapi.CapacityReservationsAPI,
	instanceTypes instanceTypeLister,
	subscriptionID string,
	location string,
	cloudConfig cloud.Configuration,
) *CapacityReservationGroupReconciler {
	return &CapacityReservationGroupReconciler{
		groupsClient:       groupsClient,
		reservationsClient: reservationsClient,
		instanceTypes:      instanceTypes,
		subscriptionID:     subscriptionID,
		location:           location,
		cloud:              cloudConfig,
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

	// Checked before the group is read, so an unsupported cloud reports itself rather than
	// surfacing as whatever error that cloud's ARM returns for an unknown resource type.
	if !cloudSupportsCapacityReservations(r.cloud) {
		r.setFalse(nodeClass, CapacityReservationGroupUnreadyReasonUnsupportedCloud,
			"capacity reservations are only available in Azure Cloud, Azure Government, and Azure China")
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
		// Short interval, not the healthy one: a group is commonly listed empty while the
		// operator is still populating it, and ARM can briefly list a fresh member as absent.
		return reconcile.Result{RequeueAfter: unreadyRequeueInterval}, nil
	}

	// Recorded before eligibility is judged: when nothing can back an offering, the member
	// names and their states are exactly what the operator needs to see, and a NodePool has
	// been authored against each of them.
	nodeClass.Status.CapacityReservationGroup = &v1beta1.CapacityReservationGroup{
		// Falling back to the requested ID keeps a required field populated; it is the same
		// group, and ARM may echo it with different casing.
		ID:                   lo.CoalesceOrEmpty(lo.FromPtr(group.ID), crgID),
		Location:             lo.FromPtr(group.Location),
		Zones:                lo.FilterMap(group.Zones, func(z *string, _ int) (string, bool) { return lo.FromPtr(z), z != nil }),
		CapacityReservations: reservations,
	}

	// Members that are not yet provisioned back no offerings, so a group made up entirely
	// of them would otherwise report Ready and project nothing, which reaches the user as
	// unschedulable pods rather than as a NodeClass problem.
	if !lo.SomeBy(reservations, func(cr v1beta1.CapacityReservation) bool { return cr.IsEligible() }) {
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeCapacityReservationGroupReady,
			CapacityReservationGroupUnreadyReasonNoEligibleReservations,
			fmt.Sprintf("no capacity reservation in group is provisioned: %s", crgID))
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}

	// Being provisioned is not the same as being usable: a member can reserve a size this
	// region does not offer, or one this NodeClass filters out. Rather than restate those
	// rules here and let the two copies drift, ask the projection itself what it would
	// produce for the status about to be written. List does not read the Ready condition,
	// so this is not recursive, and it warms the cache key the launch path uses next.
	instanceTypes, err := r.instanceTypes.List(ctx, candidateWithResolvedGroup(nodeClass))
	if err != nil {
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeCapacityReservationGroupReady,
			CapacityReservationGroupUnreadyReasonUnknownError,
			fmt.Sprintf("unknown error listing instance types for capacity reservation group: %s", err.Error()))
		return reconcile.Result{}, fmt.Errorf("listing instance types for capacity reservation group %s: %w", crgID, err)
	}
	if len(instanceTypes) == 0 {
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeCapacityReservationGroupReady,
			CapacityReservationGroupUnreadyReasonNoCompatibleReservations,
			fmt.Sprintf("no capacity reservation in group reserves a VM size this NodeClass can use: %s", crgID))
		return reconcile.Result{RequeueAfter: unreadyRequeueInterval}, nil
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
			if reservation, ok := capacityReservationFromARM(cr); ok {
				reservations = append(reservations, reservation)
			}
		}
	}
	return reservations, nil
}

// capacityReservationFromARM converts one member into the shape status records, reporting
// false for a member that cannot be represented. A member missing a value the status
// schema requires is skipped rather than written empty, which the API server would reject,
// taking the whole status update with it.
func capacityReservationFromARM(cr *armcompute.CapacityReservation) (v1beta1.CapacityReservation, bool) {
	if cr == nil || cr.SKU == nil || lo.FromPtr(cr.SKU.Name) == "" ||
		lo.FromPtr(cr.ID) == "" || lo.FromPtr(cr.Name) == "" {
		return v1beta1.CapacityReservation{}, false
	}
	// Copied rather than aliased so status does not retain a pointer into the ARM response.
	var quantity *int64
	if cr.SKU.Capacity != nil {
		quantity = lo.ToPtr(*cr.SKU.Capacity)
	}
	var provisioningState *string
	if cr.Properties != nil && lo.FromPtr(cr.Properties.ProvisioningState) != "" {
		provisioningState = cr.Properties.ProvisioningState
	}
	return v1beta1.CapacityReservation{
		ID:                lo.FromPtr(cr.ID),
		Name:              lo.FromPtr(cr.Name),
		VMSize:            lo.FromPtr(cr.SKU.Name),
		Zones:             lo.FilterMap(cr.Zones, func(z *string, _ int) (string, bool) { return lo.FromPtr(z), z != nil }),
		Quantity:          quantity,
		ProvisioningState: provisioningState,
	}, true
}

func (r *CapacityReservationGroupReconciler) setFalse(nodeClass *v1beta1.AKSNodeClass, reason, message string) {
	nodeClass.Status.CapacityReservationGroup = nil
	nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeCapacityReservationGroupReady, reason, message)
}

// candidateWithResolvedGroup is the NodeClass as it would be once this reconciliation is
// persisted. The copy keeps the speculative projection from touching the object the rest
// of the reconcile chain is still working on.
func candidateWithResolvedGroup(nodeClass *v1beta1.AKSNodeClass) *v1beta1.AKSNodeClass {
	return nodeClass.DeepCopy()
}
