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
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/awslabs/operatorpkg/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/metrics"

	// nolint SA1019 - deprecated package
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/inplaceupdate"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"

	"github.com/samber/lo"

	cloudproviderevents "github.com/Azure/karpenter-provider-azure/pkg/cloudprovider/events"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	labelspkg "github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	nodeclaimutils "github.com/Azure/karpenter-provider-azure/pkg/utils/nodeclaim"

	coreapis "sigs.k8s.io/karpenter/pkg/apis"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/events"

	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/resources"
)

const (
	NodeClassReadinessUnknownReason    = "NodeClassReadinessUnknown"
	InstanceTypeResolutionFailedReason = "InstanceTypeResolutionFailed"
	CreateInstanceFailedReason         = "CreateInstanceFailed"
)

var _ cloudprovider.CloudProvider = (*CloudProvider)(nil)

type CloudProvider struct {
	instanceTypeProvider       instancetype.Provider
	vmInstanceProvider         instance.VMProvider // Note that even when provision mode does not create with VM instance provider, it is still being used to handle existing VM instances.
	aksMachineInstanceProvider instance.AKSMachineProvider
	kubeClient                 client.Client
	imageProvider              imagefamily.NodeImageProvider
	recorder                   events.Recorder
}

func New(
	instanceTypeProvider instancetype.Provider,
	vmInstanceProvider instance.VMProvider,
	aksMachineInstanceProvider instance.AKSMachineProvider,
	recorder events.Recorder,
	kubeClient client.Client,
	imageProvider imagefamily.NodeImageProvider,
) *CloudProvider {
	return &CloudProvider{
		instanceTypeProvider:       instanceTypeProvider,
		vmInstanceProvider:         vmInstanceProvider,
		aksMachineInstanceProvider: aksMachineInstanceProvider,
		kubeClient:                 kubeClient,
		imageProvider:              imageProvider,
		recorder:                   recorder,
	}
}

func (c *CloudProvider) validateNodeClass(nodeClass *v1beta1.AKSNodeClass) error {
	nodeClassReady := nodeClass.StatusConditions().Get(status.ConditionReady)
	if nodeClassReady.IsFalse() {
		return cloudprovider.NewNodeClassNotReadyError(stderrors.New(nodeClassReady.Message))
	}
	if nodeClassReady.IsUnknown() {
		return cloudprovider.NewCreateError(fmt.Errorf("resolving NodeClass readiness, NodeClass is in Ready=Unknown, %s", nodeClassReady.Message), NodeClassReadinessUnknownReason, "NodeClass is in Ready=Unknown")
	}
	if _, err := nodeClass.GetKubernetesVersion(); err != nil {
		return err
	}
	if _, err := nodeClass.GetImages(); err != nil {
		return err
	}
	return nil
}

func (c *CloudProvider) Create(ctx context.Context, nodeClaim *karpv1.NodeClaim) (*karpv1.NodeClaim, error) {
	nodeClass, err := nodeclaimutils.GetAKSNodeClass(ctx, c.kubeClient, nodeClaim)
	if err != nil {
		if errors.IsNotFound(err) {
			c.recorder.Publish(cloudproviderevents.NodeClaimFailedToResolveNodeClass(nodeClaim))
		}
		// We treat a failure to resolve the NodeClass as an ICE since this means there is no capacity possibilities for this NodeClaim
		return nil, cloudprovider.NewInsufficientCapacityError(fmt.Errorf("resolving node class, %w", err))
	}

	/*
		// TODO: Remove this after v1
		nodePool, err := utils.ResolveNodePoolFromNodeClaim(ctx, c.kubeClient, nodeClaim)
		if err != nil {
			return nil, err
		}
		kubeletHash, err := utils.GetHashKubelet(nodePool, nodeClass)
		if err != nil {
			return nil, err
		}
	*/
	if err = c.validateNodeClass(nodeClass); err != nil {
		return nil, err
	}

	instanceTypes, err := c.resolveInstanceTypes(ctx, nodeClaim, nodeClass)
	if err != nil {
		return nil, cloudprovider.NewCreateError(fmt.Errorf("resolving instance types, %w", err), InstanceTypeResolutionFailedReason, truncateMessage(err.Error()))
	}
	if len(instanceTypes) == 0 {
		return nil, cloudprovider.NewInsufficientCapacityError(fmt.Errorf("all requested instance types were unavailable during launch"))
	}

	// Choose provider based on provision mode
	if options.FromContext(ctx).ProvisionMode == consts.ProvisionModeAKSMachineAPI {
		return c.createAKSMachineInstance(ctx, nodeClass, nodeClaim, instanceTypes)
	}

	return c.createVMInstance(ctx, nodeClass, nodeClaim, instanceTypes)
}

