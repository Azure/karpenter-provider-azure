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
	"math"
	"sort"
	"time"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/skewer"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/networksecuritygroup"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

var (
	CapacityTypeToPriority = map[string]string{
		karpv1.CapacityTypeSpot:     string(armcompute.VirtualMachinePriorityTypesSpot),
		karpv1.CapacityTypeOnDemand: string(armcompute.VirtualMachinePriorityTypesRegular),
	}
	PriorityToCapacityType = map[string]string{
		string(armcompute.VirtualMachinePriorityTypesSpot):    karpv1.CapacityTypeSpot,
		string(armcompute.VirtualMachinePriorityTypesRegular): karpv1.CapacityTypeOnDemand,
	}

	SubscriptionQuotaReachedReason              = "SubscriptionQuotaReached"
	AllocationFailureReason                     = "AllocationFailure"
	ZonalAllocationFailureReason                = "ZonalAllocationFailure"
	OverconstrainedZonalAllocationFailureReason = "OverconstrainedZonalAllocationFailure"
	OverconstrainedAllocationFailureReason      = "OverconstrainedAllocationFailure"
	SKUNotAvailableReason                       = "SKUNotAvailable"

	SubscriptionQuotaReachedTTL = 1 * time.Hour
	AllocationFailureTTL        = 1 * time.Hour
	SKUNotAvailableSpotTTL      = 1 * time.Hour
	SKUNotAvailableOnDemandTTL  = 23 * time.Hour
)

const (
	aksIdentifyingExtensionName = "computeAksLinuxBilling"
	// TODO: Why bother with a different CSE name for Windows?
	cseNameWindows = "windows-cse-agent-karpenter"
	cseNameLinux   = "cse-agent-karpenter"
)

// GetManagedExtensionNames gets the names of the VM extensions managed by Karpenter.
// This is a set of 1 or 2 extensions (depending on provisionMode): aksIdentifyingExtension and (sometimes) cse.
func GetManagedExtensionNames(provisionMode string) []string {
	result := []string{
		aksIdentifyingExtensionName,
	}
	if provisionMode == consts.ProvisionModeBootstrappingClient {
		result = append(result, cseNameLinux) // TODO: Windows
	}
	return result
}

type Resource = map[string]interface{}

type VirtualMachinePromise struct {
	VM   *armcompute.VirtualMachine
	Wait func() error
}

type Provider interface {
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
var _ Provider = (*DefaultProvider)(nil)

type DefaultProvider struct {
	location                     string
	azClient                     *AZClient
	instanceTypeProvider         instancetype.Provider
	launchTemplateProvider       *launchtemplate.Provider
	loadBalancerProvider         *loadbalancer.Provider
	networkSecurityGroupProvider *networksecuritygroup.Provider
	resourceGroup                string
	subscriptionID               string
	unavailableOfferings         *cache.UnavailableOfferings
	provisionMode                string
	responseErrorHandlers        []responseErrorHandler

	vmListQuery, nicListQuery string
}

func NewDefaultProvider(
	azClient *AZClient,
	instanceTypeProvider instancetype.Provider,
	launchTemplateProvider *launchtemplate.Provider,
	loadBalancerProvider *loadbalancer.Provider,
	networkSecurityGroupProvider *networksecuritygroup.Provider,
	offeringsCache *cache.UnavailableOfferings,
	location string,
	resourceGroup string,
	subscriptionID string,
	provisionMode string,
) *DefaultProvider {
	return &DefaultProvider{
		azClient:                     azClient,
		instanceTypeProvider:         instanceTypeProvider,
		launchTemplateProvider:       launchTemplateProvider,
		loadBalancerProvider:         loadBalancerProvider,
		networkSecurityGroupProvider: networkSecurityGroupProvider,
		location:                     location,
		resourceGroup:                resourceGroup,
		subscriptionID:               subscriptionID,
		unavailableOfferings:         offeringsCache,
		provisionMode:                provisionMode,
		responseErrorHandlers:        defaultResponseErrorHandlers(),

		vmListQuery:  GetVMListQueryBuilder(resourceGroup).String(),
		nicListQuery: GetNICListQueryBuilder(resourceGroup).String(),
	}
}

// BeginCreate creates an instance given the constraints.
// instanceTypes should be sorted by priority for spot capacity type.
// Note that the returned instance may not be finished provisioning yet.
// Errors that occur on the "sync side" of the VM create, such as quota/capacity, BadRequest due
// to invalid user input, and similar, will have the error returned here.
// Errors that occur on the "async side" of the VM create (after the request is accepted, or after polling the
// VM create and while ) will be returned
// from the VirtualMachinePromise.Wait() function.
func (p *DefaultProvider) BeginCreate(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*corecloudprovider.InstanceType,
) (*VirtualMachinePromise, error) {
	instanceTypes = orderInstanceTypesByPrice(instanceTypes, scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...))
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
	zone, err := utils.GetZone(vm)
	if err != nil {
		log.FromContext(ctx).V(1).Info("failed to get zone for VM", "vmName", *vm.Name, "error", err)
	}

	log.FromContext(ctx).Info("launched new instance",
		"launchedInstance", *vm.ID,
		"hostname", *vm.Name,
		"type", string(*vm.Properties.HardwareProfile.VMSize),
		"zone", zone,
		"capacity-type", GetCapacityType(vm))

	return vmPromise, nil
}

