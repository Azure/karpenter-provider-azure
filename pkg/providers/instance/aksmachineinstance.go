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
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/allocationstrategy"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/machinecache"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	machineUtils "github.com/Azure/karpenter-provider-azure/pkg/utils/machine"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

// Notes on terminology:
// An "instance" is a remote object, created by the API based on the template.
// A "template" is a local struct, populated from Karpenter-provided parameters with the logic further below.
// A "template" shares the struct with an "instance" representation. But read-only fields may not be populated. Ideally, the types should have been separated to avoid making cross-module assumption of the existence of certain fields.
//
// TODO: Consider extracting the template-related fields (AKSMachineTemplate, AKSMachineName, InstanceType, CapacityType, Zone, AKSMachineID, AKSMachineNodeImageVersion, VMResourceID)
// into a dedicated struct (e.g., AKSMachineDetails or AKSMachineTemplateInfo). This would clarify the relationship between
// the promise fields and functions like BuildNodeClaimFromAKSMachineTemplate, as well as reduce the number of loose arguments passed around.
// More discussion: https://github.com/Azure/karpenter-provider-azure/pull/1197#discussion_r2482957255
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
	CreationTimestamp          time.Time
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
	creationTimestamp time.Time,
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
		CreationTimestamp:          creationTimestamp,
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

type opt struct {
	useCache bool
}

// Option defines a functional option for configuring AKSMachineProvider methods that accept optional behavior.
type Option func(*opt)

// WithUseCache is an Option that tells the AKSMachineProvider to first use the machine cache
// when attempting to retrieve an AKS machine before falling back to calling the Azure API.
func WithUseCache() Option {
	return func(o *opt) {
		o.useCache = true
	}
}

type AKSMachineProvider interface {
	// BeginCreate starts the creation of an AKS machine instance.
	// Returns a promise that must be waited on to complete the creation.
	BeginCreate(ctx context.Context, nodeClass *v1beta1.AKSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*corecloudprovider.InstanceType) (*AKSMachinePromise, error)
	// Update updates the AKS machine instance with the specified name. Uses ETag for optimistic concurrency control.
	// Return NodeClaimNotFoundError if not found.
	Update(ctx context.Context, aksMachineName string, aksMachine armcontainerservice.Machine, etag *string) error
	// Get retrieves the AKS machine instance with the specified AKS machine name. Return NodeClaimNotFoundError if not found.
	Get(ctx context.Context, aksMachineName string, opts ...Option) (*armcontainerservice.Machine, error)
	// List lists all AKS machine instances in the cluster.
	List(ctx context.Context, opts ...Option) ([]*armcontainerservice.Machine, error)
	// Delete deletes the AKS machine instance with the specified name. Return NodeClaimNotFoundError if not found.
	Delete(ctx context.Context, aksMachineName string) error
	// GetMachinesPoolLocation returns the location of the AKS machines pool. The only reason this need to be exported is because armcontainerservice.Machine does not have the location field.
	GetMachinesPoolLocation() string
}

// assert that DefaultAKSMachineProvider implements Provider interface
var _ AKSMachineProvider = (*DefaultAKSMachineProvider)(nil)

type DefaultAKSMachineProvider struct {
	azClient                   *azclient.AZClient
	instanceTypeProvider       instancetype.Provider
	allocationStrategyProvider allocationstrategy.Provider
	imageResolver              imagefamily.Resolver
	subscriptionID             string
	clusterResourceGroup       string
	clusterName                string
	aksMachinesPoolName        string // Only support one AKS machine pool at a time, for now.
	aksMachinesPoolLocation    string
	batchCreationEnabled       bool
	provisioningErrorHandling  *offerings.ErrorDetailHandler
	beginCreateErrorHandling   *offerings.AKSMachineBeginCreateErrorHandler
	deletingMachines           sets.Set[string] // tracks in-flight delete operations by machine name
	deletingMachinesMu         sync.RWMutex
	machineCache               *machinecache.MachineCache
}

