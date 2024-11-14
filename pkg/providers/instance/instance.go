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
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/util/sets"
	"knative.dev/pkg/logging"

	"github.com/Azure/azure-kusto-go/kusto/kql"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"

	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	//nolint SA1019 - deprecated package
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2022-08-01/compute"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

var (
	NodePoolTagKey = strings.ReplaceAll(karpv1.NodePoolLabelKey, "/", "_")
	listQuery      string

	CapacityTypeToPriority = map[string]string{
		karpv1.CapacityTypeSpot:     string(compute.Spot),
		karpv1.CapacityTypeOnDemand: string(compute.Regular),
	}
	PriorityToCapacityType = map[string]string{
		string(compute.Spot):    karpv1.CapacityTypeSpot,
		string(compute.Regular): karpv1.CapacityTypeOnDemand,
	}

	SubscriptionQuotaReachedReason = "SubscriptionQuotaReached"
	ZonalAllocationFailureReason   = "ZonalAllocationFailure"
	SKUNotAvailableReason          = "SKUNotAvailable"

	SubscriptionQuotaReachedTTL = 1 * time.Hour
	SKUNotAvailableSpotTTL      = 1 * time.Hour
	SKUNotAvailableOnDemandTTL  = 23 * time.Hour
)

type Resource = map[string]interface{}

type Provider interface {
	Create(context.Context, *v1alpha2.AKSNodeClass, *karpv1.NodeClaim, []*corecloudprovider.InstanceType) (*armcompute.VirtualMachine, error)
	Get(context.Context, string) (*armcompute.VirtualMachine, error)
	List(context.Context) ([]*armcompute.VirtualMachine, error)
	Delete(context.Context, string) error
	// CreateTags(context.Context, string, map[string]string) error
	Update(context.Context, string, armcompute.VirtualMachineUpdate) error
	GetNic(context.Context, string, string) (*armnetwork.Interface, error)
}

// assert that DefaultProvider implements Provider interface
var _ Provider = (*DefaultProvider)(nil)

type DefaultProvider struct {
	location               string
	azClient               *AZClient
	instanceTypeProvider   instancetype.Provider
	launchTemplateProvider *launchtemplate.Provider
	loadBalancerProvider   *loadbalancer.Provider
	resourceGroup          string
	subscriptionID         string
	unavailableOfferings   *cache.UnavailableOfferings
	provisionMode          string
}

func NewDefaultProvider(
	azClient *AZClient,
	instanceTypeProvider instancetype.Provider,
	launchTemplateProvider *launchtemplate.Provider,
	loadBalancerProvider *loadbalancer.Provider,
	offeringsCache *cache.UnavailableOfferings,
	location string,
	resourceGroup string,
	subscriptionID string,
	provisionMode string,
) *DefaultProvider {
	listQuery = GetListQueryBuilder(resourceGroup).String()
	return &DefaultProvider{
		azClient:               azClient,
		instanceTypeProvider:   instanceTypeProvider,
		launchTemplateProvider: launchTemplateProvider,
		loadBalancerProvider:   loadBalancerProvider,
		location:               location,
		resourceGroup:          resourceGroup,
		subscriptionID:         subscriptionID,
		unavailableOfferings:   offeringsCache,
		provisionMode:          provisionMode,
	}
}

// Create an instance given the constraints.
// instanceTypes should be sorted by priority for spot capacity type.
func (p *DefaultProvider) Create(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*corecloudprovider.InstanceType) (*armcompute.VirtualMachine, error) {
	instanceTypes = orderInstanceTypesByPrice(instanceTypes, scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...))
	vm, instanceType, err := p.launchInstance(ctx, nodeClass, nodeClaim, instanceTypes)
	if err != nil {
		// Currently, CSE errors will lead to here
		if cleanupErr := p.cleanupAzureResources(ctx, GenerateResourceName(nodeClaim.Name)); cleanupErr != nil {
			logging.FromContext(ctx).Errorf("failed to cleanup resources for node claim %s, %w", nodeClaim.Name, cleanupErr)
		}
		return nil, err
	}
	zone, err := GetZoneID(vm)
	if err != nil {
		logging.FromContext(ctx).Error(err)
	}
	logging.FromContext(ctx).With(
		"launched-instance", *vm.ID,
		"hostname", *vm.Name,
		"type", string(*vm.Properties.HardwareProfile.VMSize),
		"zone", zone,
		"capacity-type", p.getPriorityForInstanceType(nodeClaim, instanceType)).Infof("launched new instance")

	return vm, nil
}

