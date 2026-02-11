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
	"strings"
	"sync"
	"time"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
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
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/networksecuritygroup"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

var (
	KarpCapacityTypeToVMPriority = map[string]armcompute.VirtualMachinePriorityTypes{
		karpv1.CapacityTypeSpot:     armcompute.VirtualMachinePriorityTypesSpot,
		karpv1.CapacityTypeOnDemand: armcompute.VirtualMachinePriorityTypesRegular,
	}
	VMPriorityToKarpCapacityType = map[armcompute.VirtualMachinePriorityTypes]string{
		armcompute.VirtualMachinePriorityTypesSpot:    karpv1.CapacityTypeSpot,
		armcompute.VirtualMachinePriorityTypesRegular: karpv1.CapacityTypeOnDemand,
	}
	// Note that there is no ScaleSetPriorityToKarpCapacityType because the karpenter.sh/capacity-type
	// label is the "official" label that we actually key priority off of. Selection still works though
	// because when we list instance types on-demand offerings always have v1beta1.ScaleSetPriorityRegular
	// and spot instances always have v1beta1.ScaleSetPrioritySpot, so the correct karpenter.sh/capacity-type
	// label is still selected even if the user is using kubernetes.azure.com/scalesetpriority only on the NodePool.
	VMPriorityToScaleSetPriority = map[armcompute.VirtualMachinePriorityTypes]string{
		armcompute.VirtualMachinePriorityTypesSpot:    v1beta1.ScaleSetPrioritySpot,
		armcompute.VirtualMachinePriorityTypesRegular: v1beta1.ScaleSetPriorityRegular,
	}

	aksIdentifyingExtensionEnvs = sets.New(
		azclient.PublicCloud.Name,
		azclient.ChinaCloud.Name,
		azclient.USGovernmentCloud.Name,
	)
)

const (
	aksIdentifyingExtensionName = "computeAksLinuxBilling"
	// TODO: Why bother with a different CSE name for Windows?
	cseNameWindows = "windows-cse-agent-karpenter"
	cseNameLinux   = "cse-agent-karpenter"
)

// ErrorCodeForMetrics extracts a stable Azure error code for metric labeling when possible.
func ErrorCodeForMetrics(err error) string {
	if err == nil {
		return "UnknownError"
	}
	if azErr := sdkerrors.IsResponseError(err); azErr != nil {
		if azErr.ErrorCode != "" {
			return azErr.ErrorCode
		}
		return "UnknownError"
	}
	return "UnknownError"
}

// GetManagedExtensionNames gets the names of the VM extensions managed by Karpenter.
// This is a set of 1 or 2 extensions (depending on provisionMode): aksIdentifyingExtension and (sometimes) cse.
func GetManagedExtensionNames(provisionMode string, env *auth.Environment) []string {
	var result []string
	// Only including AKS identifying extension in the clouds it is supported in
	if isAKSIdentifyingExtensionEnabled(env) {
		result = append(result, aksIdentifyingExtensionName)
	}
	if provisionMode == consts.ProvisionModeBootstrappingClient {
		result = append(result, cseNameLinux) // TODO: Windows
	}
	return result
}

func isAKSIdentifyingExtensionEnabled(env *auth.Environment) bool {
	return aksIdentifyingExtensionEnvs.Has(env.Environment.Name)
}

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
	azClient                     *AZClient
	clientManager                *AZClientManager
	instanceTypeProvider         instancetype.Provider
	launchTemplateProvider       *launchtemplate.Provider
	loadBalancerProvider         *loadbalancer.Provider
	networkSecurityGroupProvider *networksecuritygroup.Provider
	resourceGroup                string
	subscriptionID               string
	provisionMode                string
	diskEncryptionSetID          string
	errorHandling                *offerings.ResponseErrorHandler
	env                          *auth.Environment

	vmListQuery, nicListQuery string

	// knownSubRGs tracks subscription+resource-group pairs that have active NodeClasses.
	// Used by List to query VMs across all known subscriptions.
	knownSubRGsMu sync.RWMutex
	knownSubRGs   map[subRGKey]struct{}
}

// subRGKey identifies a subscription + resource group combination.
type subRGKey struct {
	subscriptionID string
	resourceGroup  string
}