func NewAKSMachineProvider(
	azClient *azclient.AZClient,
	instanceTypeProvider instancetype.Provider,
	allocationStrategyProvider allocationstrategy.Provider,
	imageResolver imagefamily.Resolver,
	offeringsCache *cache.UnavailableOfferings,
	subscriptionID string,
	clusterResourceGroup string,
	clusterName string,
	aksMachinesPoolName string,
	aksMachinesPoolLocation string,
	batchCreationEnabled bool,
	machineCache *machinecache.MachineCache,
) *DefaultAKSMachineProvider {
	provider := &DefaultAKSMachineProvider{
		azClient:                   azClient,
		instanceTypeProvider:       instanceTypeProvider,
		allocationStrategyProvider: allocationStrategyProvider,
		imageResolver:              imageResolver,
		subscriptionID:             subscriptionID,
		clusterResourceGroup:       clusterResourceGroup,
		clusterName:                clusterName,
		aksMachinesPoolName:        aksMachinesPoolName,
		aksMachinesPoolLocation:    aksMachinesPoolLocation,
		batchCreationEnabled:       batchCreationEnabled,
		provisioningErrorHandling:  offerings.NewErrorDetailHandler(offeringsCache),
		beginCreateErrorHandling:   offerings.NewAKSMachineBeginCreateErrorHandler(offeringsCache),
		deletingMachines:           sets.New[string](),
		machineCache:               machineCache,
	}

	return provider
}

// BeginCreate creates an instance given the constraints.
// Note that the returned instance may not be finished provisioning yet.
// Errors that occur on the "sync side" of the VM create, such as BadRequest due to invalid user input, and similar, will have the error returned here.
// Errors that occur on the "async side" of the VM create (after the request is accepted) will be returned from AKSMachinePromise.Wait().
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

	aksMachinePromise, err := p.beginCreateMachine(ctx, nodeClass, nodeClaim, instanceTypes, aksMachineName)
	if err != nil {
		// Clean up if creation fails.
		if err := p.deleteMachine(ctx, aksMachineName); err != nil {
			if !machineUtils.IsAKSMachineOrMachinesPoolNotFound(err) {
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

	if logger := log.FromContext(ctx).V(1); logger.Enabled() {
		logger.Info("updating AKS machine", "aksMachineName", aksMachineName, "aksMachine", BuildJSONFromAKSMachine(&aksMachine))
	}
	poller, err := p.azClient.AKSMachinesClient().BeginCreateOrUpdate(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachineName, aksMachine, options)
	if err != nil {
		if machineUtils.IsAKSMachineOrMachinesPoolNotFound(err) {
			// Can only be AKS machines pool not found.
			// Suggestion: separate the util function to not cover more than needed?
			return corecloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("failed to begin update AKS machine %q: %w", aksMachineName, err))
		}
		return fmt.Errorf("failed to begin update AKS machine %q: %w", aksMachineName, err)
	}
	_, err = poller.PollUntilDone(ctx, defaultPollerOptions())
	if err != nil {
		return fmt.Errorf("failed to update AKS machine %q during LRO: %w", aksMachineName, err)
	}
	p.machineCache.Invalidate(aksMachineName)
	log.FromContext(ctx).V(1).Info("successfully updated AKS machine", "aksMachineName", aksMachineName)
	return nil
}

// ASSUMPTION: the AKS machine will be in the current p.aksMachinesPoolName. Otherwise need rework to pass the pool name in.
func (p *DefaultAKSMachineProvider) Get(ctx context.Context, aksMachineName string, opts ...Option) (*armcontainerservice.Machine, error) {
	if !shouldAKSMachinesBeVisible(ctx) {
		return nil, corecloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("existing AKS machines management is disabled, and provision mode is not AKS machine"))
	}

	if p.aksMachinesPoolName == "" {
		// Possible when this option field is not populated, which is not required when PROVISION_MODE is not aksmachineapi.
		// But an AKS machine instance exists, whether added manually or from before switching PROVISION_MODE.
		// So, we respond similarly to if AKS machines pool is not found.
		return nil, corecloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("failed to get AKS machine, AKS machines pool name is empty"))
	}

	options := opt{}
	for _, opt := range opts {
		opt(&options)
	}

	aksMachine, err := p.machineCache.GetWithFallback(ctx, aksMachineName, options.useCache)
	if err != nil {
		if machineUtils.IsAKSMachineOrMachinesPoolNotFound(err) {
			return nil, corecloudprovider.NewNodeClaimNotFoundError(err)
		}
		return nil, err
	}

	return aksMachine, nil
}

