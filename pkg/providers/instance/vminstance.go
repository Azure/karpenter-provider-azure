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
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/logging"
	metrics "github.com/Azure/karpenter-provider-azure/pkg/metrics"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/networksecuritygroup"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

type Resource = map[string]interface{}

type VirtualMachinePromise struct {
	VM       *armcompute.VirtualMachine
	WaitFunc func() error

	providerRef VMProvider
}

func (p *VirtualMachinePromise) Cleanup(ctx context.Context) error {
	// This won't clean up leaked NICs if the VM doesn't exist... intentional?
	// From Delete(): "Leftover network interfaces (if any) will be cleaned by by GC controller"
	// Still, we could try to DeleteNic()?
	return p.providerRef.Delete(ctx, lo.FromPtr(p.VM.Name))
}

func (p *VirtualMachinePromise) Wait() error {
	return p.WaitFunc()
}
func (p *VirtualMachinePromise) GetInstanceName() string {
	return lo.FromPtr(p.VM.Name)
}

type VMProvider interface {
	BeginCreate(context.Context, *v1beta1.AKSNodeClass, *karpv1.NodeClaim, []*corecloudprovider.InstanceType) (*VirtualMachinePromise, error)
	Get(context.Context, string) (*armcompute.VirtualMachine, error)
	List(context.Context) ([]*armcompute.VirtualMachine, error)
	Delete(context.Context, string) error
	Update(context.Context, string, armcompute.VirtualMachineUpdate) error
	GetNic(context.Context, string, string) (*armnetwork.Interface, error)
	DeleteNic(context.Context, string) error
	ListNics(context.Context) ([]*armnetwork.Interface, error)
}

// assert that DefaultProvider implements Provider interface
var _ VMProvider = (*DefaultVMProvider)(nil)

type DefaultVMProvider struct {
	location                     string
	azClient                     *azclient.AZClient
	azClientManager              *azclient.AZClientManager // nil in non-azurevm modes
	instanceTypeProvider         instancetype.Provider
	imageResolver                imagefamily.Resolver
	loadBalancerProvider         *loadbalancer.Provider
	networkSecurityGroupProvider *networksecuritygroup.Provider
	resourceGroup                string
	subscriptionID               string
	provisionMode                string
	diskEncryptionSetID          string
	errorHandling                *offerings.ResponseErrorHandler
	env                          *auth.Environment

	// Fields previously on launchtemplate.Provider, now inlined
	caBundle             *string
	clusterEndpoint      string
	tenantID             string
	clusterResourceGroup string

	vmListQuery, nicListQuery string
	deletingVMs               sets.Set[string] // tracks in-flight delete operations by VM name
	deletingVMsMu             sync.RWMutex
}

func NewDefaultVMProvider(
	azClient *azclient.AZClient,
	instanceTypeProvider instancetype.Provider,
	imageResolver imagefamily.Resolver,
	loadBalancerProvider *loadbalancer.Provider,
	networkSecurityGroupProvider *networksecuritygroup.Provider,
	offeringsCache *cache.UnavailableOfferings,
	location string,
	resourceGroup string,
	subscriptionID string,
	provisionMode string,
	diskEncryptionSetID string,
	env *auth.Environment,
	caBundle *string,
	clusterEndpoint string,
	tenantID string,
	clusterResourceGroup string,
) *DefaultVMProvider {
	return &DefaultVMProvider{
		azClient:                     azClient,
		instanceTypeProvider:         instanceTypeProvider,
		imageResolver:                imageResolver,
		loadBalancerProvider:         loadBalancerProvider,
		networkSecurityGroupProvider: networkSecurityGroupProvider,
		location:                     location,
		resourceGroup:                resourceGroup,
		subscriptionID:               subscriptionID,
		provisionMode:                provisionMode,
		diskEncryptionSetID:          diskEncryptionSetID,
		env:                          env,
		caBundle:                     caBundle,
		clusterEndpoint:              clusterEndpoint,
		tenantID:                     tenantID,
		clusterResourceGroup:         clusterResourceGroup,

		vmListQuery:  GetVMListQueryBuilder(resourceGroup).String(),
		nicListQuery: GetNICListQueryBuilder(resourceGroup).String(),

		errorHandling: offerings.NewResponseErrorHandler(offeringsCache),
		deletingVMs:   sets.New[string](),
	}
}