func (p *DefaultProvider) Update(ctx context.Context, vmName string, update armcompute.VirtualMachineUpdate) error {
	return UpdateVirtualMachine(ctx, p.azClient.virtualMachinesClient, p.resourceGroup, vmName, update)
}

func (p *DefaultProvider) Get(ctx context.Context, vmName string) (*armcompute.VirtualMachine, error) {
	var vm armcompute.VirtualMachinesClientGetResponse
	var err error

	if vm, err = p.azClient.virtualMachinesClient.Get(ctx, p.resourceGroup, vmName, nil); err != nil {
		if sdkerrors.IsNotFoundErr(err) {
			return nil, corecloudprovider.NewNodeClaimNotFoundError(err)
		}
		return nil, fmt.Errorf("failed to get VM instance, %w", err)
	}

	return &vm.VirtualMachine, nil
}

func (p *DefaultProvider) List(ctx context.Context) ([]*armcompute.VirtualMachine, error) {
	req := NewQueryRequest(&(p.subscriptionID), listQuery)
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
	logging.FromContext(ctx).Debugf("Deleting virtual machine %s and associated resources", resourceName)
	return p.cleanupAzureResources(ctx, resourceName)
}

// createAKSIdentifyingExtension attaches a VM extension to identify that this VM participates in an AKS cluster
func (p *DefaultProvider) createAKSIdentifyingExtension(ctx context.Context, vmName string) (err error) {
	vmExt := p.getAKSIdentifyingExtension()
	vmExtName := *vmExt.Name
	logging.FromContext(ctx).Debugf("Creating virtual machine AKS identifying extension for %s", vmName)
	v, err := createVirtualMachineExtension(ctx, p.azClient.virtualMachinesExtensionClient, p.resourceGroup, vmName, vmExtName, *vmExt)
	if err != nil {
		logging.FromContext(ctx).Errorf("Creating VM AKS identifying extension for VM %q failed, %w", vmName, err)
		return fmt.Errorf("creating VM AKS identifying extension for VM %q, %w failed", vmName, err)
	}
	logging.FromContext(ctx).Debugf("Created  virtual machine AKS identifying extension for %s, with an id of %s", vmName, *v.ID)
	return nil
}

func (p *DefaultProvider) createCSExtension(ctx context.Context, vmName string, cse string, isWindows bool) (err error) {
	vmExt := p.getCSExtension(cse, isWindows)
	vmExtName := *vmExt.Name
	logging.FromContext(ctx).Debugf("Creating virtual machine CSE for %s", vmName)
	v, err := createVirtualMachineExtension(ctx, p.azClient.virtualMachinesExtensionClient, p.resourceGroup, vmName, vmExtName, *vmExt)
	if err != nil {
		logging.FromContext(ctx).Errorf("Creating VM CSE for VM %q failed, %w", vmName, err)
		return fmt.Errorf("creating VM CSE for VM %q, %w failed", vmName, err)
	}
	logging.FromContext(ctx).Debugf("Created virtual machine CSE for %s, with an id of %s", vmName, *v.ID)
	return nil
}