func (p *DefaultAKSMachineProvider) List(ctx context.Context, opts ...Option) ([]*armcontainerservice.Machine, error) {
	if !shouldAKSMachinesBeVisible(ctx) {
		return []*armcontainerservice.Machine{}, nil
	}

	if p.aksMachinesPoolName == "" {
		// Possible when this option field is not populated, which is not required when PROVISION_MODE is not aksmachineapi.
		// So, we respond similarly to if AKS machines pool is not found.
		return []*armcontainerservice.Machine{}, nil
	}

	options := opt{}
	for _, opt := range opts {
		opt(&options)
	}

	aksMachines, err := p.machineCache.ListWithFallback(ctx, options.useCache)
	if err != nil {
		return nil, err
	}

	return aksMachines, nil
}

func (p *DefaultAKSMachineProvider) Delete(ctx context.Context, aksMachineName string) error {
	if !shouldAKSMachinesBeVisible(ctx) {
		return corecloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("existing AKS machines management is disabled, and provision mode is not AKS machine"))
	}

	// If there's already an in-flight delete for this machine, return immediately.
	p.deletingMachinesMu.RLock()
	deleting := p.deletingMachines.Has(aksMachineName)
	p.deletingMachinesMu.RUnlock()
	if deleting {
		return nil
	}

	// Note that 'Get' also satisfies cloudprovider.Delete contract expectation (from v1.3.0)
	// of returning cloudprovider.NewNodeClaimNotFoundError if the instance is already deleted
	// This get exists to deal with the case where the operator restarted during the course of a deletion.
	// With it, we may do an extra unneeded get before delete, but without it we may erroneously issue
	// 2 deletes if the instance was being deleted and the operator restarted.
	// Since get quota is generally higher, we prefer to check w/ get rather than issue 2 deletes.
	aksMachine, err := p.Get(ctx, aksMachineName)
	if err != nil {
		return err
	}
	if isAKSMachineDeleting(aksMachine) {
		return nil
	}

	err = p.deleteMachine(ctx, aksMachineName)
	if err != nil {
		if machineUtils.IsAKSMachineOrMachinesPoolNotFound(err) {
			return corecloudprovider.NewNodeClaimNotFoundError(err)
		}
		return err
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

	p.deletingMachinesMu.Lock()
	p.deletingMachines.Insert(aksMachineName)
	p.deletingMachinesMu.Unlock()
	defer func() {
		p.deletingMachinesMu.Lock()
		p.deletingMachines.Delete(aksMachineName)
		p.deletingMachinesMu.Unlock()
	}()

	log.FromContext(ctx).V(1).Info("starting to delete AKS machine", "aksMachineName", aksMachineName)
	poller, err := p.azClient.AgentPoolsClient().BeginDeleteMachines(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachines, nil)
	if err != nil {
		return fmt.Errorf("failed to begin delete AKS machine %q: %w", aksMachineName, err)
	}

	_, err = poller.PollUntilDone(ctx, defaultPollerOptions())

	if err != nil {
		return fmt.Errorf("failed to delete AKS machine %q during LRO: %w", aksMachineName, err)
	}

	p.machineCache.Invalidate(aksMachineName)
	log.FromContext(ctx).V(1).Info("successfully deleted AKS machine", "aksMachineName", aksMachineName)
	return nil
}

// beginCreateMachine starts the creation of an AKS machine instance.
// The returned AKSMachinePromise must be called to gather any errors
// that are retrieved during async provisioning, as well as to complete the provisioning process.
//

