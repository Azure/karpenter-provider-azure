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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/inplaceupdate"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"

	"github.com/samber/lo"

	cloudproviderevents "github.com/Azure/karpenter-provider-azure/pkg/cloudprovider/events"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
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
	instanceTypeProvider instancetype.Provider
	instanceProvider     instance.Provider
	kubeClient           client.Client
	imageProvider        imagefamily.NodeImageProvider
	recorder             events.Recorder
}

func New(
	instanceTypeProvider instancetype.Provider,
	instanceProvider instance.Provider,
	recorder events.Recorder,
	kubeClient client.Client,
	imageProvider imagefamily.NodeImageProvider,
) *CloudProvider {
	return &CloudProvider{
		instanceTypeProvider: instanceTypeProvider,
		instanceProvider:     instanceProvider,
		kubeClient:           kubeClient,
		imageProvider:        imageProvider,
		recorder:             recorder,
	}
}

// Create a node given the constraints.
func (c *CloudProvider) handleInstanceCreation(ctx context.Context, instancePromise *instance.VirtualMachinePromise, nodeClaim *karpv1.NodeClaim) error {
	if c.isStandaloneNodeClaim(nodeClaim) {
		// processStandaloneNodeClaimDeletion:
		//   Standalone NodeClaims aren’t re-queued for reconciliation in the provision_trigger controller,
		//   so we delete them synchronously. After marking Launched=true,
		//   their status can’t be reverted to false once the delete completes due to how core caches nodeclaims in
		// 	 the lanch controller. This ensures we retry continuously until we hit the registration TTL
		err := instancePromise.Wait()
		if err != nil {
			return c.handleNodeClaimCreationError(ctx, err, instancePromise, nodeClaim, false)
		}
	} else {
		// For NodePool-managed nodeclaims, launch a single goroutine to poll the returned promise.
		// Note that we could store the LRO details on the NodeClaim, but we don't bother today because Karpenter
		// crashes should be rare, and even in the case of a crash, as long as the node comes up successfully there's
		// no issue. If the node doesn't come up successfully in that case, the node and the linked claim will
		// be garbage collected after the TTL, but the cause of the nodes issue will be lost, as the LRO URL was
		// only held in memory.
		go c.waitOnPromise(ctx, instancePromise, nodeClaim)
	}
	return nil
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
	instancePromise, err := c.instanceProvider.BeginCreate(ctx, nodeClass, nodeClaim, instanceTypes)
	if err != nil {
		return nil, cloudprovider.NewCreateError(fmt.Errorf("creating instance failed, %w", err), CreateInstanceFailedReason, truncateMessage(err.Error()))
	}

	if err = c.handleInstanceCreation(ctx, instancePromise, nodeClaim); err != nil {
		return nil, err
	}

	instance := instancePromise.VM
	instanceType, _ := lo.Find(instanceTypes, func(i *cloudprovider.InstanceType) bool {
		return i.Name == string(lo.FromPtr(instance.Properties.HardwareProfile.VMSize))
	})

	nc, err := c.instanceToNodeClaim(ctx, instance, instanceType)
	if err != nil {
		return nil, err
	}
	inPlaceUpdateHash, err := inplaceupdate.HashFromNodeClaim(options.FromContext(ctx), nc, nodeClass)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate in place update hash, %w", err)
	}
	nc.Annotations = lo.Assign(nc.Annotations, map[string]string{
		v1beta1.AnnotationAKSNodeClassHash:        nodeClass.Hash(),
		v1beta1.AnnotationAKSNodeClassHashVersion: v1beta1.AKSNodeClassHashVersion,
		v1beta1.AnnotationInPlaceUpdateHash:       inPlaceUpdateHash,
	})
	return nc, nil
}

