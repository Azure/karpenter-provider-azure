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

package nodeclaim

import (
	"context"
	"fmt"
	"regexp"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

// GetAKSNodeClass resolves the AKSNodeClass from the NodeClaim's NodeClassRef.
// If the NodeClass for the nodeClaim has DeletionTimestamp set, an error is returned.
func GetAKSNodeClass(ctx context.Context, kubeClient client.Client, nodeClaim *karpv1.NodeClaim) (*v1beta1.AKSNodeClass, error) {
	if nodeClaim.Spec.NodeClassRef == nil {
		return nil, fmt.Errorf("nodeClaim %s does not have a nodeClassRef", nodeClaim.Name)
	}

	nodeClass := &v1beta1.AKSNodeClass{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Name: nodeClaim.Spec.NodeClassRef.Name}, nodeClass); err != nil {
		return nil, fmt.Errorf("getting AKSNodeClass %s: %w", nodeClaim.Spec.NodeClassRef.Name, err)
	}

	if !nodeClass.DeletionTimestamp.IsZero() {
		return nil, utils.NewTerminatingResourceError(schema.GroupResource{Group: apis.Group, Resource: "aksnodeclasses"}, nodeClass.Name)
	}

	return nodeClass, nil
}

// TODO: Could go onto vmInstanceProvider?
// GetVM gets the Azure VM associated with the NodeClaim
func GetVM(ctx context.Context, vmInstanceProvider instance.VMProvider, nodeClaim *karpv1.NodeClaim) (*armcompute.VirtualMachine, error) {
	vmName, err := GetVMName(nodeClaim.Status.ProviderID)
	if err != nil {
		return nil, err
	}

	vm, err := vmInstanceProvider.Get(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("getting azure VM %s: %w", vmName, err)
	}

	return vm, nil
}

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