func (c *CloudProvider) createVMInstance(ctx context.Context, nodeClass *v1beta1.AKSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType) (*karpv1.NodeClaim, error) {
	vmPromise, err := c.vmInstanceProvider.BeginCreate(ctx, nodeClass, nodeClaim, instanceTypes)
	if err != nil {
		return nil, cloudprovider.NewCreateError(fmt.Errorf("creating instance failed, %w", err), CreateInstanceFailedReason, truncateMessage(err.Error()))
	}

	if err := c.handleInstancePromise(ctx, vmPromise, nodeClaim); err != nil {
		return nil, err
	}

	vm := vmPromise.VM // This is best-effort populated by Karpenter to be used to create the VM server-side. Not all fields are guaranteed to be populated, especially status fields.
	// Double-check the code before making assumptions on their presence.
	instanceType, _ := lo.Find(instanceTypes, func(i *cloudprovider.InstanceType) bool {
		return i.Name == string(lo.FromPtr(vm.Properties.HardwareProfile.VMSize))
	})
	newNodeClaim, err := c.vmInstanceToNodeClaim(ctx, vm, instanceType)
	if err != nil {
		return nil, err
	}
	// Propagate single-value wellKnownLabels from the nodeClaim requirements to the labels.
	// This is required for scheduling in core to work correctly. If this is not done, on subsequent scheduling passes before the Node is
	// registered, the NodeClaim will not have the labels required to match the Pod and so a new NodeClaim will be created each time.
	// Note that AWS does this by explicitly setting the labels in their CloudProvider (see https://github.com/aws/karpenter-provider-aws/blob/main/pkg/cloudprovider/cloudprovider.go#L456)
	// rather than doing it in bulk here.
	// TODO: should we do like AWS and smuggle all of these labels through VM tags rather than just setting them here?
	newNodeClaim.Labels = lo.Assign(newNodeClaim.Labels, labelspkg.GetWellKnownSingleValuedRequirementLabels(scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)))

	if err := setAdditionalAnnotationsForNewNodeClaim(ctx, newNodeClaim, nodeClass); err != nil {
		return nil, err
	}
	return newNodeClaim, nil
}

func (c *CloudProvider) createAKSMachineInstance(ctx context.Context, nodeClass *v1beta1.AKSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType) (*karpv1.NodeClaim, error) {
	// Begin the creation of the instance
	aksMachinePromise, err := c.aksMachineInstanceProvider.BeginCreate(ctx, nodeClass, nodeClaim, instanceTypes)
	if err != nil {
		return nil, cloudprovider.NewCreateError(fmt.Errorf("creating AKS machine failed, %w", err), CreateInstanceFailedReason, truncateMessage(err.Error()))
	}

	// Handle the promise
	if err := c.handleInstancePromise(ctx, aksMachinePromise, nodeClaim); err != nil {
		return nil, err
	}

	// Convert the AKS machine to a NodeClaim
	newNodeClaim, err := instance.BuildNodeClaimFromAKSMachineTemplate(
		ctx, aksMachinePromise.AKSMachineTemplate,
		aksMachinePromise.InstanceType,
		aksMachinePromise.CapacityType,
		lo.ToPtr(aksMachinePromise.Zone),
		aksMachinePromise.AKSMachineID,
		aksMachinePromise.VMResourceID,
		false,
		aksMachinePromise.AKSMachineNodeImageVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to build NodeClaim from AKS machine template, %w", err)
	}

	if err := setAdditionalAnnotationsForNewNodeClaim(ctx, newNodeClaim, nodeClass); err != nil {
		return nil, err
	}

	return newNodeClaim, nil
}