// SetAZClientManager sets the multi-subscription client manager for azurevm mode.
// When set, the VM provider will use per-subscription Azure clients based on
// the AzureNodeClass's subscriptionID field.
func (p *DefaultVMProvider) SetAZClientManager(mgr *azclient.AZClientManager) {
	p.azClientManager = mgr
}

// resolveEffectiveClients returns the Azure SDK clients, resource group, and location
// to use for a given NodeClass. In azurevm mode with per-NodeClass overrides, these
// may differ from the provider defaults.
func (p *DefaultVMProvider) resolveEffectiveClients(nodeClass *v1beta1.AKSNodeClass) (vmClient azclient.VirtualMachinesAPI, nicClient azclient.NetworkInterfacesAPI, rg string, location string, subID string, err error) {
	rg = p.resourceGroup
	location = p.location
	subID = p.subscriptionID

	if nodeClass.Spec.ResourceGroup != nil && *nodeClass.Spec.ResourceGroup != "" {
		rg = *nodeClass.Spec.ResourceGroup
	}
	if nodeClass.Spec.Location != nil && *nodeClass.Spec.Location != "" {
		location = *nodeClass.Spec.Location
	}
	if nodeClass.Spec.SubscriptionID != nil && *nodeClass.Spec.SubscriptionID != "" {
		subID = *nodeClass.Spec.SubscriptionID
	}

	// If we have a client manager and the subscription differs, use per-subscription clients
	if p.azClientManager != nil && subID != p.subscriptionID {
		clients, err := p.azClientManager.GetClients(subID)
		if err != nil {
			return nil, nil, "", "", "", err
		}
		return clients.VirtualMachinesClient, clients.NetworkInterfacesClient, rg, location, subID, nil
	}

	return p.azClient.VirtualMachinesClient(), p.azClient.NetworkInterfacesClient(), rg, location, subID, nil
}

// BeginCreate creates an instance given the constraints.
// instanceTypes should be sorted by priority for spot capacity type.
// Note that the returned instance may not be finished provisioning yet.
// Errors that occur on the "sync side" of the VM create, such as quota/capacity, BadRequest due to invalid user input, and similar, will have the error returned here.
// Errors that occur on the "async side" of the VM create (after the request is accepted) will be returned from VirtualMachinePromise.Wait().
func (p *DefaultVMProvider) BeginCreate(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*corecloudprovider.InstanceType,
) (*VirtualMachinePromise, error) {
	instanceTypes = offerings.OrderInstanceTypesByPrice(instanceTypes, scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...))
	vmPromise, err := p.beginLaunchInstance(ctx, nodeClass, nodeClaim, instanceTypes)
	if err != nil {
		// There may be orphan NICs (created before promise started)
		// This err block is hit only for sync failures. Async (VM provisioning) failures will be returned by the vmPromise.Wait() function
		if cleanupErr := p.cleanupAzureResources(ctx, GenerateResourceName(nodeClaim.Name), true); cleanupErr != nil {
			log.FromContext(ctx).Error(cleanupErr, "failed to cleanup resources for node claim", "NodeClaim", nodeClaim.Name)
		}
		return nil, err
	}
	vm := vmPromise.VM
	zone, err := utils.MakeAKSLabelZoneFromVM(vm)
	if err != nil {
		log.FromContext(ctx).V(1).Info("failed to get zone for VM", "vmName", *vm.Name, "error", err)
	}

	log.FromContext(ctx).Info("launched new instance",
		"launchedInstance", *vm.ID,
		"hostname", *vm.Name,
		"type", string(*vm.Properties.HardwareProfile.VMSize),
		"zone", zone,
		"capacity-type", GetCapacityTypeFromVM(vm))

	return vmPromise, nil
}

