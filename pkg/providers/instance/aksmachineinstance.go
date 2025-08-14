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
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

var (
	NodePoolTagKey = strings.ReplaceAll(karpv1.NodePoolLabelKey, "/", "_")
)

// Intended for lifecycle handling on the higher abstractions.
type InstancePromise interface {
	// Cleanup removes the instance from the cloud provider.
	Cleanup(ctx context.Context) error
	// Wait blocks until the instance is ready.
	Wait() error
	// GetInstanceName returns the name of the instance. Recommended to be used for logging only due to generic nature.
	GetInstanceName() string
}

// Notes on terminology:
// An "instance" is a remote object, created by the API based on the template.
// A "template" is a local struct, populated from Karpenter-provided parameters with the logic further below.
// A "template" shares the struct with an "instance" representation. But read-only fields may not be populated. Ideally, the types should have been separated to avoid making cross-module assumption of the existence of certain fields.
type AKSMachinePromise struct {
	waitFunc    func() error
	providerRef AKSMachineProvider

	AKSMachineTemplate *armcontainerservice.Machine
	AKSMachineName     string
	InstanceType       *corecloudprovider.InstanceType // Despite the reference nature, this is guaranteed to exist
	CapacityType       string
	Zone               string

	AKSMachineCreationTimestamp time.Time
	AKSMachineID                string
	AKSMachineNodeImageVersion  string
	VMResourceID                string
}

func NewAKSMachinePromise(
	providerRef AKSMachineProvider,
	aksMachineTemplate *armcontainerservice.Machine,
	waitFunc func() error,
	aksMachineName string,
	instanceType *corecloudprovider.InstanceType,
	capacityType string,
	zone string,
	aksMachineCreationTimestamp time.Time,
	aksMachineID string,
	aksMachineNodeImageVersion string,
	vmResourceID string,
) *AKSMachinePromise {
	return &AKSMachinePromise{
		providerRef:                 providerRef,
		AKSMachineTemplate:          aksMachineTemplate,
		waitFunc:                    waitFunc,
		AKSMachineName:              aksMachineName,
		InstanceType:                instanceType,
		CapacityType:                capacityType,
		Zone:                        zone,
		AKSMachineCreationTimestamp: aksMachineCreationTimestamp,
		AKSMachineID:                aksMachineID,
		AKSMachineNodeImageVersion:  aksMachineNodeImageVersion,
		VMResourceID:                vmResourceID,
	}
}

func (p *AKSMachinePromise) Cleanup(ctx context.Context) error {
	return p.providerRef.Delete(ctx, p.AKSMachineName)
}

func (p *AKSMachinePromise) Wait() error {
	return p.waitFunc()
}
func (p *AKSMachinePromise) GetInstanceName() string {
	return p.AKSMachineName
}

type AKSMachineProvider interface {
	// BeginCreate starts the creation of an AKS machine instance.
	// Returns a promise that must be waited on to complete the creation.
	BeginCreate(ctx context.Context, nodeClass *v1beta1.AKSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*corecloudprovider.InstanceType) (*AKSMachinePromise, error)
	// Update updates the AKS machine instance with the specified name. Return NodeClaimNotFoundError if not found.
	Update(ctx context.Context, aksMachineName string, aksMachine armcontainerservice.Machine) error
	// Get retrieves the AKS machine instance with the specified name. This could be either VM name or AKS machine name. Return NodeClaimNotFoundError if not found.
	Get(ctx context.Context, aksMachineNameOrVMName string) (*armcontainerservice.Machine, error)
	// List lists all AKS machine instances in the cluster.
	List(ctx context.Context) ([]*armcontainerservice.Machine, error)
	// Delete deletes the AKS machine instance with the specified name. Return NodeClaimNotFoundError if not found.
	Delete(ctx context.Context, aksMachineName string) error
	// GetMachinesPoolLocation returns the location of the AKS machines pool. The only reason this need to be exported is because armcontainerservice.Machine does not have the location field.
	GetMachinesPoolLocation() string
}

// assert that DefaultAKSMachineProvider implements Provider interface
var _ AKSMachineProvider = (*DefaultAKSMachineProvider)(nil)

type DefaultAKSMachineProvider struct {
	azClient                *AZClient
	instanceTypeProvider    instancetype.Provider
	imageResolver           imagefamily.Resolver
	subscriptionID          string
	clusterResourceGroup    string
	clusterName             string
	aksMachinesPoolName     string // Only support one AKS machine pool at a time, for now.
	aksMachinesPoolLocation string
	errorHandling           *offerings.CloudErrorHandling
}