// Update updates the VM with the given updates. If Tags are specified, the tags are also updated on the associated network interface and VM extensions.
// Note that this means that this method can fail if the extensions have not been created yet. It is expected that the caller handles this and retries the update
// to propagate the tags to the extensions once they're created.
func (p *DefaultProvider) Update(ctx context.Context, vmName string, update armcompute.VirtualMachineUpdate) error {
	if update.Tags != nil {
		// If there are tags for other resources, do those first. This is a hedge to avoid updating the VM first which may cause us to think subsequent updates aren't needed
		// because the VM already has the updates

		// Update NIC tags
		_, err := p.azClient.networkInterfacesClient.UpdateTags(
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

		extensionNames := GetManagedExtensionNames(p.provisionMode)
		pollers := make(map[string]*runtime.Poller[armcompute.VirtualMachineExtensionsClientUpdateResponse], len(extensionNames))
		// Update tags on VM extensions
		for _, extName := range extensionNames {
			poller, err := p.azClient.virtualMachinesExtensionClient.BeginUpdate(
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
				return fmt.Errorf("updating VM extension %q for VM %q: %w", extName, vmName, err)
			}
			pollers[extName] = poller
		}

		for extName, poller := range pollers {
			_, err := poller.PollUntilDone(ctx, nil)
			if err != nil {
				return fmt.Errorf("polling VM extension %q for VM %q: %w", extName, vmName, err)
			}
		}
	}

	err := UpdateVirtualMachine(ctx, p.azClient.virtualMachinesClient, p.resourceGroup, vmName, update)
	if err != nil {
		return err
	}

	return nil
}

func (p *DefaultProvider) Get(ctx context.Context, vmName string) (*armcompute.VirtualMachine, error) {
	var vm armcompute.VirtualMachinesClientGetResponse
	var err error

	if vm, err = p.azClient.virtualMachinesClient.Get(ctx, p.resourceGroup, vmName, nil); err != nil {
		azErr := sdkerrors.IsResponseError(err)
		if azErr != nil && (azErr.ErrorCode == "NotFound" || azErr.ErrorCode == "ResourceNotFound") {
			return nil, corecloudprovider.NewNodeClaimNotFoundError(err)
		}
		return nil, fmt.Errorf("failed to get VM instance, %w", err)
	}

	return &vm.VirtualMachine, nil
}

func (p *DefaultProvider) List(ctx context.Context) ([]*armcompute.VirtualMachine, error) {
	req := NewQueryRequest(&(p.subscriptionID), p.vmListQuery)
	client := p.azClient.azureResourceGraphClient
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

func (p *DefaultProvider) Delete(ctx context.Context, resourceName string) error {
	// Note that 'Get' also satisfies cloudprovider.Delete contract expectation (from v1.3.0)
	// of returning cloudprovider.NewNodeClaimNotFoundError if the instance is already deleted
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

func (p *DefaultProvider) GetNic(ctx context.Context, rg, nicName string) (*armnetwork.Interface, error) {
	nicResponse, err := p.azClient.networkInterfacesClient.Get(ctx, rg, nicName, nil)
	if err != nil {
		return nil, err
	}
	return &nicResponse.Interface, nil
}

// ListNics returns all network interfaces in the resource group that have the nodepool tag
func (p *DefaultProvider) ListNics(ctx context.Context) ([]*armnetwork.Interface, error) {
	req := NewQueryRequest(&(p.subscriptionID), p.nicListQuery)
	client := p.azClient.azureResourceGraphClient
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

func (p *DefaultProvider) DeleteNic(ctx context.Context, nicName string) error {
	return deleteNicIfExists(ctx, p.azClient.networkInterfacesClient, p.resourceGroup, nicName)
}

// createAKSIdentifyingExtension attaches a VM extension to identify that this VM participates in an AKS cluster
func (p *DefaultProvider) createAKSIdentifyingExtension(ctx context.Context, vmName string, tags map[string]*string) (err error) {
	vmExt := p.getAKSIdentifyingExtension(tags)
	vmExtName := *vmExt.Name
	log.FromContext(ctx).V(1).Info("creating virtual machine AKS identifying extension", "vmName", vmName)
	v, err := createVirtualMachineExtension(ctx, p.azClient.virtualMachinesExtensionClient, p.resourceGroup, vmName, vmExtName, *vmExt)
	if err != nil {
		return fmt.Errorf("creating VM AKS identifying extension %q for VM %q: %w", vmExtName, vmName, err)
	}
	log.FromContext(ctx).V(1).Info("created virtual machine AKS identifying extension",
		"vmName", vmName,
		"extensionID", *v.ID,
	)
	return nil
}

func (p *DefaultProvider) createCSExtension(ctx context.Context, vmName string, cse string, isWindows bool, tags map[string]*string) error {
	vmExt := p.getCSExtension(cse, isWindows, tags)
	vmExtName := *vmExt.Name
	log.FromContext(ctx).V(1).Info("creating virtual machine CSE", "vmName", vmName)
	v, err := createVirtualMachineExtension(ctx, p.azClient.virtualMachinesExtensionClient, p.resourceGroup, vmName, vmExtName, *vmExt)
	if err != nil {
		return fmt.Errorf("creating VM CSE for VM %q: %w", vmName, err)
	}
	log.FromContext(ctx).V(1).Info("created virtual machine CSE",
		"vmName", vmName,
		"extensionID", *v.ID,
	)
	return nil
}

func (p *DefaultProvider) newNetworkInterfaceForVM(opts *createNICOptions) armnetwork.Interface {
	var ipv4BackendPools []*armnetwork.BackendAddressPool
	for _, poolID := range opts.BackendPools.IPv4PoolIDs {
		ipv4BackendPools = append(ipv4BackendPools, &armnetwork.BackendAddressPool{
			ID: &poolID,
		})
	}

	skuAcceleratedNetworkingRequirements := scheduling.NewRequirements(
		scheduling.NewRequirement(v1beta1.LabelSKUAcceleratedNetworking, v1.NodeSelectorOpIn, "true"))

	enableAcceleratedNetworking := false
	if err := opts.InstanceType.Requirements.Compatible(skuAcceleratedNetworkingRequirements); err == nil {
		enableAcceleratedNetworking = true
	}

	var nsgRef *armnetwork.SecurityGroup
	if opts.NetworkSecurityGroupID != "" {
		nsgRef = &armnetwork.SecurityGroup{
			ID: &opts.NetworkSecurityGroupID,
		}
	}

	nic := armnetwork.Interface{
		Location: lo.ToPtr(p.location),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
				{
					Name: &opts.NICName,
					Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
						Primary:                   lo.ToPtr(true),
						PrivateIPAllocationMethod: lo.ToPtr(armnetwork.IPAllocationMethodDynamic),

						LoadBalancerBackendAddressPools: ipv4BackendPools,
					},
				},
			},
			NetworkSecurityGroup:        nsgRef,
			EnableAcceleratedNetworking: lo.ToPtr(enableAcceleratedNetworking),
			EnableIPForwarding:          lo.ToPtr(false),
		},
	}
	if opts.NetworkPlugin == consts.NetworkPluginAzure && opts.NetworkPluginMode != consts.NetworkPluginModeOverlay {
		// AzureCNI without overlay requires secondary IPs, for pods. (These IPs are not included in backend address pools.)
		// NOTE: Unlike AKS RP, this logic does not reduce secondary IP count by the number of expected hostNetwork pods, favoring simplicity instead
		for i := 1; i < int(opts.MaxPods); i++ {
			nic.Properties.IPConfigurations = append(
				nic.Properties.IPConfigurations,
				&armnetwork.InterfaceIPConfiguration{
					Name: lo.ToPtr(fmt.Sprintf("ipconfig%d", i)),
					Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
						Primary:                   lo.ToPtr(false),
						PrivateIPAllocationMethod: lo.ToPtr(armnetwork.IPAllocationMethodDynamic),
					},
				},
			)
		}
	}
	return nic
}

