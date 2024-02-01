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
	"strconv"
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

	corecloudprovider "github.com/aws/karpenter-core/pkg/cloudprovider"
	"github.com/aws/karpenter-core/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/aws/karpenter-core/pkg/apis/v1alpha5"
	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"

	//nolint SA1019 - deprecated package
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2022-08-01/compute"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

var (
	NodePoolTagKey = strings.ReplaceAll(corev1beta1.NodePoolLabelKey, "/", "_")
	listQuery      string

	CapacityTypeToPriority = map[string]string{
		corev1beta1.CapacityTypeSpot:     string(compute.Spot),
		corev1beta1.CapacityTypeOnDemand: string(compute.Regular),
	}
	PriorityToCapacityType = map[string]string{
		string(compute.Spot):    corev1beta1.CapacityTypeSpot,
		string(compute.Regular): corev1beta1.CapacityTypeOnDemand,
	}

	SubscriptionQuotaReachedReason = "SubscriptionQuotaReached"
	ZonalAllocationFailureReason   = "ZonalAllocationFailure"
	SKUNotAvailableReason          = "SKUNotAvailable"

	SubscriptionQuotaReachedTTL = 1 * time.Hour
	SKUNotAvailableSpotTTL      = 1 * time.Hour
	SKUNotAvailableOnDemandTTL  = 23 * time.Hour
)

type Resource = map[string]interface{}

type Provider struct {
	location               string
	azClient               *AZClient
	instanceTypeProvider   *instancetype.Provider
	launchTemplateProvider *launchtemplate.Provider
	loadBalancerProvider   *loadbalancer.Provider
	resourceGroup          string
	subnetID               string
	subscriptionID         string
	unavailableOfferings   *cache.UnavailableOfferings
}

func NewProvider(
	_ context.Context, // TODO: Why is this here?
	azClient *AZClient,
	instanceTypeProvider *instancetype.Provider,
	launchTemplateProvider *launchtemplate.Provider,
	loadBalancerProvider *loadbalancer.Provider,
	offeringsCache *cache.UnavailableOfferings,
	location string,
	resourceGroup string,
	subnetID string,
	subscriptionID string,
) *Provider {
	listQuery = GetListQueryBuilder(resourceGroup).String()
	return &Provider{
		azClient:               azClient,
		instanceTypeProvider:   instanceTypeProvider,
		launchTemplateProvider: launchTemplateProvider,
		loadBalancerProvider:   loadBalancerProvider,
		location:               location,
		resourceGroup:          resourceGroup,
		subnetID:               subnetID,
		subscriptionID:         subscriptionID,
		unavailableOfferings:   offeringsCache,
	}
}