// Update updates the VM with the given updates. If Tags are specified, the tags are also updated on the associated network interface and VM extensions.
// Note that this means that this method can fail if the extensions have not been created yet. It is expected that the caller handles this and retries the update
// to propagate the tags to the extensions once they're created.
func (p *DefaultVMProvider) Update(ctx context.Context, vmName string, update armcompute.VirtualMachineUpdate) error {
	if update.Tags != nil {
		// If there are tags for other resources, do those first. This is a hedge to avoid updating the VM first which may cause us to think subsequent updates aren't needed
		// because the VM already has the updates

		// Update NIC tags
		_, err := p.azClient.NetworkInterfacesClient().UpdateTags(
			ctx,
			p.resourceGroup,
			vmName, // NIC is named the same as the VM
			armnetwork.TagsObject{
				Tags: update.Tags,
			},
			nil,
		)
		if err != nil {
			return fmt.Errorf("updating NIC tags for %q: %w", vmName, err)
		}

		extensionNames := GetManagedExtensionNames(p.provisionMode, p.env)
		pollers := make(map[string]*runtime.Poller[armcompute.VirtualMachineExtensionsClientUpdateResponse], len(extensionNames))
		// Update tags on VM extensions
		for _, extName := range extensionNames {
			poller, err := p.azClient.VirtualMachineExtensionsClient().BeginUpdate(
				ctx,
				p.resourceGroup,
				vmName,
				extName,
				armcompute.VirtualMachineExtensionUpdate{
					Tags: update.Tags,
				},
				nil,
			)
			if err != nil {
				// TODO: This is a bit of a hack based on how this Update function is currently used.
				// Currently this function will not be called by any callers until a claim has been Registered, which means that the CSE had to have succeeded.
				// The aksIdentifyingExtensionName is not currently guaranteed to be on the VM though, as Karpenter could have failed over during the initial VM create
				// after CSE but before aksIdentifyingExtensionName. So, for now, we just ignore NotFound errors for the aksIdentifyingExtensionName.
				azErr := sdkerrors.IsResponseError(err)
				if extName == aksIdentifyingExtensionName && azErr != nil && azErr.StatusCode == http.StatusNotFound {
					log.FromContext(ctx).V(0).Info("extension not found when updating tags", "extensionName", extName, "vmName", vmName)
					continue
				}
				return fmt.Errorf("updating VM extension %q for VM %q: %w", extName, vmName, err)
			}
			pollers[extName] = poller
		}

		for extName, poller := range pollers {
			// Poll more frequently than the default of 30s
			opts := &runtime.PollUntilDoneOptions{
				Frequency: 3 * time.Second,
			}
			_, err := poller.PollUntilDone(ctx, opts)
			if err != nil {
				return fmt.Errorf("polling VM extension %q for VM %q: %w", extName, vmName, err)
			}
		}
	}

	err := UpdateVirtualMachine(ctx, p.azClient.VirtualMachinesClient(), p.resourceGroup, vmName, update)
	if err != nil {
		return err
	}

	return nil
}

func (p *DefaultVMProvider) Get(ctx context.Context, vmName string) (*armcompute.VirtualMachine, error) {
	var vm armcompute.VirtualMachinesClientGetResponse
	var err error

	if vm, err = p.azClient.VirtualMachinesClient().Get(ctx, p.resourceGroup, vmName, nil); err != nil {
		if sdkerrors.IsNotFoundErr(err) {
			return nil, corecloudprovider.NewNodeClaimNotFoundError(err)
		}
		return nil, fmt.Errorf("failed to get VM instance, %w", err)
	}

	return &vm.VirtualMachine, nil
}

func (p *DefaultVMProvider) List(ctx context.Context) ([]*armcompute.VirtualMachine, error) {
	req := NewQueryRequest(&(p.subscriptionID), p.vmListQuery)
	client := p.azClient.AzureResourceGraphClient()
	data, err := GetResourceData(ctx, client, *req)
	if err != nil {
		return nil, fmt.Errorf("querying azure resource graph, %w", err)
	}
	var vmList []*armcompute.VirtualMachine
	for i := range data {
		vm, err := createVMFromQueryResponseData(data[i])
		if err != nil {
			return nil, fmt.Errorf("creating VM object from query response data, %w", err)
		}
		vmList = append(vmList, vm)
	}
	return vmList, nil
}

