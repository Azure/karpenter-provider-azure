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
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

var (
	NodePoolTagKey = strings.ReplaceAll(karpv1.NodePoolLabelKey, "/", "_")
)

type VMImageIDContextKey string

const VMImageIDKey VMImageIDContextKey = "vmimageid"

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

	AKSMachineID               string
	AKSMachineNodeImageVersion string
	VMResourceID               string
}

func NewAKSMachinePromise(
	providerRef AKSMachineProvider,
	aksMachineTemplate *armcontainerservice.Machine,
	waitFunc func() error,
	aksMachineName string,
	instanceType *corecloudprovider.InstanceType,
	capacityType string,
	zone string,
	aksMachineID string,
	aksMachineNodeImageVersion string,
	vmResourceID string,
) *AKSMachinePromise {
	return &AKSMachinePromise{
		providerRef:                providerRef,
		AKSMachineTemplate:         aksMachineTemplate,
		waitFunc:                   waitFunc,
		AKSMachineName:             aksMachineName,
		InstanceType:               instanceType,
		CapacityType:               capacityType,
		Zone:                       zone,
		AKSMachineID:               aksMachineID,
		AKSMachineNodeImageVersion: aksMachineNodeImageVersion,
		VMResourceID:               vmResourceID,
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
	// Update updates the AKS machine instance with the specified name. Uses ETag for optimistic concurrency control.
	// Return NodeClaimNotFoundError if not found.
	Update(ctx context.Context, aksMachineName string, aksMachine armcontainerservice.Machine, etag *string) error
	// Get retrieves the AKS machine instance with the specified AKS machine name. Return NodeClaimNotFoundError if not found.
	Get(ctx context.Context, aksMachineName string) (*armcontainerservice.Machine, error)
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
	errorHandling           *offerings.CloudErrorHandler
}

func NewAKSMachineProvider(
	ctx context.Context,
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
		errorHandling:           offerings.NewCloudErrorHandler(offeringsCache),
	}

	return provider
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
) (*AKSMachinePromise, error) {
	aksMachineName, err := GetAKSMachineNameFromNodeClaimName(nodeClaim.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to generate AKS machine name from NodeClaim name %q: %w", nodeClaim.Name, err)
	}
	instanceTypes = offerings.OrderInstanceTypesByPrice(instanceTypes, scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...))

	aksMachinePromise, err := p.beginCreateMachine(ctx, nodeClass, nodeClaim, instanceTypes, aksMachineName)
	if err != nil {
		// Clean up if creation fails.
		if err := p.deleteMachine(ctx, aksMachineName); err != nil {
			if !IsAKSMachineOrMachinesPoolNotFound(err) {
				log.FromContext(ctx).Error(err, "failed to delete AKS machine after failed creation", "aksMachineName", aksMachineName)
			}
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

func (p *DefaultAKSMachineProvider) Update(ctx context.Context, aksMachineName string, aksMachine armcontainerservice.Machine, etag *string) error {
	if !shouldAKSMachinesBeVisible(ctx) {
		return corecloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("existing AKS machines management is disabled, and provision mode is not AKS machine"))
	}

	var options *armcontainerservice.MachinesClientBeginCreateOrUpdateOptions
	if etag != nil {
		options = &armcontainerservice.MachinesClientBeginCreateOrUpdateOptions{
			IfMatch: etag,
		}
	}

	poller, err := p.azClient.aksMachinesClient.BeginCreateOrUpdate(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachineName, aksMachine, options)
	if err != nil {
		if IsAKSMachineOrMachinesPoolNotFound(err) {
			// Can only be AKS machines pool not found.
			// Suggestion: separate the util function to not cover more than needed?
			return corecloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("failed to begin update AKS machine %q: %w", aksMachineName, err))
		}
		return fmt.Errorf("failed to begin update AKS machine %q: %w", aksMachineName, err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to update AKS machine %q during LRO: %w", aksMachineName, err)
	}
	return nil
}

// ASSUMPTION: the AKS machine will be in the current p.aksMachinesPoolName. Otherwise need rework to pass the pool name in.
func (p *DefaultAKSMachineProvider) Get(ctx context.Context, aksMachineName string) (*armcontainerservice.Machine, error) {
	if !shouldAKSMachinesBeVisible(ctx) {
		return nil, corecloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("existing AKS machines management is disabled, and provision mode is not AKS machine"))
	}

	if p.aksMachinesPoolName == "" {
		// Possible when this option field is not populated, which is not required when PROVISION_MODE is not aksmachineapi.
		// But an AKS machine instance exists, whether added manually or from before switching PROVISION_MODE.
		// So, we respond similarly to if AKS machines pool is not found.
		return nil, corecloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("failed to get AKS machine, AKS machines pool name is empty"))
	}

	// ASSUMPTION: AKS machines API accepts only AKS machine name.
	aksMachine, err := p.getMachine(ctx, aksMachineName)
	if err != nil {
		if IsAKSMachineOrMachinesPoolNotFound(err) {
			return nil, corecloudprovider.NewNodeClaimNotFoundError(err)
		}
		return nil, err
	}

	return aksMachine, nil
}