// Create an instance given the constraints.
// instanceTypes should be sorted by priority for spot capacity type.
func (p *Provider) Create(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass, nodeClaim *corev1beta1.NodeClaim, instanceTypes []*corecloudprovider.InstanceType) (*armcompute.VirtualMachine, error) {
	instanceTypes = orderInstanceTypesByPrice(instanceTypes, scheduling.NewNodeSelectorRequirements(nodeClaim.Spec.Requirements...))
	vm, instanceType, err := p.launchInstance(ctx, nodeClass, nodeClaim, instanceTypes)
	if err != nil {
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

func (p *Provider) Update(ctx context.Context, vmName string, update armcompute.VirtualMachineUpdate) error {
	return UpdateVirtualMachine(ctx, p.azClient.virtualMachinesClient, p.resourceGroup, vmName, update)
}

func (p *Provider) Get(ctx context.Context, vmName string) (*armcompute.VirtualMachine, error) {
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

func (p *Provider) List(ctx context.Context) ([]*armcompute.VirtualMachine, error) {
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

func (p *Provider) Delete(ctx context.Context, resourceName string) error {
	logging.FromContext(ctx).Debugf("Deleting virtual machine %s and associated resources")
	return p.cleanupAzureResources(ctx, resourceName)
}

// createAKSIdentifyingExtension attaches a VM extension to identify that this VM participates in an AKS cluster
func (p *Provider) createAKSIdentifyingExtension(ctx context.Context, vmName string) (err error) {
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

func (p *Provider) newNetworkInterfaceForVM(vmName string, backendPools *loadbalancer.BackendAddressPools, instanceType *corecloudprovider.InstanceType) armnetwork.Interface {
	var ipv4BackendPools []*armnetwork.BackendAddressPool
	for _, poolID := range backendPools.IPv4PoolIDs {
		poolID := poolID
		ipv4BackendPools = append(ipv4BackendPools, &armnetwork.BackendAddressPool{
			ID: &poolID,
		})
	}

	skuAcceleratedNetworkingRequirements := scheduling.NewRequirements(scheduling.NewRequirement(v1alpha2.LabelSKUAcceleratedNetworking, v1.NodeSelectorOpIn, "true"))

	enableAcceleratedNetworking := false
	if err := instanceType.Requirements.Compatible(skuAcceleratedNetworkingRequirements); err == nil {
		enableAcceleratedNetworking = true
	}

	return armnetwork.Interface{
		Location: to.Ptr(p.location),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
				{
					Name: &vmName,
					Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
						Primary:                   to.Ptr(true),
						PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodDynamic),
						Subnet: &armnetwork.Subnet{
							ID: &p.subnetID,
						},
						LoadBalancerBackendAddressPools: ipv4BackendPools,
					},
				},
			},
			EnableAcceleratedNetworking: to.Ptr(enableAcceleratedNetworking),
			EnableIPForwarding:          to.Ptr(true),
		},
	}
}

func GenerateResourceName(nodeClaimName string) string {
	return fmt.Sprintf("aks-%s", nodeClaimName)
}

func (p *Provider) createNetworkInterface(ctx context.Context, nicName string, launchTemplateConfig *launchtemplate.Template, instanceType *corecloudprovider.InstanceType) (string, error) {
	backendPools, err := p.loadBalancerProvider.LoadBalancerBackendPools(ctx)
	if err != nil {
		return "", err
	}

	nic := p.newNetworkInterfaceForVM(nicName, backendPools, instanceType)
	p.applyTemplateToNic(&nic, launchTemplateConfig)
	logging.FromContext(ctx).Debugf("Creating network interface %s", nicName)
	res, err := createNic(ctx, p.azClient.networkInterfacesClient, p.resourceGroup, nicName, nic)
	if err != nil {
		return "", err
	}
	logging.FromContext(ctx).Debugf("Successfully created network interface: %v", *res.ID)
	return *res.ID, nil
}

// newVMObject is a helper func that creates a new armcompute.VirtualMachine
// from key input.
func newVMObject(
	vmName,
	nicReference,
	zone,
	capacityType string,
	location string,
	sshPublicKey string,
	nodeIdentities []string,
	nodeClass *v1alpha2.AKSNodeClass,
	nodeClaim *corev1beta1.NodeClaim,
	launchTemplate *launchtemplate.Template,
	instanceType *corecloudprovider.InstanceType) armcompute.VirtualMachine {
	// Build the image reference from template
	imageReference := armcompute.ImageReference{}
	if v1alpha2.IsComputeGalleryImageID(launchTemplate.ImageID) {
		imageReference.ID = &launchTemplate.ImageID
	} else {
		imageReference.CommunityGalleryImageID = &launchTemplate.ImageID
	}
	vm := armcompute.VirtualMachine{
		Location: to.Ptr(location),
		Identity: ConvertToVirtualMachineIdentity(nodeIdentities),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{
				VMSize: to.Ptr(armcompute.VirtualMachineSizeTypes(instanceType.Name)),
			},

			StorageProfile: &armcompute.StorageProfile{
				OSDisk: &armcompute.OSDisk{
					Name:         to.Ptr(vmName),
					DiskSizeGB:   nodeClass.Spec.OSDiskSizeGB,
					CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesFromImage),
					DeleteOption: to.Ptr(armcompute.DiskDeleteOptionTypesDelete),
				},
				ImageReference: &imageReference,
			},

			NetworkProfile: &armcompute.NetworkProfile{
				NetworkInterfaces: []*armcompute.NetworkInterfaceReference{
					{
						ID: &nicReference,
						Properties: &armcompute.NetworkInterfaceReferenceProperties{
							Primary:      to.Ptr(true),
							DeleteOption: to.Ptr(armcompute.DeleteOptionsDelete),
						},
					},
				},
			},

			OSProfile: &armcompute.OSProfile{
				AdminUsername: to.Ptr("azureuser"),
				ComputerName:  &vmName,
				LinuxConfiguration: &armcompute.LinuxConfiguration{
					DisablePasswordAuthentication: to.Ptr(true),
					SSH: &armcompute.SSHConfiguration{
						PublicKeys: []*armcompute.SSHPublicKey{
							{
								KeyData: to.Ptr(sshPublicKey),
								Path:    to.Ptr("/home/" + "azureuser" + "/.ssh/authorized_keys"),
							},
						},
					},
				},
				CustomData: to.Ptr(launchTemplate.UserData),
			},
			Priority: to.Ptr(armcompute.VirtualMachinePriorityTypes(
				CapacityTypeToPriority[capacityType]),
			),
		},
		Zones: lo.Ternary(len(zone) > 0, []*string{&zone}, []*string{}),
		Tags:  launchTemplate.Tags,
	}
	setVMPropertiesStorageProfile(vm.Properties, instanceType, nodeClass)
	setVMPropertiesBillingProfile(vm.Properties, capacityType)
	setVMTagsProvisionerName(vm.Tags, nodeClaim)

	return vm
}