func GenerateResourceName(nodeClaimName string) string {
	return fmt.Sprintf("aks-%s", nodeClaimName)
}

type createNICOptions struct {
	NICName                string
	BackendPools           *loadbalancer.BackendAddressPools
	InstanceType           *corecloudprovider.InstanceType
	LaunchTemplate         *launchtemplate.Template
	NetworkPlugin          string
	NetworkPluginMode      string
	MaxPods                int32
	NetworkSecurityGroupID string
}

func (p *DefaultProvider) createNetworkInterface(ctx context.Context, opts *createNICOptions) (string, error) {
	nic := p.newNetworkInterfaceForVM(opts)
	p.applyTemplateToNic(&nic, opts.LaunchTemplate)
	log.FromContext(ctx).V(1).Info("creating network interface", "nicName", opts.NICName)
	res, err := createNic(ctx, p.azClient.networkInterfacesClient, p.resourceGroup, opts.NICName, nic)
	if err != nil {
		return "", err
	}
	log.FromContext(ctx).V(1).Info("successfully created network interface", "nicName", opts.NICName, "nicID", *res.ID)
	return *res.ID, nil
}

// createVMOptions contains all the parameters needed to create a VM
type createVMOptions struct {
	VMName             string
	NicReference       string
	Zone               string
	CapacityType       string
	Location           string
	SSHPublicKey       string
	LinuxAdminUsername string
	NodeIdentities     []string
	NodeClass          *v1beta1.AKSNodeClass
	LaunchTemplate     *launchtemplate.Template
	InstanceType       *corecloudprovider.InstanceType
	ProvisionMode      string
	UseSIG             bool
}