func (p *DefaultAKSMachineProvider) List(ctx context.Context) ([]*armcontainerservice.Machine, error) {
	if !shouldAKSMachinesBeVisible(ctx) {
		return []*armcontainerservice.Machine{}, nil
	}

	if p.aksMachinesPoolName == "" {
		// Possible when this option field is not populated, which is not required when PROVISION_MODE is not aksmachineapi.
		// So, we respond similarly to if AKS machines pool is not found.
		return []*armcontainerservice.Machine{}, nil
	}

	aksMachines, err := p.listMachines(ctx)
	if err != nil {
		return nil, err
	}

	return aksMachines, nil
}

func (p *DefaultAKSMachineProvider) Delete(ctx context.Context, aksMachineName string) error {
	if !shouldAKSMachinesBeVisible(ctx) {
		return corecloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("existing AKS machines management is disabled, and provision mode is not AKS machine"))
	}

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
		if IsAKSMachineOrMachinesPoolNotFound(err) {
			return corecloudprovider.NewNodeClaimNotFoundError(err)
		}
		return err
	}
	return nil
}

func (p *DefaultAKSMachineProvider) GetMachinesPoolLocation() string {
	return p.aksMachinesPoolLocation
}

func (p *DefaultAKSMachineProvider) rehydrateMachine(aksMachine *armcontainerservice.Machine) {
	// This needs to be rehydrated per the current behavior of both AKS machine API and AKS AgentPool API: priority will shows up only for spot.
	// Suggestion: rework/research more on this pattern RP-side?
	if aksMachine.Properties != nil && aksMachine.Properties.Priority == nil {
		aksMachine.Properties.Priority = lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular)
	}
}

func (p *DefaultAKSMachineProvider) getMachine(ctx context.Context, aksMachineName string) (*armcontainerservice.Machine, error) {
	resp, err := p.azClient.aksMachinesClient.Get(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachineName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get AKS machine %q: %w", aksMachineName, err)
	}
	aksMachine := lo.ToPtr(resp.Machine)
	p.rehydrateMachine(aksMachine)

	return aksMachine, nil
}

func (p *DefaultAKSMachineProvider) listMachines(ctx context.Context) ([]*armcontainerservice.Machine, error) {
	var machines []*armcontainerservice.Machine
	pager := p.azClient.aksMachinesClient.NewListPager(p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, nil)
	if pager == nil {
		return nil, fmt.Errorf("failed to list AKS machines: created pager is nil")
	}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if IsAKSMachineOrMachinesPoolNotFound(err) {
				// AKS machines pool not found. Handle gracefully.
				// Suggestion: separate the util function to not cover more than needed?
				log.FromContext(ctx).V(1).Info("failed to list AKS machines: AKS machines pool not found, treating as no AKS machines found")
				break
			}

			return nil, fmt.Errorf("failed to list AKS machines: %w", err)
		}

		for _, aksMachine := range page.Value {
			// Filter to only include machines created by Karpenter
			// Check if the AKS machine has the Karpenter nodepool tag
			if aksMachine.Properties != nil && aksMachine.Properties.Tags != nil {
				if _, hasKarpenterTag := aksMachine.Properties.Tags[NodePoolTagKey]; hasKarpenterTag {
					p.rehydrateMachine(aksMachine)
					machines = append(machines, aksMachine)
				}
			}
		}
	}

	return machines, nil
}