// setVMPropertiesStorageProfile enables ephemeral os disk for instance types that support it
func setVMPropertiesStorageProfile(vmProperties *armcompute.VirtualMachineProperties, instanceType *corecloudprovider.InstanceType, nodeClass *v1alpha2.AKSNodeClass) {
	// use ephemeral disk if it is large enough
	if *nodeClass.Spec.OSDiskSizeGB <= getEphemeralMaxSizeGB(instanceType) {
		vmProperties.StorageProfile.OSDisk.DiffDiskSettings = &armcompute.DiffDiskSettings{
			Option: to.Ptr(armcompute.DiffDiskOptionsLocal),
			// placement (cache/resource) is left to CRP
		}
		vmProperties.StorageProfile.OSDisk.Caching = to.Ptr(armcompute.CachingTypesReadOnly)
	}
}

// setVMPropertiesBillingProfile sets a default MaxPrice of -1 for Spot
func setVMPropertiesBillingProfile(vmProperties *armcompute.VirtualMachineProperties, capacityType string) {
	if capacityType == v1alpha5.CapacityTypeSpot {
		vmProperties.EvictionPolicy = to.Ptr(armcompute.VirtualMachineEvictionPolicyTypesDelete)
		vmProperties.BillingProfile = &armcompute.BillingProfile{
			MaxPrice: to.Ptr(float64(-1)),
		}
	}
}

// setVMTagsProvisionerName sets "karpenter.sh/provisioner-name" tag
func setVMTagsProvisionerName(tags map[string]*string, nodeClaim *corev1beta1.NodeClaim) {
	if val, ok := nodeClaim.Labels[corev1beta1.NodePoolLabelKey]; ok {
		tags[NodePoolTagKey] = &val
	}
}

func (p *Provider) createVirtualMachine(ctx context.Context, vm armcompute.VirtualMachine, vmName string) (*armcompute.VirtualMachine, error) {
	result, err := CreateVirtualMachine(ctx, p.azClient.virtualMachinesClient, p.resourceGroup, vmName, vm)
	if err != nil {
		logging.FromContext(ctx).Errorf("Creating virtual machine %q failed: %v", vmName, err)
		return nil, fmt.Errorf("virtualMachine.BeginCreateOrUpdate for VM %q failed: %w", vmName, err)
	}
	logging.FromContext(ctx).Debugf("Created virtual machine %s", *result.ID)
	return result, nil
}

func (p *Provider) launchInstance(
	ctx context.Context, nodeClass *v1alpha2.AKSNodeClass, nodeClaim *corev1beta1.NodeClaim, instanceTypes []*corecloudprovider.InstanceType) (*armcompute.VirtualMachine, *corecloudprovider.InstanceType, error) {
	instanceType, capacityType, zone := p.pickSkuSizePriorityAndZone(ctx, nodeClaim, instanceTypes)
	if instanceType == nil {
		return nil, nil, corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("no instance types available"))
	}
	launchTemplate, err := p.getLaunchTemplate(ctx, nodeClass, nodeClaim, instanceType, capacityType)
	if err != nil {
		return nil, nil, fmt.Errorf("getting launch template: %w", err)
	}
	// resourceName for the NIC, VM, and Disk
	resourceName := GenerateResourceName(nodeClaim.Name)

	// create network interface
	nicReference, err := p.createNetworkInterface(ctx, resourceName, launchTemplate, instanceType)
	if err != nil {
		return nil, nil, err
	}

	sshPublicKey := options.FromContext(ctx).SSHPublicKey
	nodeIdentityIDs := options.FromContext(ctx).NodeIdentities
	vm := newVMObject(resourceName, nicReference, zone, capacityType, p.location, sshPublicKey, nodeIdentityIDs, nodeClass, nodeClaim, launchTemplate, instanceType)

	logging.FromContext(ctx).Debugf("Creating virtual machine %s (%s)", resourceName, instanceType.Name)
	// Uses AZ Client to create a new virtual machine using the vm object we prepared earlier
	resp, err := p.createVirtualMachine(ctx, vm, resourceName)
	if err != nil {
		azErr := p.handleResponseErrors(ctx, instanceType, zone, capacityType, err)
		return nil, nil, azErr
	}

	err = p.createAKSIdentifyingExtension(ctx, resourceName)
	if err != nil {
		return nil, nil, err
	}
	return resp, instanceType, nil
}