func NewDefaultVMProvider(
	azClient *AZClient,
	clientManager *AZClientManager,
	instanceTypeProvider instancetype.Provider,
	launchTemplateProvider *launchtemplate.Provider,
	loadBalancerProvider *loadbalancer.Provider,
	networkSecurityGroupProvider *networksecuritygroup.Provider,
	offeringsCache *cache.UnavailableOfferings,
	location string,
	resourceGroup string,
	subscriptionID string,
	provisionMode string,
	diskEncryptionSetID string,
	env *auth.Environment,
) *DefaultVMProvider {
	return &DefaultVMProvider{
		azClient:                     azClient,
		clientManager:                clientManager,
		instanceTypeProvider:         instanceTypeProvider,
		launchTemplateProvider:       launchTemplateProvider,
		loadBalancerProvider:         loadBalancerProvider,
		networkSecurityGroupProvider: networkSecurityGroupProvider,
		location:                     location,
		resourceGroup:                resourceGroup,
		subscriptionID:               subscriptionID,
		provisionMode:                provisionMode,
		diskEncryptionSetID:          diskEncryptionSetID,
		env:                          env,

		vmListQuery:  GetVMListQueryBuilder(resourceGroup).String(),
		nicListQuery: GetNICListQueryBuilder(resourceGroup).String(),

		errorHandling: offerings.NewResponseErrorHandler(offeringsCache),

		knownSubRGs: map[subRGKey]struct{}{
			{subscriptionID: subscriptionID, resourceGroup: resourceGroup}: {},
		},
	}
}

// nodeClassConfig holds the resolved subscription, resource group, and location for a NodeClass.
type nodeClassConfig struct {
	subscriptionID string
	resourceGroup  string
	location       string
}

// resolveNodeClassConfig extracts the effective subscription, resource group, and location
// from an AKSNodeClass, falling back to controller defaults for any unset field.
func (p *DefaultVMProvider) resolveNodeClassConfig(nodeClass *v1beta1.AKSNodeClass) nodeClassConfig {
	cfg := nodeClassConfig{
		subscriptionID: p.subscriptionID,
		resourceGroup:  p.resourceGroup,
		location:       p.location,
	}
	if nodeClass.Spec.SubscriptionID != nil && lo.FromPtr(nodeClass.Spec.SubscriptionID) != "" {
		cfg.subscriptionID = lo.FromPtr(nodeClass.Spec.SubscriptionID)
	}
	if nodeClass.Spec.ResourceGroup != nil && lo.FromPtr(nodeClass.Spec.ResourceGroup) != "" {
		cfg.resourceGroup = lo.FromPtr(nodeClass.Spec.ResourceGroup)
	}
	if nodeClass.Spec.Location != nil && lo.FromPtr(nodeClass.Spec.Location) != "" {
		cfg.location = lo.FromPtr(nodeClass.Spec.Location)
	}
	return cfg
}

// trackSubRG registers a subscription+resource-group pair as known, so List() queries it.
func (p *DefaultVMProvider) trackSubRG(subID, rg string) {
	key := subRGKey{subscriptionID: subID, resourceGroup: rg}
	p.knownSubRGsMu.RLock()
	_, exists := p.knownSubRGs[key]
	p.knownSubRGsMu.RUnlock()
	if exists {
		return
	}
	p.knownSubRGsMu.Lock()
	p.knownSubRGs[key] = struct{}{}
	p.knownSubRGsMu.Unlock()
}

// getKnownSubRGs returns a snapshot of all known subscription+resource-group pairs.
func (p *DefaultVMProvider) getKnownSubRGs() []subRGKey {
	p.knownSubRGsMu.RLock()
	defer p.knownSubRGsMu.RUnlock()
	keys := make([]subRGKey, 0, len(p.knownSubRGs))
	for k := range p.knownSubRGs {
		keys = append(keys, k)
	}
	return keys
}

// getClientForSubscription returns the AZClient for the given subscription, using the
// client manager to lazily create and cache clients.
func (p *DefaultVMProvider) getClientForSubscription(subID string) (*AZClient, error) {
	if p.clientManager != nil {
		return p.clientManager.GetClient(subID)
	}
	// Fallback: no client manager, use the default client (single-sub mode)
	return p.azClient, nil
}

// parseResourceID extracts the subscription ID and resource group from an Azure resource ID.
// Expected format: /subscriptions/<sub>/resourceGroups/<rg>/providers/...
func parseResourceID(resourceID string) (subscriptionID, resourceGroup string, err error) {
	parts := strings.Split(strings.TrimPrefix(resourceID, "/"), "/")
	if len(parts) < 4 || !strings.EqualFold(parts[0], "subscriptions") || !strings.EqualFold(parts[2], "resourceGroups") {
		return "", "", fmt.Errorf("invalid Azure resource ID format: %s", resourceID)
	}
	return parts[1], parts[3], nil
}