// newVMObject creates a new armcompute.VirtualMachine from the provided options
func newVMObject(opts *createVMOptions) *armcompute.VirtualMachine {
	if opts.LaunchTemplate.IsWindows {
		return &armcompute.VirtualMachine{} // TODO(Windows)
	}

	vm := &armcompute.VirtualMachine{
		Name:     lo.ToPtr(opts.VMName), // TODO: I think it's safe to set this, even though it's read only
		Location: lo.ToPtr(opts.Location),
		Identity: ConvertToVirtualMachineIdentity(opts.NodeIdentities),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{
				VMSize: lo.ToPtr(armcompute.VirtualMachineSizeTypes(opts.InstanceType.Name)),
			},

			StorageProfile: &armcompute.StorageProfile{
				OSDisk: &armcompute.OSDisk{
					Name:         lo.ToPtr(opts.VMName),
					DiskSizeGB:   opts.NodeClass.Spec.OSDiskSizeGB,
					CreateOption: lo.ToPtr(armcompute.DiskCreateOptionTypesFromImage),
					DeleteOption: lo.ToPtr(armcompute.DiskDeleteOptionTypesDelete),
				},
			},

			NetworkProfile: &armcompute.NetworkProfile{
				NetworkInterfaces: []*armcompute.NetworkInterfaceReference{
					{
						ID: &opts.NicReference,
						Properties: &armcompute.NetworkInterfaceReferenceProperties{
							Primary:      lo.ToPtr(true),
							DeleteOption: lo.ToPtr(armcompute.DeleteOptionsDelete),
						},
					},
				},
			},

			OSProfile: &armcompute.OSProfile{
				AdminUsername: lo.ToPtr(opts.LinuxAdminUsername),
				ComputerName:  &opts.VMName,
				LinuxConfiguration: &armcompute.LinuxConfiguration{
					DisablePasswordAuthentication: lo.ToPtr(true),
					SSH: &armcompute.SSHConfiguration{
						PublicKeys: []*armcompute.SSHPublicKey{
							{
								KeyData: lo.ToPtr(opts.SSHPublicKey),
								Path:    lo.ToPtr("/home/" + opts.LinuxAdminUsername + "/.ssh/authorized_keys"),
							},
						},
					},
				},
			},
			Priority: lo.ToPtr(armcompute.VirtualMachinePriorityTypes(
				CapacityTypeToPriority[opts.CapacityType]),
			),
		},
		Zones: utils.MakeVMZone(opts.Zone),
		Tags:  opts.LaunchTemplate.Tags,
	}
	setVMPropertiesOSDiskType(vm.Properties, opts.LaunchTemplate)
	setImageReference(vm.Properties, opts.LaunchTemplate.ImageID, opts.UseSIG)
	setVMPropertiesBillingProfile(vm.Properties, opts.CapacityType)

	if opts.ProvisionMode == consts.ProvisionModeBootstrappingClient {
		vm.Properties.OSProfile.CustomData = lo.ToPtr(opts.LaunchTemplate.CustomScriptsCustomData)
	} else {
		vm.Properties.OSProfile.CustomData = lo.ToPtr(opts.LaunchTemplate.ScriptlessCustomData)
	}

	return vm
}

func setVMPropertiesOSDiskType(vmProperties *armcompute.VirtualMachineProperties, launchTemplate *launchtemplate.Template) {
	placement := launchTemplate.StorageProfilePlacement
	if launchTemplate.StorageProfileIsEphemeral {
		vmProperties.StorageProfile.OSDisk.DiffDiskSettings = &armcompute.DiffDiskSettings{
			Option:    lo.ToPtr(armcompute.DiffDiskOptionsLocal),
			Placement: lo.ToPtr(placement),
		}
		vmProperties.StorageProfile.OSDisk.Caching = lo.ToPtr(armcompute.CachingTypesReadOnly)
	}
}

