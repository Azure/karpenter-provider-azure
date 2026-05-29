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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
)

const (
	// FleetNameTagKey is applied to all Fleet VMs so the executor can discover them after LRO.
	FleetNameTagKey = "karpenter.azure.com_fleet-name"
)

// executor sends batches to the Azure Fleet API.
// It transforms a pending batch into a Fleet CreateOrUpdate call, waits for
// the LRO, runs VM assignment, and distributes results back to each request.
type executor struct {
	fleetClient   FleetAPI
	vmClient      VMAPI
	errorHandler  *offerings.FleetErrorHandler
	clusterName   string
	resourceGroup string
	location      string
}

func newExecutor(
	fleetClient FleetAPI,
	vmClient VMAPI,
	errorHandler *offerings.FleetErrorHandler,
	clusterName, resourceGroup, location string,
) *executor {
	return &executor{
		fleetClient:   fleetClient,
		vmClient:      vmClient,
		errorHandler:  errorHandler,
		clusterName:   clusterName,
		resourceGroup: resourceGroup,
		location:      location,
	}
}

// executeBatch is the batcher.ExecuteBatch[FleetVMProvisionRequest, FleetBatchResponse] implementation.
// It orchestrates: fleet name → body → PUT → LRO poll → VM list → shared state → distribute responses.
func (e *executor) executeBatch(ctx context.Context, batch *batcher.Batch[FleetVMProvisionRequest, FleetBatchResponse]) {
	logger := log.FromContext(ctx).WithValues("batchKey", batch.Key, "batchSize", len(batch.Requests))

	// 1. Compute deterministic fleet name from batch key.
	name := fleetName(e.clusterName, batch.Key)
	logger = logger.WithValues("fleetName", name)

	// 2. Collect merged instance types and build assignment requests.
	mergedInstanceTypes := make(map[string]*cloudprovider.InstanceType)
	requests := make([]*VMAssignmentRequest, 0, len(batch.Requests))
	var representative *FleetVMProvisionRequest

	for _, br := range batch.Requests {
		req := &br.Payload
		if representative == nil {
			representative = req
		}
		for k, v := range req.InstanceTypes {
			mergedInstanceTypes[k] = v
		}
		requests = append(requests, &VMAssignmentRequest{
			NodeClaimName:   req.NodeClaimName,
			AcceptableSKUs:  req.AcceptableSKUs,
			AcceptableZones: req.AcceptableZones,
			InstanceTypes:   req.InstanceTypes,
		})
	}

	// 3. Build fleet body from the representative request (all requests in same batch
	//    share the same template/image/subnet per batch key guarantee).
	//    Inject fleet-name tag so we can discover the VMs after LRO.
	fields := extractBatchKeyFields(representative)
	fleetTags := make(map[string]*string, len(representative.Tags)+1)
	for k, v := range representative.Tags {
		fleetTags[k] = v
	}
	fleetTags[FleetNameTagKey] = lo.ToPtr(name)

	fleetBody := BuildFleetBody(
		fields,
		int32(len(batch.Requests)),
		fleetTags,
		nil, // spotMaxPrice: nil → default -1 (up to on-demand price)
		e.location,
		representative.LBBackendPools,
		mergedInstanceTypes,
		false, // useSIG: not used in POC
		representative.Extensions,
	)

	// 4. Call Fleet API BeginCreateOrUpdate.
	logger.Info("submitting fleet create-or-update")
	if v := logger.V(1); v.Enabled() {
		if data, mErr := json.Marshal(fleetBody); mErr == nil {
			v.Info("fleet request body", "fleetName", name, "json", string(data))
		} else {
			v.Info("fleet request body marshal failed", "error", mErr.Error())
		}
	}
	poller, err := e.fleetClient.BeginCreateOrUpdate(ctx, e.resourceGroup, name, *fleetBody, nil)
	if err != nil {
		logger.Error(err, "fleet BeginCreateOrUpdate failed")
		e.distributeError(batch, fmt.Errorf("fleet create: %w", err))
		return
	}

	// 5. Poll LRO to completion.
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		logger.Error(err, "fleet LRO poll failed")
		e.distributeError(batch, fmt.Errorf("fleet LRO: %w", err))
		return
	}
	logger.Info("fleet LRO completed")

	// 6. List VMs created by this Fleet (identified by fleet-name tag).
	vms, err := e.listFleetVMs(ctx, name)
	if err != nil {
		logger.Error(err, "failed to list fleet VMs")
		e.distributeError(batch, fmt.Errorf("list fleet VMs: %w", err))
		return
	}
	logger.Info("listed fleet VMs", "count", len(vms))

	sharedState := NewFleetSharedState(
		requests,
		mergedInstanceTypes,
		e.vmClient,
		name,
		e.resourceGroup,
	)
	sharedState.SetVMs(vms)

	// 7. Distribute shared state to all requests.
	e.distributeSharedState(batch, sharedState)
}