func (p *DefaultProvider) newNetworkInterfaceForVM(opts *createNICOptions) armnetwork.Interface {
	var ipv4BackendPools []*armnetwork.BackendAddressPool
	for _, poolID := range opts.BackendPools.IPv4PoolIDs {
		ipv4BackendPools = append(ipv4BackendPools, &armnetwork.BackendAddressPool{
			ID: &poolID,
		})
	}

	skuAcceleratedNetworkingRequirements := scheduling.NewRequirements(scheduling.NewRequirement(v1alpha2.LabelSKUAcceleratedNetworking, v1.NodeSelectorOpIn, "true"))

	enableAcceleratedNetworking := false
	if err := opts.InstanceType.Requirements.Compatible(skuAcceleratedNetworkingRequirements); err == nil {
		enableAcceleratedNetworking = true
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
			EnableAcceleratedNetworking: lo.ToPtr(enableAcceleratedNetworking),
			EnableIPForwarding:          lo.ToPtr(false),
		},
	}
	if opts.NetworkPlugin == consts.NetworkPluginAzure && opts.NetworkPluginMode != consts.NetworkPluginModeOverlay {
		// AzureCNI without overlay requires secondary IPs, for pods. (These IPs are not included in backend address pools.)
		// NOTE: Unlike AKS RP, this logic does not reduce secondary IP count by the number of expected hostNetwork pods, favoring simplicity instead
		// TODO: When MaxPods comes from the AKSNodeClass kubelet configuration, get the number of secondary
		// ips from the nodeclass instead of using the default
		for i := 1; i < consts.DefaultKubernetesMaxPods; i++ {
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
	NICName           string
	BackendPools      *loadbalancer.BackendAddressPools
	InstanceType      *corecloudprovider.InstanceType
	LaunchTemplate    *launchtemplate.Template
	NetworkPlugin     string
	NetworkPluginMode string
}

func (p *DefaultProvider) createNetworkInterface(ctx context.Context, opts *createNICOptions) (string, error) {
	nic := p.newNetworkInterfaceForVM(opts)
	p.applyTemplateToNic(&nic, opts.LaunchTemplate)
	logging.FromContext(ctx).Debugf("Creating network interface %s", opts.NICName)
	res, err := createNic(ctx, p.azClient.networkInterfacesClient, p.resourceGroup, opts.NICName, nic)
	if err != nil {
		return "", err
	}
	logging.FromContext(ctx).Debugf("Successfully created network interface: %v", *res.ID)
	return *res.ID, nil
}

func (p *DefaultProvider) GetNic(ctx context.Context, rg, nicName string) (*armnetwork.Interface, error) {
	nicResponse, err := p.azClient.networkInterfacesClient.Get(ctx, rg, nicName, nil)
	if err != nil {
		return nil, err
	}
	return &nicResponse.Interface, nil
}

// newVMObject is a helper func that creates a new armcompute.VirtualMachine
// from key input.
func newVMObject(
	ctx context.Context,
	vmName,
	nicReference,
	zone,
	capacityType,
	location,
	sshPublicKey string,
	nodeIdentities []string,
	nodeClass *v1alpha2.AKSNodeClass,
	launchTemplate *launchtemplate.Template,
	instanceType *corecloudprovider.InstanceType,
	provisionMode string) armcompute.VirtualMachine {
	if launchTemplate.IsWindows {
		return armcompute.VirtualMachine{} // TODO(Windows)
	}

	vm := armcompute.VirtualMachine{
		Location: lo.ToPtr(location),
		Identity: ConvertToVirtualMachineIdentity(nodeIdentities),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{
				VMSize: lo.ToPtr(armcompute.VirtualMachineSizeTypes(instanceType.Name)),
			},

			StorageProfile: &armcompute.StorageProfile{
				OSDisk: &armcompute.OSDisk{
					Name:         lo.ToPtr(vmName),
					DiskSizeGB:   nodeClass.Spec.OSDiskSizeGB,
					CreateOption: lo.ToPtr(armcompute.DiskCreateOptionTypesFromImage),
					DeleteOption: lo.ToPtr(armcompute.DiskDeleteOptionTypesDelete),
				},
			},

			NetworkProfile: &armcompute.NetworkProfile{
				NetworkInterfaces: []*armcompute.NetworkInterfaceReference{
					{
						ID: &nicReference,
						Properties: &armcompute.NetworkInterfaceReferenceProperties{
							Primary:      lo.ToPtr(true),
							DeleteOption: lo.ToPtr(armcompute.DeleteOptionsDelete),
						},
					},
				},
			},

			OSProfile: &armcompute.OSProfile{
				AdminUsername: lo.ToPtr("azureuser"),
				ComputerName:  &vmName,
				LinuxConfiguration: &armcompute.LinuxConfiguration{
					DisablePasswordAuthentication: lo.ToPtr(true),
					SSH: &armcompute.SSHConfiguration{
						PublicKeys: []*armcompute.SSHPublicKey{
							{
								KeyData: lo.ToPtr(sshPublicKey),
								Path:    lo.ToPtr("/home/" + "azureuser" + "/.ssh/authorized_keys"),
							},
						},
					},
				},
			},
			Priority: lo.ToPtr(armcompute.VirtualMachinePriorityTypes(
				CapacityTypeToPriority[capacityType]),
			),
		},
		Zones: lo.Ternary(len(zone) > 0, []*string{&zone}, []*string{}),
		Tags:  launchTemplate.Tags,
	}
	setVMPropertiesOSDiskType(vm.Properties, launchTemplate.StorageProfile)
	setImageReference(ctx, vm.Properties, launchTemplate.ImageID)
	setVMPropertiesBillingProfile(vm.Properties, capacityType)

	if provisionMode == consts.ProvisionModeBootstrappingClient {
		vm.Properties.OSProfile.CustomData = lo.ToPtr(launchTemplate.CustomScriptsCustomData)
	} else {
		vm.Properties.OSProfile.CustomData = lo.ToPtr(launchTemplate.ScriptlessCustomData)
	}

	return vm
}