// nolint:gocyclo
func (p *Provider) handleResponseErrors(ctx context.Context, instanceType *corecloudprovider.InstanceType, zone, capacityType string, err error) error {
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
			if offering.CapacityType != capacityType {
				continue
			}
			// If we have a quota limit of 0 vcpus, we mark the offerings unavailable for an hour.
			// CPU limits of 0 are usually due to a subscription having no allocated quota for that instance type at all on the subscription.
			if cpuLimitIsZero(err) {
				p.unavailableOfferings.MarkUnavailableWithTTL(ctx, SubscriptionQuotaReachedReason, instanceType.Name, offering.Zone, capacityType, SubscriptionQuotaReachedTTL)
			} else {
				p.unavailableOfferings.MarkUnavailable(ctx, SubscriptionQuotaReachedReason, instanceType.Name, offering.Zone, capacityType)
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
		if capacityType == corev1beta1.CapacityTypeOnDemand { // should not happen, defensive check
			err = fmt.Errorf("unexpected SkuNotAvailable error for %s (on-demand): %w", instanceType.Name, err)
			skuNotAvailableTTL = SKUNotAvailableOnDemandTTL // still mark all offerings as unavailable, but with a longer TTL
		}
		// mark the instance type as unavailable for all offerings/zones for the capacity type
		for _, offering := range instanceType.Offerings {
			if offering.CapacityType != capacityType {
				continue
			}
			p.unavailableOfferings.MarkUnavailableWithTTL(ctx, SKUNotAvailableReason, instanceType.Name, offering.Zone, capacityType, skuNotAvailableTTL)
		}

		logging.FromContext(ctx).Error(err)
		return fmt.Errorf("the requested SKU is unavailable for instance type %s in zone %s with capacity type %s, for more details please visit: https://aka.ms/azureskunotavailable", instanceType.Name, zone, capacityType)
	}
	if sdkerrors.ZonalAllocationFailureOccurred(err) {
		logging.FromContext(ctx).With("zone", zone).Error(err)
		p.unavailableOfferings.MarkUnavailable(ctx, ZonalAllocationFailureReason, instanceType.Name, zone, corev1beta1.CapacityTypeOnDemand)
		p.unavailableOfferings.MarkUnavailable(ctx, ZonalAllocationFailureReason, instanceType.Name, zone, corev1beta1.CapacityTypeSpot)

		return fmt.Errorf("unable to allocate resources in the selected zone (%s). (will try a different zone to fulfill your request)", zone)
	}
	if sdkerrors.RegionalQuotaHasBeenReached(err) {
		logging.FromContext(ctx).Error(err)
		// InsufficientCapacityError is appropriate here because trying any other instance type will not help
		return corecloudprovider.NewInsufficientCapacityError(fmt.Errorf("regional %s vCPU quota limit for subscription has been reached. To scale beyond this limit, please review the quota increase process here: https://learn.microsoft.com/en-us/azure/quotas/regional-quota-requests", capacityType))
	}
	return err
}

func getEphemeralMaxSizeGB(instanceType *corecloudprovider.InstanceType) int32 {
	reqs := instanceType.Requirements.Get(v1alpha2.LabelSKUStorageEphemeralOSMaxSize).Values()
	if len(reqs) == 0 || len(reqs) > 1 {
		return 0
	}
	maxSize, err := strconv.ParseFloat(reqs[0], 32)
	if err != nil {
		return 0
	}
	// decimal places are truncated, so we round down
	return int32(maxSize)
}

func cpuLimitIsZero(err error) bool {
	return strings.Contains(err.Error(), "Current Limit: 0")
}

func (p *Provider) applyTemplateToNic(nic *armnetwork.Interface, template *launchtemplate.Template) {
	// set tags
	nic.Tags = template.Tags
}