func NewAKSMachineProvider(
	ctx context.Context,
	provisionMode string,
	azClient *AZClient,
	instanceTypeProvider instancetype.Provider,
	imageResolver imagefamily.Resolver,
	offeringsCache *cache.UnavailableOfferings,
	subscriptionID string,
	clusterResourceGroup string,
	clusterName string,
	aksMachinesPoolName string,
	aksMachinesPoolLocation string,
) *DefaultAKSMachineProvider {
	provider := &DefaultAKSMachineProvider{
		azClient:                azClient,
		instanceTypeProvider:    instanceTypeProvider,
		imageResolver:           imageResolver,
		clusterResourceGroup:    clusterResourceGroup,
		subscriptionID:          subscriptionID,
		clusterName:             clusterName,
		aksMachinesPoolName:     aksMachinesPoolName,
		aksMachinesPoolLocation: aksMachinesPoolLocation,
		errorHandling: &offerings.CloudErrorHandling{
			UnavailableOfferings: offeringsCache,
			CloudErrorHandlers:   offerings.DefaultCloudErrorHandlers(),
		},
	}

	return provider
}

// doesAKSMachinesPoolExists checks if the AKS machines pool exists
func (p *DefaultAKSMachineProvider) doesAKSMachinesPoolExists(ctx context.Context) (bool, *armcontainerservice.AgentPool, error) {
	resp, err := p.azClient.agentPoolsClient.Get(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, nil)
	if err != nil {
		if IsARMNotFound(err) {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("failed to check if AKS machines pool exists: %w", err)
	}
	return true, lo.ToPtr(resp.AgentPool), nil
}

// BeginCreate creates an instance given the constraints.
// Note that the returned instance may not be finished provisioning yet.
// Errors that occur on the "sync side" of the VM create, such as BadRequest due
// to invalid user input, and similar, will have the error returned here.
// Errors that occur on the "async side" of the VM create (after the request is accepted, or after polling the
// VM create and while ) will be returned
// from the AKSMachinePromise.Wait().
func (p *DefaultAKSMachineProvider) BeginCreate(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*corecloudprovider.InstanceType,
) (*AKSMachinePromise, error) { // XPMT: ☑️
	aksMachineName := GetAKSMachineNameFromNodeClaimName(nodeClaim.Name)
	instanceTypes = offerings.OrderInstanceTypesByPrice(instanceTypes, scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...))

	aksMachinePromise, err := p.beginCreateMachine(ctx, nodeClass, nodeClaim, instanceTypes, aksMachineName)
	if err != nil {
		// Clean up if creation fails.
		if err := p.deleteMachine(ctx, aksMachineName); err != nil {
			log.FromContext(ctx).Error(err, "failed to delete AKS machine after failed creation", "aksMachineName", aksMachineName)
			// We don't return the cleanup error here, as we want to return the original error from beginCreateMachine
		}
		return nil, err
	}

	log.FromContext(ctx).Info("launched new AKS machine instance",
		"aksMachineName", aksMachineName,
		"instance-type", aksMachinePromise.InstanceType.Name,
		"zone", aksMachinePromise.Zone,
		"capacity-type", aksMachinePromise.CapacityType,
	)

	return aksMachinePromise, nil
}

func (p *DefaultAKSMachineProvider) Update(ctx context.Context, aksMachineName string, aksMachine armcontainerservice.Machine) error {
	poller, err := p.azClient.aksMachinesClient.BeginCreateOrUpdate(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachineName, aksMachine, nil)
	if err != nil {
		if IsARMNotFound(err) {
			// XPMT: TODO: check API: see what happens when Machines pool is not found when calling Machines get.
			return corecloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("aksMachine.BeginCreateOrUpdate for AKS machine %q failed: %w", aksMachineName, err))
		}
		return fmt.Errorf("aksMachine.BeginCreateOrUpdate for AKS machine %q failed: %w", aksMachineName, err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("AKS machine update failed for %q during LRO, API error: %w", aksMachineName, err)
	}
	return nil
}