func (p *DefaultVMProvider) Delete(ctx context.Context, resourceName string) error {
	// If there's already an in-flight delete for this VM, return immediately.
	p.deletingVMsMu.RLock()
	deleting := p.deletingVMs.Has(resourceName)
	p.deletingVMsMu.RUnlock()
	if deleting {
		return nil
	}

	// Note that 'Get' also satisfies cloudprovider.Delete contract expectation (from v1.3.0)
	// of returning cloudprovider.NewNodeClaimNotFoundError if the instance is already deleted.
	// This get exists to deal with the case where the operator restarted during the course of a deletion.
	// With it, we may do an extra unneeded get before delete, but without it we may erroneously issue
	// 2 deletes if the instance was being deleted and the operator restarted.
	// Since get quota is generally higher, we prefer to check w/ get rather than issue 2 deletes.
	vm, err := p.Get(ctx, resourceName)
	if err != nil {
		return err
	}
	// Check if the instance is already shutting down to reduce the number of API calls.
	// Leftover network interfaces (if any) will be cleaned by by GC controller.
	if utils.IsVMDeleting(*vm) {
		return nil
	}

	log.FromContext(ctx).V(1).Info("deleting virtual machine and associated resources", "vmName", resourceName)
	return p.cleanupAzureResources(ctx, resourceName, false)
}

func (p *DefaultVMProvider) GetNic(ctx context.Context, rg, nicName string) (*armnetwork.Interface, error) {
	nicResponse, err := p.azClient.NetworkInterfacesClient().Get(ctx, rg, nicName, nil)
	if err != nil {
		return nil, err
	}
	return &nicResponse.Interface, nil
}

// ListNics returns all network interfaces in the resource group that have the nodepool tag
func (p *DefaultVMProvider) ListNics(ctx context.Context) ([]*armnetwork.Interface, error) {
	req := NewQueryRequest(&(p.subscriptionID), p.nicListQuery)
	client := p.azClient.AzureResourceGraphClient()
	data, err := GetResourceData(ctx, client, *req)
	if err != nil {
		return nil, fmt.Errorf("querying azure resource graph, %w", err)
	}
	var nicList []*armnetwork.Interface
	for i := range data {
		nic, err := createNICFromQueryResponseData(data[i])
		if err != nil {
			return nil, fmt.Errorf("creating NIC object from query response data, %w", err)
		}
		nicList = append(nicList, nic)
	}
	return nicList, nil
}

func (p *DefaultVMProvider) DeleteNic(ctx context.Context, nicName string) error {
	return deleteNicIfExists(ctx, p.azClient.NetworkInterfacesClient(), p.resourceGroup, nicName)
}

// E.g., aks-default-2jf98
func GenerateResourceName(nodeClaimName string) string {
	return fmt.Sprintf("aks-%s", nodeClaimName)
}

type createResult struct {
	Poller *runtime.Poller[armcompute.VirtualMachinesClientCreateOrUpdateResponse]
	VM     *armcompute.VirtualMachine
}

// createVirtualMachine creates a new VM or returns an existing one if it already exists.
func (p *DefaultVMProvider) createVirtualMachine(ctx context.Context, vmName string, vm *armcompute.VirtualMachine, imageID string, instanceType *corecloudprovider.InstanceType, zone, capacityType, nodePoolName string) (*createResult, error) {
	// We assume that if a vm exists, we successfully created it with the right parameters from the nodeclaims during another run before a restart.
	resp, err := p.azClient.VirtualMachinesClient().Get(ctx, p.resourceGroup, vmName, nil)
	if err == nil {
		return &createResult{VM: &resp.VirtualMachine}, nil
	}
	if !sdkerrors.IsNotFoundErr(err) {
		return nil, fmt.Errorf("getting VM %q: %w", vmName, err)
	}
	log.FromContext(ctx).V(1).Info("creating virtual machine", "vmName", vmName, logging.InstanceType, instanceType.Name)
	VMCreateStartMetric.With(map[string]string{
		metrics.ImageLabel:        imageID,
		metrics.SizeLabel:         instanceType.Name,
		metrics.ZoneLabel:         zone,
		metrics.CapacityTypeLabel: capacityType,
		metrics.NodePoolLabel:     nodePoolName,
	}).Inc()

	poller, err := p.azClient.VirtualMachinesClient().BeginCreateOrUpdate(ctx, p.resourceGroup, vmName, *vm, nil)
	if err != nil {
		VMCreateFailureMetric.With(map[string]string{
			metrics.ImageLabel:        imageID,
			metrics.SizeLabel:         instanceType.Name,
			metrics.ZoneLabel:         zone,
			metrics.CapacityTypeLabel: capacityType,
			metrics.NodePoolLabel:     nodePoolName,
			metrics.PhaseLabel:        phaseSyncFailure,
			metrics.ErrorCodeLabel:    ErrorCodeForMetrics(err),
		}).Inc()
		return nil, fmt.Errorf("virtualMachine.BeginCreateOrUpdate for VM %q failed: %w", vmName, err)
	}
	return &createResult{Poller: poller, VM: vm}, nil
}

