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

package utils

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/mitchellh/hashstructure/v2"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"

	"github.com/samber/lo"

	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/cloud-provider-azure/pkg/provider"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// GetVMName parses the provider ID stored on the node to get the vmName
// associated with a node
func GetVMName(providerID string) (string, error) {
	// standalone VMs have providerID in the format: azure:///subscriptions/<subscriptionID>/resourceGroups/<resourceGroup>/providers/Microsoft.Compute/virtualMachines/<instanceID>
	r := regexp.MustCompile(`azure:///subscriptions/.*/resourceGroups/.*/providers/Microsoft.Compute/virtualMachines/(?P<InstanceID>.*)`)
	matches := r.FindStringSubmatch(providerID)
	if matches == nil {
		return "", fmt.Errorf("parsing vm name %s", providerID)
	}
	for i, name := range r.SubexpNames() {
		if name == "InstanceID" {
			return matches[i], nil
		}
	}
	return "", fmt.Errorf("parsing vm name %s", providerID)
}

func ResourceIDToProviderID(ctx context.Context, id string) string {
	providerID := fmt.Sprintf("azure://%s", id)
	// for historical reasons Azure providerID has the resource group name in lower case
	providerIDLowerRG, err := provider.ConvertResourceGroupNameToLower(providerID)
	if err != nil {
		log.FromContext(ctx).WithValues("providerID", providerID).Info("WARN: failed to convert resource group name to lower case in providerID", "error", err)
		// fallback to original providerID
		return providerID
	}
	return providerIDLowerRG
}

// WithDefaultFloat64 returns the float64 value of the supplied environment variable or, if not present,
// the supplied default value. If the float64 conversion fails, returns the default
func WithDefaultFloat64(key string, def float64) float64 {
	val, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return def
	}
	return f
}

func ImageReferenceToString(imageRef *armcompute.ImageReference) string {
	// Check for Custom Image
	if imageRef.ID != nil && *imageRef.ID != "" {
		return *imageRef.ID
	}

	// Check for Community Image
	if imageRef.CommunityGalleryImageID != nil && *imageRef.CommunityGalleryImageID != "" {
		return *imageRef.CommunityGalleryImageID
	}

	// Check for Shared Gallery Image
	if imageRef.SharedGalleryImageID != nil && *imageRef.SharedGalleryImageID != "" {
		return *imageRef.SharedGalleryImageID
	}

	// Check for Platform Image and use standard string representation
	if imageRef.Publisher != nil && imageRef.Offer != nil && imageRef.SKU != nil && imageRef.Version != nil {
		// Use the standard format: Publisher:Offer:Sku:Version
		return fmt.Sprintf("%s:%s:%s:%s",
			*imageRef.Publisher, *imageRef.Offer, *imageRef.SKU, *imageRef.Version)
	}

	return ""
}

func IsVMDeleting(vm armcompute.VirtualMachine) bool {
	if vm.Properties != nil && vm.Properties.ProvisioningState != nil {
		return *vm.Properties.ProvisioningState == "Deleting"
	}
	return false
}

// StringMap returns the string map representation of the resource list
func StringMap(list v1.ResourceList) map[string]string {
	if list == nil {
		return nil
	}
	m := make(map[string]string)
	for k, v := range list {
		m[k.String()] = v.String()
	}
	return m
}

// PrettySlice truncates a slice after a certain number of max items to ensure
// that the Slice isn't too long
func PrettySlice[T any](s []T, maxItems int) string {
	var sb strings.Builder
	for i, elem := range s {
		if i > maxItems-1 {
			fmt.Fprintf(&sb, " and %d other(s)", len(s)-i)
			break
		} else if i > 0 {
			fmt.Fprint(&sb, ", ")
		}
		fmt.Fprint(&sb, elem)
	}
	return sb.String()
}

// GetMaxPods resolves what we should set max pods to for a given nodeclass.
// If not specified, defaults based on network-plugin. 30 for "azure", 110 for "kubenet",
// or 250 for "none" and network plugin mode overlay.
func GetMaxPods(nodeClass *v1beta1.AKSNodeClass, networkPlugin, networkPluginMode string) int32 {
	if nodeClass.Spec.MaxPods != nil {
		return lo.FromPtr(nodeClass.Spec.MaxPods)
	}
	switch {
	case networkPlugin == consts.NetworkPluginNone:
		return consts.DefaultNetPluginNoneMaxPods
	case networkPlugin == consts.NetworkPluginAzure && networkPluginMode == consts.NetworkPluginModeOverlay:
		return consts.DefaultOverlayMaxPods
	case networkPlugin == consts.NetworkPluginAzure && networkPluginMode == consts.NetworkPluginModeNone:
		return consts.DefaultNodeSubnetMaxPods
	default:
		return consts.DefaultKubernetesMaxPods
	}
}

var managedVNETPattern = regexp.MustCompile(`(?i)^aks-vnet-\d{8}$`)

const managedSubnetName = "aks-subnet"

// IsAKSManagedVNET determines if the vnet managed or not.
// Note: You can "trick" this function if you really try by (for example) createding a VNET that looks like
// an AKS managed VNET, with the same resource group as the MC RG, in a different subscription, or by creating
// your own VNET in the MC RG whose name matches the AKS pattern but the VNET is actually yours rather than ours.
func IsAKSManagedVNET(nodeResourceGroup string, subnetID string) (bool, error) {
	// TODO: I kinda think we should be using arm.ParseResourceID rather than rolling our own
	id, err := GetVnetSubnetIDComponents(subnetID)
	if err != nil {
		return false, err
	}

	return managedVNETPattern.MatchString(id.VNetName) &&
		strings.EqualFold(nodeResourceGroup, id.ResourceGroupName) &&
		strings.EqualFold(id.SubnetName, managedSubnetName), nil
}

// HasChanged returns if the given value has changed, given the existing and new instance
//
// This option is accessible in place of using a ChangeMonitor, when there's access to both
// the existing and new data.
func HasChanged(existing, new any, options *hashstructure.HashOptions) bool {
	// In the case of errors, the zero value from hashing will be compared, similar to ChangeMonitor
	existingHV, _ := hashstructure.Hash(existing, hashstructure.FormatV2, options)
	newHV, _ := hashstructure.Hash(new, hashstructure.FormatV2, options)
	return existingHV != newHV
}