// ASSUMPTION: the AKS machine will be in the current p.aksMachinesPoolName. Otherwise need rework to pass the pool name in.
func (p *DefaultAKSMachineProvider) Get(ctx context.Context, aksMachineNameOrVMName string) (*armcontainerservice.Machine, error) { // XPMT: ✅
	if p.aksMachinesPoolName == "" {
		// Possible when this option field is not populated, which is not required when PROVISION_MODE is not aksmachineapi.
		// But an AKS machine instance exists, whether added manually or from before switching PROVISION_MODE.
		// So, we respond similarly to if AKS machines pool is not found.
		return nil, corecloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("failed to get AKS machine instance, AKS machines pool name is empty"))
	}

	// ASSUMPTION: AKS machines API accepts either VM name or AKS machine name.
	resp, err := p.azClient.aksMachinesClient.Get(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachineNameOrVMName, nil)
	if err != nil {
		if IsARMNotFound(err) {
			// XPMT: TODO: check API: see what happens when Machines pool is not found when calling Machines get.
			return nil, corecloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("failed to get AKS machine instance, %w", err))
		}
		return nil, fmt.Errorf("failed to get AKS machine instance, %w", err)
	}

	return &resp.Machine, nil
}

func (p *DefaultAKSMachineProvider) List(ctx context.Context) ([]*armcontainerservice.Machine, error) {
	var machines []*armcontainerservice.Machine

	if p.aksMachinesPoolName == "" {
		// Possible when this option field is not populated, which is not required when PROVISION_MODE is not aksmachineapi.
		// So, we respond similarly to if AKS machines pool is not found.
		return machines, nil
	}

	pager := p.azClient.aksMachinesClient.NewListPager(p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, nil)
	if pager == nil {
		exists, _, err := p.doesAKSMachinesPoolExists(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to create pager for listing AKS machines and failed to check if AKS machines pool exists: %w", err)
		}
		if !exists {
			// AKS machines pool not found. Handle gracefully.
			return machines, nil
		}
		return nil, fmt.Errorf("failed to create pager for listing AKS machines")
	}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if IsARMNotFound(err) {
				// XPMT: TODO: check API: see what happens when Machines pool is not found when calling Machines list. It is probably just one of this and the above.
				// AKS machines pool not found (deleted mid-air?). Handle gracefully.
				break
			}

			return nil, fmt.Errorf("failed to list AKS machines: %w", err)
		}

		for _, aksMachine := range page.Value {
			// Filter to only include machines created by Karpenter
			// Check if the AKS machine has the Karpenter nodepool tag
			if aksMachine.Properties != nil && aksMachine.Properties.Tags != nil {
				if _, hasKarpenterTag := aksMachine.Properties.Tags[NodePoolTagKey]; hasKarpenterTag {
					machines = append(machines, aksMachine)
				}
			}
		}
	}

	return machines, nil
}

func (p *DefaultAKSMachineProvider) Delete(ctx context.Context, aksMachineName string) error { // XPMT: ✅
	// Note that 'Get' also satisfies cloudprovider.Delete contract expectation (from v1.3.0)
	// of returning cloudprovider.NewNodeClaimNotFoundError if the instance is already deleted
	aksMachine, err := p.Get(ctx, aksMachineName)
	if err != nil {
		return err
	}
	if IsAKSMachineDeleting(aksMachine) {
		return nil
	}

	err = p.deleteMachine(ctx, aksMachineName)
	if err != nil {
		if IsARMNotFound(err) {
			return corecloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("failed to delete AKS machine instance, %w", err))
		}
		return fmt.Errorf("failed to delete AKS machine instance, %w", err)
	}
	return nil
}

func (p *DefaultAKSMachineProvider) GetMachinesPoolLocation() string {
	return p.aksMachinesPoolLocation
}