// beginLaunchInstance starts the launch of a VM instance.
// The returned VirtualMachinePromise must be called to gather any errors
// that are retrieved during async provisioning, as well as to complete the provisioning process.
//
//nolint:gocyclo
func (p *DefaultVMProvider) beginLaunchInstance(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*corecloudprovider.InstanceType,
) (*VirtualMachinePromise, error) {
	instanceType, capacityType, zone := offerings.PickSkuSizePriorityAndZone(ctx, nodeClaim, instanceTypes)
	if instanceType == nil {
		return nil, corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("no instance types available"))
	}

	// resourceName for the NIC, VM, and Disk
	resourceName := GenerateResourceName(nodeClaim.Name)

	// Resolve bootstrap data and build the VM template
	vm, bootstrap, err := p.buildVMTemplate(ctx, instanceType, capacityType, zone, nodeClass, nodeClaim, "" /* nicReference filled after NIC creation */)
	if err != nil {
		return nil, fmt.Errorf("building VM template: %w", err)
	}

	// Create NIC using subnet and tags from bootstrap data
	nicReference, err := p.buildAndCreateNIC(ctx, resourceName, instanceType, nodeClass, bootstrap.SubnetID, bootstrap.Tags)
	if err != nil {
		return nil, err
	}

	// Patch the NIC reference into the VM template
	vm.Properties.NetworkProfile = configureNetworkProfile(nicReference)

	// Create or retrieve existing VM
	result, err := p.createVirtualMachine(ctx, resourceName, vm, bootstrap.ImageID, instanceType, zone, capacityType, nodeClaim.Labels[karpv1.NodePoolLabelKey])
	if err != nil {
		sku, skuErr := p.instanceTypeProvider.Get(ctx, nodeClass, instanceType.Name)
		if skuErr != nil {
			return nil, fmt.Errorf("failed to get instance type %q: %w", instanceType.Name, err)
		}
		handledError := p.errorHandling.Handle(ctx, sku, instanceType, zone, capacityType, err)
		if handledError != nil {
			return nil, handledError
		}
		return nil, err
	}

	// Patch the VM object to fill out a few fields that are needed later.
	// This is a bit of a hack that saves us doing a GET now.
	// The reason to avoid a GET is that it can fail, and if it does the future above will be lost,
	// which we don't want.
	result.VM.ID = lo.ToPtr(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s", p.subscriptionID, p.resourceGroup, resourceName))
	result.VM.Properties.TimeCreated = lo.ToPtr(time.Now())

	return &VirtualMachinePromise{
		providerRef: p,
		WaitFunc: func() error {
			if result.Poller == nil {
				// Poller is nil means the VM existed already and we're done.
				return nil
			}

			_, err = result.Poller.PollUntilDone(ctx, nil)
			if err != nil {
				VMCreateFailureMetric.With(map[string]string{
					metrics.ImageLabel:        bootstrap.ImageID,
					metrics.SizeLabel:         instanceType.Name,
					metrics.ZoneLabel:         zone,
					metrics.CapacityTypeLabel: capacityType,
					metrics.NodePoolLabel:     nodeClaim.Labels[karpv1.NodePoolLabelKey],
					metrics.PhaseLabel:        phaseAsyncFailure,
					metrics.ErrorCodeLabel:    ErrorCodeForMetrics(err),
				}).Inc()

				sku, skuErr := p.instanceTypeProvider.Get(ctx, nodeClass, instanceType.Name)
				if skuErr != nil {
					return fmt.Errorf("failed to get instance type %q: %w", instanceType.Name, err)
				}
				handledError := p.errorHandling.Handle(ctx, sku, instanceType, zone, capacityType, err)
				if handledError != nil {
					return handledError
				}
				return err
			}

			if p.provisionMode == consts.ProvisionModeBootstrappingClient {
				err = p.createCSExtensionFromSpec(ctx, resourceName, bootstrap.CustomScriptsCSE, bootstrap.IsWindows, bootstrap.Tags)
				if err != nil {
					return err
				}
			}
			// In AzureVM mode, skip AKS-specific extensions (billing + identifying).
			// The VM is not part of an AKS cluster, so these extensions are irrelevant.
			if p.provisionMode != consts.ProvisionModeAzureVM && isAKSIdentifyingExtensionEnabled(p.env) {
				err = p.createAKSIdentifyingExtensionFromSpec(ctx, resourceName, bootstrap.Tags)
				if err != nil {
					return err
				}
			}

			return nil
		},
		VM: result.VM,
	}, nil
}

