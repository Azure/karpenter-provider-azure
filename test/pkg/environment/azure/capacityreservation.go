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

package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const contributorRoleDefinitionID = "b24988ac-6180-42a0-ab88-20f7382dd24c"

// CapacityReservationMember is one reservation within a group. Azure allows only one
// per VM size per placement.
type CapacityReservationMember struct {
	// VMSize reserved by this member.
	VMSize string
	// Capacity is the number of instances reserved. Zero is valid and is the cheap
	// choice for tests: Azure only accepts a non-zero quantity when it can genuinely set
	// that capacity aside, which is far from guaranteed even when on-demand capacity and
	// family quota are both plentiful. A VM still associates with a zero-quantity
	// reservation; it is simply overallocated.
	Capacity int32
}

// CapacityReservationGroupOptions describes a capacity reservation group to create for a
// test, along with the member reservations it holds.
type CapacityReservationGroupOptions struct {
	// Name of the group. Members are named "<Name>-<VM size>".
	Name string
	// Members to create in the group.
	Members []CapacityReservationMember
	// ARMZones is the placement of the group and all its members. Empty produces a
	// regional group, whose consuming VMs must omit zones.
	ARMZones []string
}

// ExpectKarpenterCanReadCapacityReservationGroups grants the Karpenter workload identity
// Contributor on the node resource group, which every group these tests create inherits.
//
// Granted once for the suite rather than once per group because the grant is the slow
// part: within a single run Azure made one assignment effective immediately and another
// only after thirteen minutes. Paying that once, before any spec runs, is the difference
// between one readiness wait and five independent chances to exceed it.
func (env *Environment) ExpectKarpenterCanReadCapacityReservationGroups(ctx context.Context) {
	GinkgoHelper()

	scope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", env.SubscriptionID, env.NodeResourceGroup)
	roleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", env.SubscriptionID, contributorRoleDefinitionID)
	Expect(env.RBACManager.EnsureRole(ctx, scope, roleDefinitionID, env.GetKarpenterWorkloadIdentity(ctx))).To(Succeed(),
		"failed to grant Contributor on %s", scope)
}

// memberName keeps reservation names stable and unique per size within a group.
func memberName(groupName, vmSize string) string {
	return groupName + "-" + strings.ToLower(strings.TrimPrefix(vmSize, "Standard_"))
}

// ExpectCreatedCapacityReservationGroup creates a capacity reservation group and its
// member reservations in the node resource group, and returns the group's ARM ID.
//
// Cleanup goes through the environment tracker rather than DeferCleanup: Azure refuses
// to delete a reservation that still has instances allocated, and the tracker runs after
// the test's Kubernetes objects are removed. Reserved capacity is billed, so a leaked
// group costs money until the cluster's resource group is deleted.
func (env *Environment) ExpectCreatedCapacityReservationGroup(ctx context.Context, opts CapacityReservationGroupOptions) string {
	GinkgoHelper()

	zones := lo.ToSlicePtr(opts.ARMZones)

	group, err := env.CapacityReservationGroupsClient.CreateOrUpdate(ctx, env.NodeResourceGroup, opts.Name, armcompute.CapacityReservationGroup{
		Location: lo.ToPtr(env.Region),
		Zones:    zones,
	}, nil)
	Expect(err).ToNot(HaveOccurred(), "failed to create capacity reservation group %s", opts.Name)
	groupID := lo.FromPtr(group.ID)

	memberNames := lo.Map(opts.Members, func(m CapacityReservationMember, _ int) string {
		return memberName(opts.Name, m.VMSize)
	})
	env.tracker.Add(groupID, func() error {
		return env.deleteCapacityReservationGroup(opts.Name, memberNames)
	})

	for _, member := range opts.Members {
		reservation, err := env.CapacityReservationsClient.BeginCreateOrUpdate(ctx, env.NodeResourceGroup, opts.Name, memberName(opts.Name, member.VMSize), armcompute.CapacityReservation{
			Location: lo.ToPtr(env.Region),
			SKU:      &armcompute.SKU{Name: lo.ToPtr(member.VMSize), Capacity: lo.ToPtr(int64(member.Capacity))},
			Zones:    zones,
		}, nil)
		Expect(err).ToNot(HaveOccurred(), "failed to request capacity reservation for %s", member.VMSize)
		// A capacity shortage surfaces here rather than at VM creation, because this is
		// where Azure actually sets the capacity aside.
		_, err = reservation.PollUntilDone(ctx, nil)
		Expect(err).ToNot(HaveOccurred(), "failed to reserve %d x %s in %s zones %v", member.Capacity, member.VMSize, env.Region, opts.ARMZones)
	}

	return groupID
}

func (env *Environment) deleteCapacityReservationGroup(groupName string, reservationNames []string) error {
	ctx := context.Background()
	// VM deletion is asynchronous and outlives the Kubernetes objects, so both deletes can
	// be refused with 409 while instances are still releasing the reservation. Re-deleting
	// a member the previous attempt already removed is safe: ARM answers 204, not 404.
	return retryOnConflict(ctx, 15*time.Minute, func() error {
		// Members have to go first; Azure rejects deleting a group that still has any.
		for _, reservationName := range reservationNames {
			poller, err := env.CapacityReservationsClient.BeginDelete(ctx, env.NodeResourceGroup, groupName, reservationName, nil)
			if err != nil {
				return fmt.Errorf("deleting capacity reservation %s: %w", reservationName, err)
			}
			if _, err := poller.PollUntilDone(ctx, nil); err != nil {
				return fmt.Errorf("deleting capacity reservation %s: %w", reservationName, err)
			}
		}
		if _, err := env.CapacityReservationGroupsClient.Delete(ctx, env.NodeResourceGroup, groupName, nil); err != nil {
			// ARM answers 202 when it defers the delete, which the generated client reports as
			// an error because it only accepts 200 and 204. The delete was accepted either way.
			var responseErr *azcore.ResponseError
			if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusAccepted {
				return fmt.Errorf("deleting capacity reservation group %s: %w", groupName, err)
			}
		}
		return nil
	})
}

func retryOnConflict(ctx context.Context, timeout time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := fn()
		var responseErr *azcore.ResponseError
		if err == nil || !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusConflict {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("still conflicting after %s: %w", timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Second):
		}
	}
}

// ExpectCapacityReservationUtilization returns the VM IDs Azure reports as allocated
// against the group's member reservation. This is the authoritative answer to whether a
// launch actually consumed reserved capacity.
func (env *Environment) ExpectCapacityReservationUtilization(ctx context.Context, groupName, vmSize string) []string {
	GinkgoHelper()

	reservation, err := env.CapacityReservationsClient.Get(ctx, env.NodeResourceGroup, groupName, memberName(groupName, vmSize),
		&armcompute.CapacityReservationsClientGetOptions{Expand: lo.ToPtr(armcompute.CapacityReservationInstanceViewTypesInstanceView)})
	Expect(err).ToNot(HaveOccurred(), "failed to read capacity reservation for group %s", groupName)
	Expect(reservation.Properties).ToNot(BeNil())

	if reservation.Properties.InstanceView == nil || reservation.Properties.InstanceView.UtilizationInfo == nil {
		return nil
	}
	return lo.Map(reservation.Properties.InstanceView.UtilizationInfo.VirtualMachinesAllocated,
		func(vm *armcompute.SubResourceReadOnly, _ int) string { return lo.FromPtr(vm.ID) })
}