func (p *DefaultAKSMachineProvider) deleteMachine(ctx context.Context, aksMachineName string) error {
	log.FromContext(ctx).V(1).Info("deleting AKS machine", "aksMachineName", aksMachineName)

	// Suggestion: we could utilize this batch capability to optimize performance
	aksMachines := armcontainerservice.AgentPoolDeleteMachinesParameter{
		MachineNames: []*string{&aksMachineName},
	}

	poller, err := p.azClient.agentPoolsClient.BeginDeleteMachines(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachines, nil)
	if err != nil {
		return fmt.Errorf("failed to begin delete for AKS machine %q: %w", aksMachineName, err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to delete AKS machine %q: %w", aksMachineName, err)
	}

	log.FromContext(ctx).V(1).Info("successfully deleted AKS machine", "aksMachineName", aksMachineName)
	return nil
}

// beginCreateMachine starts the creation of an AKS machine instance.
// The returned AKSMachinePromise must be called to gather any errors
// that are retrieved during async provisioning, as well as to complete the provisioning process.
// nolint: gocyclo
func (p *DefaultAKSMachineProvider) beginCreateMachine(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*corecloudprovider.InstanceType,
	aksMachineName string,
) (*AKSMachinePromise, error) {
	// Reuse the existing AKS machine if it exists, and skip the creation.
	// Note: currently, we do not support different offerings requirements for the NodeClaim with the same name that attempted creation recently. The same applies with VM-based provisioning.
	// This supported case is often a result of Karpenter restarting while the node is being created, in which the NodeClaim to create stays the same after restart.
	resp, err := p.azClient.aksMachinesClient.Get(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachineName, nil)
	if err == nil {
		// Existing AKS machine found, reuse it.

		// Reconstruct properties from existing AKS machine instance.
		if len(resp.Machine.Zones) == 0 || resp.Machine.Zones[0] == nil {
			// ASSUMPTION: always single zone, for now
			return nil, fmt.Errorf("found existing AKS machine instance %s, but corrupted due to irretrievable zone", aksMachineName)
		}
		if resp.Machine.Properties == nil {
			return nil, fmt.Errorf("found existing AKS machine instance %s, but corrupted due to irretrievable properties", aksMachineName)
		}
		if resp.Machine.Properties.Hardware == nil || resp.Machine.Properties.Hardware.VMSize == nil {
			return nil, fmt.Errorf("found existing AKS machine instance %s, but corrupted due to irretrievable VM size", aksMachineName)
		}
		if resp.Machine.Properties.Priority == nil {
			return nil, fmt.Errorf("found existing AKS machine instance %s, but corrupted due to irretrievable priority", aksMachineName)
		}
		if resp.Machine.Properties.Status == nil || resp.Machine.Properties.Status.CreationTimestamp == nil {
			return nil, fmt.Errorf("found existing AKS machine instance %s, but corrupted due to irretrievable creation timestamp", aksMachineName)
		}
		if resp.Machine.Properties.ResourceID == nil {
			return nil, fmt.Errorf("found existing AKS machine instance %s, but corrupted due to irretrievable VM resource ID", aksMachineName)
		}
		if resp.Machine.ID == nil {
			return nil, fmt.Errorf("found existing AKS machine instance %s, but corrupted due to irretrievable ID", aksMachineName)
		}
		aksMachineZone := lo.FromPtr(resp.Machine.Zones[0])
		machineVMSize := lo.FromPtr(resp.Machine.Properties.Hardware.VMSize)
		machinePriority := lo.FromPtr(resp.Machine.Properties.Priority)
		machineCreationTimestamp := lo.FromPtr(resp.Machine.Properties.Status.CreationTimestamp)
		vmResourceID := lo.FromPtr(resp.Machine.Properties.ResourceID)
		aksMachineID := lo.FromPtr(resp.Machine.ID)
		aksMachineNodeImageVersion := lo.FromPtr(resp.Machine.Properties.NodeImageVersion)

		instanceType := offerings.GetInstanceTypeFromVMSize(machineVMSize, instanceTypes)
		capacityType := GetCapacityTypeFromAKSScaleSetPriority(machinePriority)
		zone := utils.GetAKSZoneFromARMZone(p.aksMachinesPoolLocation, aksMachineZone)

		if resp.Machine.Properties.ProvisioningState != nil && lo.FromPtr(resp.Machine.Properties.ProvisioningState) == "Failed" {
			// Unfortunately, that was more like a remain than a usable aksMachine.
			// ASSUMPTION: this is irrecoverable (i.e., polling would have failed).
			return nil, p.handleMachineProvisioningError(ctx, "reusing existing AKS machine instance", aksMachineName, nodeClass, instanceType, zone, capacityType, resp.Machine.Properties.Status.ProvisioningError)
		}

		log.FromContext(ctx).V(1).Info("reused existing AKS machine instance",
			"aksMachineName", aksMachineName,
			"instance-type", instanceType.Name,
			"zone", zone,
			"capacity-type", capacityType,
		)

		return NewAKSMachinePromise(
			p,
			lo.ToPtr(resp.Machine),
			func() error {
				// We hope the AKS machine completed provisioning at this point. Otherwise, if fails, it would not be handled until registration TTL.
				// Suggestion: create a new poller just to handle it the same way as new machines. That will improve performance for such cases.
				return nil
			},
			aksMachineName,
			instanceType,
			capacityType,
			zone,
			machineCreationTimestamp,
			aksMachineID,
			aksMachineNodeImageVersion,
			vmResourceID,
		), nil
	} else if !IsARMNotFound(err) {
		// Not fatal. Will fall back to normal creation.
		log.FromContext(ctx).Error(err, "failed to check for existing machine", "aksMachineName", aksMachineName)
	}

	// Decide on offerings
	instanceType, capacityType, zone := offerings.PickSkuSizePriorityAndZone(ctx, nodeClaim, instanceTypes)
	if instanceType == nil {
		return nil, corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("no instance types available"))
	}

	// Build the AKS machine template
	aksMachineTemplate, err := p.buildAKSMachineTemplate(ctx, instanceType, capacityType, zone, nodeClass, nodeClaim)
	if err != nil {
		return nil, fmt.Errorf("failed to build AKS machine template from template: %w", err)
	}
	log.FromContext(ctx).V(1).Info("XPMT: AKS machine template built", "machineObj", *aksMachineTemplate)

	// Call the AKS machine API with the template to create the AKS machine instance
	log.FromContext(ctx).V(1).Info("creating AKS machine instance", "aksMachineName", aksMachineName, "instance-type", instanceType.Name)

	// Resolve VM image ID
	// E.g., "/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03"
	vmImageID, err := p.imageResolver.ResolveNodeImageFromNodeClass(nodeClass, instanceType)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve VM image ID: %w", err)
	}
	osImageSubscriptionID, osImageResourceGroup, osImageGallery, osImageName, osImageVersion, err := parseVMImageID(vmImageID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse VM image ID %q: %w", vmImageID, err)
	}
	header := http.Header{}
	header.Set("AKSHTTPCustomFeatures", "Microsoft.ContainerService/UseCustomizedOSImage")
	header.Set("OSImageName", osImageName)                          // E.g. "2204gen2containerd"
	header.Set("OSImageResourceGroup", osImageResourceGroup)        // E.g. "AKS-Ubuntu"
	header.Set("OSImageSubscriptionID", osImageSubscriptionID)      // E.g., "10945678-1234-1234-1234-123456789012"
	header.Set("OSImageGallery", osImageGallery)                    // E.g., "AKSUbuntu"
	header.Set("OSImageVersion", osImageVersion)                    // E.g., "2022.10.03"
	ctxWithHeader := context.WithValue(ctx, "vmimageid", vmImageID) // This line is really just for testing (see fake/aksmachinesapi.go). Azure-sdk-for-go is restrictive in extracting the header out.
	ctxWithHeader = policy.WithHTTPHeader(ctxWithHeader, header)

	poller, err := p.azClient.aksMachinesClient.BeginCreateOrUpdate(ctxWithHeader, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachineName, *aksMachineTemplate, nil)
	if err != nil {
		return nil, fmt.Errorf("aksMachine.BeginCreateOrUpdate for AKS machine %q failed: %w", aksMachineName, err)
	}

	// Get once after begin create to retrieve VMResourceID.
	getResp, err := p.azClient.aksMachinesClient.Get(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachineName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get AKS machine instance %q once after begin creation: %w", aksMachineName, err)
	}
	gotAKSMachine := lo.ToPtr(getResp.Machine)
	// Process what we got.
	if gotAKSMachine.ID == nil {
		return nil, fmt.Errorf("failed to get AKS machine instance %q once after begin creation: AKS machine ID is nil", aksMachineName)
	}
	if gotAKSMachine.Properties == nil {
		return nil, fmt.Errorf("failed to get AKS machine instance %q once after begin creation: AKS machine properties is nil", aksMachineName)
	}
	if gotAKSMachine.Properties.ResourceID == nil {
		return nil, fmt.Errorf("failed to get AKS machine instance %q once after begin creation: AKS machine VM resource ID is nil", aksMachineName)
	}
	if gotAKSMachine.Properties.Status == nil || gotAKSMachine.Properties.Status.CreationTimestamp == nil {
		return nil, fmt.Errorf("failed to get AKS machine instance %q once after begin creation: AKS machine creation timestamp is nil", aksMachineName)
	}
	if gotAKSMachine.Properties.NodeImageVersion == nil {
		return nil, fmt.Errorf("failed to get AKS machine instance %q once after begin creation: AKS machine node image version is nil", aksMachineName)
	}
	if gotAKSMachine.Properties.ProvisioningState != nil && lo.FromPtr(gotAKSMachine.Properties.ProvisioningState) == "Failed" {
		// We luckily catch failed state early (compared to during polling).
		// ASSUMPTION: this is irrecoverable (i.e., polling would have failed).
		return nil, p.handleMachineProvisioningError(ctx, "get once after begin creation", aksMachineName, nodeClass, instanceType, zone, capacityType, gotAKSMachine.Properties.Status.ProvisioningError)
	}

	// Return LRO
	return NewAKSMachinePromise(
		p,
		aksMachineTemplate,
		func() error {
			log.FromContext(ctx).V(1).Info("XPMT: waiting for AKS machine creation to complete", "aksMachineName", aksMachineName)

			_, err := poller.PollUntilDone(ctx, nil)
			if err != nil {
				// XPMT: (topic) Quota errors return on async phase
				// Could be quota error; will be handled with custom logic below

				// Get once after begin create to retrieve error details. This is because if the poller returns error, the sdk doesn't let us look at the real results.
				// XPMT: TODO: verify again when the API is ready
				getRespAfterFailed, _ := p.azClient.aksMachinesClient.Get(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachineName, nil)
				failedAKSMachine := lo.ToPtr(getRespAfterFailed.Machine)
				if failedAKSMachine.Properties != nil && failedAKSMachine.Properties.Status != nil && failedAKSMachine.Properties.Status.ProvisioningError != nil {
					return p.handleMachineProvisioningError(ctx, "async", aksMachineName, nodeClass, instanceType, zone, capacityType, failedAKSMachine.Properties.Status.ProvisioningError)
				}
				// This should not be expected.
				return fmt.Errorf("AKS machine creation failed for %q during async, API error: %w", aksMachineName, err)
			}

			log.FromContext(ctx).V(1).Info("AKS machine creation completed successfully",
				"aksMachineName", aksMachineName,
				"aksMachineID", gotAKSMachine.ID)
			return nil
		},
		aksMachineName,
		instanceType,
		capacityType,
		zone,
		lo.FromPtr(gotAKSMachine.Properties.Status.CreationTimestamp),
		lo.FromPtr(gotAKSMachine.ID),
		lo.FromPtr(gotAKSMachine.Properties.NodeImageVersion),
		lo.FromPtr(gotAKSMachine.Properties.ResourceID),
	), nil
}

