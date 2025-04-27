package instance

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	vmExtensionType = "Microsoft.Compute/virtualMachines/extensions"
)

const (
	cseNameWindows      = "windows-cse-agent-karpenter"
	cseTypeWindows      = "CustomScriptExtension"
	csePublisherWindows = "Microsoft.Compute"
	cseVersionWindows   = "1.10"
	cseNameLinux        = "cse-agent-karpenter"
	cseTypeLinux        = "CustomScript"
	csePublisherLinux   = "Microsoft.Azure.Extensions"
	cseVersionLinux     = "2.0"
)

const (
	aksIdentifyingExtensionName      = "computeAksLinuxBilling"
	aksIdentifyingExtensionPublisher = "Microsoft.AKS"
	aksIdentifyingExtensionTypeLinux = "Compute.AKS.Linux.Billing"
)

// createAKSIdentifyingExtension attaches a VM extension to identify that this VM participates in an AKS cluster
func (p *DefaultProvider) createAKSIdentifyingExtension(ctx context.Context, vmName string) (err error) {
	vmExt := p.getAKSIdentifyingExtension()
	vmExtName := *vmExt.Name
	log.FromContext(ctx).V(1).Info(fmt.Sprintf("Creating virtual machine AKS identifying extension for %s", vmName))
	v, err := createVirtualMachineExtension(ctx, p.azClient.virtualMachinesExtensionClient, p.resourceGroup, vmName, vmExtName, *vmExt)
	if err != nil {
		log.FromContext(ctx).Error(err, fmt.Sprintf("Creating VM AKS identifying extension for VM %q failed", vmName))
		return fmt.Errorf("creating VM AKS identifying extension for VM %q, %w failed", vmName, err)
	}
	log.FromContext(ctx).V(1).Info(fmt.Sprintf("Created  virtual machine AKS identifying extension for %s, with an id of %s", vmName, *v.ID))
	return nil
}

func (p *DefaultProvider) createCSExtension(ctx context.Context, vmName string, cse string, isWindows bool) (err error) {
	vmExt := p.getCSExtension(cse, isWindows)
	vmExtName := *vmExt.Name
	log.FromContext(ctx).V(1).Info(fmt.Sprintf("Creating virtual machine CSE for %s", vmName))
	v, err := createVirtualMachineExtension(ctx, p.azClient.virtualMachinesExtensionClient, p.resourceGroup, vmName, vmExtName, *vmExt)
	if err != nil {
		log.FromContext(ctx).Error(err, fmt.Sprintf("Creating VM CSE for VM %q failed", vmName))
		return fmt.Errorf("creating VM CSE for VM %q, %w failed", vmName, err)
	}
	log.FromContext(ctx).V(1).Info(fmt.Sprintf("Created virtual machine CSE for %s, with an id of %s", vmName, *v.ID))
	return nil
}

func (p *DefaultProvider) requiredVMExtensionsInstalled(vm armcompute.VirtualMachine) bool {
	if len(vm.Resources) < 2 {
		return false
	}

	var foundCSE bool // zero values are bool
	var foundIdentifying bool
	for _, extension := range vm.Resources {
		if extension == nil {
			continue
		}
		if lo.FromPtr(extension.Name) == cseNameLinux {
			foundCSE = true
		}
		if lo.FromPtr(extension.Name) == aksIdentifyingExtensionName {
			foundIdentifying = true
		}
	}
	return foundCSE && foundIdentifying
}

func (p *DefaultProvider) getAKSIdentifyingExtension() *armcompute.VirtualMachineExtension {
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