func (p *DefaultAKSMachineProvider) beginCreateMachine(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*corecloudprovider.InstanceType,
	aksMachineName string,
) (*AKSMachinePromise, error) {
	// Reuse the existing AKS machine if it exists, and skip the creation.
	//
	// This repeated creation is possible in a race condition where we successfully launched the AKS machine
	// but crashed/restarted before we could set the NodeClaim's Launched status. In that case, the NodeClaim
	// will be re-queued for creation, but the AKS machine already exists.
	//
	// We assume that if an AKS machine exists, we successfully created it with the right parameters from the
	// NodeClaim during a previous run.
	// However, offerings properties (e.g., instanceType, capacityType, zone) are decided below and not deterministic,
	// thus, they may differ in the new attempts.
	//
	// If we attempted to recreate with different properties, the API would reject the request due to property
	// conflicts, blocking the NodeClaim until liveness TTL is hit. This guard will just reuse the existing AKS machine,
	// potentially with original offerings properties, which is acceptable, as it just complete the original intention.
	existingAKSMachine, err := p.machineCache.GetWithFallback(ctx, aksMachineName, true)
	if err == nil {
		// Existing AKS machine found, reuse it.
		return p.reuseExistingMachine(ctx, aksMachineName, nodeClaim, instanceTypes, existingAKSMachine)
	} else if !machineUtils.IsAKSMachineOrMachinesPoolNotFound(err) {
		// Not fatal. Will fall back to normal creation.
		log.FromContext(ctx).Error(err, "failed to check for existing AKS machine", "aksMachineName", aksMachineName)
	}

	// Decide on offerings
	instanceOfferings := p.allocationStrategyProvider.FilterInstanceOfferings(
		ctx,
		allocationstrategy.NewInstanceOfferings(instanceTypes),
		scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...),
	)
	instanceType, capacityType, zone := offerings.PickSkuSizePriorityAndZone(ctx, instanceOfferings)
	if instanceType == nil {
		return nil, corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("no instance types available"))
	}

	// Build the AKS machine template
	aksMachineTemplate, err := p.buildAKSMachineTemplate(ctx, instanceType, capacityType, zone, nodeClass, nodeClaim)
	if err != nil {
		return nil, fmt.Errorf("failed to build AKS machine template from template: %w", err)
	}

	// Call the AKS machine API with the template to create the AKS machine instance
	if logger := log.FromContext(ctx).V(1); logger.Enabled() {
		logger.Info("creating AKS machine", "aksMachineName", aksMachineName, "instance-type", instanceType.Name, "aksMachine", BuildJSONFromAKSMachine(aksMachineTemplate))
	}

	// Branch between batch and non-batch creation paths.
	if p.batchCreationEnabled {
		return p.beginCreateMachineBatch(ctx, aksMachineTemplate, aksMachineName, instanceType, capacityType, zone)
	}
	return p.beginCreateMachineNonBatch(ctx, aksMachineTemplate, aksMachineName, instanceType, capacityType, zone)
}

// beginCreateMachineBatch handles the batch creation path using the AKS machines header batch API and GET-based poller.
func (p *DefaultAKSMachineProvider) beginCreateMachineBatch(
	ctx context.Context,
	aksMachineTemplate *armcontainerservice.Machine,
	aksMachineName string,
	instanceType *corecloudprovider.InstanceType,
	capacityType string,
	zone string,
) (*AKSMachinePromise, error) {
	handlableError, err := p.azClient.AKSMachinesBatchClient().BeginCreateWithBatch(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachineName, aksMachineTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to begin create AKS machine %q, unhandled error: %w", aksMachineName, err)
	}
	if handlableError != nil {
		return nil, p.handleMachineBeginCreateError(ctx, aksMachineName, instanceType, zone, capacityType, handlableError)
	}

	// Get once after begin create to retrieve VMResourceID.
	// In fact, the AKS machine object we want here is already returned with the PUT request above. However, the SDK have prevented us from accessing it easily.
	// TODO: find a way to access that instead of making another GET call like this.
	gotAKSMachine, err := p.getCreatedMachineAndHandleEarlyProvisioningError(ctx, aksMachineName, instanceType, zone, capacityType)
	if err != nil {
		return nil, err
	}

	// Return LRO
	return NewAKSMachinePromise(
		p,
		aksMachineTemplate,
		func() (pollingErr error) {
			defer func() {
				if r := recover(); r != nil {
					err := fmt.Errorf("%v", r)
					pollingErr = fmt.Errorf("failed to create AKS machine %q during LRO, AKS API panicked: %w", aksMachineName, err)
				}
			}()

			provisioningErr, pollerErr := p.machineCache.PollUntilDone(ctx, aksMachineName)
			if pollerErr != nil {
				pollingErr = fmt.Errorf("failed to create AKS machine %q during LRO (GET poller), poller error: %w", aksMachineName, pollerErr)
				return
			}
			if provisioningErr != nil {
				pollingErr = p.handleMachineProvisioningError(ctx, "LRO (GET poller)", aksMachineName, instanceType, zone, capacityType, provisioningErr)
				return
			}
			log.FromContext(ctx).V(1).Info("successfully created AKS machine",
				"aksMachineName", aksMachineName,
				"aksMachineID", gotAKSMachine.ID)
			return
		},
		aksMachineName,
		instanceType,
		capacityType,
		zone,
		lo.FromPtr(gotAKSMachine.ID),
		lo.FromPtr(gotAKSMachine.Properties.NodeImageVersion),
		lo.FromPtr(gotAKSMachine.Properties.ResourceID),
		lo.FromPtr(gotAKSMachine.Properties.Status.CreationTimestamp),
	), nil
}