// BeginCreate creates an instance given the constraints.
// instanceTypes should be sorted by priority for spot capacity type.
// Note that the returned instance may not be finished provisioning yet.
// Errors that occur on the "sync side" of the VM create, such as quota/capacity, BadRequest due
// to invalid user input, and similar, will have the error returned here.
// Errors that occur on the "async side" of the VM create (after the request is accepted, or after polling the
// VM create and while ) will be returned
// from the VirtualMachinePromise.Wait() function.
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
		ncCfg := p.resolveNodeClassConfig(nodeClass)
		cleanupClient, clientErr := p.getClientForSubscription(ncCfg.subscriptionID)
		if clientErr != nil {
			log.FromContext(ctx).Error(clientErr, "failed to get client for cleanup", "subscriptionID", ncCfg.subscriptionID)
			return nil, err
		}
		if cleanupErr := p.cleanupAzureResourcesWithClient(ctx, cleanupClient, ncCfg.resourceGroup, GenerateResourceName(nodeClaim.Name), true); cleanupErr != nil {
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

		extensionNames := GetManagedExtensionNames(p.provisionMode, p.env)
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

func (p *DefaultVMProvider) Get(ctx context.Context, vmName string) (*armcompute.VirtualMachine, error) {
	// Try the default subscription+RG first.
	vm, err := p.getFromSubRG(ctx, p.azClient, p.resourceGroup, vmName)
	if err == nil {
		return vm, nil
	}

	// If not found in the default sub+RG, try other known subscription+RG pairs.
	if corecloudprovider.IsNodeClaimNotFoundError(err) {
		for _, key := range p.getKnownSubRGs() {
			if key.subscriptionID == p.subscriptionID && key.resourceGroup == p.resourceGroup {
				continue // already tried
			}
			client, clientErr := p.getClientForSubscription(key.subscriptionID)
			if clientErr != nil {
				log.FromContext(ctx).V(1).Info("failed to get client for subscription during Get", "subscriptionID", key.subscriptionID, "error", clientErr)
				continue
			}
			vm, altErr := p.getFromSubRG(ctx, client, key.resourceGroup, vmName)
			if altErr == nil {
				return vm, nil
			}
			if !corecloudprovider.IsNodeClaimNotFoundError(altErr) {
				log.FromContext(ctx).V(1).Info("error getting VM from alternate sub+RG", "subscriptionID", key.subscriptionID, "resourceGroup", key.resourceGroup, "error", altErr)
			}
		}
	}

	return nil, err
}

// getFromSubRG gets a VM from a specific subscription+RG combination.
func (p *DefaultVMProvider) getFromSubRG(ctx context.Context, client *AZClient, resourceGroup, vmName string) (*armcompute.VirtualMachine, error) {
	vm, err := client.virtualMachinesClient.Get(ctx, resourceGroup, vmName, nil)
	if err != nil {
		if sdkerrors.IsNotFoundErr(err) {
			return nil, corecloudprovider.NewNodeClaimNotFoundError(err)
		}
		return nil, fmt.Errorf("failed to get VM instance, %w", err)
	}
	return &vm.VirtualMachine, nil
}

func (p *DefaultVMProvider) List(ctx context.Context) ([]*armcompute.VirtualMachine, error) {
	var vmList []*armcompute.VirtualMachine

	// Query all known subscription+resource-group pairs for VMs.
	seen := make(map[subRGKey]struct{})
	for _, key := range p.getKnownSubRGs() {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		query := GetVMListQueryBuilder(key.resourceGroup).String()
		subID := key.subscriptionID
		req := NewQueryRequest(&subID, query)
		client := p.azClient.azureResourceGraphClient // ARG client is subscription-agnostic
		data, err := GetResourceData(ctx, client, *req)
		if err != nil {
			return nil, fmt.Errorf("querying azure resource graph for subscription %s, rg %s: %w", key.subscriptionID, key.resourceGroup, err)
		}
		for i := range data {
			vm, err := createVMFromQueryResponseData(data[i])
			if err != nil {
				return nil, fmt.Errorf("creating VM object from query response data, %w", err)
			}
			vmList = append(vmList, vm)
		}
	}

	return vmList, nil
}

func (p *DefaultVMProvider) Delete(ctx context.Context, resourceName string) error {
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

	// Parse subscription+RG from the VM's resource ID to use the correct client for deletion.
	if vm.ID != nil {
		subID, rg, parseErr := parseResourceID(lo.FromPtr(vm.ID))
		if parseErr == nil && (subID != p.subscriptionID || rg != p.resourceGroup) {
			client, clientErr := p.getClientForSubscription(subID)
			if clientErr != nil {
				return fmt.Errorf("getting client for subscription %s: %w", subID, clientErr)
			}
			return p.cleanupAzureResourcesWithClient(ctx, client, rg, resourceName, false)
		}
	}
	return p.cleanupAzureResources(ctx, resourceName, false)
}

func (p *DefaultVMProvider) GetNic(ctx context.Context, rg, nicName string) (*armnetwork.Interface, error) {
	nicResponse, err := p.azClient.networkInterfacesClient.Get(ctx, rg, nicName, nil)
	if err != nil {
		return nil, err
	}
	return &nicResponse.Interface, nil
}

// ListNics returns all network interfaces in all known resource groups that have the nodepool tag
func (p *DefaultVMProvider) ListNics(ctx context.Context) ([]*armnetwork.Interface, error) {
	var nicList []*armnetwork.Interface

	seen := make(map[subRGKey]struct{})
	for _, key := range p.getKnownSubRGs() {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		query := GetNICListQueryBuilder(key.resourceGroup).String()
		subID := key.subscriptionID
		req := NewQueryRequest(&subID, query)
		client := p.azClient.azureResourceGraphClient
		data, err := GetResourceData(ctx, client, *req)
		if err != nil {
			return nil, fmt.Errorf("querying azure resource graph for subscription %s, rg %s: %w", key.subscriptionID, key.resourceGroup, err)
		}
		for i := range data {
			nic, err := createNICFromQueryResponseData(data[i])
			if err != nil {
				return nil, fmt.Errorf("creating NIC object from query response data, %w", err)
			}
			nicList = append(nicList, nic)
		}
	}

	return nicList, nil
}

func (p *DefaultVMProvider) DeleteNic(ctx context.Context, nicName string) error {
	return deleteNicIfExists(ctx, p.azClient.networkInterfacesClient, p.resourceGroup, nicName)
}

// createAKSIdentifyingExtension attaches a VM extension to identify that this VM participates in an AKS cluster
func (p *DefaultVMProvider) createAKSIdentifyingExtension(ctx context.Context, vmName string, tags map[string]*string) (err error) {
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

func (p *DefaultVMProvider) createCSExtension(ctx context.Context, vmName string, cse string, isWindows bool, tags map[string]*string) error {
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

func (p *DefaultVMProvider) newNetworkInterfaceForVM(opts *createNICOptions) armnetwork.Interface {
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

	location := opts.Location
	if location == "" {
		location = p.location
	}
	nic := armnetwork.Interface{
		Location: lo.ToPtr(location),
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

// E.g., aks-default-2jf98
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
	Location               string
}

func (p *DefaultVMProvider) createNetworkInterface(ctx context.Context, client *AZClient, resourceGroup string, opts *createNICOptions) (string, error) {
	nic := p.newNetworkInterfaceForVM(opts)
	p.applyTemplateToNic(&nic, opts.LaunchTemplate)
	log.FromContext(ctx).V(1).Info("creating network interface", "nicName", opts.NICName)
	res, err := createNic(ctx, client.networkInterfacesClient, resourceGroup, opts.NICName, nic)
	if err != nil {
		return "", err
	}
	log.FromContext(ctx).V(1).Info("successfully created network interface", "nicName", opts.NICName, "nicID", *res.ID)
	return *res.ID, nil
}

// createVMOptions contains all the parameters needed to create a VM
type createVMOptions struct {
	VMName              string
	NicReference        string
	Zone                string
	CapacityType        string
	Location            string
	SSHPublicKey        string
	LinuxAdminUsername  string
	NodeIdentities      []string
	NodeClass           *v1beta1.AKSNodeClass
	LaunchTemplate      *launchtemplate.Template
	InstanceType        *corecloudprovider.InstanceType
	ProvisionMode       string
	UseSIG              bool
	DiskEncryptionSetID string
	NodePoolName        string
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
			Priority: lo.ToPtr(KarpCapacityTypeToVMPriority[opts.CapacityType]),
		},
		Zones: utils.MakeARMZonesFromAKSLabelZone(opts.Zone),
		Tags:  opts.LaunchTemplate.Tags,
	}
	setVMPropertiesOSDiskType(vm.Properties, opts.LaunchTemplate)
	setVMPropertiesOSDiskEncryption(vm.Properties, opts.DiskEncryptionSetID)
	setImageReference(vm.Properties, opts.LaunchTemplate.ImageID, opts.UseSIG)
	setVMPropertiesBillingProfile(vm.Properties, opts.CapacityType)
	setVMPropertiesSecurityProfile(vm.Properties, opts.NodeClass)
	setVMPropertiesDataDisk(vm.Properties, opts.NodeClass)

	if opts.ProvisionMode == consts.ProvisionModeBootstrappingClient {
		vm.Properties.OSProfile.CustomData = lo.ToPtr(opts.LaunchTemplate.CustomScriptsCustomData)
	} else {
		vm.Properties.OSProfile.CustomData = lo.ToPtr(opts.LaunchTemplate.ScriptlessCustomData)
	}

	return vm
}

func setVMPropertiesDataDisk(vmProperties *armcompute.VirtualMachineProperties, nodeClass *v1beta1.AKSNodeClass) {
	if nodeClass.Spec.DataDiskSizeGB != nil && *nodeClass.Spec.DataDiskSizeGB > 0 {
		vmProperties.StorageProfile.DataDisks = []*armcompute.DataDisk{{
			Lun:          lo.ToPtr(int32(0)),
			Name:         lo.ToPtr(lo.FromPtr(vmProperties.StorageProfile.OSDisk.Name) + "-data"),
			CreateOption: lo.ToPtr(armcompute.DiskCreateOptionTypesEmpty),
			DiskSizeGB:   lo.ToPtr(int32(*nodeClass.Spec.DataDiskSizeGB)),
			DeleteOption: lo.ToPtr(armcompute.DiskDeleteOptionTypesDelete),
			ManagedDisk: &armcompute.ManagedDiskParameters{
				StorageAccountType: lo.ToPtr(armcompute.StorageAccountTypesPremiumLRS),
			},
		}}
	}
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

func setVMPropertiesOSDiskEncryption(vmProperties *armcompute.VirtualMachineProperties, diskEncryptionSetID string) {
	if diskEncryptionSetID != "" {
		if vmProperties.StorageProfile.OSDisk.ManagedDisk == nil {
			vmProperties.StorageProfile.OSDisk.ManagedDisk = &armcompute.ManagedDiskParameters{}
		}
		vmProperties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet = &armcompute.DiskEncryptionSetParameters{
			ID: lo.ToPtr(diskEncryptionSetID),
		}
	}
}

// setImageReference sets the image reference for the VM based on if we are using self hosted karpenter or the node auto provisioning addon
func setImageReference(vmProperties *armcompute.VirtualMachineProperties, imageID string, useSIG bool) {
	// Full ARM resource IDs (from SIG or custom Compute Gallery images) use ImageReference.ID.
	// Community gallery images use CommunityGalleryImageID.
	if useSIG || strings.HasPrefix(strings.ToLower(imageID), "/subscriptions/") {
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

func setVMPropertiesSecurityProfile(vmProperties *armcompute.VirtualMachineProperties, nodeClass *v1beta1.AKSNodeClass) {
	if nodeClass.Spec.Security != nil && nodeClass.Spec.Security.EncryptionAtHost != nil {
		if vmProperties.SecurityProfile == nil {
			vmProperties.SecurityProfile = &armcompute.SecurityProfile{}
		}
		vmProperties.SecurityProfile.EncryptionAtHost = nodeClass.Spec.Security.EncryptionAtHost
	}
}

type createResult struct {
	Poller *runtime.Poller[armcompute.VirtualMachinesClientCreateOrUpdateResponse]
	VM     *armcompute.VirtualMachine
}

// createVirtualMachine creates a new VM using the provided options or skips the creation of a vm if it already exists, which means opts is not guaranteed except VMName
func (p *DefaultVMProvider) createVirtualMachine(ctx context.Context, client *AZClient, resourceGroup string, opts *createVMOptions) (*createResult, error) {
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
	resp, err := client.virtualMachinesClient.Get(ctx, resourceGroup, opts.VMName, nil)
	// If status == ok, we want to return the existing vmm
	if err == nil {
		return &createResult{VM: &resp.VirtualMachine}, nil
	}
	// if status != ok, and for a reason other than we did not find the vm
	if !sdkerrors.IsNotFoundErr(err) {
		return nil, fmt.Errorf("getting VM %q: %w", opts.VMName, err)
	}
	vm := newVMObject(opts)
	log.FromContext(ctx).V(1).Info("creating virtual machine", "vmName", opts.VMName, logging.InstanceType, opts.InstanceType.Name)
	VMCreateStartMetric.With(map[string]string{
		metrics.ImageLabel:        opts.LaunchTemplate.ImageID,
		metrics.SizeLabel:         opts.InstanceType.Name,
		metrics.ZoneLabel:         opts.Zone,
		metrics.CapacityTypeLabel: opts.CapacityType,
		metrics.NodePoolLabel:     opts.NodePoolName,
	}).Inc()

	poller, err := client.virtualMachinesClient.BeginCreateOrUpdate(ctx, resourceGroup, opts.VMName, *vm, nil)
	if err != nil {
		VMCreateFailureMetric.With(map[string]string{
			metrics.ImageLabel:        opts.LaunchTemplate.ImageID,
			metrics.SizeLabel:         opts.InstanceType.Name,
			metrics.ZoneLabel:         opts.Zone,
			metrics.CapacityTypeLabel: opts.CapacityType,
			metrics.NodePoolLabel:     opts.NodePoolName,
			metrics.PhaseLabel:        phaseSyncFailure,
			metrics.ErrorCodeLabel:    ErrorCodeForMetrics(err),
		}).Inc()
		return nil, fmt.Errorf("virtualMachine.BeginCreateOrUpdate for VM %q failed: %w", opts.VMName, err)
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
	launchTemplate, err := p.getLaunchTemplate(ctx, nodeClass, nodeClaim, instanceType, capacityType)
	if err != nil {
		return nil, fmt.Errorf("getting launch template: %w", err)
	}

	// Resolve per-NodeClass subscription/RG/location overrides, falling back to controller defaults.
	ncCfg := p.resolveNodeClassConfig(nodeClass)
	p.trackSubRG(ncCfg.subscriptionID, ncCfg.resourceGroup)

	azClient, err := p.getClientForSubscription(ncCfg.subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("getting Azure client for subscription %s: %w", ncCfg.subscriptionID, err)
	}

	// resourceName for the NIC, VM, and Disk
	resourceName := GenerateResourceName(nodeClaim.Name)

	var backendPools *loadbalancer.BackendAddressPools
	networkPlugin := options.FromContext(ctx).NetworkPlugin
	networkPluginMode := options.FromContext(ctx).NetworkPluginMode
	var nsgID string

	if options.FromContext(ctx).ProvisionMode == consts.ProvisionModeEKSHybrid {
		// In ekshybrid mode: no LB backend pools (nodes use VPN, not Azure LB),
		// no AKS NSG lookup (NSG is on the subnet via terraform),
		// and network plugin is none (Cilium handles networking on the EKS side).
		backendPools = &loadbalancer.BackendAddressPools{}
		networkPlugin = consts.NetworkPluginNone
		networkPluginMode = consts.NetworkPluginModeNone
	} else {
		var err error
		backendPools, err = p.loadBalancerProvider.LoadBalancerBackendPools(ctx)
		if err != nil {
			return nil, fmt.Errorf("getting backend pools: %w", err)
		}

		isAKSManagedVNET, err := utils.IsAKSManagedVNET(options.FromContext(ctx).NodeResourceGroup, launchTemplate.SubnetID)
		if err != nil {
			return nil, fmt.Errorf("checking if vnet is managed: %w", err)
		}
		if !isAKSManagedVNET {
			nsg, err := p.networkSecurityGroupProvider.ManagedNetworkSecurityGroup(ctx)
			if err != nil {
				return nil, fmt.Errorf("getting managed network security group: %w", err)
			}
			nsgID = lo.FromPtr(nsg.ID)
		}
	}

	// TODO: Not returning after launching this LRO because
	// TODO: doing so would bypass the capacity and other errors that are currently handled by
	// TODO: core pkg/controllers/nodeclaim/lifecycle/controller.go - in particular, there are metrics/events
	// TODO: emitted in capacity failure cases that we probably want.
	nicReference, err := p.createNetworkInterface(
		ctx,
		azClient,
		ncCfg.resourceGroup,
		&createNICOptions{
			NICName:                resourceName,
			NetworkPlugin:          networkPlugin,
			NetworkPluginMode:      networkPluginMode,
			MaxPods:                utils.GetMaxPods(nodeClass, networkPlugin, networkPluginMode),
			LaunchTemplate:         launchTemplate,
			BackendPools:           backendPools,
			InstanceType:           instanceType,
			NetworkSecurityGroupID: nsgID,
			Location:               ncCfg.location,
		},
	)
	if err != nil {
		return nil, err
	}

	result, err := p.createVirtualMachine(ctx, azClient, ncCfg.resourceGroup, &createVMOptions{
		VMName:              resourceName,
		NicReference:        nicReference,
		Zone:                zone,
		CapacityType:        capacityType,
		Location:            ncCfg.location,
		SSHPublicKey:        options.FromContext(ctx).SSHPublicKey,
		LinuxAdminUsername:  options.FromContext(ctx).LinuxAdminUsername,
		NodeIdentities:      mergeIdentities(options.FromContext(ctx).NodeIdentities, nodeClass.Spec.ManagedIdentities),
		NodeClass:           nodeClass,
		LaunchTemplate:      launchTemplate,
		InstanceType:        instanceType,
		ProvisionMode:       p.provisionMode,
		UseSIG:              options.FromContext(ctx).UseSIG,
		DiskEncryptionSetID: p.diskEncryptionSetID,
		NodePoolName:        nodeClaim.Labels[karpv1.NodePoolLabelKey],
	})
	if err != nil {
		sku, skuErr := p.instanceTypeProvider.Get(ctx, nodeClass, instanceType.Name)
		if skuErr != nil {
			return nil, fmt.Errorf("failed to get instance type %q: %w", instanceType.Name, err)
		}
		handledError := p.errorHandling.Handle(ctx, sku, instanceType, zone, capacityType, err)
		if handledError != nil {
			// At this point, the error is handled in provider layer (e.g., unavailable offerings cache), but not yet Karpenter core.
			// Thus the error needs to be returned.
			// Assuming that `HandleResponseError` already format/convert the error for such (e.g., `InsufficientCapacityError`).
			return nil, handledError
		}
		return nil, err
	}

	// Patch the VM object to fill out a few fields that are needed later.
	// This is a bit of a hack that saves us doing a GET now.
	// The reason to avoid a GET is that it can fail, and if it does the future above will be lost,
	// which we don't want.
	result.VM.ID = lo.ToPtr(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s", ncCfg.subscriptionID, ncCfg.resourceGroup, resourceName))
	result.VM.Properties.TimeCreated = lo.ToPtr(time.Now())

	return &VirtualMachinePromise{
		providerRef: p,
		WaitFunc: func() error {
			if result.Poller == nil {
				// Poller is nil means the VM existed already and we're done.
				// TODO: if the VM doesn't have extensions this will still happen and we will have to
				// TODO: wait for the TTL for the claim to be deleted and recreated. This will most likely
				// TODO: happen during Karpenter pod restart.
				return nil
			}

			_, err = result.Poller.PollUntilDone(ctx, nil)
			if err != nil {
				VMCreateFailureMetric.With(map[string]string{
					metrics.ImageLabel:        launchTemplate.ImageID,
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
					// At this point, the error is handled in provider layer (e.g., unavailable offerings cache), but not yet Karpenter core.
					// Thus the error needs to be returned.
					// Assuming that `HandleResponseError` already format/convert the error for such (e.g., `InsufficientCapacityError`).
					return handledError
				}
				return err
			}

			// In ekshybrid mode, skip both CSE and AKS billing extension.
			// The image self-bootstraps via a systemd service that reads VM tags from IMDS.
			if p.provisionMode != consts.ProvisionModeEKSHybrid {
				if p.provisionMode == consts.ProvisionModeBootstrappingClient {
					err = p.createCSExtension(ctx, resourceName, launchTemplate.CustomScriptsCSE, launchTemplate.IsWindows, launchTemplate.Tags)
					if err != nil {
						// An error here is handled by CloudProvider create and calls vmInstanceProvider.Delete (which cleans up the azure resources)
						return err
					}
				}
				if isAKSIdentifyingExtensionEnabled(p.env) {
					err = p.createAKSIdentifyingExtension(ctx, resourceName, launchTemplate.Tags)
					if err != nil {
						return err
					}
				}
			}

			return nil
		},
		VM: result.VM,
	}, nil
}

func (p *DefaultVMProvider) applyTemplateToNic(nic *armnetwork.Interface, template *launchtemplate.Template) {
	// set tags
	nic.Tags = template.Tags
	for _, ipConfig := range nic.Properties.IPConfigurations {
		ipConfig.Properties.Subnet = &armnetwork.Subnet{ID: &template.SubnetID}
	}
}

func (p *DefaultVMProvider) getLaunchTemplate(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceType *corecloudprovider.InstanceType,
	capacityType string,
) (*launchtemplate.Template, error) {
	// We need to get all single-valued requirement labels from the instance type and the nodeClaim to pass down to kubelet.
	// We don't just include single-value labels from the instance type because in the case where the label is NOT single-value on the instance
	// (i.e. there are options), the nodeClaim may have selected one of those options via its requirements which we want to include.

	// These may contain restricted labels from the pod that we need to filter out. We don't bother filtering the instance type requirements below because
	// we know those can't be restricted since they're controlled by the provider and none use the kubernetes.io domain.
	claimLabels := labels.GetFilteredSingleValuedRequirementLabels(
		scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...),
		func(k string, req *scheduling.Requirement) bool {
			return labels.IsKubeletLabel(k)
		},
	)
	additionalLabels := lo.Assign(
		claimLabels,
		labels.GetAllSingleValuedRequirementLabels(instanceType.Requirements),
		map[string]string{karpv1.CapacityTypeLabelKey: capacityType},
	)

	launchTemplate, err := p.launchTemplateProvider.GetTemplate(ctx, nodeClass, nodeClaim, instanceType, additionalLabels)
	if err != nil {
		return nil, fmt.Errorf("getting launch templates, %w", err)
	}

	return launchTemplate, nil
}

// mustDeleteNic parameter is used to determine whether NIC deletion failure is considered an error.
// We may not want to return error of NIC cannot be deleted, as it is "by design" that NIC deletion may not be successful when VM deletion is not completed.
// NIC garbage collector is expected to handle such cases.
func (p *DefaultVMProvider) cleanupAzureResources(ctx context.Context, resourceName string, mustDeleteNic bool) error {
	return p.cleanupAzureResourcesWithClient(ctx, p.azClient, p.resourceGroup, resourceName, mustDeleteNic)
}

// cleanupAzureResourcesWithClient cleans up a VM and its NIC using an explicit client and resource group.
func (p *DefaultVMProvider) cleanupAzureResourcesWithClient(ctx context.Context, client *AZClient, resourceGroup string, resourceName string, mustDeleteNic bool) error {
	vmErr := deleteVirtualMachineIfExists(ctx, client.virtualMachinesClient, resourceGroup, resourceName)
	if vmErr != nil {
		log.FromContext(ctx).Error(vmErr, "virtualMachine.Delete failed", "vmName", resourceName)
	}
	// The order here is intentional, if the VM was created successfully, then we attempt to delete the vm, the
	// nic, disk and all associated resources will be removed. If the VM was not created successfully and a nic was found,
	// then we attempt to delete the nic.

	nicErr := deleteNicIfExists(ctx, client.networkInterfacesClient, resourceGroup, resourceName)

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

func (p *DefaultVMProvider) getAKSIdentifyingExtension(tags map[string]*string) *armcompute.VirtualMachineExtension {
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

func (p *DefaultVMProvider) getCSExtension(cse string, isWindows bool, tags map[string]*string) *armcompute.VirtualMachineExtension {
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

// mergeIdentities combines global node identities with per-NodeClass managed identities,
// deduplicating by case-insensitive resource ID comparison.
func mergeIdentities(global []string, perNodeClass []string) []string {
	if len(perNodeClass) == 0 {
		return global
	}
	seen := make(map[string]bool, len(global)+len(perNodeClass))
	result := make([]string, 0, len(global)+len(perNodeClass))
	for _, id := range global {
		lower := strings.ToLower(id)
		if !seen[lower] {
			seen[lower] = true
			result = append(result, id)
		}
	}
	for _, id := range perNodeClass {
		lower := strings.ToLower(id)
		if !seen[lower] {
			seen[lower] = true
			result = append(result, id)
		}
	}
	return result
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

func GetCapacityTypeFromVM(vm *armcompute.VirtualMachine) string {
	if vm != nil && vm.Properties != nil && vm.Properties.Priority != nil {
		return VMPriorityToKarpCapacityType[*vm.Properties.Priority]
	}
	return ""
}

func GetScaleSetPriorityLabelFromVM(vm *armcompute.VirtualMachine) string {
	if vm != nil && vm.Properties != nil && vm.Properties.Priority != nil {
		return VMPriorityToScaleSetPriority[*vm.Properties.Priority]
	}
	return ""
}