// setVMPropertiesOSDiskType enables ephemeral os disk for instance types that support it
func setVMPropertiesOSDiskType(vmProperties *armcompute.VirtualMachineProperties, storageProfile string) {
	if storageProfile == "Ephemeral" {
		vmProperties.StorageProfile.OSDisk.DiffDiskSettings = &armcompute.DiffDiskSettings{
			Option: lo.ToPtr(armcompute.DiffDiskOptionsLocal),
			// placement (cache/resource) is left to CRP
		}
		vmProperties.StorageProfile.OSDisk.Caching = lo.ToPtr(armcompute.CachingTypesReadOnly)
	}
}

// setImageReference sets the image reference for the VM based on if we are using self hosted karpenter or the node auto provisioning addon
func setImageReference(ctx context.Context, vmProperties *armcompute.VirtualMachineProperties, imageID string) {
	if options.FromContext(ctx).UseSIG {
		vmProperties.StorageProfile.ImageReference = &armcompute.ImageReference{
			ID: lo.ToPtr(imageID),
		}
		return
	}
	vmProperties.StorageProfile.ImageReference = &armcompute.ImageReference{
		CommunityGalleryImageID: lo.ToPtr(imageID),
	}
	return
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

// setNodePoolNameTag sets "karpenter.sh/nodepool" tag
func setNodePoolNameTag(tags map[string]*string, nodeClaim *karpv1.NodeClaim) {
	if val, ok := nodeClaim.Labels[karpv1.NodePoolLabelKey]; ok {
		tags[NodePoolTagKey] = &val
	}
}

func (p *DefaultProvider) createVirtualMachine(ctx context.Context, vm armcompute.VirtualMachine, vmName string) (*armcompute.VirtualMachine, error) {
	result, err := CreateVirtualMachine(ctx, p.azClient.virtualMachinesClient, p.resourceGroup, vmName, vm)
	if err != nil {
		logging.FromContext(ctx).Errorf("Creating virtual machine %q failed: %v", vmName, err)
		return nil, fmt.Errorf("virtualMachine.BeginCreateOrUpdate for VM %q failed: %w", vmName, err)
	}
	logging.FromContext(ctx).Debugf("Created virtual machine %s", *result.ID)
	return result, nil
}

func (p *DefaultProvider) launchInstance(
	ctx context.Context, nodeClass *v1alpha2.AKSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*corecloudprovider.InstanceType) (*armcompute.VirtualMachine, *corecloudprovider.InstanceType, error) {
	instanceType, capacityType, zone := p.pickSkuSizePriorityAndZone(ctx, nodeClaim, instanceTypes)
	if instanceType == nil {
		return nil, nil, corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("no instance types available"))
	}
	launchTemplate, err := p.getLaunchTemplate(ctx, nodeClass, nodeClaim, instanceType, capacityType)
	if err != nil {
		return nil, nil, fmt.Errorf("getting launch template: %w", err)
	}

	// set provisioner tag for NIC, VM, and Disk
	setNodePoolNameTag(launchTemplate.Tags, nodeClaim)

	// resourceName for the NIC, VM, and Disk
	resourceName := GenerateResourceName(nodeClaim.Name)

	// create network interface
	backendPools, err := p.loadBalancerProvider.LoadBalancerBackendPools(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("getting backend pools: %w", err)
	}
	nicReference, err := p.createNetworkInterface(ctx,
		&createNICOptions{
			NICName:           resourceName,
			NetworkPlugin:     options.FromContext(ctx).NetworkPlugin,
			NetworkPluginMode: options.FromContext(ctx).NetworkPluginMode,
			LaunchTemplate:    launchTemplate,
			BackendPools:      backendPools,
			InstanceType:      instanceType,
		},
	)
	if err != nil {
		return nil, nil, err
	}

	sshPublicKey := options.FromContext(ctx).SSHPublicKey
	nodeIdentityIDs := options.FromContext(ctx).NodeIdentities
	vm := newVMObject(ctx, resourceName, nicReference, zone, capacityType, p.location, sshPublicKey, nodeIdentityIDs, nodeClass, launchTemplate, instanceType, p.provisionMode)

	logging.FromContext(ctx).Debugf("Creating virtual machine %s (%s)", resourceName, instanceType.Name)
	// Uses AZ Client to create a new virtual machine using the vm object we prepared earlier
	resp, err := p.createVirtualMachine(ctx, vm, resourceName)
	if err != nil {
		azErr := p.handleResponseErrors(ctx, instanceType, zone, capacityType, err)
		return nil, nil, azErr
	}

	if p.provisionMode == consts.ProvisionModeBootstrappingClient {
		err = p.createCSExtension(ctx, resourceName, launchTemplate.CustomScriptsCSE, launchTemplate.IsWindows)
		if err != nil {
			// This should fall back to cleanupAzureResources
			return nil, nil, err
		}
	}

	err = p.createAKSIdentifyingExtension(ctx, resourceName)
	if err != nil {
		return nil, nil, err
	}
	return resp, instanceType, nil
}