// handleInstancePromise handles the instance promise, primarily deciding on sync/async provisioning.
func (c *CloudProvider) handleInstancePromise(ctx context.Context, instancePromise instance.Promise, nodeClaim *karpv1.NodeClaim) error {
	if isNodeClaimStandalone(nodeClaim) {
		// Standalone NodeClaims aren't re-queued for reconciliation in the provision_trigger controller,
		// so we delete them synchronously. After marking Launched=true,
		// their status can't be reverted to false once the delete completes due to how core caches nodeclaims in
		// the launch controller. This ensures we retry continuously until we hit the registration TTL
		err := instancePromise.Wait()
		if err != nil {
			c.handleInstancePromiseWaitError(ctx, instancePromise, nodeClaim, err)
			return cloudprovider.NewCreateError(fmt.Errorf("creating standalone instance failed, %w", err), CreateInstanceFailedReason, truncateMessage(err.Error()))
		}
	}
	// For NodePool-managed nodeclaims, launch a single goroutine to poll the returned promise.
	// Note that we could store the LRO details on the NodeClaim, but we don't bother today because Karpenter
	// crashes should be rare, and even in the case of a crash, as long as the node comes up successfully there's
	// no issue. If the node doesn't come up successfully in that case, the node and the linked claim will
	// be garbage collected after the TTL, but the cause of the nodes issue will be lost, as the LRO URL was
	// only held in memory.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				err := fmt.Errorf("%v", r)
				// Only log if context is still active to avoid logging after test completes
				if ctx.Err() == nil {
					log.FromContext(ctx).Error(err, "panic during waiting on instance promise")
				}
			}
		}()

		err := instancePromise.Wait()

		// Wait until the claim is Launched, to avoid racing with creation.
		// This isn't strictly required, but without this, failure test scenarios are harder
		// to write because the nodeClaim gets deleted by error handling below before
		// the EnsureApplied call finishes, so EnsureApplied creates it again (which is wrong/isn't how
		// it would actually happen in production).
		c.waitUntilLaunched(ctx, nodeClaim)

		if err != nil {
			c.handleInstancePromiseWaitError(ctx, instancePromise, nodeClaim, err)

			// For async provisioning, also delete the NodeClaim
			if deleteErr := c.kubeClient.Delete(ctx, nodeClaim); deleteErr != nil {
				deleteErr = client.IgnoreNotFound(deleteErr)
				if deleteErr != nil {
					log.FromContext(ctx).Error(deleteErr, "failed to delete nodeclaim, will wait for liveness TTL", "NodeClaim", nodeClaim.Name)
				}
			}
			metrics.NodeClaimsDisruptedTotal.Inc(map[string]string{
				metrics.ReasonLabel:       "async_provisioning",
				metrics.NodePoolLabel:     nodeClaim.Labels[karpv1.NodePoolLabelKey],
				metrics.CapacityTypeLabel: nodeClaim.Labels[karpv1.CapacityTypeLabelKey],
			})
		}
	}()
	return nil
}

func (c *CloudProvider) handleInstancePromiseWaitError(ctx context.Context, instancePromise instance.Promise, nodeClaim *karpv1.NodeClaim, waitErr error) {
	c.recorder.Publish(cloudproviderevents.NodeClaimFailedToRegister(nodeClaim, waitErr))
	log.FromContext(ctx).Error(waitErr, "failed launching nodeclaim")

	cleanUpError := instancePromise.Cleanup(ctx)
	if cleanUpError != nil {
		// Fallback to garbage collection to clean up the instance, if it survived.
		if cloudprovider.IgnoreNodeClaimNotFoundError(cleanUpError) != nil {
			log.FromContext(ctx).Error(cleanUpError, "failed to delete instance", "instanceName", instancePromise.GetInstanceName())
		}
	}
}

func (c *CloudProvider) waitUntilLaunched(ctx context.Context, nodeClaim *karpv1.NodeClaim) {
	freshClaim := &karpv1.NodeClaim{}
	for {
		err := c.kubeClient.Get(ctx, types.NamespacedName{Namespace: nodeClaim.Namespace, Name: nodeClaim.Name}, freshClaim)
		if err != nil {
			if client.IgnoreNotFound(err) == nil {
				return
			}
			// Only log if context is still active to avoid logging after test completes
			if ctx.Err() == nil {
				log.FromContext(ctx).Error(err, "failed getting nodeclaim to wait until launched")
			}
		}

		if cond := freshClaim.StatusConditions().Get(karpv1.ConditionTypeLaunched); !cond.IsUnknown() {
			return
		}

		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return // context was canceled
		}
	}
}