// setImageReference sets the image reference for the VM based on if we are using self hosted karpenter or the node auto provisioning addon
func setImageReference(vmProperties *armcompute.VirtualMachineProperties, imageID string, useSIG bool) {
	if useSIG {
		vmProperties.StorageProfile.ImageReference = &armcompute.ImageReference{
			ID: lo.ToPtr(imageID),
		}
		return
	}
	vmProperties.StorageProfile.ImageReference = &armcompute.ImageReference{
		CommunityGalleryImageID: lo.ToPtr(imageID),
	}
}

// setVMPropertiesBillingProfile sets a default MaxPrice of -1 for Spot
func setVMPropertiesBillingProfile(vmProperties *armcompute.VirtualMachineProperties, capacityType string) {
	if capacityType == karpv1.CapacityTypeSpot {
		vmProperties.EvictionPolicy = lo.ToPtr(armcompute.VirtualMachineEvictionPolicyTypesDelete)
		vmProperties.BillingProfile = &armcompute.BillingProfile{
			MaxPrice: lo.ToPtr(float64(-1)),
		}
	}
}

type createResult struct {
	Poller *runtime.Poller[armcompute.VirtualMachinesClientCreateOrUpdateResponse]
	VM     *armcompute.VirtualMachine
}

// createVirtualMachine creates a new VM using the provided options or skips the creation of a vm if it already exists, which means opts is not guaranteed except VMName
func (p *DefaultProvider) createVirtualMachine(ctx context.Context, opts *createVMOptions) (*createResult, error) {
	// We assume that if a vm exists, we successfully created it with the right parameters from the nodeclaims during another run before a restart.
	// there are some non-deterministic properties that may change.
	// Zones: zones are non-detrminsitic as we do a random pick out of zones on the nodeclaim that satisfy the workload requirements.
	// 	      Nodeclaim can have Requirements: Zone-1, Zone-2, Zone-3
	//        Then we pick a random zone from that list in each create call that satisfies the workload
	// UnavailableOfferingsCache: The unavailable offerings cache is used to determine if we should pick the sku, zone, or even priority.
	//        Errors for things like subscription level spot quota, SKU Quota, etc are stored in the unavailable offerings cache.
	//        So values like the SKU, Priority(Spot/On-Demand), may be different, which results in a different image, different
	//        os.CustomData.
	// If any of these properties are modified, the existing vm will return a 409 status code "PropertyChangeNotAllowed".
	// this results in create being blocked on the nodeclaim until liveness TTL is hit.
	resp, err := p.azClient.virtualMachinesClient.Get(ctx, p.resourceGroup, opts.VMName, nil)
	// If status == ok, we want to return the existing vmm
	if err == nil {
		return &createResult{VM: &resp.VirtualMachine}, nil
	}
	// if status != ok, and for a reason other than we did not find the vm
	azErr := sdkerrors.IsResponseError(err)
	if azErr != nil && (azErr.ErrorCode != "NotFound" && azErr.ErrorCode != "ResourceNotFound") {
		return nil, fmt.Errorf("getting VM %q: %w", opts.VMName, err)
	}
	vm := newVMObject(opts)
	log.FromContext(ctx).V(1).Info("creating virtual machine", "vmName", opts.VMName, "instance-type", opts.InstanceType.Name)

	poller, err := p.azClient.virtualMachinesClient.BeginCreateOrUpdate(ctx, p.resourceGroup, opts.VMName, *vm, nil)
	if err != nil {
		return nil, fmt.Errorf("virtualMachine.BeginCreateOrUpdate for VM %q failed: %w", opts.VMName, err)
	}
	return &createResult{Poller: poller, VM: vm}, nil
}

