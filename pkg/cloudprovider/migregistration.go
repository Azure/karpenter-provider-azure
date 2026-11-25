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

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	nodeutils "sigs.k8s.io/karpenter/pkg/utils/node"
	nodeclaimutils "sigs.k8s.io/karpenter/pkg/utils/nodeclaim"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

type MIGRegistrationHook struct {
	kubeClient client.Client
}

func NewMIGRegistrationHook(kubeClient client.Client) *MIGRegistrationHook {
	return &MIGRegistrationHook{kubeClient: kubeClient}
}

func (*MIGRegistrationHook) Name() string { return "ManagedMIGReboot" }

// Registered retains the unregistered taint through the initial managed MIG reboot.
// Current managed MIG images reboot on both A100 and H100. Reboot-free images will
// need a different launch contract. If the first boot is missed, fail closed and let
// the upstream registration timeout replace the claim rather than admitting workloads.
func (h *MIGRegistrationHook) Registered(ctx context.Context, nodeClaim *karpv1.NodeClaim) (corecloudprovider.NodeLifecycleHookResult, error) {
	if nodeClaim.Annotations[v1beta1.AnnotationMIGRebootRequired] != "true" {
		return corecloudprovider.NodeLifecycleHookResult{}, nil
	}
	pending := corecloudprovider.NodeLifecycleHookResult{RequeueAfter: 5 * time.Second}
	node, err := nodeclaimutils.NodeForNodeClaim(ctx, h.kubeClient, nodeClaim)
	if err != nil {
		return pending, fmt.Errorf("getting managed MIG node: %w", err)
	}
	bootID := node.Status.NodeInfo.BootID
	if bootID == "" {
		return pending, nil
	}
	initialBootID := nodeClaim.Annotations[v1beta1.AnnotationMIGInitialBootID]
	if initialBootID == "" {
		// The lifecycle controller persists hook metadata even while registration is pending.
		nodeClaim.Annotations[v1beta1.AnnotationMIGInitialBootID] = bootID
		log.FromContext(ctx).Info("waiting for managed MIG bootstrap reboot", "initialBootID", bootID)
		return pending, nil
	}
	if bootID == initialBootID || nodeutils.GetCondition(node, corev1.NodeReady).Status != corev1.ConditionTrue {
		return pending, nil
	}
	if ready, err := migResourcesRegistered(node, nodeClaim); !ready || err != nil {
		return pending, err
	}
	log.FromContext(ctx).Info("managed MIG bootstrap reboot completed", "initialBootID", initialBootID, "bootID", bootID)
	return corecloudprovider.NodeLifecycleHookResult{}, nil
}

func migResourcesRegistered(node *corev1.Node, nodeClaim *karpv1.NodeClaim) (bool, error) {
	// Use launch-time capacity, not pod requests or a potentially edited NodeClass.
	// CPU-only demand must also wait for the full MIG inventory after reboot.
	foundGPU := false
	for name, expected := range nodeClaim.Status.Capacity {
		if (name != "nvidia.com/gpu" && !strings.HasPrefix(string(name), "nvidia.com/mig-")) || expected.Sign() <= 0 {
			continue
		}
		foundGPU = true
		actual := node.Status.Allocatable[name]
		if actual.Cmp(expected) != 0 {
			return false, nil
		}
	}
	if !foundGPU {
		return false, fmt.Errorf("managed MIG nodeclaim has no expected GPU capacity")
	}
	return true, nil
}