func (c *CloudProvider) List(ctx context.Context) ([]*karpv1.NodeClaim, error) {
	var nodeClaims []*karpv1.NodeClaim

	// List AKS machine-based nodes
	aksMachineInstances, err := c.aksMachineInstanceProvider.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing AKS machine instances, %w", err)
	}

	for _, aksMachineInstance := range aksMachineInstances {
		nodeClaim, err := c.resolveNodeClaimFromAKSMachine(ctx, aksMachineInstance)
		if err != nil {
			return nil, fmt.Errorf("converting AKS machine instance to node claim, %w", err)
		}

		nodeClaims = append(nodeClaims, nodeClaim)
	}

	// List VM-based nodes
	vmInstances, err := c.vmInstanceProvider.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing VM instances, %w", err)
	}

	for _, instance := range vmInstances {
		instanceType, err := c.resolveInstanceTypeFromVMInstance(ctx, instance)
		if err != nil {
			return nil, fmt.Errorf("resolving instance type for VM instance, %w", err)
		}
		nodeClaim, err := c.vmInstanceToNodeClaim(ctx, instance, instanceType)
		if err != nil {
			return nil, fmt.Errorf("converting VM instance to node claim, %w", err)
		}

		nodeClaims = append(nodeClaims, nodeClaim)
	}
	return nodeClaims, nil
}

func (c *CloudProvider) Get(ctx context.Context, providerID string) (*karpv1.NodeClaim, error) {
	vmName, err := nodeclaimutils.GetVMName(providerID)
	if err != nil {
		return nil, fmt.Errorf("getting vm name, %w", err)
	}
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues("vmName", vmName))

	aksMachinesPoolName := options.FromContext(ctx).AKSMachinesPoolName
	if aksMachineName, err := instance.GetAKSMachineNameFromVMName(aksMachinesPoolName, vmName); err == nil {
		// This could be an AKS machine-based node; try getting the AKS machine instance
		ctx := log.IntoContext(ctx, log.FromContext(ctx).WithValues("aksMachineName", aksMachineName))

		aksMachine, err := c.aksMachineInstanceProvider.Get(ctx, aksMachineName)
		if err == nil {
			nodeClaim, err := c.resolveNodeClaimFromAKSMachine(ctx, aksMachine)
			if err != nil {
				return nil, fmt.Errorf("converting AKS machine instance to node claim, %w", err)
			}
			return nodeClaim, nil
		} else if cloudprovider.IgnoreNodeClaimNotFoundError(err) != nil {
			return nil, fmt.Errorf("getting AKS machine instance, %w", err)
		}
		// Fallback to legacy VM-based node if not found
		// In the case that it is indeed AKS machine node, but somehow fail GET AKS machine and succeeded GET VM, ideally we want this call to fail.
		// However, being misrepresented only once is not fatal. "Illegal" in-place update will be reconciled back to the before, and there is no drift for VM nodes that won't happen with AKS machine nodes + DriftAction.
		// So, we could live with this for now.
	}

	vm, err := c.vmInstanceProvider.Get(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("getting VM instance, %w", err)
	}
	instanceType, err := c.resolveInstanceTypeFromVMInstance(ctx, vm)
	if err != nil {
		return nil, fmt.Errorf("resolving instance type, %w", err)
	}
	return c.vmInstanceToNodeClaim(ctx, vm, instanceType)
}

func (c *CloudProvider) LivenessProbe(req *http.Request) error {
	return c.instanceTypeProvider.LivenessProbe(req)
}