// beginCreateMachineNonBatch handles the non-batch creation path using the standard AKS machines API and SDK poller.
func (p *DefaultAKSMachineProvider) beginCreateMachineNonBatch(
	ctx context.Context,
	aksMachineTemplate *armcontainerservice.Machine,
	aksMachineName string,
	instanceType *corecloudprovider.InstanceType,
	capacityType string,
	zone string,
) (*AKSMachinePromise, error) {
	poller, err := p.azClient.AKSMachinesClient().BeginCreateOrUpdate(ctx, p.clusterResourceGroup, p.clusterName, p.aksMachinesPoolName, aksMachineName, *aksMachineTemplate, nil)
	if err != nil {
		he := offerings.ErrorToHandlableError(err)
		if he != nil {
			return nil, p.handleMachineBeginCreateError(ctx, aksMachineName, instanceType, zone, capacityType, he)
		}
		return nil, fmt.Errorf("failed to begin create AKS machine %q, unhandled error: %w", aksMachineName, err)
	}

	// Get once after begin create to retrieve VMResourceID.
	// In fact, the AKS machine object we want here is already returned with the PUT request above. However, the SDK have prevented us from accessing it easily.
	// TODO: find a way to access that instead of making another GET call like this.
	gotAKSMachine, err := p.getCreatedMachineAndHandleEarlyProvisioningError(ctx, aksMachineName, instanceType, zone, capacityType)
	if err != nil {
		return nil, err
	}

	// Return LRO
	return NewAKSMachinePromise(
		p,
		aksMachineTemplate,
		func() (pollingErr error) {
			defer func() {
				if r := recover(); r != nil {
					err := fmt.Errorf("%v", r)
					pollingErr = fmt.Errorf("failed to create AKS machine %q during LRO, AKS API panicked: %w", aksMachineName, err)
				}
			}()
			// Use SDK poller (non-batch case)
			_, err := poller.PollUntilDone(ctx, defaultPollerOptions()) // This may panic if it is deleted mid-way.
			if err != nil {
				// Could be quota error; will be handled with custom logic below

				// Get once after begin create to retrieve error details. This is because if the poller returns error, the sdk doesn't let us look at the real results.
				failedAKSMachine, _ := p.machineCache.GetWithFallback(ctx, aksMachineName, false)
				if failedAKSMachine.Properties != nil && failedAKSMachine.Properties.Status != nil && failedAKSMachine.Properties.Status.ProvisioningError != nil {
					pollingErr = p.handleMachineProvisioningError(ctx, "LRO", aksMachineName, instanceType, zone, capacityType, failedAKSMachine.Properties.Status.ProvisioningError)
					return
				}
				// This should not be expected.
				pollingErr = fmt.Errorf("failed to create AKS machine %q during LRO, AKS API returned error: %w", aksMachineName, err)
				return
			}

			log.FromContext(ctx).V(1).Info("successfully created AKS machine",
				"aksMachineName", aksMachineName,
				"aksMachineID", gotAKSMachine.ID)
			return
		},
		aksMachineName,
		instanceType,
		capacityType,
		zone,
		lo.FromPtr(gotAKSMachine.ID),
		lo.FromPtr(gotAKSMachine.Properties.NodeImageVersion),
		lo.FromPtr(gotAKSMachine.Properties.ResourceID),
		lo.FromPtr(gotAKSMachine.Properties.Status.CreationTimestamp),
	), nil
}