// nolint:gocyclo
func (p *DefaultProvider) handleResponseErrors(ctx context.Context, instanceType *corecloudprovider.InstanceType, zone, capacityType string, err error) error {
	if sdkerrors.LowPriorityQuotaHasBeenReached(err) {
		// Mark in cache that spot quota has been reached for this subscription
		p.unavailableOfferings.MarkSpotUnavailableWithTTL(ctx, SubscriptionQuotaReachedTTL)

		logging.FromContext(ctx).Error(err)
		return fmt.Errorf("this subscription has reached the regional vCPU quota for spot (LowPriorityQuota). To scale beyond this limit, please review the quota increase process here: https://docs.microsoft.com/en-us/azure/azure-portal/supportability/low-priority-quota")
	}
	if sdkerrors.SKUFamilyQuotaHasBeenReached(err) {
		// Subscription quota has been reached for this VM SKU, mark the instance type as unavailable in all zones available to the offering
		// This will also update the TTL for an existing offering in the cache that is already unavailable

		logging.FromContext(ctx).Error(err)
		for _, offering := range instanceType.Offerings {
			if getOfferingCapacityType(offering) != capacityType {
				continue
			}
			// If we have a quota limit of 0 vcpus, we mark the offerings unavailable for an hour.
			// CPU limits of 0 are usually due to a subscription having no allocated quota for that instance type at all on the subscription.
			if cpuLimitIsZero(err) {
				p.unavailableOfferings.MarkUnavailableWithTTL(ctx, SubscriptionQuotaReachedReason, instanceType.Name, getOfferingZone(offering), capacityType, SubscriptionQuotaReachedTTL)
			} else {
				p.unavailableOfferings.MarkUnavailable(ctx, SubscriptionQuotaReachedReason, instanceType.Name, getOfferingZone(offering), capacityType)
			}
		}
		return fmt.Errorf("subscription level %s vCPU quota for %s has been reached (may try provision an alternative instance type)", capacityType, instanceType.Name)
	}
	if sdkerrors.IsSKUNotAvailable(err) {
		// https://aka.ms/azureskunotavailable: either not available for a location or zone, or out of capacity for Spot.
		// We only expect to observe the Spot case, not location or zone restrictions, because:
		// - SKUs with location restriction are already filtered out via sku.HasLocationRestriction
		// - zonal restrictions are filtered out internally by sku.AvailabilityZones, and don't get offerings
		skuNotAvailableTTL := SKUNotAvailableSpotTTL
		err = fmt.Errorf("out of spot capacity for %s: %w", instanceType.Name, err)
		if capacityType == karpv1.CapacityTypeOnDemand { // should not happen, defensive check
			err = fmt.Errorf("unexpected SkuNotAvailable error for %s (on-demand): %w", instanceType.Name, err)
			skuNotAvailableTTL = SKUNotAvailableOnDemandTTL // still mark all offerings as unavailable, but with a longer TTL
		}
		// mark the instance type as unavailable for all offerings/zones for the capacity type
		for _, offering := range instanceType.Offerings {
			if getOfferingCapacityType(offering) != capacityType {
				continue
			}
			p.unavailableOfferings.MarkUnavailableWithTTL(ctx, SKUNotAvailableReason, instanceType.Name, getOfferingZone(offering), capacityType, skuNotAvailableTTL)
		}

		logging.FromContext(ctx).Error(err)
		return fmt.Errorf("the requested SKU is unavailable for instance type %s in zone %s with capacity type %s, for more details please visit: https://aka.ms/azureskunotavailable", instanceType.Name, zone, capacityType)
	}
	if sdkerrors.ZonalAllocationFailureOccurred(err) {
		logging.FromContext(ctx).With("zone", zone).Error(err)
		p.unavailableOfferings.MarkUnavailable(ctx, ZonalAllocationFailureReason, instanceType.Name, zone, karpv1.CapacityTypeOnDemand)
		p.unavailableOfferings.MarkUnavailable(ctx, ZonalAllocationFailureReason, instanceType.Name, zone, karpv1.CapacityTypeSpot)

		return fmt.Errorf("unable to allocate resources in the selected zone (%s). (will try a different zone to fulfill your request)", zone)
	}
	if sdkerrors.RegionalQuotaHasBeenReached(err) {
		logging.FromContext(ctx).Error(err)
		// InsufficientCapacityError is appropriate here because trying any other instance type will not help
		return corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("regional %s vCPU quota limit for subscription has been reached. To scale beyond this limit, please review the quota increase process here: https://learn.microsoft.com/en-us/azure/quotas/regional-quota-requests", capacityType))
	}
	return err
}