// GetInstanceTypes returns all available InstanceTypes
// May return apimachinery.NotFoundError if NodeClass is not found.
func (c *CloudProvider) GetInstanceTypes(ctx context.Context, nodePool *karpv1.NodePool) ([]*cloudprovider.InstanceType, error) {
	nodeClass, err := c.resolveNodeClassFromNodePool(ctx, nodePool)
	if err != nil {
		if errors.IsNotFound(err) {
			c.recorder.Publish(cloudproviderevents.NodePoolFailedToResolveNodeClass(nodePool))
		}
		// We must return an error here in the event of the node class not being found. Otherwise users just get
		// no instance types and a failure to schedule with no indicator pointing to a bad configuration
		// as the cause.
		return nil, fmt.Errorf("resolving node class, %w", err)
	}
	instanceTypes, err := c.instanceTypeProvider.List(ctx, nodeClass)
	if err != nil {
		return nil, err
	}
	return instanceTypes, nil
}

func (c *CloudProvider) Delete(ctx context.Context, nodeClaim *karpv1.NodeClaim) error {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues("NodeClaim", nodeClaim.Name))

	// AKS machine-based node?
	if aksMachineName, isAKSMachine := instance.GetAKSMachineNameFromNodeClaim(nodeClaim); isAKSMachine {
		return c.aksMachineInstanceProvider.Delete(ctx, aksMachineName)
	}

	// VM-based node
	vmName, err := nodeclaimutils.GetVMName(nodeClaim.Status.ProviderID)
	if err != nil {
		return fmt.Errorf("getting VM name, %w", err)
	}
	return c.vmInstanceProvider.Delete(ctx, vmName)
}

func (c *CloudProvider) IsDrifted(ctx context.Context, nodeClaim *karpv1.NodeClaim) (cloudprovider.DriftReason, error) {
	// Not needed when GetInstanceTypes removes nodepool dependency
	nodePoolName, ok := nodeClaim.Labels[karpv1.NodePoolLabelKey]
	if !ok {
		return "", nil
	}
	nodePool := &karpv1.NodePool{}
	if err := c.kubeClient.Get(ctx, types.NamespacedName{Name: nodePoolName}, nodePool); err != nil {
		return "", client.IgnoreNotFound(err)
	}
	if nodePool.Spec.Template.Spec.NodeClassRef == nil {
		return "", nil
	}
	nodeClass, err := c.resolveNodeClassFromNodePool(ctx, nodePool)
	if err != nil {
		if errors.IsNotFound(err) {
			c.recorder.Publish(cloudproviderevents.NodePoolFailedToResolveNodeClass(nodePool))
		}
		return "", client.IgnoreNotFound(fmt.Errorf("resolving node class, %w", err))
	}
	driftReason, err := c.isNodeClassDrifted(ctx, nodeClaim, nodeClass)
	if err != nil {
		return "", err
	}
	return driftReason, nil
}

// Name returns the CloudProvider implementation name.
func (c *CloudProvider) Name() string {
	return "azure"
}

func (c *CloudProvider) GetSupportedNodeClasses() []status.Object {
	return []status.Object{&v1beta1.AKSNodeClass{}}
}

// TODO: review repair policies
func (c *CloudProvider) RepairPolicies() []cloudprovider.RepairPolicy {
	return []cloudprovider.RepairPolicy{
		// Supported Kubelet fields
		{
			ConditionType:      corev1.NodeReady,
			ConditionStatus:    corev1.ConditionFalse,
			TolerationDuration: 10 * time.Minute,
		},
		{
			ConditionType:      corev1.NodeReady,
			ConditionStatus:    corev1.ConditionUnknown,
			TolerationDuration: 10 * time.Minute,
		},
	}
}

