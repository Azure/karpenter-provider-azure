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

package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet/v2"
	"github.com/go-logr/logr"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
)

const (
	// FleetNameTagKey records which Fleet created a VM.
	FleetNameTagKey = "karpenter.azure.com_fleet-name"

	// MaxFleetCapacity is the maximum number of VMs requested from a single Fleet resource.
	// Larger batches are split into sub-batches that each create their own Fleet.
	MaxFleetCapacity = 1000

	// Compute Fleet resource names are limited to 80 characters.
	maxFleetNameLength = 80
)

type fleetBatch = batcher.Batch[FleetVMProvisionRequest, FleetBatchResponse]
type fleetBatchedRequest = batcher.BatchedRequest[FleetVMProvisionRequest, FleetBatchResponse]

// executor turns a batch of compatible requests into Fleet creates and distributes the
// resulting VMs back to each request.
type executor struct {
	fleetClient   azapi.FleetsAPI
	clusterName   string
	resourceGroup string

	// maxFleetCapacity defaults to MaxFleetCapacity; it is a field only for test overrides.
	maxFleetCapacity int
}

func newExecutor(fleetClient azapi.FleetsAPI, clusterName, resourceGroup string) *executor {
	return &executor{
		fleetClient:      fleetClient,
		clusterName:      clusterName,
		resourceGroup:    resourceGroup,
		maxFleetCapacity: MaxFleetCapacity,
	}
}

// executeBatch implements batcher.ExecuteBatch. Every request receives exactly one response.
func (e *executor) executeBatch(ctx context.Context, batch *fleetBatch) {
	if batch == nil || len(batch.Requests) == 0 {
		return
	}
	logger := log.FromContext(ctx).WithValues("batchKey", batch.Key, "batchSize", len(batch.Requests))
	if len(batch.Requests) <= e.maxFleetCapacity {
		e.createFleet(ctx, batch.Key, batch.Requests, logger)
		return
	}
	e.createSplitFleets(ctx, batch, logger)
}

// createSplitFleets creates one Fleet per chunk of at most maxFleetCapacity requests, in parallel.
// Each chunk distributes its own result, so one chunk failing does not fail the others.
func (e *executor) createSplitFleets(ctx context.Context, batch *fleetBatch, logger logr.Logger) {
	chunks := lo.Chunk(batch.Requests, e.maxFleetCapacity)
	logger.Info("splitting fleet batch", "subBatches", len(chunks), "maxFleetCapacity", e.maxFleetCapacity)

	var wg sync.WaitGroup
	for i, chunk := range chunks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The batcher only recovers panics on its own goroutine, so recover here as well.
			defer func() {
				if r := recover(); r != nil {
					logger.Error(fmt.Errorf("%v", r), "panic creating fleet sub-batch", "subBatch", i)
					for _, req := range chunk {
						select {
						case req.ResponseChan <- &batcher.Response[FleetBatchResponse]{Err: fmt.Errorf("fleet sub-batch panicked: %v", r)}:
						default:
						}
					}
				}
			}()
			e.createFleet(ctx, batch.Key, chunk, logger.WithValues("subBatch", i, "subBatchSize", len(chunk)))
		}()
	}
	wg.Wait()
}

// createFleet issues one Fleet PUT for the requests, lists the VMs it created, assigns them
// to NodeClaims, and distributes the shared result (or an error) to every request.
func (e *executor) createFleet(ctx context.Context, batchKey string, requests []*fleetBatchedRequest, logger logr.Logger) {
	targetCapacity := len(requests)
	if targetCapacity > MaxFleetCapacity {
		distributeError(requests, fmt.Errorf("fleet capacity must not exceed %d", MaxFleetCapacity))
		return
	}
	name, err := fleetName(e.clusterName, batchKey)
	if err != nil {
		distributeError(requests, fmt.Errorf("generating fleet name: %w", err))
		return
	}
	logger = logger.WithValues("fleetName", name)

	// Requests sharing a batch key have equivalent bodies; use the first one.
	representative := &requests[0].Payload
	body, err := fleetBodyForBatch(representative.FleetBody, int32(targetCapacity), name)
	if err != nil {
		distributeError(requests, fmt.Errorf("preparing fleet body: %w", err))
		return
	}

	// The LRO is intentionally not polled: once BeginCreateOrUpdate returns, the VMs are listed.
	logger.Info("creating fleet", "targetCapacity", len(requests))
	if _, err = e.fleetClient.BeginCreateOrUpdate(ctx, e.resourceGroup, name, body, nil); err != nil {
		logger.Error(err, "fleet create failed")
		distributeError(requests, fmt.Errorf("creating fleet %s: %w", name, err))
		return
	}

	vms, err := e.listFleetVMs(ctx, name, *body.Location, logger)
	if err != nil {
		logger.Error(err, "listing fleet VMs failed")
		distributeError(requests, fmt.Errorf("listing VMs for fleet %s: %w", name, err))
		return
	}

	nodeClaimNames := lo.Map(requests, func(r *fleetBatchedRequest, _ int) string { return r.Payload.NodeClaimName })
	state := newFleetSharedState(name, nodeClaimNames, vms)
	logger.Info("fleet created", "vmCount", len(vms), "assigned", len(state.assignments), "surplus", len(state.surplus))
	distribute(requests, FleetBatchResponse{SharedState: state})
}