func (c *CloudProvider) waitOnPromise(ctx context.Context, promise *instance.VirtualMachinePromise, nodeClaim *karpv1.NodeClaim) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("%v", r)
			log.FromContext(ctx).Error(err, "panic during waitOnPromise")
		}
	}()

	err := promise.Wait()

	// Wait until the claim is Launched, to avoid racing with creation.
	// This isn't strictly required, but without this, failure test scenarios are harder
	// to write because the nodeClaim gets deleted by error handling below before
	// the EnsureApplied call finishes, so EnsureApplied creates it again (which is wrong/isn't how
	// it would actually happen in production).
	c.waitUntilLaunched(ctx, nodeClaim)

	if err != nil {
		_ = c.handleNodeClaimCreationError(ctx, err, promise, nodeClaim, true)
		return
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
			log.FromContext(ctx).Error(err, "failed getting nodeclaim to wait until launched")
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

// handleNodeClaimCreationError handles common error processing for both standalone and async node claim creation failures
func (c *CloudProvider) handleNodeClaimCreationError(ctx context.Context, err error, promise *instance.VirtualMachinePromise, nodeClaim *karpv1.NodeClaim, removeNodeClaim bool) error {
	c.recorder.Publish(cloudproviderevents.NodeClaimFailedToRegister(nodeClaim, err))
	log.FromContext(ctx).Error(err, "failed launching nodeclaim")

	// Clean up the VM
	// TODO: This won't clean up leaked NICs if the VM doesn't exist... intentional?
	vmName := lo.FromPtr(promise.VM.Name)
	deleteErr := c.instanceProvider.Delete(ctx, vmName)
	if cloudprovider.IgnoreNodeClaimNotFoundError(deleteErr) != nil {
		log.FromContext(ctx).Error(deleteErr, "failed to delete VM", "vmName", vmName)
	}

	// For async provisioning, also delete the NodeClaim
	if removeNodeClaim {
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

	// For standalone node claims, return a CreateError
	if !removeNodeClaim {
		return cloudprovider.NewCreateError(fmt.Errorf("creating instance failed, %w", err), CreateInstanceFailedReason, truncateMessage(err.Error()))
	}
	return nil
}

func (c *CloudProvider) List(ctx context.Context) ([]*karpv1.NodeClaim, error) {
	instances, err := c.instanceProvider.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing instances, %w", err)
	}

	var nodeClaims []*karpv1.NodeClaim
	for _, instance := range instances {
		instanceType, err := c.resolveInstanceTypeFromInstance(ctx, instance)
		if err != nil {
			return nil, fmt.Errorf("resolving instance type, %w", err)
		}
		nodeClaim, err := c.instanceToNodeClaim(ctx, instance, instanceType)
		if err != nil {
			return nil, fmt.Errorf("converting instance to node claim, %w", err)
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
	instance, err := c.instanceProvider.Get(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("getting instance, %w", err)
	}
	instanceType, err := c.resolveInstanceTypeFromInstance(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("resolving instance type, %w", err)
	}
	return c.instanceToNodeClaim(ctx, instance, instanceType)
}

func (c *CloudProvider) LivenessProbe(req *http.Request) error {
	return c.instanceTypeProvider.LivenessProbe(req)
}

// GetInstanceTypes returns all available InstanceTypes
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
	vmName, err := nodeclaimutils.GetVMName(nodeClaim.Status.ProviderID)
	if err != nil {
		return fmt.Errorf("getting VM name, %w", err)
	}
	return c.instanceProvider.Delete(ctx, vmName)
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

func (c *CloudProvider) resolveInstanceTypeFromInstance(ctx context.Context, instance *armcompute.VirtualMachine) (*cloudprovider.InstanceType, error) {
	nodePool, err := c.resolveNodePoolFromInstance(ctx, instance)
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
		return i.Name == string(lo.FromPtr(instance.Properties.HardwareProfile.VMSize))
	})
	return instanceType, nil
}

func (c *CloudProvider) resolveNodePoolFromInstance(ctx context.Context, instance *armcompute.VirtualMachine) (*karpv1.NodePool, error) {
	nodePoolName, ok := instance.Tags[launchtemplate.NodePoolTagKey]
	if ok && *nodePoolName != "" {
		nodePool := &karpv1.NodePool{}
		if err := c.kubeClient.Get(ctx, types.NamespacedName{Name: *nodePoolName}, nodePool); err != nil {
			return nil, err
		}
		return nodePool, nil
	}

	return nil, errors.NewNotFound(schema.GroupResource{Group: coreapis.Group, Resource: "nodepools"}, "")
}

func (c *CloudProvider) instanceToNodeClaim(ctx context.Context, vm *armcompute.VirtualMachine, instanceType *cloudprovider.InstanceType) (*karpv1.NodeClaim, error) {
	nodeClaim := &karpv1.NodeClaim{}
	labels := map[string]string{}
	annotations := map[string]string{}

	if instanceType != nil {
		labels = instance.GetAllSingleValuedRequirementLabels(instanceType)
		nodeClaim.Status.Capacity = lo.PickBy(instanceType.Capacity, func(_ corev1.ResourceName, v resource.Quantity) bool { return !resources.IsZero(v) })
		nodeClaim.Status.Allocatable = lo.PickBy(instanceType.Allocatable(), func(_ corev1.ResourceName, v resource.Quantity) bool { return !resources.IsZero(v) })
	}

	if zone, err := utils.GetZone(vm); err != nil {
		log.FromContext(ctx).Info("failed to get zone for VM, zone label will be empty", "vmName", *vm.Name, "error", err)
	} else {
		labels[corev1.LabelTopologyZone] = zone
	}

	labels[karpv1.CapacityTypeLabelKey] = instance.GetCapacityType(vm)

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
	nodeClaim.Status.ProviderID = utils.ResourceIDToProviderID(ctx, *vm.ID)
	if vm.Properties != nil && vm.Properties.StorageProfile != nil && vm.Properties.StorageProfile.ImageReference != nil {
		nodeClaim.Status.ImageID = utils.ImageReferenceToString(vm.Properties.StorageProfile.ImageReference)
	}
	return nodeClaim, nil
}

func GenerateNodeClaimName(vmName string) string {
	return strings.TrimLeft("aks-", vmName)
}

const truncateAt = 1200

func (c *CloudProvider) isStandaloneNodeClaim(nodeClaim *karpv1.NodeClaim) bool {
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