// beginLaunchInstance starts the launch of a VM instance.
// The returned VirtualMachinePromise must be called to gather any errors
// that are retrieved during async provisioning, as well as to complete the provisioning process.
// nolint: gocyclo
func (p *DefaultProvider) beginLaunchInstance(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*corecloudprovider.InstanceType,
) (*VirtualMachinePromise, error) {
	instanceType, capacityType, zone := p.pickSkuSizePriorityAndZone(ctx, nodeClaim, instanceTypes)
	if instanceType == nil {
		return nil, corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("no instance types available"))
	}
	launchTemplate, err := p.getLaunchTemplate(ctx, nodeClass, nodeClaim, instanceType, capacityType)
	if err != nil {
		return nil, fmt.Errorf("getting launch template: %w", err)
	}

	// resourceName for the NIC, VM, and Disk
	resourceName := GenerateResourceName(nodeClaim.Name)

	backendPools, err := p.loadBalancerProvider.LoadBalancerBackendPools(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting backend pools: %w", err)
	}
	networkPlugin := options.FromContext(ctx).NetworkPlugin
	networkPluginMode := options.FromContext(ctx).NetworkPluginMode

	isAKSManagedVNET, err := utils.IsAKSManagedVNET(options.FromContext(ctx).NodeResourceGroup, launchTemplate.SubnetID)
	if err != nil {
		return nil, fmt.Errorf("checking if vnet is managed: %w", err)
	}
	var nsgID string
	if !isAKSManagedVNET {
		nsg, err := p.networkSecurityGroupProvider.ManagedNetworkSecurityGroup(ctx)
		if err != nil {
			return nil, fmt.Errorf("getting managed network security group: %w", err)
		}
		nsgID = lo.FromPtr(nsg.ID)
	}

	// TODO: Not returning after launching this LRO because
	// TODO: doing so would bypass the capacity and other errors that are currently handled by
	// TODO: core pkg/controllers/nodeclaim/lifecycle/controller.go - in particular, there are metrics/events
	// TODO: emitted in capacity failure cases that we probably want.
	nicReference, err := p.createNetworkInterface(
		ctx,
		&createNICOptions{
			NICName:                resourceName,
			NetworkPlugin:          networkPlugin,
			NetworkPluginMode:      networkPluginMode,
			MaxPods:                utils.GetMaxPods(nodeClass, networkPlugin, networkPluginMode),
			LaunchTemplate:         launchTemplate,
			BackendPools:           backendPools,
			InstanceType:           instanceType,
			NetworkSecurityGroupID: nsgID,
		},
	)
	if err != nil {
		return nil, err
	}

	result, err := p.createVirtualMachine(ctx, &createVMOptions{
		VMName:             resourceName,
		NicReference:       nicReference,
		Zone:               zone,
		CapacityType:       capacityType,
		Location:           p.location,
		SSHPublicKey:       options.FromContext(ctx).SSHPublicKey,
		LinuxAdminUsername: options.FromContext(ctx).LinuxAdminUsername,
		NodeIdentities:     options.FromContext(ctx).NodeIdentities,
		NodeClass:          nodeClass,
		LaunchTemplate:     launchTemplate,
		InstanceType:       instanceType,
		ProvisionMode:      p.provisionMode,
		UseSIG:             options.FromContext(ctx).UseSIG,
	})
	if err != nil {
		sku, skuErr := p.instanceTypeProvider.Get(ctx, nodeClass, instanceType.Name)
		if skuErr != nil {
			return nil, fmt.Errorf("failed to get instance type %q: %w", instanceType.Name, err)
		}
		azErr := p.handleResponseErrors(ctx, sku, instanceType, zone, capacityType, err)
		return nil, azErr
	}

	// Patch the VM object to fill out a few fields that are needed later.
	// This is a bit of a hack that saves us doing a GET now.
	// The reason to avoid a GET is that it can fail, and if it does the future above will be lost,
	// which we don't want.
	result.VM.ID = lo.ToPtr(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s", p.subscriptionID, p.resourceGroup, resourceName))
	result.VM.Properties.TimeCreated = lo.ToPtr(time.Now())

	return &VirtualMachinePromise{
		Wait: func() error {
			if result.Poller == nil {
				// Poller is nil means the VM existed already and we're done.
				// TODO: if the VM doesn't have extensions this will still happen and we will have to
				// TODO: wait for the TTL for the claim to be deleted and recreated. This will most likely
				// TODO: happen during Karpenter pod restart.
				return nil
			}

			_, err = result.Poller.PollUntilDone(ctx, nil)
			if err != nil {
				sku, skuErr := p.instanceTypeProvider.Get(ctx, nodeClass, instanceType.Name)
				if skuErr != nil {
					return fmt.Errorf("failed to get instance type %q: %w", instanceType.Name, err)
				}
				azErr := p.handleResponseErrors(ctx, sku, instanceType, zone, capacityType, err)
				return azErr
			}

			if p.provisionMode == consts.ProvisionModeBootstrappingClient {
				err = p.createCSExtension(ctx, resourceName, launchTemplate.CustomScriptsCSE, launchTemplate.IsWindows, launchTemplate.Tags)
				if err != nil {
					// An error here is handled by CloudProvider create and calls instanceProvider.Delete (which cleans up the azure resources)
					return err
				}
			}

			err = p.createAKSIdentifyingExtension(ctx, resourceName, launchTemplate.Tags)
			if err != nil {
				return err
			}

			return nil
		},
		VM: result.VM,
	}, nil
}

func (p *DefaultProvider) handleResponseErrors(ctx context.Context, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, responseError error) error {
	for _, handler := range p.responseErrorHandlers {
		if handler.matchError(responseError) {
			return handler.handleResponseError(ctx, p, sku, instanceType, zone, capacityType, responseError)
		}
	}
	// responseError didn't match any of our handlers, return it as is
	return responseError
}

func (p *DefaultProvider) applyTemplateToNic(nic *armnetwork.Interface, template *launchtemplate.Template) {
	// set tags
	nic.Tags = template.Tags
	for _, ipConfig := range nic.Properties.IPConfigurations {
		ipConfig.Properties.Subnet = &armnetwork.Subnet{ID: &template.SubnetID}
	}
}