// distributeError sends an error to all requests in the batch.
func (e *executor) distributeError(batch *batcher.Batch[FleetVMProvisionRequest, FleetBatchResponse], err error) {
	for _, req := range batch.Requests {
		req.ResponseChan <- &batcher.Response[FleetBatchResponse]{
			Payload: FleetBatchResponse{Error: err},
		}
	}
}

// distributeSharedState sends the shared state to all requests in the batch.
func (e *executor) distributeSharedState(batch *batcher.Batch[FleetVMProvisionRequest, FleetBatchResponse], state *FleetSharedState) {
	for _, req := range batch.Requests {
		req.ResponseChan <- &batcher.Response[FleetBatchResponse]{
			Payload: FleetBatchResponse{SharedState: state},
		}
	}
}

// fleetName returns the deterministic fleet name: "fleet-{clusterName}-{hash8}"
// The name is stable for a given batch key configuration — same config always produces
// the same Fleet resource. This makes BeginCreateOrUpdate idempotent.
// batchKey format: "<nodepool>/<capacityType>/<hash16>"
func fleetName(clusterName, batchKey string) string {
	// Extract last segment (the 16-char hex hash), take first 8 chars.
	lastSlash := strings.LastIndex(batchKey, "/")
	hash := batchKey[lastSlash+1:]
	if len(hash) > 8 {
		hash = hash[:8]
	}
	return fmt.Sprintf("fleet-%s-%s", clusterName, hash)
}

// extractBatchKeyFields builds the BatchKeyFields from a FleetVMProvisionRequest.
// Used by the executor to pass to BuildFleetBody.
func extractBatchKeyFields(req *FleetVMProvisionRequest) BatchKeyFields {
	return BatchKeyFields{
		NodePoolName:        req.NodeClaim.Labels[karpv1.NodePoolLabelKey],
		CapacityType:        req.CapacityType,
		ImageID:             req.LaunchTemplate.ImageID,
		SubnetID:            req.LaunchTemplate.SubnetID,
		SSHPublicKey:        req.SSHPublicKey,
		AdminUsername:       req.AdminUsername,
		CustomData:          req.LaunchTemplate.ScriptlessCustomData,
		OSDiskSizeGB:        int(req.LaunchTemplate.StorageProfileSizeGB),
		OSDiskType:          string(req.LaunchTemplate.StorageProfilePlacement),
		EncryptionAtHost:    req.NodeClass.GetEncryptionAtHost(),
		DiskEncryptionSetID: req.DiskEncryptionSetID,
		NodeIdentities:      joinSorted(req.NodeIdentities),
		NSG:                 req.NSG,
		CandidateSKUs:       sortedCopy(req.AcceptableSKUs),
		Zones:               sortedCopy(req.AcceptableZones),
	}
}

// listFleetVMs lists all VMs in the resource group that carry the fleet-name tag
// matching the given name. This discovers VMs created by the Fleet VMSS Flex.
func (e *executor) listFleetVMs(ctx context.Context, name string) ([]*armcompute.VirtualMachine, error) {
	pager := e.vmClient.NewListPager(e.resourceGroup, nil)
	var vms []*armcompute.VirtualMachine
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing VMs page: %w", err)
		}
		for _, vm := range page.Value {
			if vm == nil || vm.Tags == nil {
				continue
			}
			if tagVal, ok := vm.Tags[FleetNameTagKey]; ok && tagVal != nil && *tagVal == name {
				vms = append(vms, vm)
			}
		}
	}
	return vms, nil
}