func (p *Provider) getLaunchTemplate(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass, nodeClaim *corev1beta1.NodeClaim,
	instanceType *corecloudprovider.InstanceType, capacityType string) (*launchtemplate.Template, error) {
	additionalLabels := lo.Assign(GetAllSingleValuedRequirementLabels(instanceType), map[string]string{corev1beta1.CapacityTypeLabelKey: capacityType})

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
func (p *Provider) pickSkuSizePriorityAndZone(ctx context.Context, nodeClaim *corev1beta1.NodeClaim, instanceTypes []*corecloudprovider.InstanceType) (*corecloudprovider.InstanceType, string, string) {
	if len(instanceTypes) == 0 {
		return nil, "", ""
	}
	// InstanceType/VM SKU - just pick the first one for now. They are presorted by cheapest offering price (taking node requirements into account)
	instanceType := instanceTypes[0]
	logging.FromContext(ctx).Infof("Selected instance type %s", instanceType.Name)
	// Priority - Provisioner defaults to Regular, so pick Spot if it is explicitly included in requirements (and is offered in at least one zone)
	priority := p.getPriorityForInstanceType(nodeClaim, instanceType)
	// Zone - ideally random/spread from requested zones that support given Priority
	requestedZones := scheduling.NewNodeSelectorRequirements(nodeClaim.Spec.Requirements...).Get(v1.LabelTopologyZone)
	priorityOfferings := lo.Filter(instanceType.Offerings.Available(), func(o corecloudprovider.Offering, _ int) bool {
		return o.CapacityType == priority && requestedZones.Has(o.Zone)
	})
	zonesWithPriority := lo.Map(priorityOfferings, func(o corecloudprovider.Offering, _ int) string { return o.Zone })
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

func (p *Provider) cleanupAzureResources(ctx context.Context, resourceName string) (err error) {
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
func (p *Provider) getPriorityForInstanceType(nodeClaim *corev1beta1.NodeClaim, instanceType *corecloudprovider.InstanceType) string {
	requirements := scheduling.NewNodeSelectorRequirements(nodeClaim.
		Spec.Requirements...)

	if requirements.Get(corev1beta1.CapacityTypeLabelKey).Has(corev1beta1.CapacityTypeSpot) {
		for _, offering := range instanceType.Offerings.Available() {
			if requirements.Get(v1.LabelTopologyZone).Has(offering.Zone) && offering.CapacityType == corev1beta1.CapacityTypeSpot {
				return corev1beta1.CapacityTypeSpot
			}
		}
	}
	return corev1beta1.CapacityTypeOnDemand
}

func orderInstanceTypesByPrice(instanceTypes []*corecloudprovider.InstanceType, requirements scheduling.Requirements) []*corecloudprovider.InstanceType {
	// Order instance types so that we get the cheapest instance types of the available offerings
	sort.Slice(instanceTypes, func(i, j int) bool {
		iPrice := math.MaxFloat64
		jPrice := math.MaxFloat64
		if len(instanceTypes[i].Offerings.Available().Requirements(requirements)) > 0 {
			iPrice = instanceTypes[i].Offerings.Available().Requirements(requirements).Cheapest().Price
		}
		if len(instanceTypes[j].Offerings.Available().Requirements(requirements)) > 0 {
			jPrice = instanceTypes[j].Offerings.Available().Requirements(requirements).Cheapest().Price
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

func (p *Provider) getAKSIdentifyingExtension() *armcompute.VirtualMachineExtension {
	const (
		vmExtensionType                  = "Microsoft.Compute/virtualMachines/extensions"
		aksIdentifyingExtensionName      = "computeAksLinuxBilling"
		aksIdentifyingExtensionPublisher = "Microsoft.AKS"
		aksIdentifyingExtensionTypeLinux = "Compute.AKS.Linux.Billing"
	)

	vmExtension := &armcompute.VirtualMachineExtension{
		Location: to.Ptr(p.location),
		Name:     to.Ptr(aksIdentifyingExtensionName),
		Properties: &armcompute.VirtualMachineExtensionProperties{
			Publisher:               to.Ptr(aksIdentifyingExtensionPublisher),
			TypeHandlerVersion:      to.Ptr("1.0"),
			AutoUpgradeMinorVersion: to.Ptr(true),
			Settings:                &map[string]interface{}{},
			Type:                    to.Ptr(aksIdentifyingExtensionTypeLinux),
		},
		Type: to.Ptr(vmExtensionType),
	}

	return vmExtension
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