func cpuLimitIsZero(err error) bool {
	return strings.Contains(err.Error(), "Current Limit: 0")
}

func (p *DefaultProvider) applyTemplateToNic(nic *armnetwork.Interface, template *launchtemplate.Template) {
	// set tags
	nic.Tags = template.Tags
	for _, ipConfig := range nic.Properties.IPConfigurations {
		ipConfig.Properties.Subnet = &armnetwork.Subnet{ID: &template.SubnetID}
	}
}

func (p *DefaultProvider) getLaunchTemplate(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass, nodeClaim *karpv1.NodeClaim,
	instanceType *corecloudprovider.InstanceType, capacityType string) (*launchtemplate.Template, error) {
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
func (p *DefaultProvider) pickSkuSizePriorityAndZone(ctx context.Context, nodeClaim *karpv1.NodeClaim, instanceTypes []*corecloudprovider.InstanceType) (*corecloudprovider.InstanceType, string, string) {
	if len(instanceTypes) == 0 {
		return nil, "", ""
	}
	// InstanceType/VM SKU - just pick the first one for now. They are presorted by cheapest offering price (taking node requirements into account)
	instanceType := instanceTypes[0]
	logging.FromContext(ctx).Infof("Selected instance type %s", instanceType.Name)
	// Priority - Nodepool defaults to Regular, so pick Spot if it is explicitly included in requirements (and is offered in at least one zone)
	priority := p.getPriorityForInstanceType(nodeClaim, instanceType)
	// Zone - ideally random/spread from requested zones that support given Priority
	requestedZones := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...).Get(v1.LabelTopologyZone)
	priorityOfferings := lo.Filter(instanceType.Offerings.Available(), func(o corecloudprovider.Offering, _ int) bool {
		return getOfferingCapacityType(o) == priority && requestedZones.Has(getOfferingZone(o))
	})
	zonesWithPriority := lo.Map(priorityOfferings, func(o corecloudprovider.Offering, _ int) string { return getOfferingZone(o) })
	if zone, ok := sets.New(zonesWithPriority...).PopAny(); ok {
		if len(zone) > 0 {
			// Zones in zonal Offerings have <region>-<number> format; the zone returned from here will be used for VM instantiation,
			// which expects just the zone number, without region
			zone = string(zone[len(zone)-1])
		}
		return instanceType, priority, zone
	}
	return nil, "", ""
}

