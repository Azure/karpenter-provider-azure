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

package cloudprovider

// This file provides shared types and helper functions for provisioning tests that
// run against BOTH AKSMachineAPI and AKSScriptless (VM) modes. Helper functions
// check options.FromContext(ctx).ProvisionMode directly to determine mode-specific
// behavior, eliminating the need for an adapter struct.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"

	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

// creationResult holds normalized fields extracted from either a VM or AKS Machine creation input.
type creationResult struct {
	vmSize      string
	zone        string
	zoneErr     error
	zones       []*string
	tags        map[string]*string
	isEphemeral bool
	diskSizeGB  *int32
	imageRef    string
	osDiskType  string // AKS Machine: OSDiskType value ("Managed"/"Ephemeral"); VM: empty (use isEphemeral/diffDiskOption instead)
	// VM-specific fields (populated only for AKSScriptless/BootstrappingClient mode)
	customData              string // decoded base64 custom data from OSProfile
	diffDiskOption          string // DiffDiskSettings.Option value (e.g. "Local")
	isCommunityGalleryImage bool   // whether the image source is a community gallery
}

// DONE!: provisionTestMode adapters eliminated. Using direct helper functions with options.FromContext(ctx).ProvisionMode checks instead.

// Error type constants for setProvisioningError
const (
	errLowPriorityQuota     = "LowPriorityQuota"
	errOverconstrainedZonal = "OverconstrainedZonal"
	errOverconstrained      = "Overconstrained"
	errAllocationFailed     = "AllocationFailed"
	errSKUFamilyQuota       = "SKUFamilyQuota"
	errSKUFamilyQuotaZero   = "SKUFamilyQuotaZero"
	errRegionalCoresQuota   = "RegionalCoresQuota"
	errGenericCreation      = "GenericCreation"
)

// VM error message strings (must contain the substrings the error handler parses)
const (
	vmLowPriorityQuotaMsg     = "Operation could not be completed as it results in exceeding approved LowPriorityCores quota. Additional details - Deployment Model: Resource Manager, Location: westus2, Current Limit: 0, Current Usage: 0, Additional Required: 32, (Minimum) New Limit Required: 32."
	vmOverconstrainedZonalMsg = "Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive. Please remove some constraints and try again."
	vmOverconstrainedMsg      = "Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive."
	vmAllocationFailedMsg     = "Allocation failed. We do not have sufficient capacity for the requested VM size in this region."
	vmFamilyQuotaExceededMsg  = "Operation could not be completed as it results in exceeding approved standardDLSv5Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: westus2, Current Limit: 100, Current Usage: 96, Additional Required: 32, (Minimum) New Limit Required: 128."
	vmFamilyQuotaZeroMsg      = "Operation could not be completed as it results in exceeding approved standardDLSv5Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: westus2, Current Limit: 0, Current Usage: 0, Additional Required: 32, (Minimum) New Limit Required: 32."
	vmRegionalCoresQuotaMsg   = "Operation could not be completed as it results in exceeding approved Total Regional Cores quota. Additional details - Deployment Model: Resource Manager, Location: uksouth, Current Limit: 100, Current Usage: 100, Additional Required: 64, (Minimum) New Limit Required: 164."
	vmGenericCreationMsg      = "Failed to create VM"
)

// WellKnownLabelEntry describes a label and its expected behavior in tests.
// Shared between AKSScriptless and BootstrappingClient modes.
type WellKnownLabelEntry struct {
	Name      string
	Label     string
	ValueFunc func() string
	SetupFunc func()
	// ExpectedInKubeletLabels indicates if we expect to see this in the KUBELET_NODE_LABELS section of the custom script extension.
	// If this is false it means that Karpenter will not set it on the node via KUBELET_NODE_LABELS.
	// It does NOT mean that it will not be on the resulting Node object in a real cluster, as it may be written by another process.
	// We expect that if ExpectedOnNode is set, ExpectedInKubeletLabels is also set.
	ExpectedInKubeletLabels bool
	// ExpectedOnNode indicates if we expect to see this on the node.
	// If this is false it means is that Karpenter will not set it on the node directly via kube-apiserver.
	// It does NOT mean that it will not be on the resulting Node object in a real cluster, as it may be written as part of KUBELET_NODE_LABELS (see above)
	// or by another process. We're asserting on this distinction currently because it helps clarify who is doing what
	ExpectedOnNode bool
}