func (p *DefaultProvider) getLaunchTemplate(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceType *corecloudprovider.InstanceType,
	capacityType string,
) (*launchtemplate.Template, error) {
	additionalLabels := lo.Assign(GetAllSingleValuedRequirementLabels(instanceType), map[string]string{karpv1.CapacityTypeLabelKey: capacityType})

	launchTemplate, err := p.launchTemplateProvider.GetTemplate(ctx, nodeClass, nodeClaim, instanceType, additionalLabels)
	if err != nil {
		return nil, fmt.Errorf("getting launch templates, %w", err)
	}

	return launchTemplate, nil
}

// GetAllSingleValuedRequirementLabels converts instanceType.Requirements to labels
// Like   instanceType.Requirements.Labels() it uses single-valued requirements
// Unlike instanceType.Requirements.Labels() it does not filter out restricted Node labels
func GetAllSingleValuedRequirementLabels(instanceType *corecloudprovider.InstanceType) map[string]string {
	labels := map[string]string{}
	if instanceType == nil {
		return labels
	}
	for key, req := range instanceType.Requirements {
		if req.Len() == 1 {
			labels[key] = req.Values()[0]
		}
	}
	return labels
}

// pick the "best" SKU, priority and zone, from InstanceType options (and their offerings) in the request
func (p *DefaultProvider) pickSkuSizePriorityAndZone(
	ctx context.Context,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*corecloudprovider.InstanceType,
) (*corecloudprovider.InstanceType, string, string) {
	if len(instanceTypes) == 0 {
		return nil, "", ""
	}
	// InstanceType/VM SKU - just pick the first one for now. They are presorted by cheapest offering price (taking node requirements into account)
	instanceType := instanceTypes[0]
	log.FromContext(ctx).Info("selected instance type", "instance-type", instanceType.Name)
	// Priority - Nodepool defaults to Regular, so pick Spot if it is explicitly included in requirements (and is offered in at least one zone)
	priority := p.getPriorityForInstanceType(nodeClaim, instanceType)
	// Zone - ideally random/spread from requested zones that support given Priority
	requestedZones := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...).Get(v1.LabelTopologyZone)
	priorityOfferings := lo.Filter(instanceType.Offerings.Available(), func(o *corecloudprovider.Offering, _ int) bool {
		return getOfferingCapacityType(o) == priority && requestedZones.Has(getOfferingZone(o))
	})
	zonesWithPriority := lo.Map(priorityOfferings, func(o *corecloudprovider.Offering, _ int) string { return getOfferingZone(o) })
	if zone, ok := sets.New(zonesWithPriority...).PopAny(); ok {
		return instanceType, priority, zone
	}
	return nil, "", ""
}

// mustDeleteNic parameter is used to determine whether NIC deletion failure is considered an error.
// We may not want to return error of NIC cannot be deleted, as it is "by design" that NIC deletion may not be successful when VM deletion is not completed.
// NIC garbage collector is expected to handle such cases.
func (p *DefaultProvider) cleanupAzureResources(ctx context.Context, resourceName string, mustDeleteNic bool) error {
	vmErr := deleteVirtualMachineIfExists(ctx, p.azClient.virtualMachinesClient, p.resourceGroup, resourceName)
	if vmErr != nil {
		log.FromContext(ctx).Error(vmErr, "virtualMachine.Delete failed", "vmName", resourceName)
	}
	// The order here is intentional, if the VM was created successfully, then we attempt to delete the vm, the
	// nic, disk and all associated resources will be removed. If the VM was not created successfully and a nic was found,
	// then we attempt to delete the nic.

	nicErr := deleteNicIfExists(ctx, p.azClient.networkInterfacesClient, p.resourceGroup, resourceName)

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

// getPriorityForInstanceType selects spot if both constraints are flexible and there is an available offering.
// The Azure Cloud Provider defaults to Regular, so spot must be explicitly included in capacity type requirements.
//
// This returns from a single pre-selected InstanceType, rather than all InstanceType options in nodeRequest,
// because Azure Cloud Provider does client-side selection of particular InstanceType from options
func (p *DefaultProvider) getPriorityForInstanceType(nodeClaim *karpv1.NodeClaim, instanceType *corecloudprovider.InstanceType) string {
	requirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)

	if requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeSpot) {
		for _, offering := range instanceType.Offerings.Available() {
			if requirements.Get(v1.LabelTopologyZone).Has(getOfferingZone(offering)) && getOfferingCapacityType(offering) == karpv1.CapacityTypeSpot {
				return karpv1.CapacityTypeSpot
			}
		}
	}
	return karpv1.CapacityTypeOnDemand
}