func (p *DefaultAKSMachineProvider) deleteMachine(ctx context.Context, aksMachineName string) error {
	log.FromContext(ctx).V(1).Info("deleting AKS machine", "aksMachineName", aksMachineName)

	// Suggestion: we could utilize this batch capability to optimize performance
	aksMachines := armcontainerservice.AgentPoolDeleteMachinesParameter{
		MachineNames: []*string{&aksMachineName},
	}

	poller, err := p.azClient.agentPoolsClient.BeginDeleteMachines(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachines, nil)
	if err != nil {
		return fmt.Errorf("failed to begin delete AKS machine %q: %w", aksMachineName, err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to delete AKS machine %q during LRO: %w", aksMachineName, err)
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
	existingAKSMachine, err := p.getMachine(ctx, aksMachineName)
	if err == nil {
		// Existing AKS machine found, reuse it.
		return p.reuseExistingMachine(ctx, aksMachineName, nodeClass, nodeClaim, instanceTypes, existingAKSMachine)
	} else if !IsAKSMachineOrMachinesPoolNotFound(err) {
		// Not fatal. Will fall back to normal creation.
		log.FromContext(ctx).Error(err, "failed to check for existing AKS machine", "aksMachineName", aksMachineName)
	}

	// Decide on offerings
	instanceType, capacityType, zone := offerings.PickSkuSizePriorityAndZone(ctx, nodeClaim, instanceTypes)
	if instanceType == nil {
		return nil, corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("no instance types available"))
	}

	// Determine creation timestamp for Karpenter's perspective
	creationTimestamp := NewAKSMachineTimestamp()

	// Build the AKS machine template
	aksMachineTemplate, err := p.buildAKSMachineTemplate(ctx, instanceType, capacityType, zone, nodeClass, nodeClaim, creationTimestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to build AKS machine template from template: %w", err)
	}
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
	header.Set("OSImageName", osImageName)                           // E.g. "2204gen2containerd"
	header.Set("OSImageResourceGroup", osImageResourceGroup)         // E.g. "AKS-Ubuntu"
	header.Set("OSImageSubscriptionID", osImageSubscriptionID)       // E.g., "10945678-1234-1234-1234-123456789012"
	header.Set("OSImageGallery", osImageGallery)                     // E.g., "AKSUbuntu"
	header.Set("OSImageVersion", osImageVersion)                     // E.g., "2022.10.03"
	ctxWithHeader := context.WithValue(ctx, VMImageIDKey, vmImageID) // This line is really just for testing (see fake/aksmachinesapi.go). Azure-sdk-for-go is restrictive in extracting the header out.
	ctxWithHeader = policy.WithHTTPHeader(ctxWithHeader, header)

	// Call the AKS machine API with the template to create the AKS machine instance
	log.FromContext(ctx).V(1).Info("creating AKS machine", "aksMachineName", aksMachineName, "instance-type", instanceType.Name)
	poller, err := p.azClient.aksMachinesClient.BeginCreateOrUpdate(ctxWithHeader, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachineName, *aksMachineTemplate, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin create AKS machine %q: %w", aksMachineName, err)
	}

	// Get once after begin create to retrieve VMResourceID.
	// In fact, the AKS machine object we want here is already returned with the PUT request above. However, the SDK have prevented us from accessing it easily.
	// TODO: find a way to access that instead of making another GET call like this.
	gotAKSMachine, err := p.getMachine(ctx, aksMachineName)
	if err != nil {
		return nil, fmt.Errorf("failed to get AKS machine %q once after begin creation: %w", aksMachineName, err)
	}
	// Process what we got.
	if err := validateRetrievedAKSMachineBasicProperties(gotAKSMachine); err != nil {
		return nil, fmt.Errorf("failed to get AKS machine %q once after begin creation: %w", aksMachineName, err)
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
			_, err := poller.PollUntilDone(ctx, nil)
			if err != nil {
				// Could be quota error; will be handled with custom logic below

				// Get once after begin create to retrieve error details. This is because if the poller returns error, the sdk doesn't let us look at the real results.
				failedAKSMachine, _ := p.getMachine(ctx, aksMachineName)
				if failedAKSMachine.Properties != nil && failedAKSMachine.Properties.Status != nil && failedAKSMachine.Properties.Status.ProvisioningError != nil {
					return p.handleMachineProvisioningError(ctx, "LRO", aksMachineName, nodeClass, instanceType, zone, capacityType, failedAKSMachine.Properties.Status.ProvisioningError)
				}
				// This should not be expected.
				return fmt.Errorf("failed to create AKS machine %q during LRO, AKS API returned error: %w", aksMachineName, err)
			}

			log.FromContext(ctx).V(1).Info("successfully created AKS machine",
				"aksMachineName", aksMachineName,
				"aksMachineID", gotAKSMachine.ID)
			return nil
		},
		aksMachineName,
		instanceType,
		capacityType,
		zone,
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

	handledError := p.errorHandling.Handle(ctx, sku, instanceType, zone, capacityType, innerError)
	if handledError != nil {
		// If error is handled, return it (wrapped)
		return fmt.Errorf("failed to create AKS machine %q during %s, handled provisioning error: %w", aksMachineName, phase, handledError)
	}

	// XPMT: TODO(comtalyst): revalidate this + see if it makes more sense to loop over

	return fmt.Errorf("failed to create AKS machine %q during %s, unhandled provisioning error: code=%s, message=%s", aksMachineName, phase, lo.FromPtr(innerError.Code), lo.FromPtr(innerError.Message))
}

func (p *DefaultAKSMachineProvider) reuseExistingMachine(ctx context.Context, aksMachineName string, nodeClass *v1beta1.AKSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*corecloudprovider.InstanceType, existingAKSMachine *armcontainerservice.Machine) (*AKSMachinePromise, error) {
	// Reconstruct properties from existing AKS machine instance.
	if err := validateRetrievedAKSMachineBasicProperties(existingAKSMachine); err != nil {
		return nil, fmt.Errorf("found existing AKS machine %s, but %w", aksMachineName, err)
	}
	if existingAKSMachine.Properties.Tags == nil || existingAKSMachine.Properties.Tags[launchtemplate.KarpenterAKSMachineNodeClaimTagKey] == nil {
		// This is not included in validateRetrievedAKSMachineBasicProperties as inplaceupdate can repair it.
		// Although, we don't want to reuse a machine until that happens.
		return nil, fmt.Errorf("found existing AKS machine %s, but %w", aksMachineName, fmt.Errorf("irretrievable karpenter.azure.com_aksmachine_nodeclaim tag"))
	}

	var existingAKSMachineZone string
	if len(existingAKSMachine.Zones) == 0 || existingAKSMachine.Zones[0] == nil {
		existingAKSMachineZone = "" // No zone
	} else {
		existingAKSMachineZone = lo.FromPtr(existingAKSMachine.Zones[0])
	}
	existingAKSMachineVMSize := lo.FromPtr(existingAKSMachine.Properties.Hardware.VMSize)
	existingAKSMachinePriority := lo.FromPtr(existingAKSMachine.Properties.Priority)
	existingAKSMachineVMResourceID := lo.FromPtr(existingAKSMachine.Properties.ResourceID)
	existingAKSMachineID := lo.FromPtr(existingAKSMachine.ID)
	existingAKSMachineNodeImageVersion := lo.FromPtr(existingAKSMachine.Properties.NodeImageVersion)
	existingAKSMachineNodeClaimName := lo.FromPtr(existingAKSMachine.Properties.Tags[launchtemplate.KarpenterAKSMachineNodeClaimTagKey])

	instanceType := offerings.GetInstanceTypeFromVMSize(existingAKSMachineVMSize, instanceTypes)
	capacityType := GetCapacityTypeFromAKSScaleSetPriority(existingAKSMachinePriority)
	zone := utils.GetAKSLabelZoneFromARMZone(p.aksMachinesPoolLocation, existingAKSMachineZone)

	if existingAKSMachineNodeClaimName != nodeClaim.Name {
		// Might be possible from NodePool name hash collision within AKS machine name
		// See how AKS machine name is generated for more details.
		// ASSUMPTION: repeated failure will eventually result in NodeClaim reaching registration TTL, then gets re-created with the new hash, recovering from the collision.
		return nil, fmt.Errorf("found existing AKS machine %s, but its karpenter.azure.com_aksmachine_nodeclaim tag %q does not match the NodeClaim to create %q", aksMachineName, existingAKSMachineNodeClaimName, nodeClaim.Name)
	}
	if existingAKSMachine.Properties.ProvisioningState != nil && lo.FromPtr(existingAKSMachine.Properties.ProvisioningState) == "Failed" {
		// Unfortunately, that was more like a remain than a usable aksMachine.
		// ASSUMPTION: this is irrecoverable (i.e., polling would have failed).
		return nil, p.handleMachineProvisioningError(ctx, "reusing existing AKS machine", aksMachineName, nodeClass, instanceType, zone, capacityType, existingAKSMachine.Properties.Status.ProvisioningError)
	}

	log.FromContext(ctx).V(1).Info("reused existing AKS machine",
		"aksMachineName", aksMachineName,
		"instance-type", instanceType.Name,
		"zone", zone,
		"capacity-type", capacityType,
	)

	return NewAKSMachinePromise(
		p,
		existingAKSMachine,
		func() error {
			// We hope the AKS machine completed provisioning at this point. Otherwise, if fails, it would not be handled until registration TTL.
			// Suggestion: create a new poller just to handle it the same way as new machines. That will improve performance for such cases.
			return nil
		},
		aksMachineName,
		instanceType,
		capacityType,
		zone,
		existingAKSMachineID,
		existingAKSMachineNodeImageVersion,
		existingAKSMachineVMResourceID,
	), nil
}