// wellKnownLabelEntries returns the shared label entries used by both AKSScriptless and BootstrappingClient test modes.
func wellKnownLabelEntries() []WellKnownLabelEntry {
	return []WellKnownLabelEntry{
		// Well known
		{Name: v1.LabelTopologyRegion, Label: v1.LabelTopologyRegion, ValueFunc: func() string { return fake.Region }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: karpv1.NodePoolLabelKey, Label: karpv1.NodePoolLabelKey, ValueFunc: func() string { return nodePool.Name }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1.LabelTopologyZone, Label: v1.LabelTopologyZone, ValueFunc: func() string { return fakeZone1 }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1.LabelInstanceTypeStable, Label: v1.LabelInstanceTypeStable, ValueFunc: func() string { return "Standard_NC24ads_A100_v4" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1.LabelOSStable, Label: v1.LabelOSStable, ValueFunc: func() string { return "linux" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1.LabelArchStable, Label: v1.LabelArchStable, ValueFunc: func() string { return "amd64" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: karpv1.CapacityTypeLabelKey, Label: karpv1.CapacityTypeLabelKey, ValueFunc: func() string { return "on-demand" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		// Well Known to AKS
		{Name: v1beta1.LabelSKUName, Label: v1beta1.LabelSKUName, ValueFunc: func() string { return "Standard_NC24ads_A100_v4" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.LabelSKUFamily, Label: v1beta1.LabelSKUFamily, ValueFunc: func() string { return "N" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.LabelSKUSeries, Label: v1beta1.LabelSKUSeries, ValueFunc: func() string { return "NCads_v4" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.LabelSKUVersion, Label: v1beta1.LabelSKUVersion, ValueFunc: func() string { return "4" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.LabelSKUStorageEphemeralOSMaxSize, Label: v1beta1.LabelSKUStorageEphemeralOSMaxSize, ValueFunc: func() string { return "429" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.LabelSKUAcceleratedNetworking, Label: v1beta1.LabelSKUAcceleratedNetworking, ValueFunc: func() string { return "true" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.LabelSKUStoragePremiumCapable, Label: v1beta1.LabelSKUStoragePremiumCapable, ValueFunc: func() string { return "true" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.LabelSKUGPUName, Label: v1beta1.LabelSKUGPUName, ValueFunc: func() string { return "A100" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.LabelSKUGPUManufacturer, Label: v1beta1.LabelSKUGPUManufacturer, ValueFunc: func() string { return "nvidia" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.LabelSKUGPUCount, Label: v1beta1.LabelSKUGPUCount, ValueFunc: func() string { return "1" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.LabelSKUCPU, Label: v1beta1.LabelSKUCPU, ValueFunc: func() string { return "24" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.LabelSKUMemory, Label: v1beta1.LabelSKUMemory, ValueFunc: func() string { return "8192" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		// AKS domain
		{Name: v1beta1.AKSLabelCPU, Label: v1beta1.AKSLabelCPU, ValueFunc: func() string { return "24" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.AKSLabelMemory, Label: v1beta1.AKSLabelMemory, ValueFunc: func() string { return "8192" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.AKSLabelMode + "=user", Label: v1beta1.AKSLabelMode, ValueFunc: func() string { return "user" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.AKSLabelMode + "=system", Label: v1beta1.AKSLabelMode, ValueFunc: func() string { return "system" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.AKSLabelScaleSetPriority + "=regular", Label: v1beta1.AKSLabelScaleSetPriority, ValueFunc: func() string { return "regular" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.AKSLabelScaleSetPriority + "=spot", Label: v1beta1.AKSLabelScaleSetPriority, ValueFunc: func() string { return "spot" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{Name: v1beta1.AKSLabelOSSKU, Label: v1beta1.AKSLabelOSSKU, ValueFunc: func() string { return "Ubuntu" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
		{
			Name:  v1beta1.AKSLabelFIPSEnabled,
			Label: v1beta1.AKSLabelFIPSEnabled,
			// Needs special setup because it only works on FIPS
			SetupFunc: func() {
				testOptions.UseSIG = true
				ctx = options.ToContext(ctx, testOptions)

				nodeClass.Spec.FIPSMode = &v1beta1.FIPSModeFIPS
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
				test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			},
			ValueFunc:               func() string { return "true" },
			ExpectedInKubeletLabels: true,
			ExpectedOnNode:          true,
		},
		// Deprecated Labels -- note that these are not expected in kubelet labels or on the node.
		// They are written by CloudProvider so don't need to be sent to kubelet, and they aren't required on the node object because Karpenter does a mapping from
		// the new labels to the old labels for compatibility.
		{Name: v1.LabelFailureDomainBetaRegion, Label: v1.LabelFailureDomainBetaRegion, ValueFunc: func() string { return fake.Region }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
		{Name: v1.LabelFailureDomainBetaZone, Label: v1.LabelFailureDomainBetaZone, ValueFunc: func() string { return fakeZone1 }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
		{Name: "beta.kubernetes.io/arch", Label: "beta.kubernetes.io/arch", ValueFunc: func() string { return "amd64" }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
		{Name: "beta.kubernetes.io/os", Label: "beta.kubernetes.io/os", ValueFunc: func() string { return "linux" }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
		{Name: v1.LabelInstanceType, Label: v1.LabelInstanceType, ValueFunc: func() string { return "Standard_NC24ads_A100_v4" }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
		{Name: "topology.disk.csi.azure.com/zone", Label: "topology.disk.csi.azure.com/zone", ValueFunc: func() string { return fakeZone1 }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
		// Unsupported labels
		{Name: v1.LabelWindowsBuild, Label: v1.LabelWindowsBuild, ValueFunc: func() string { return "window" }, ExpectedInKubeletLabels: true, ExpectedOnNode: false},
		// Cluster Label
		{Name: v1beta1.AKSLabelCluster, Label: v1beta1.AKSLabelCluster, ValueFunc: func() string { return "test-resourceGroup" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
	}
}

// nonSchedulableLabelsMap returns the shared non-schedulable labels used by both AKSScriptless and BootstrappingClient test modes.
func nonSchedulableLabelsMap() map[string]string {
	return map[string]string{
		labels.AKSLabelRole:                     "agent",
		v1beta1.AKSLabelKubeletIdentityClientID: test.Options().KubeletIdentityClientID,
		"kubernetes.azure.com/mode":             "user", // TODO: Will become a WellKnownLabel soon
		//We expect the vnetInfoLabels because we're simulating network plugin Azure by default and they are included there
		labels.AKSLabelSubnetName:          "aks-subnet",
		labels.AKSLabelVNetGUID:            test.Options().VnetGUID,
		labels.AKSLabelAzureCNIOverlay:     strconv.FormatBool(true),
		labels.AKSLabelPodNetworkType:      consts.NetworkPluginModeOverlay,
		karpv1.NodeDoNotSyncTaintsLabelKey: "true",
	}
}

// Helper functions that directly check the provision mode from context

// isVMMode returns true when the current provision mode uses VM-based creation
// (AKSScriptless or BootstrappingClient).
func isVMMode() bool {
	mode := options.FromContext(ctx).ProvisionMode
	return mode == consts.ProvisionModeAKSScriptless || mode == consts.ProvisionModeBootstrappingClient
}

// isAKSMachineMode returns true when the current provision mode uses AKS Machine API.
func isAKSMachineMode() bool {
	return options.FromContext(ctx).ProvisionMode == consts.ProvisionModeAKSMachineAPI
}

// setProvisioningError injects a provisioning error by error type constant,
// using the appropriate API based on the current provision mode.
//
//nolint:gocyclo
func setProvisioningError(errorType string) {
	if isAKSMachineMode() {
		switch errorType {
		case errLowPriorityQuota:
			azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorLowPriorityCoresQuota(fake.Region)
		case errOverconstrainedZonal:
			azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorOverconstrainedZonalAllocation()
		case errOverconstrained:
			azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorOverconstrainedAllocation()
		case errAllocationFailed:
			azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorAllocationFailed()
		case errSKUFamilyQuota:
			azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorVMFamilyQuotaExceeded("westus2", "Standard NCASv3_T4", 24, 24, 8, 32)
		case errSKUFamilyQuotaZero:
			azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorVMFamilyQuotaExceeded("westus2", "Standard NCASv3_T4", 0, 0, 8, 8)
		case errRegionalCoresQuota:
			azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorTotalRegionalCoresQuota(fake.Region)
		case errGenericCreation:
			azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorAny()
		}
	} else {
		var code string
		var msg string
		switch errorType {
		case errLowPriorityQuota:
			code, msg = sdkerrors.OperationNotAllowed, vmLowPriorityQuotaMsg
		case errOverconstrainedZonal:
			code, msg = sdkerrors.OverconstrainedZonalAllocationRequest, vmOverconstrainedZonalMsg
		case errOverconstrained:
			code, msg = sdkerrors.OverconstrainedAllocationRequest, vmOverconstrainedMsg
		case errAllocationFailed:
			code, msg = sdkerrors.AllocationFailed, vmAllocationFailedMsg
		case errSKUFamilyQuota:
			code, msg = sdkerrors.OperationNotAllowed, vmFamilyQuotaExceededMsg
		case errSKUFamilyQuotaZero:
			code, msg = sdkerrors.OperationNotAllowed, vmFamilyQuotaZeroMsg
		case errRegionalCoresQuota:
			code, msg = sdkerrors.OperationNotAllowed, vmRegionalCoresQuotaMsg
		case errGenericCreation:
			code, msg = sdkerrors.OperationNotAllowed, vmGenericCreationMsg
		}
		azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
			&azcore.ResponseError{
				ErrorCode:   code,
				RawResponse: &http.Response{Body: createVMSDKErrorBody(code, msg)},
			},
		)
	}
}

// clearProvisioningError removes any injected provisioning error.
func clearProvisioningError() {
	if isAKSMachineMode() {
		azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
	} else {
		azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
		azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.Error.Set(nil)
	}
}

// getCreateCallCount returns how many creation API calls were made.
func getCreateCallCount() int {
	if isAKSMachineMode() {
		return azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()
	}
	return azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()
}

// popCreationResult pops the most recent creation input and returns normalized fields.
func popCreationResult() creationResult {
	GinkgoHelper()
	if isAKSMachineMode() {
		return popAKSMachineCreationResult()
	}
	return popVMCreationResult()
}

func popAKSMachineCreationResult() creationResult {
	GinkgoHelper()
	input := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
	m := input.AKSMachine
	Expect(m.Properties).ToNot(BeNil())
	Expect(m.Properties.Hardware).ToNot(BeNil())
	Expect(m.Properties.Hardware.VMSize).ToNot(BeNil())

	zone, zoneErr := instance.GetAKSLabelZoneFromAKSMachine(&m, fake.Region)
	isEphemeral := false
	var osDiskType string
	if m.Properties.OperatingSystem != nil && m.Properties.OperatingSystem.OSDiskType != nil {
		osDiskType = string(*m.Properties.OperatingSystem.OSDiskType)
		isEphemeral = *m.Properties.OperatingSystem.OSDiskType == armcontainerservice.OSDiskTypeEphemeral
	}
	var diskSizeGB *int32
	if m.Properties.OperatingSystem != nil {
		diskSizeGB = m.Properties.OperatingSystem.OSDiskSizeGB
	}
	return creationResult{
		vmSize:      lo.FromPtr(m.Properties.Hardware.VMSize),
		zone:        zone,
		zoneErr:     zoneErr,
		zones:       m.Zones,
		tags:        m.Properties.Tags,
		isEphemeral: isEphemeral,
		diskSizeGB:  diskSizeGB,
		osDiskType:  osDiskType,
		imageRef:    lo.FromPtr(m.Properties.NodeImageVersion),
	}
}

func popVMCreationResult() creationResult {
	GinkgoHelper()
	input := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
	vm := input.VM
	Expect(vm.Properties).ToNot(BeNil())
	Expect(vm.Properties.HardwareProfile).ToNot(BeNil())
	Expect(vm.Properties.StorageProfile).ToNot(BeNil())
	Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
	Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())

	zone, zoneErr := utils.MakeAKSLabelZoneFromVM(&vm)
	isEphemeral := vm.Properties.StorageProfile.OSDisk.DiffDiskSettings != nil
	var diskSizeGB *int32
	if vm.Properties.StorageProfile.OSDisk.DiskSizeGB != nil {
		diskSizeGB = vm.Properties.StorageProfile.OSDisk.DiskSizeGB
	}
	var imageRef string
	isCommunityGallery := false
	if vm.Properties.StorageProfile.ImageReference != nil {
		if vm.Properties.StorageProfile.ImageReference.ID != nil {
			imageRef = *vm.Properties.StorageProfile.ImageReference.ID
		}
		isCommunityGallery = vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID != nil
	}
	var diffDiskOption string
	if vm.Properties.StorageProfile.OSDisk.DiffDiskSettings != nil && vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option != nil {
		diffDiskOption = string(*vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option)
	}
	var customData string
	if vm.Properties.OSProfile != nil && vm.Properties.OSProfile.CustomData != nil {
		decodedBytes, err := base64.StdEncoding.DecodeString(*vm.Properties.OSProfile.CustomData)
		if err == nil {
			customData = string(decodedBytes)
		}
	}
	return creationResult{
		vmSize:                  string(*vm.Properties.HardwareProfile.VMSize),
		zone:                    zone,
		zoneErr:                 zoneErr,
		zones:                   vm.Zones,
		tags:                    vm.Tags,
		isEphemeral:             isEphemeral,
		diskSizeGB:              diskSizeGB,
		imageRef:                imageRef,
		customData:              customData,
		diffDiskOption:          diffDiskOption,
		isCommunityGalleryImage: isCommunityGallery,
	}
}

// getSubnetID extracts the subnet ID from the most recent creation input
// (NIC for VM mode, machine properties for AKS Machine mode).
func getSubnetID() string {
	GinkgoHelper()
	if isAKSMachineMode() {
		input := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
		m := input.AKSMachine
		// Preserved nil guards from original AKS Machine subnet test
		Expect(m.Properties.Network).ToNot(BeNil())
		Expect(m.Properties.Network.VnetSubnetID).ToNot(BeNil())
		return *m.Properties.Network.VnetSubnetID
	}

	// VM mode
	nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
	// Preserved nil guard from original VM subnet test
	Expect(nic).ToNot(BeNil())
	if nic.Interface.Properties != nil &&
		len(nic.Interface.Properties.IPConfigurations) > 0 &&
		nic.Interface.Properties.IPConfigurations[0].Properties != nil &&
		nic.Interface.Properties.IPConfigurations[0].Properties.Subnet != nil {
		return lo.FromPtr(nic.Interface.Properties.IPConfigurations[0].Properties.Subnet.ID)
	}
	return ""
}

// setZoneAllocError injects a ZoneAllocationFailed error for a specific SKU and zone.
func setZoneAllocError(sku, zone string) {
	if isAKSMachineMode() {
		azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorZoneAllocationFailed(sku, zone)
	} else {
		azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.Error.Set(
			&azcore.ResponseError{ErrorCode: sdkerrors.ZoneAllocationFailed},
		)
	}
}

// setSkuNotAvailable injects a SKUNotAvailable error for a specific SKU name.
func setSkuNotAvailable(skuName string) {
	if isAKSMachineMode() {
		azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorSkuNotAvailable(skuName, fake.Region)
	} else {
		azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
			&azcore.ResponseError{ErrorCode: sdkerrors.SKUNotAvailableErrorCode},
		)
	}
}

// createVMSDKErrorBody constructs an Azure SDK error response body for VM error injection.
func createVMSDKErrorBody(code, message string) io.ReadCloser {
	return io.NopCloser(bytes.NewReader([]byte(fmt.Sprintf(`{"error":{"code": "%s", "message": "%s"}}`, code, message))))
}

// setupProvisionModeAKSMachineAPITestEnvironment configures test infrastructure for AKSMachineAPI provisioning mode.
func setupProvisionModeAKSMachineAPITestEnvironment() {
	testOptions = test.Options(test.OptionsFields{
		ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
		UseSIG:        lo.ToPtr(true),
	})

	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, testOptions)

	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
	statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID)
	test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
	cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
	cloudProviderNonZonal = New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, azureEnvNonZonal.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider, azureEnvNonZonal.InstanceTypeStore)
	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	clusterNonZonal = state.NewCluster(fakeClock, env.Client, cloudProviderNonZonal)
	coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
	coreProvisionerNonZonal = provisioning.NewProvisioner(env.Client, recorder, cloudProviderNonZonal, clusterNonZonal, fakeClock)

	ExpectApplied(ctx, env.Client, nodeClass, nodePool)
	ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
}

// setupAKSMachineAPIModeWithBatch configures test infrastructure for AKSMachineAPI provisioning mode
// with batch creation enabled (grouper → coordinator → GET poller).
func setupAKSMachineAPIModeWithBatch() {
	testOptions = test.Options(test.OptionsFields{
		ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
		UseSIG:        lo.ToPtr(true),
	})
	testOptions.BatchCreationEnabled = true
	testOptions.BatchIdleTimeoutMS = 100
	testOptions.BatchMaxTimeoutMS = 1000
	testOptions.MaxBatchSize = 50

	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, testOptions)

	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
	statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID)
	test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
	cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
	cloudProviderNonZonal = New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, azureEnvNonZonal.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider, azureEnvNonZonal.InstanceTypeStore)

	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	clusterNonZonal = state.NewCluster(fakeClock, env.Client, cloudProviderNonZonal)
	coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
	coreProvisionerNonZonal = provisioning.NewProvisioner(env.Client, recorder, cloudProviderNonZonal, clusterNonZonal, fakeClock)

	ExpectApplied(ctx, env.Client, nodeClass, nodePool)
	ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
}

// setupProvisionModeAKSScriptlessTestEnvironment configures test infrastructure for AKSScriptless (VM) provisioning mode.
func setupProvisionModeAKSScriptlessTestEnvironment() {
	testOptions = test.Options(test.OptionsFields{
		ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSScriptless),
		UseSIG:        lo.ToPtr(false),
	})

	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, testOptions)

	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
	statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID)
	test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
	cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
	cloudProviderNonZonal = New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, azureEnvNonZonal.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider, azureEnvNonZonal.InstanceTypeStore)
	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	clusterNonZonal = state.NewCluster(fakeClock, env.Client, cloudProviderNonZonal)
	coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
	coreProvisionerNonZonal = provisioning.NewProvisioner(env.Client, recorder, cloudProviderNonZonal, clusterNonZonal, fakeClock)

	// VM mode needs NSG setup for NIC creation
	nsg := test.MakeNetworkSecurityGroup(options.FromContext(ctx).NodeResourceGroup, fmt.Sprintf("aks-agentpool-%s-nsg", options.FromContext(ctx).ClusterID))
	azureEnv.NetworkSecurityGroupAPI.NSGs.Store(nsg.ID, nsg)

	ExpectApplied(ctx, env.Client, nodeClass, nodePool)
	ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
}

func setupProvisionModeBootstrappingClientTestEnvironment() {
	testOptions = test.Options(test.OptionsFields{
		ProvisionMode: lo.ToPtr(consts.ProvisionModeBootstrappingClient),
		UseSIG:        lo.ToPtr(true),
	})

	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, testOptions)

	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
	statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID)
	test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
	cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
	cloudProviderNonZonal = New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, azureEnvNonZonal.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider, azureEnvNonZonal.InstanceTypeStore)
	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	clusterNonZonal = state.NewCluster(fakeClock, env.Client, cloudProviderNonZonal)
	coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
	coreProvisionerNonZonal = provisioning.NewProvisioner(env.Client, recorder, cloudProviderNonZonal, clusterNonZonal, fakeClock)

	// VM mode needs NSG setup for NIC creation
	nsg := test.MakeNetworkSecurityGroup(options.FromContext(ctx).NodeResourceGroup, fmt.Sprintf("aks-agentpool-%s-nsg", options.FromContext(ctx).ClusterID))
	azureEnv.NetworkSecurityGroupAPI.NSGs.Store(nsg.ID, nsg)

	ExpectApplied(ctx, env.Client, nodeClass, nodePool)
	ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
}

// teardownTestEnvironment resets state after each test.
func teardownTestEnvironment() {
	cloudProvider.WaitForInstancePromises()
	cluster.Reset()
	clusterNonZonal.Reset()
	azureEnv.Reset()
	azureEnvNonZonal.Reset()
}

// ─── Multi-instance provisioning helpers and tests ─────────────────────────────
//
// Why these tests exist:
// The standard provisioner creates NodeClaims in a serial for-loop. Each
// cloudProvider.Create() blocks on the batch grouper's response channel, and
// the batch idle timeout fires before the next Create() starts — so every
// batch window closes with exactly 1 machine. Batching never actually happens
// at the cloudprovider integration level.
//
// To close this gap, these tests call cloudProvider.Create() concurrently from
// goroutines, ensuring multiple requests land in the same batch window. This
// exercises the full path:
//   cloudProvider.Create() → AKSMachineProvider.BeginCreate() →
//   BatchingMachinesClient → Grouper → Coordinator → fake API
//
// The tests are split into two groups:
// - runSharedMultiInstanceProvisionTests: mode-agnostic correctness checks
//   (work for all provision modes, no batch-specific assertions)
// - runBatchSpecificMultiInstanceTests: batch grouping assertions
//   (only run under batch-enabled contexts)

// concurrentCreateResult holds the outcome of a single cloudProvider.Create() call.
type concurrentCreateResult struct {
	NodeClaim *karpv1.NodeClaim
	Err       error
}

// concurrentCreateAndWaitForPromises creates multiple NodeClaims concurrently
// via cloudProvider.Create(), sets the Launched condition on each (mirroring
// what the core lifecycle controller does in production), then waits for all
// async promise goroutines to complete.
//
// This is the concurrent counterpart to CreateAndWaitForPromises.
// The concurrency is what makes batching actually happen in tests — all
// Create() calls enqueue into the grouper before the idle timeout fires.
func concurrentCreateAndWaitForPromises(claims []*karpv1.NodeClaim) []concurrentCreateResult {
	GinkgoHelper()
	results := make([]concurrentCreateResult, len(claims))
	var wg sync.WaitGroup
	wg.Add(len(claims))
	for i, nc := range claims {
		go func(idx int, claim *karpv1.NodeClaim) {
			defer GinkgoRecover()
			defer wg.Done()
			created, err := cloudProvider.Create(ctx, claim)
			// Simulate what the core lifecycle Launch controller does after Create():
			// set Launched=True so the async promise goroutine's waitUntilLaunched
			// unblocks. Without this, the goroutine polls indefinitely.
			fresh := &karpv1.NodeClaim{}
			if getErr := env.Client.Get(ctx, types.NamespacedName{
				Name: claim.Name, Namespace: claim.Namespace,
			}, fresh); getErr == nil {
				fresh.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
				_ = env.Client.Status().Update(ctx, fresh)
			}
			results[idx] = concurrentCreateResult{NodeClaim: created, Err: err}
		}(i, nc)
	}
	wg.Wait()
	cloudProvider.WaitForInstancePromises()
	return results
}

// makeNodeClaimForInstanceType creates a NodeClaim requesting a specific instance type,
// applies it to the fake API server, and returns it ready for cloudProvider.Create().
// Uses the package-level nodePool and nodeClass.
func makeNodeClaimForInstanceType(instanceType string) *karpv1.NodeClaim {
	GinkgoHelper()
	nc := coretest.NodeClaim(karpv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
		},
		Spec: karpv1.NodeClaimSpec{
			NodeClassRef: &karpv1.NodeClassReference{
				Group: object.GVK(nodeClass).Group,
				Kind:  object.GVK(nodeClass).Kind,
				Name:  nodeClass.Name,
			},
			Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
				{NodeSelectorRequirement: v1.NodeSelectorRequirement{
					Key: v1.LabelInstanceTypeStable, Operator: v1.NodeSelectorOpIn,
					Values: []string{instanceType},
				}},
			},
		},
	})
	ExpectApplied(ctx, env.Client, nc)
	return nc
}

// countMachinesInDataStore counts machines stored in the fake AKS data storage.
func countMachinesInDataStore() int {
	count := 0
	azureEnv.AKSDataStorage.AKSMachines.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// collectMachinesFromDataStore returns all machines from the fake AKS data storage.
func collectMachinesFromDataStore() []armcontainerservice.Machine {
	var machines []armcontainerservice.Machine
	azureEnv.AKSDataStorage.AKSMachines.Range(func(_, v any) bool {
		machines = append(machines, v.(armcontainerservice.Machine))
		return true
	})
	return machines
}

// isBatchEnabled returns true if batch creation is enabled in the current test options.
func isBatchEnabled() bool {
	return testOptions.BatchCreationEnabled
}

// runSharedMultiInstanceProvisionTests verifies multi-instance provisioning
// correctness. These tests are mode-agnostic — they assert on outcomes (machines
// created, correct properties) but NOT on batch grouping (API call counts).
// They work under any provision mode context (AKSMachineAPI, AKSMachineAPI+Batch,
// AKSScriptless).
func runSharedMultiInstanceProvisionTests() {
	Context("Multi-Instance Provisioning", func() {
		It("should provision multiple instances with the same instance type", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)

			const count = 3
			claims := make([]*karpv1.NodeClaim, count)
			for i := 0; i < count; i++ {
				claims[i] = makeNodeClaimForInstanceType("Standard_D2_v2")
			}

			results := concurrentCreateAndWaitForPromises(claims)

			for i, r := range results {
				Expect(r.Err).ToNot(HaveOccurred(), "claim %d failed", i)
				Expect(r.NodeClaim).ToNot(BeNil(), "claim %d returned nil", i)
			}

			if isAKSMachineMode() {
				Expect(countMachinesInDataStore()).To(Equal(count))
			}
		})

		It("should provision multiple instances with different instance types", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)

			claims := []*karpv1.NodeClaim{
				makeNodeClaimForInstanceType("Standard_D2_v2"),
				makeNodeClaimForInstanceType("Standard_D2_v2"),
				makeNodeClaimForInstanceType("Standard_D4s_v3"),
			}

			results := concurrentCreateAndWaitForPromises(claims)

			for i, r := range results {
				Expect(r.Err).ToNot(HaveOccurred(), "claim %d failed", i)
				Expect(r.NodeClaim).ToNot(BeNil(), "claim %d returned nil", i)
			}

			if isAKSMachineMode() {
				Expect(countMachinesInDataStore()).To(Equal(3))
				// Verify at least one machine has each VM size
				machines := collectMachinesFromDataStore()
				vmSizes := make(map[string]int)
				for _, m := range machines {
					if m.Properties != nil && m.Properties.Hardware != nil && m.Properties.Hardware.VMSize != nil {
						vmSizes[*m.Properties.Hardware.VMSize]++
					}
				}
				Expect(vmSizes).To(HaveKey("Standard_D2_v2"))
				Expect(vmSizes).To(HaveKey("Standard_D4s_v3"))
			}
		})
	})
}

// runBatchSpecificMultiInstanceTests verifies batch grouping behavior — that
// concurrent creates with the same template land in the same batch (single API
// call) and different templates produce separate batches. Only meaningful under
// batch-enabled contexts.
//
// Key assertion: CalledWithInput.Len() counts how many times the fake's
// BeginCreateOrUpdate was called. The coordinator calls it once per batch,
// so N same-template creates → 1 call, K distinct templates → K calls.
func runBatchSpecificMultiInstanceTests() {
	Context("Batch Grouping", func() {
		It("should batch same-template instances into a single API call", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()

			// Create 5 NodeClaims all requesting the same instance type.
			// They should all land in the same batch because they produce
			// identical template hashes (same VM size, same node class config).
			const count = 5
			claims := make([]*karpv1.NodeClaim, count)
			for i := 0; i < count; i++ {
				claims[i] = makeNodeClaimForInstanceType("Standard_D2_v2")
			}

			results := concurrentCreateAndWaitForPromises(claims)

			for i, r := range results {
				Expect(r.Err).ToNot(HaveOccurred(), "claim %d failed", i)
				Expect(r.NodeClaim).ToNot(BeNil(), "claim %d returned nil", i)
			}

			// Core assertion: all 5 went through a single BeginCreateOrUpdate call
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1),
				"expected 1 batch API call for 5 same-template creates")

			// All 5 machines should be in the data store
			Expect(countMachinesInDataStore()).To(Equal(count))
		})

		It("should split different-template instances into separate batches", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()

			// Create 5 NodeClaims: 3 × Standard_D2_v2, 2 × Standard_D4s_v3.
			// Different VM sizes produce different template hashes, so these
			// should result in 2 separate batches.
			claims := []*karpv1.NodeClaim{
				makeNodeClaimForInstanceType("Standard_D2_v2"),
				makeNodeClaimForInstanceType("Standard_D2_v2"),
				makeNodeClaimForInstanceType("Standard_D2_v2"),
				makeNodeClaimForInstanceType("Standard_D4s_v3"),
				makeNodeClaimForInstanceType("Standard_D4s_v3"),
			}

			results := concurrentCreateAndWaitForPromises(claims)

			for i, r := range results {
				Expect(r.Err).ToNot(HaveOccurred(), "claim %d failed", i)
				Expect(r.NodeClaim).ToNot(BeNil(), "claim %d returned nil", i)
			}

			// Core assertion: 2 distinct template hashes → 2 batch API calls
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(2),
				"expected 2 batch API calls for 2 distinct template hashes")

			// All 5 machines should be in the data store
			Expect(countMachinesInDataStore()).To(Equal(5))

			// Verify correct VM size distribution
			machines := collectMachinesFromDataStore()
			vmSizes := make(map[string]int)
			for _, m := range machines {
				if m.Properties != nil && m.Properties.Hardware != nil && m.Properties.Hardware.VMSize != nil {
					vmSizes[*m.Properties.Hardware.VMSize]++
				}
			}
			Expect(vmSizes["Standard_D2_v2"]).To(Equal(3))
			Expect(vmSizes["Standard_D4s_v3"]).To(Equal(2))
		})

		It("should propagate per-machine tags through batch", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()

			// Create 3 same-template NodeClaims. Even though they share the
			// same template, each machine should get unique per-machine tags
			// (e.g., nodeclaim name, creation timestamp) because the batch
			// coordinator preserves per-machine entries.
			const count = 3
			claims := make([]*karpv1.NodeClaim, count)
			for i := 0; i < count; i++ {
				claims[i] = makeNodeClaimForInstanceType("Standard_D2_v2")
			}

			results := concurrentCreateAndWaitForPromises(claims)

			for i, r := range results {
				Expect(r.Err).ToNot(HaveOccurred(), "claim %d failed", i)
			}

			// All should be batched into 1 call
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

			// Each machine should have unique tags
			machines := collectMachinesFromDataStore()
			Expect(machines).To(HaveLen(count))

			machineNames := make(map[string]bool)
			for _, m := range machines {
				Expect(m.Name).ToNot(BeNil(), "machine name should not be nil")
				Expect(machineNames).ToNot(HaveKey(*m.Name), "machine names should be unique")
				machineNames[*m.Name] = true

				// Per-machine tags should be set
				Expect(m.Properties).ToNot(BeNil())
				Expect(m.Properties.Tags).ToNot(BeNil(), "per-machine tags should not be nil for machine %s", *m.Name)
			}
		})

		It("should propagate batch error to all machines in the batch", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()

			// Inject a BeginError — this simulates the Azure API returning
			// an error for the entire batch.
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.BeginError.Set(
				&azcore.ResponseError{ErrorCode: "BatchFailed"},
			)
			defer azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.BeginError.Set(nil)

			const count = 3
			claims := make([]*karpv1.NodeClaim, count)
			for i := 0; i < count; i++ {
				claims[i] = makeNodeClaimForInstanceType("Standard_D2_v2")
			}

			results := concurrentCreateAndWaitForPromises(claims)

			// All creates should have failed
			for i, r := range results {
				Expect(r.Err).To(HaveOccurred(), "claim %d should have failed", i)
			}

			// No machines should have been stored
			Expect(countMachinesInDataStore()).To(Equal(0))
		})
	})
}