// mustDeleteNic parameter is used to determine whether NIC deletion failure is considered an error.
// We may not want to return error of NIC cannot be deleted, as it is "by design" that NIC deletion may not be successful when VM deletion is not completed.
// NIC garbage collector is expected to handle such cases.
func (p *DefaultVMProvider) cleanupAzureResources(ctx context.Context, resourceName string, mustDeleteNic bool) error {
	vmErr := p.deleteVirtualMachineIfExists(ctx, resourceName)
	if vmErr != nil {
		log.FromContext(ctx).Error(vmErr, "virtualMachine.Delete failed", "vmName", resourceName)
	}
	// The order here is intentional, if the VM was created successfully, then we attempt to delete the vm, the
	// nic, disk and all associated resources will be removed. If the VM was not created successfully and a nic was found,
	// then we attempt to delete the nic.

	nicErr := deleteNicIfExists(ctx, p.azClient.NetworkInterfacesClient(), p.resourceGroup, resourceName)

	if mustDeleteNic {
		// Don't log NIC error here since mustDeleteNic is true (critical cleanup scenario).
		// Both VM and NIC errors are returned to the caller for proper handling and logging.
		// Logging here would create duplicate logs when the caller processes the joined error.
		return errors.Join(vmErr, nicErr)
	} else {
		// Log NIC error here since mustDeleteNic is false (best-effort cleanup scenario).
		// Because we're not returning nicErr to the caller we need to log here.
		// Without this log, NIC deletion failures would be silently ignored.
		if nicErr != nil {
			log.FromContext(ctx).Error(nicErr, "networkinterface.Delete failed", "nicName", resourceName)
		}
		return vmErr
	}
}

// deleteVirtualMachineIfExists checks if a virtual machine exists, and if it does, we delete it with a cascading delete
func (p *DefaultVMProvider) deleteVirtualMachineIfExists(ctx context.Context, vmName string) error {
	_, err := p.azClient.VirtualMachinesClient().Get(ctx, p.resourceGroup, vmName, nil)
	if err != nil {
		if sdkerrors.IsNotFoundErr(err) {
			return nil
		}
		return err
	}
	return p.deleteVirtualMachine(ctx, vmName)
}

func (p *DefaultVMProvider) deleteVirtualMachine(ctx context.Context, vmName string) error {
	p.deletingVMsMu.Lock()
	p.deletingVMs.Insert(vmName)
	p.deletingVMsMu.Unlock()
	defer func() {
		p.deletingVMsMu.Lock()
		p.deletingVMs.Delete(vmName)
		p.deletingVMsMu.Unlock()
	}()

	poller, err := p.azClient.VirtualMachinesClient().BeginDelete(
		ctx,
		p.resourceGroup,
		vmName,
		&armcompute.VirtualMachinesClientBeginDeleteOptions{ForceDeletion: lo.ToPtr(true)},
	)
	if err != nil {
		return err
	}

	_, err = poller.PollUntilDone(ctx, nil)

	if err != nil {
		if sdkerrors.IsNotFoundErr(err) {
			return nil
		}
		return err
	}
	return nil
}