func orderInstanceTypesByPrice(instanceTypes []*corecloudprovider.InstanceType, requirements scheduling.Requirements) []*corecloudprovider.InstanceType {
	// Order instance types so that we get the cheapest instance types of the available offerings
	sort.Slice(instanceTypes, func(i, j int) bool {
		iPrice := math.MaxFloat64
		jPrice := math.MaxFloat64
		if len(instanceTypes[i].Offerings.Available().Compatible(requirements)) > 0 {
			iPrice = instanceTypes[i].Offerings.Available().Compatible(requirements).Cheapest().Price
		}
		if len(instanceTypes[j].Offerings.Available().Compatible(requirements)) > 0 {
			jPrice = instanceTypes[j].Offerings.Available().Compatible(requirements).Cheapest().Price
		}
		if iPrice == jPrice {
			return instanceTypes[i].Name < instanceTypes[j].Name
		}
		return iPrice < jPrice
	})
	return instanceTypes
}

func GetCapacityType(instance *armcompute.VirtualMachine) string {
	if instance != nil && instance.Properties != nil && instance.Properties.Priority != nil {
		return PriorityToCapacityType[string(*instance.Properties.Priority)]
	}
	return ""
}

func (p *DefaultProvider) getAKSIdentifyingExtension(tags map[string]*string) *armcompute.VirtualMachineExtension {
	const (
		vmExtensionType                  = "Microsoft.Compute/virtualMachines/extensions"
		aksIdentifyingExtensionPublisher = "Microsoft.AKS"
		aksIdentifyingExtensionTypeLinux = "Compute.AKS.Linux.Billing"
	)

	vmExtension := &armcompute.VirtualMachineExtension{
		Location: lo.ToPtr(p.location),
		Name:     lo.ToPtr(aksIdentifyingExtensionName),
		Properties: &armcompute.VirtualMachineExtensionProperties{
			Publisher:               lo.ToPtr(aksIdentifyingExtensionPublisher),
			TypeHandlerVersion:      lo.ToPtr("1.0"),
			AutoUpgradeMinorVersion: lo.ToPtr(true),
			Settings:                &map[string]interface{}{},
			Type:                    lo.ToPtr(aksIdentifyingExtensionTypeLinux),
		},
		Type: lo.ToPtr(vmExtensionType),
		Tags: tags,
	}

	return vmExtension
}

func (p *DefaultProvider) getCSExtension(cse string, isWindows bool, tags map[string]*string) *armcompute.VirtualMachineExtension {
	const (
		vmExtensionType     = "Microsoft.Compute/virtualMachines/extensions"
		cseTypeWindows      = "CustomScriptExtension"
		csePublisherWindows = "Microsoft.Compute"
		cseVersionWindows   = "1.10"
		cseTypeLinux        = "CustomScript"
		csePublisherLinux   = "Microsoft.Azure.Extensions"
		cseVersionLinux     = "2.0"
	)

	return &armcompute.VirtualMachineExtension{
		Location: lo.ToPtr(p.location),
		Name:     lo.ToPtr(lo.Ternary(isWindows, cseNameWindows, cseNameLinux)),
		Type:     lo.ToPtr(vmExtensionType),
		Properties: &armcompute.VirtualMachineExtensionProperties{
			AutoUpgradeMinorVersion: lo.ToPtr(true),
			Type:                    lo.ToPtr(lo.Ternary(isWindows, cseTypeWindows, cseTypeLinux)),
			Publisher:               lo.ToPtr(lo.Ternary(isWindows, csePublisherWindows, csePublisherLinux)),
			TypeHandlerVersion:      lo.ToPtr(lo.Ternary(isWindows, cseVersionWindows, cseVersionLinux)),
			Settings:                &map[string]interface{}{},
			ProtectedSettings: &map[string]interface{}{
				"commandToExecute": cse,
			},
		},
		Tags: tags,
	}
}

func ConvertToVirtualMachineIdentity(nodeIdentities []string) *armcompute.VirtualMachineIdentity {
	var identity *armcompute.VirtualMachineIdentity
	if len(nodeIdentities) > 0 {
		identityMap := make(map[string]*armcompute.UserAssignedIdentitiesValue)
		for _, identityID := range nodeIdentities {
			identityMap[identityID] = &armcompute.UserAssignedIdentitiesValue{}
		}

		if len(identityMap) > 0 {
			identity = &armcompute.VirtualMachineIdentity{
				Type:                   lo.ToPtr(armcompute.ResourceIdentityTypeUserAssigned),
				UserAssignedIdentities: identityMap,
			}
		}
	}

	return identity
}

func getOfferingCapacityType(offering *corecloudprovider.Offering) string {
	return offering.Requirements.Get(karpv1.CapacityTypeLabelKey).Any()
}

func getOfferingZone(offering *corecloudprovider.Offering) string {
	return offering.Requirements.Get(v1.LabelTopologyZone).Any()
}