// May return apimachinery.NotFoundError if NodePool is not found.
func (c *CloudProvider) resolveNodeClassFromNodePool(ctx context.Context, nodePool *karpv1.NodePool) (*v1beta1.AKSNodeClass, error) {
	nodeClass := &v1beta1.AKSNodeClass{}
	if err := c.kubeClient.Get(ctx, types.NamespacedName{Name: nodePool.Spec.Template.Spec.NodeClassRef.Name}, nodeClass); err != nil {
		return nil, err
	}
	// For the purposes of NodeClass CloudProvider resolution, we treat deleting NodeClasses as NotFound
	if !nodeClass.DeletionTimestamp.IsZero() {
		// For the purposes of NodeClass CloudProvider resolution, we treat deleting NodeClasses as NotFound,
		// but we return a different error message to be clearer to users
		return nil, utils.NewTerminatingResourceError(schema.GroupResource{Group: apis.Group, Resource: "aksnodeclasses"}, nodeClass.Name)
	}
	return nodeClass, nil
}
func (c *CloudProvider) resolveInstanceTypes(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1beta1.AKSNodeClass) ([]*cloudprovider.InstanceType, error) {
	instanceTypes, err := c.instanceTypeProvider.List(ctx, nodeClass)
	if err != nil {
		return nil, fmt.Errorf("getting instance types, %w", err)
	}

	reqs := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)
	return lo.Filter(instanceTypes, func(i *cloudprovider.InstanceType, _ int) bool {
		return reqs.Compatible(i.Requirements, v1beta1.AllowUndefinedWellKnownAndRestrictedLabels) == nil &&
			len(i.Offerings.Compatible(reqs).Available()) > 0 &&
			resources.Fits(nodeClaim.Spec.Resources.Requests, i.Allocatable())
	}), nil

	// Old logic
	// return lo.Filter(instanceTypes, func(i *cloudprovider.InstanceType, _ int) bool {
	//	return reqs.Get(v1.LabelInstanceTypeStable).Has(i.Name) &&
	//		len(i.Offerings.Requirements(reqs).Available()) > 0
	// }), nil
}

func (c *CloudProvider) resolveInstanceTypeFromVMInstance(ctx context.Context, vm *armcompute.VirtualMachine) (*cloudprovider.InstanceType, error) {
	nodePool, err := c.resolveNodePoolFromVMInstance(ctx, vm)
	if err != nil {
		// If we can't resolve the provisioner, we fallback to not getting instance type info
		return nil, client.IgnoreNotFound(fmt.Errorf("resolving node pool, %w", err))
	}
	instanceTypes, err := c.GetInstanceTypes(ctx, nodePool)
	if err != nil {
		// If we can't resolve the nodepool, we fallback to not getting instance type info
		return nil, client.IgnoreNotFound(fmt.Errorf("resolving node template, %w", err))
	}
	instanceType, _ := lo.Find(instanceTypes, func(i *cloudprovider.InstanceType) bool {
		return i.Name == string(lo.FromPtr(vm.Properties.HardwareProfile.VMSize))
	})
	return instanceType, nil
}

func (c *CloudProvider) resolveNodePoolFromVMInstance(ctx context.Context, vm *armcompute.VirtualMachine) (*karpv1.NodePool, error) {
	nodePoolName, ok := vm.Tags[launchtemplate.NodePoolTagKey]
	if ok && *nodePoolName != "" {
		nodePool := &karpv1.NodePool{}
		if err := c.kubeClient.Get(ctx, types.NamespacedName{Name: *nodePoolName}, nodePool); err != nil {
			return nil, err
		}
		return nodePool, nil
	}

	return nil, errors.NewNotFound(schema.GroupResource{Group: coreapis.Group, Resource: "nodepools"}, "")
}

func (c *CloudProvider) vmInstanceToNodeClaim(ctx context.Context, vm *armcompute.VirtualMachine, instanceType *cloudprovider.InstanceType) (*karpv1.NodeClaim, error) {
	nodeClaim := &karpv1.NodeClaim{}
	labels := map[string]string{}
	annotations := map[string]string{}

	if instanceType != nil {
		labels = labelspkg.GetAllSingleValuedRequirementLabels(instanceType.Requirements)
		nodeClaim.Status.Capacity = lo.PickBy(instanceType.Capacity, func(_ corev1.ResourceName, v resource.Quantity) bool { return !resources.IsZero(v) })
		nodeClaim.Status.Allocatable = lo.PickBy(instanceType.Allocatable(), func(_ corev1.ResourceName, v resource.Quantity) bool { return !resources.IsZero(v) })
	}

	if zone, err := utils.MakeAKSLabelZoneFromVM(vm); err != nil {
		log.FromContext(ctx).Info("failed to get zone for VM, zone label will be empty", "vmName", *vm.Name, "error", err)
	} else {
		labels[corev1.LabelTopologyZone] = zone
	}

	labels[karpv1.CapacityTypeLabelKey] = instance.GetCapacityTypeFromVM(vm)

	if tag, ok := vm.Tags[launchtemplate.NodePoolTagKey]; ok {
		labels[karpv1.NodePoolLabelKey] = *tag
	}

	nodeClaim.Name = GenerateNodeClaimName(*vm.Name)
	nodeClaim.Labels = labels
	nodeClaim.Annotations = annotations
	nodeClaim.CreationTimestamp = metav1.Time{Time: *vm.Properties.TimeCreated}
	// Set the deletionTimestamp to be the current time if the instance is currently terminating
	if utils.IsVMDeleting(*vm) {
		nodeClaim.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	}
	nodeClaim.Status.ProviderID = utils.VMResourceIDToProviderID(ctx, *vm.ID)
	if vm.Properties != nil && vm.Properties.StorageProfile != nil && vm.Properties.StorageProfile.ImageReference != nil {
		nodeClaim.Status.ImageID = utils.ImageReferenceToString(vm.Properties.StorageProfile.ImageReference)
	}
	return nodeClaim, nil
}