// For use in beginCreateMachine only. Otherwise need to rework parameters, do nil check better, and generalize error messaging.
func (p *DefaultAKSMachineProvider) handleMachineProvisioningError(ctx context.Context, phase string, aksMachineName string, instanceType *corecloudprovider.InstanceType, zone string, capacityType string, provisioningError *armcontainerservice.ErrorDetail) error {
	if provisioningError == nil {
		return fmt.Errorf("failed to create AKS machine %q during %s, unhandled provisioning error: nil", aksMachineName, phase)
	}

	var innerError armcontainerservice.ErrorDetail
	if len(provisioningError.Details) > 0 && provisioningError.Details[0] != nil {
		// This should be VM creation error.
		// ASSUMPTION: the length of details is always <= 1. And VM creation error Karpenter may expect is always at Details[0].
		// Suggestion: suggest API change to have an explicit VM create error, if not changing Karpenter to rely on AKS machine ProvisioningError instead?
		innerError = *provisioningError.Details[0]
	} else {
		// Fallback to AKS machine API-level error. Though, this is unlikely to be handled by Karpenter.
		innerError = *provisioningError
	}

	sku, skuErr := p.instanceTypeProvider.Get(ctx, instanceType.Name)
	if skuErr != nil {
		return fmt.Errorf("failed to get instance type %q: %w, provisioning error left unhandled: code=%s, message=%s", instanceType.Name, skuErr, lo.FromPtr(innerError.Code), lo.FromPtr(innerError.Message))
	}

	err := p.provisioningErrorHandling.Handle(ctx, sku, instanceType, zone, capacityType, innerError)
	if err != nil {
		// If error is handled, return it (wrapped)
		return fmt.Errorf("failed to create AKS machine %q during %s, handled provisioning error: %w", aksMachineName, phase, err)
	}

	return fmt.Errorf("failed to create AKS machine %q during %s, unhandled provisioning error: code=%s, message=%s", aksMachineName, phase, lo.FromPtr(innerError.Code), lo.FromPtr(innerError.Message))
}

func (p *DefaultAKSMachineProvider) handleMachineBeginCreateError(ctx context.Context, aksMachineName string, instanceType *corecloudprovider.InstanceType, zone string, capacityType string, he *offerings.HandlableError) error {
	sku, skuErr := p.instanceTypeProvider.Get(ctx, instanceType.Name)
	if skuErr != nil {
		return fmt.Errorf("failed to get instance type %q: %w, begin create error left unhandled: %w", instanceType.Name, skuErr, he)
	}

	err := p.beginCreateErrorHandling.Handle(ctx, sku, instanceType, zone, capacityType, he)
	if err != nil {
		return fmt.Errorf("failed to begin create AKS machine %q, handled error: %w", aksMachineName, err)
	}

	return fmt.Errorf("failed to begin create AKS machine %q, unhandled error: %w", aksMachineName, he)
}