func fleetBodyForBatch(template *armcomputefleet.Fleet, capacity int32, name string) (armcomputefleet.Fleet, error) {
	if template == nil || template.Properties == nil {
		return armcomputefleet.Fleet{}, fmt.Errorf("fleet body and properties are required")
	}
	if template.Location == nil || *template.Location == "" {
		return armcomputefleet.Fleet{}, fmt.Errorf("fleet location is required")
	}
	regular := template.Properties.RegularPriorityProfile
	spot := template.Properties.SpotPriorityProfile
	if (regular == nil) == (spot == nil) {
		return armcomputefleet.Fleet{}, fmt.Errorf("exactly one fleet priority profile is required")
	}

	body := *template
	properties := *template.Properties
	body.Properties = &properties
	if regular != nil {
		profile := *regular
		profile.Capacity = lo.ToPtr(capacity)
		body.Properties.RegularPriorityProfile = &profile
	} else {
		profile := *spot
		profile.Capacity = lo.ToPtr(capacity)
		body.Properties.SpotPriorityProfile = &profile
	}
	body.Tags = maps.Clone(template.Tags)
	if body.Tags == nil {
		body.Tags = make(map[string]*string, 1)
	}
	body.Tags[FleetNameTagKey] = lo.ToPtr(name)
	return body, nil
}

// listFleetVMs pages through the Fleet's VMs and converts the successful ones into
// armcompute.VirtualMachine stubs carrying ID, name, size, zone, and location.
// Provisioning state is not available from this API and must be resolved separately.
func (e *executor) listFleetVMs(ctx context.Context, name, location string, logger logr.Logger) ([]*armcompute.VirtualMachine, error) {
	pager := e.fleetClient.NewListVirtualMachinesPager(e.resourceGroup, name, nil)
	var vms []*armcompute.VirtualMachine
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, fvm := range page.Value {
			if vm := toVirtualMachine(fvm, location, logger); vm != nil {
				vms = append(vms, vm)
			}
		}
	}
	return vms, nil
}

// toVirtualMachine converts a Fleet VM entry; it returns nil for entries without a name and for
// VMs that Fleet reports as failed, since those were never created.
func toVirtualMachine(fvm *armcomputefleet.VirtualMachine, location string, logger logr.Logger) *armcompute.VirtualMachine {
	if fvm == nil || fvm.Name == nil {
		return nil
	}
	if lo.FromPtr(fvm.OperationStatus) == armcomputefleet.VMOperationStatusFailed {
		var code string
		if fvm.Error != nil {
			code = lo.FromPtr(fvm.Error.Code)
		}
		logger.Info("skipping failed fleet VM", "vm", *fvm.Name, "vmSize", lo.FromPtr(fvm.VMSize), "errorCode", code)
		return nil
	}
	// Every VM a Fleet creates lives in the Fleet's region; location is needed for zone labels.
	vm := &armcompute.VirtualMachine{
		ID:       fvm.ID,
		Name:     fvm.Name,
		Type:     fvm.Type,
		Location: lo.ToPtr(location),
	}
	if fvm.VMSize != nil {
		vm.Properties = &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{
				VMSize: lo.ToPtr(armcompute.VirtualMachineSizeTypes(*fvm.VMSize)),
			},
		}
	}
	if fvm.Zone != nil {
		vm.Zones = []*string{fvm.Zone}
	}
	return vm
}

func distributeError(requests []*fleetBatchedRequest, err error) {
	distribute(requests, FleetBatchResponse{Error: err})
}

// distribute sends the same Fleet result or error payload to every request in the batch
// through its response channel. Successful responses share the same FleetSharedState.
func distribute(requests []*fleetBatchedRequest, resp FleetBatchResponse) {
	for _, req := range requests {
		req.ResponseChan <- &batcher.Response[FleetBatchResponse]{Payload: resp}
	}
}

// fleetName returns "fleet-{clusterName}-{hash8}-{random16hex}", truncating
// the cluster name if needed. batchKey has the form "{nodePoolName}/{hash16}".
func fleetName(clusterName, batchKey string) (string, error) {
	hash := batchKey[strings.LastIndex(batchKey, "/")+1:]
	hash = hash[:min(8, len(hash))]
	var randomSuffix [8]byte
	if _, err := rand.Read(randomSuffix[:]); err != nil {
		return "", fmt.Errorf("generating random suffix: %w", err)
	}
	suffix := fmt.Sprintf("-%s-%s", hash, hex.EncodeToString(randomSuffix[:]))
	const prefix = "fleet-"
	clusterName = clusterName[:min(len(clusterName), maxFleetNameLength-len(prefix)-len(suffix))]
	return prefix + clusterName + suffix, nil
}
