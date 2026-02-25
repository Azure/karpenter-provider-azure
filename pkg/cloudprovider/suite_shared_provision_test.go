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

// This file provides shared types and setup helpers for provisioning tests that
// run against BOTH AKSMachineAPI and AKSScriptless (VM) modes. The adapter pattern
// abstracts away mode-specific error injection and result extraction, allowing
// identical test logic to verify behavior across both provisioning paths.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"k8s.io/client-go/tools/record"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
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
	// VM-specific fields (populated only for AKSScriptless mode)
	customData    string // decoded base64 custom data from OSProfile
	diffDiskOption string // DiffDiskSettings.Option value (e.g. "Local")
	isCommunityGalleryImage bool // whether the image source is a community gallery
}

// provisionTestMode provides mode-specific callbacks for shared provisioning tests.
// All closures capture package-level vars (azureEnv, etc.) by reference, so they
// always reflect the value set by the current test's BeforeEach.
type provisionTestMode struct {
	// isVM is true for AKSScriptless (VM) mode, false for AKSMachineAPI mode.
	isVM bool
	// setError injects a provisioning error by error type constant.
	setError func(errorType string)
	// setZoneAllocError injects a ZoneAllocationFailed error for a specific SKU and zone.
	setZoneAllocError func(sku, zone string)
	// setSkuNotAvailable injects a SKUNotAvailable error for a specific SKU name.
	setSkuNotAvailable func(skuName string)
	// clearError removes any injected provisioning error.
	clearError func()
	// getCreateCallCount returns how many creation API calls were made.
	getCreateCallCount func() int
	// popCreationResult pops the most recent creation input and returns normalized fields.
	popCreationResult func() creationResult
	// getSubnetID extracts the subnet ID from the most recent creation input (NIC for VM, machine properties for AKS Machine).
	getSubnetID func() string
}

// Error type constants for provisionTestMode.setError
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
	vmLowPriorityQuotaMsg = "Operation could not be completed as it results in exceeding approved LowPriorityCores quota. Additional details - Deployment Model: Resource Manager, Location: westus2, Current Limit: 0, Current Usage: 0, Additional Required: 32, (Minimum) New Limit Required: 32."
	vmOverconstrainedZonalMsg = "Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive. Please remove some constraints and try again."
	vmOverconstrainedMsg = "Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive."
	vmAllocationFailedMsg = "Allocation failed. We do not have sufficient capacity for the requested VM size in this region."
	vmFamilyQuotaExceededMsg = "Operation could not be completed as it results in exceeding approved standardDLSv5Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: westus2, Current Limit: 100, Current Usage: 96, Additional Required: 32, (Minimum) New Limit Required: 128."
	vmFamilyQuotaZeroMsg = "Operation could not be completed as it results in exceeding approved standardDLSv5Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: westus2, Current Limit: 0, Current Usage: 0, Additional Required: 32, (Minimum) New Limit Required: 32."
	vmRegionalCoresQuotaMsg = "Operation could not be completed as it results in exceeding approved Total Regional Cores quota. Additional details - Deployment Model: Resource Manager, Location: uksouth, Current Limit: 100, Current Usage: 100, Additional Required: 64, (Minimum) New Limit Required: 164."
	vmGenericCreationMsg = "Failed to create VM"
)

// createVMSDKErrorBody constructs an Azure SDK error response body for VM error injection.
func createVMSDKErrorBody(code, message string) io.ReadCloser {
	return io.NopCloser(bytes.NewReader([]byte(fmt.Sprintf(`{"error":{"code": "%s", "message": "%s"}}`, code, message))))
}

// setupAKSMachineAPIMode configures test infrastructure for AKSMachineAPI provisioning mode.
func setupAKSMachineAPIMode() {
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

// setupVMMode configures test infrastructure for AKSScriptless (VM) provisioning mode.
func setupVMMode() {
	testOptions = test.Options(test.OptionsFields{
		UseSIG: lo.ToPtr(true),
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

// teardownProvisionMode resets state after each test.
func teardownProvisionMode() {
	cloudProvider.WaitForInstancePromises()
	cluster.Reset()
	clusterNonZonal.Reset()
	azureEnv.Reset()
	azureEnvNonZonal.Reset()
}

// aksMachineProvisionMode returns the adapter for AKSMachineAPI provisioning mode.
func aksMachineProvisionMode() provisionTestMode {
	return provisionTestMode{
		isVM: false,
		setError: func(errorType string) {
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
		},
		setZoneAllocError: func(sku, zone string) {
			azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorZoneAllocationFailed(sku, zone)
		},
		setSkuNotAvailable: func(skuName string) {
			azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorSkuNotAvailable(skuName, fake.Region)
		},
		clearError: func() {
			azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
		},
		getCreateCallCount: func() int {
			return azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()
		},
		popCreationResult: func() creationResult {
			GinkgoHelper()
			input := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			m := input.AKSMachine
			// Preserved nil guards from original AKS Machine tests
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
		},
		getSubnetID: func() string {
			GinkgoHelper()
			input := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			m := input.AKSMachine
			// Preserved nil guards from original AKS Machine subnet test
			Expect(m.Properties.Network).ToNot(BeNil())
			Expect(m.Properties.Network.VnetSubnetID).ToNot(BeNil())
			return *m.Properties.Network.VnetSubnetID
		},
	}
}

// vmProvisionMode returns the adapter for AKSScriptless (VM) provisioning mode.
func vmProvisionMode() provisionTestMode {
	return provisionTestMode{
		isVM: true,
		setError: func(errorType string) {
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
		},
		setZoneAllocError: func(sku, zone string) {
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.Error.Set(
				&azcore.ResponseError{ErrorCode: sdkerrors.ZoneAllocationFailed},
			)
		},
		setSkuNotAvailable: func(skuName string) {
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
				&azcore.ResponseError{ErrorCode: sdkerrors.SKUNotAvailableErrorCode},
			)
		},
		clearError: func() {
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.Error.Set(nil)
		},
		getCreateCallCount: func() int {
			return azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()
		},
		popCreationResult: func() creationResult {
			GinkgoHelper()
			input := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			vm := input.VM
			// Preserved nil guards from original VM tests
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
		},
		getSubnetID: func() string {
			GinkgoHelper()
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
		},
	}
}