func (p *DefaultProvider) cleanupAzureResources(ctx context.Context, resourceName string) (err error) {
	vmErr := deleteVirtualMachineIfExists(ctx, p.azClient.virtualMachinesClient, p.resourceGroup, resourceName)
	if vmErr != nil {
		logging.FromContext(ctx).Errorf("virtualMachine.Delete for %s failed: %v", resourceName, vmErr)
	}
	// The order here is intentional, if the VM was created successfully, then we attempt to delete the vm, the
	// nic, disk and all associated resources will be removed. If the VM was not created successfully and a nic was found,
	// then we attempt to delete the nic.
	nicErr := deleteNicIfExists(ctx, p.azClient.networkInterfacesClient, p.resourceGroup, resourceName)
	if nicErr != nil {
		logging.FromContext(ctx).Errorf("networkInterface.Delete for %s failed: %v", resourceName, nicErr)
	}

	return errors.Join(vmErr, nicErr)
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

func (p *DefaultProvider) getAKSIdentifyingExtension() *armcompute.VirtualMachineExtension {
	const (
		vmExtensionType                  = "Microsoft.Compute/virtualMachines/extensions"
		aksIdentifyingExtensionName      = "computeAksLinuxBilling"
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
	}

	return vmExtension
}

func (p *DefaultProvider) getCSExtension(cse string, isWindows bool) *armcompute.VirtualMachineExtension {
	const (
		vmExtensionType     = "Microsoft.Compute/virtualMachines/extensions"
		cseNameWindows      = "windows-cse-agent-karpenter"
		cseTypeWindows      = "CustomScriptExtension"
		csePublisherWindows = "Microsoft.Compute"
		cseVersionWindows   = "1.10"
		cseNameLinux        = "cse-agent-karpenter"
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
	}
}

// GetZoneID returns the zone ID for the given virtual machine, or an empty string if there is no zone specified
func GetZoneID(vm *armcompute.VirtualMachine) (string, error) {
	if vm == nil {
		return "", fmt.Errorf("cannot pass in a nil virtual machine")
	}
	if vm.Name == nil {
		return "", fmt.Errorf("virtual machine is missing name")
	}
	if vm.Zones == nil {
		return "", nil
	}
	if len(vm.Zones) == 1 {
		return *(vm.Zones)[0], nil
	}
	if len(vm.Zones) > 1 {
		return "", fmt.Errorf("virtual machine %v has multiple zones", *vm.Name)
	}
	return "", nil
}

func GetListQueryBuilder(rg string) *kql.Builder {
	return kql.New(`Resources`).
		AddLiteral(` | where type == "microsoft.compute/virtualmachines"`).
		AddLiteral(` | where resourceGroup == `).AddString(strings.ToLower(rg)). // ARG VMs appear to have lowercase RG
		AddLiteral(` | where tags has_cs `).AddString(NodePoolTagKey)
}

func createVMFromQueryResponseData(data map[string]interface{}) (*armcompute.VirtualMachine, error) {
	jsonString, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	vm := armcompute.VirtualMachine{}
	err = json.Unmarshal(jsonString, &vm)
	if err != nil {
		return nil, err
	}
	if vm.ID == nil {
		return nil, fmt.Errorf("virtual machine is missing id")
	}
	if vm.Name == nil {
		return nil, fmt.Errorf("virtual machine is missing name")
	}
	if vm.Tags == nil {
		return nil, fmt.Errorf("virtual machine is missing tags")
	}
	// We see inconsistent casing being returned by ARG for the last segment
	// of the vm.ID string. This forces it to be lowercase.
	parts := strings.Split(lo.FromPtr(vm.ID), "/")
	parts[len(parts)-1] = strings.ToLower(parts[len(parts)-1])
	vm.ID = lo.ToPtr(strings.Join(parts, "/"))
	return &vm, nil
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

func getOfferingCapacityType(offering corecloudprovider.Offering) string {
	return offering.Requirements.Get(karpv1.CapacityTypeLabelKey).Any()
}

func getOfferingZone(offering corecloudprovider.Offering) string {
	return offering.Requirements.Get(v1.LabelTopologyZone).Any()
}