func (p *DefaultAKSMachineProvider) reuseExistingMachine(ctx context.Context, aksMachineName string, nodeClaim *karpv1.NodeClaim, instanceTypes []*corecloudprovider.InstanceType, existingAKSMachine *armcontainerservice.Machine) (*AKSMachinePromise, error) {
	// Reconstruct properties from existing AKS machine instance.
	if err := validateRetrievedAKSMachineBasicProperties(existingAKSMachine); err != nil {
		return nil, fmt.Errorf("found existing AKS machine %s, but %w", aksMachineName, err)
	}
	if existingAKSMachine.Properties.Tags == nil || existingAKSMachine.Properties.Tags[launchtemplate.KarpenterAKSMachineNodeClaimTagKey] == nil {
		// This is not included in validateRetrievedAKSMachineBasicProperties as inplaceupdate can repair it.
		// Although, we don't want to reuse a machine until that happens.
		return nil, fmt.Errorf("found existing AKS machine %s, but %w", aksMachineName, fmt.Errorf("irretrievable karpenter.azure.com_aksmachine_nodeclaim tag"))
	}

	existingAKSMachineVMSize := lo.FromPtr(existingAKSMachine.Properties.Hardware.VMSize)
	existingAKSMachinePriority := lo.FromPtr(existingAKSMachine.Properties.Priority)
	existingAKSMachineVMResourceID := lo.FromPtr(existingAKSMachine.Properties.ResourceID)
	existingAKSMachineID := lo.FromPtr(existingAKSMachine.ID)
	existingAKSMachineNodeImageVersion := lo.FromPtr(existingAKSMachine.Properties.NodeImageVersion)
	existingAKSMachineNodeClaimName := lo.FromPtr(existingAKSMachine.Properties.Tags[launchtemplate.KarpenterAKSMachineNodeClaimTagKey])
	existingAKSMachineCreationTimestamp := lo.FromPtr(existingAKSMachine.Properties.Status.CreationTimestamp)

	instanceType := offerings.GetInstanceTypeFromVMSize(existingAKSMachineVMSize, instanceTypes)
	capacityType := getCapacityTypeFromAKSScaleSetPriority(existingAKSMachinePriority)
	zone, err := zones.MakeAKSLabelZoneFromARMZones(p.aksMachinesPoolLocation, existingAKSMachine.Zones)
	if err != nil {
		return nil, fmt.Errorf("found existing AKS machine %s, but failed to determine zone: %w", aksMachineName, err)
	}

	if existingAKSMachineNodeClaimName != nodeClaim.Name {
		// Might be possible from NodePool name hash collision within AKS machine name
		// See how AKS machine name is generated for more details.
		// ASSUMPTION: repeated failure will eventually result in NodeClaim reaching registration TTL, then gets re-created with the new hash, recovering from the collision.
		return nil, fmt.Errorf("found existing AKS machine %s, but its karpenter.azure.com_aksmachine_nodeclaim tag %q does not match the NodeClaim to create %q", aksMachineName, existingAKSMachineNodeClaimName, nodeClaim.Name)
	}
	if existingAKSMachine.Properties.ProvisioningState != nil && lo.FromPtr(existingAKSMachine.Properties.ProvisioningState) == consts.ProvisioningStateFailed {
		// Unfortunately, that was more like a remain than a usable aksMachine.
		// ASSUMPTION: this is irrecoverable (i.e., polling would have failed).
		if existingAKSMachine.Properties.Status == nil || existingAKSMachine.Properties.Status.ProvisioningError == nil {
			return nil, fmt.Errorf("found existing AKS machine %s, but it is in Failed state and ProvisioningError is nil", aksMachineName)
		}
		return nil, p.handleMachineProvisioningError(ctx, "reusing existing AKS machine", aksMachineName, instanceType, zone, capacityType, existingAKSMachine.Properties.Status.ProvisioningError)
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
		existingAKSMachineCreationTimestamp,
	), nil
}

func (p *DefaultAKSMachineProvider) getCreatedMachineAndHandleEarlyProvisioningError(ctx context.Context, aksMachineName string, instanceType *corecloudprovider.InstanceType, zone string, capacityType string) (*armcontainerservice.Machine, error) {
	gotAKSMachine, err := p.machineCache.GetWithFallback(ctx, aksMachineName, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get AKS machine %q once after begin creation: %w", aksMachineName, err)
	}
	if err := validateRetrievedAKSMachineBasicProperties(gotAKSMachine); err != nil {
		return nil, fmt.Errorf("failed to get AKS machine %q once after begin creation: %w", aksMachineName, err)
	}
	if lo.FromPtr(gotAKSMachine.Properties.ProvisioningState) == consts.ProvisioningStateFailed {
		// We luckily catch failed state early (compared to during polling).
		// ASSUMPTION: this is irrecoverable (i.e., polling would have failed).
		if gotAKSMachine.Properties.Status == nil || gotAKSMachine.Properties.Status.ProvisioningError == nil {
			return nil, fmt.Errorf("failed to get AKS machine %q once after begin creation: AKS machine is in Failed state but ProvisioningError is nil", aksMachineName)
		}
		return nil, p.handleMachineProvisioningError(ctx, "get once after begin creation", aksMachineName, instanceType, zone, capacityType, gotAKSMachine.Properties.Status.ProvisioningError)
	}
	return gotAKSMachine, nil
}