// For use in beginCreateMachine only. Otherwise need to rework parameters, do nil check better, and generalize error messaging.
func (p *DefaultAKSMachineProvider) handleMachineProvisioningError(ctx context.Context, phase string, aksMachineName string, nodeClass *v1beta1.AKSNodeClass, instanceType *corecloudprovider.InstanceType, zone string, capacityType string, provisioningError *armcontainerservice.CloudErrorBody) error {
	var innerError armcontainerservice.CloudErrorBody
	if len(provisioningError.Details) > 0 && provisioningError.Details[0] != nil {
		// This should be VM creation error.
		// ASSUMPTION: the length of details is always <= 1. And VM creation error Karpenter may expect is always at Details[0].
		// Suggestion: suggest API change to have an explicit VM create error, if not changing Karpenter to rely on AKS machine ProvisioningError instead?
		innerError = *provisioningError.Details[0]
	} else {
		// Fallback to AKS machine API-level error. Though, this is unlikely to be handled by Karpenter.
		innerError = *provisioningError
	}

	sku, skuErr := p.instanceTypeProvider.Get(ctx, nodeClass, instanceType.Name)
	if skuErr != nil {
		return fmt.Errorf("failed to get instance type %q: %w, provisioning error left unhandled: code=%s, message=%s", instanceType.Name, skuErr, lo.FromPtr(innerError.Code), lo.FromPtr(innerError.Message))
	}

	handledError := p.errorHandling.HandleCloudError(ctx, sku, instanceType, zone, capacityType, innerError)
	if handledError != nil {
		// If error is handled, return it (wrapped)
		return fmt.Errorf("AKS machine creation failed for %q during %s, handled provisioning error: %w", aksMachineName, phase, handledError)
	}

	// XPMT: TODO: loop over instead of fixing on [0]

	return fmt.Errorf("AKS machine creation failed for %q during %s, unhandled provisioning error: code=%s, message=%s", aksMachineName, phase, lo.FromPtr(innerError.Code), lo.FromPtr(innerError.Message))
}