func GenerateNodeClaimName(vmName string) string {
	return strings.TrimPrefix(vmName, "aks-")
}

const truncateAt = 1200

func isNodeClaimStandalone(nodeClaim *karpv1.NodeClaim) bool {
	// NodeClaims without the nodepool label are considered standalone
	_, hasNodePoolLabel := nodeClaim.Labels[karpv1.NodePoolLabelKey]
	return !hasNodePoolLabel
}

func truncateMessage(msg string) string {
	if len(msg) < truncateAt {
		return msg
	}
	return msg[:truncateAt] + "..."
}

func setAdditionalAnnotationsForNewNodeClaim(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1beta1.AKSNodeClass) error {
	// Additional annotations
	// ASSUMPTION: this is not needed in other places that the core also wants NodeClaim (e.g., Get, List).
	// As of the time of writing, AWS is doing something similar.
	// Suggestion: could have added this in instance.BuildNodeClaimFromAKSMachine, but might sacrifice some performance (little?), and need to consider that the calculated hash may change.
	inPlaceUpdateHash, err := inplaceupdate.HashFromNodeClaim(options.FromContext(ctx), nodeClaim, nodeClass)
	if err != nil {
		return fmt.Errorf("failed to calculate in place update hash, %w", err)
	}
	nodeClaim.Annotations = lo.Assign(nodeClaim.Annotations, map[string]string{
		v1beta1.AnnotationAKSNodeClassHash:        nodeClass.Hash(),
		v1beta1.AnnotationAKSNodeClassHashVersion: v1beta1.AKSNodeClassHashVersion,
		v1beta1.AnnotationInPlaceUpdateHash:       inPlaceUpdateHash,
	})
	return nil
}

func (c *CloudProvider) resolveNodeClaimFromAKSMachine(ctx context.Context, aksMachine *armcontainerservice.Machine) (*karpv1.NodeClaim, error) {
	var instanceTypes []*cloudprovider.InstanceType
	nodePool, err := instance.FindNodePoolFromAKSMachine(ctx, aksMachine, c.kubeClient)
	if err == nil {
		gotInstanceTypes, err := c.GetInstanceTypes(ctx, nodePool)
		if err == nil {
			instanceTypes = gotInstanceTypes
		} else if client.IgnoreNotFound(err) != nil {
			// Unknown error
			return nil, fmt.Errorf("resolving node pool instance types, %w", err)
		}
		// If GetInstanceTypes returns not found, we tolerate. But, possible instance types will be empty.
	} else if client.IgnoreNotFound(err) != nil {
		// Unknown error
		return nil, fmt.Errorf("resolving node pool, %w", err)
	}
	// If FindNodePoolFromAKSMachine returns not found, we tolerate. But, possible instance types will be empty.

	// ASSUMPTION: all machines are in the same location, and in the current pool.
	nodeClaim, err := instance.BuildNodeClaimFromAKSMachine(ctx, aksMachine, instanceTypes, c.aksMachineInstanceProvider.GetMachinesPoolLocation())
	if err != nil {
		return nil, fmt.Errorf("converting AKS machine instance to node claim, %w", err)
	}

	return nodeClaim, nil
}
