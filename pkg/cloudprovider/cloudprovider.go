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
	"net/http"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/client"

	// nolint SA1019 - deprecated package
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/inplaceupdate"

	cloudproviderevents "github.com/Azure/karpenter-provider-azure/pkg/cloudprovider/events"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/samber/lo"
	nodeclaimutil "sigs.k8s.io/karpenter/pkg/utils/nodeclaim"

	coreapis "sigs.k8s.io/karpenter/pkg/apis"
	corev1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"
	"sigs.k8s.io/karpenter/pkg/events"

	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/functional"
	"sigs.k8s.io/karpenter/pkg/utils/resources"
)

func init() {
	coreapis.Settings = append(coreapis.Settings, apis.Settings...)
}

var _ cloudprovider.CloudProvider = (*CloudProvider)(nil)

type CloudProvider struct {
	instanceTypeProvider *instancetype.Provider
	instanceProvider     *instance.Provider
	kubeClient           client.Client
	imageProvider        *imagefamily.Provider
	recorder             events.Recorder
}

func New(instanceTypeProvider *instancetype.Provider, instanceProvider *instance.Provider, recorder events.Recorder,
	kubeClient client.Client, imageProvider *imagefamily.Provider) *CloudProvider {
	return &CloudProvider{
		instanceTypeProvider: instanceTypeProvider,
		instanceProvider:     instanceProvider,
		kubeClient:           kubeClient,
		imageProvider:        imageProvider,
		recorder:             recorder,
	}
}

// Create a node given the constraints.
func (c *CloudProvider) Create(ctx context.Context, nodeClaim *corev1beta1.NodeClaim) (*corev1beta1.NodeClaim, error) {
	nodeClass, err := c.resolveNodeClassFromNodeClaim(ctx, nodeClaim)
	if err != nil {
		if errors.IsNotFound(err) {
			c.recorder.Publish(cloudproviderevents.NodeClaimFailedToResolveNodeClass(nodeClaim))
		}
		// We treat a failure to resolve the NodeClass as an ICE since this means there is no capacity possibilities for this NodeClaim
		return nil, cloudprovider.NewInsufficientCapacityError(fmt.Errorf("resolving node class, %w", err))
	}

	instanceTypes, err := c.resolveInstanceTypes(ctx, nodeClaim, nodeClass)
	if err != nil {
		return nil, fmt.Errorf("resolving instance types, %w", err)
	}
	if len(instanceTypes) == 0 {
		return nil, cloudprovider.NewInsufficientCapacityError(fmt.Errorf("all requested instance types were unavailable during launch"))
	}
	instance, err := c.instanceProvider.Create(ctx, nodeClass, nodeClaim, instanceTypes)
	if err != nil {
		return nil, fmt.Errorf("creating instance, %w", err)
	}
	instanceType, _ := lo.Find(instanceTypes, func(i *cloudprovider.InstanceType) bool {
		return i.Name == string(*instance.Properties.HardwareProfile.VMSize)
	})

	return c.instanceToNodeClaim(ctx, instance, instanceType)
}

func (c *CloudProvider) List(ctx context.Context) ([]*corev1beta1.NodeClaim, error) {
	instances, err := c.instanceProvider.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing instances, %w", err)
	}
	var nodeClaims []*corev1beta1.NodeClaim
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

func (c *CloudProvider) Get(ctx context.Context, providerID string) (*corev1beta1.NodeClaim, error) {
	vmName, err := utils.GetVMName(providerID)
	if err != nil {
		return nil, fmt.Errorf("getting vm name, %w", err)
	}
	ctx = logging.WithLogger(ctx, logging.FromContext(ctx).With("id", vmName))
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
func (c *CloudProvider) GetInstanceTypes(ctx context.Context, nodePool *corev1beta1.NodePool) ([]*cloudprovider.InstanceType, error) {
	if nodePool == nil {
		return c.instanceTypeProvider.List(ctx, &corev1beta1.KubeletConfiguration{}, &v1alpha2.AKSNodeClass{})
	}

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
	instanceTypes, err := c.instanceTypeProvider.List(ctx, nodePool.Spec.Template.Spec.Kubelet, nodeClass)
	if err != nil {
		return nil, err
	}
	return instanceTypes, nil
}

func (c *CloudProvider) Delete(ctx context.Context, nodeClaim *corev1beta1.NodeClaim) error {
	ctx = logging.WithLogger(ctx, logging.FromContext(ctx).With("nodeclaim", nodeClaim.Name))

	vmName, err := utils.GetVMName(nodeClaim.Status.ProviderID)
	if err != nil {
		return fmt.Errorf("getting VM name, %w", err)
	}
	return c.instanceProvider.Delete(ctx, vmName)
}

func (c *CloudProvider) IsDrifted(ctx context.Context, nodeClaim *corev1beta1.NodeClaim) (cloudprovider.DriftReason, error) {
	// Not needed when GetInstanceTypes removes nodepool dependency
	nodePoolName, ok := nodeClaim.Labels[corev1beta1.NodePoolLabelKey]
	if !ok {
		return "", nil
	}
	nodePool := &corev1beta1.NodePool{}
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

	k8sVersionDrifted, err := c.isK8sVersionDrifted(ctx, nodeClaim)
	if err != nil {
		return "", err
	}
	if k8sVersionDrifted != "" {
		return k8sVersionDrifted, nil
	}
	imageVersionDrifted, err := c.isImageVersionDrifted(ctx, nodeClaim, nodeClass)
	if err != nil {
		return "", err
	}
	if imageVersionDrifted != "" {
		return imageVersionDrifted, nil
	}
	return "", nil
}

// Name returns the CloudProvider implementation name.
func (c *CloudProvider) Name() string {
	return "azure"
}

func (c *CloudProvider) resolveNodeClassFromNodeClaim(ctx context.Context, nodeClaim *corev1beta1.NodeClaim) (*v1alpha2.AKSNodeClass, error) {
	nodeClass := &v1alpha2.AKSNodeClass{}
	if err := c.kubeClient.Get(ctx, types.NamespacedName{Name: nodeClaim.Spec.NodeClassRef.Name}, nodeClass); err != nil {
		return nil, err
	}
	// For the purposes of NodeClass CloudProvider resolution, we treat deleting NodeClasses as NotFound
	if !nodeClass.DeletionTimestamp.IsZero() {
		return nil, errors.NewNotFound(v1alpha2.SchemeGroupVersion.WithResource("aksnodeclasses").GroupResource(), nodeClass.Name)
	}
	return nodeClass, nil
}

func (c *CloudProvider) resolveNodeClassFromNodePool(ctx context.Context, nodePool *corev1beta1.NodePool) (*v1alpha2.AKSNodeClass, error) {
	nodeClass := &v1alpha2.AKSNodeClass{}
	if err := c.kubeClient.Get(ctx, types.NamespacedName{Name: nodePool.Spec.Template.Spec.NodeClassRef.Name}, nodeClass); err != nil {
		return nil, err
	}
	// For the purposes of NodeClass CloudProvider resolution, we treat deleting NodeClasses as NotFound
	if !nodeClass.DeletionTimestamp.IsZero() {
		return nil, errors.NewNotFound(v1alpha2.SchemeGroupVersion.WithResource("aksnodeclasses").GroupResource(), nodeClass.Name)
	}
	return nodeClass, nil
}
func (c *CloudProvider) resolveInstanceTypes(ctx context.Context, nodeClaim *corev1beta1.NodeClaim, nodeClass *v1alpha2.AKSNodeClass) ([]*cloudprovider.InstanceType, error) {
	instanceTypes, err := c.instanceTypeProvider.List(ctx, nodeClaim.Spec.Kubelet, nodeClass)
	if err != nil {
		return nil, fmt.Errorf("getting instance types, %w", err)
	}

	reqs := scheduling.NewNodeSelectorRequirements(nodeClaim.Spec.Requirements...)
	return lo.Filter(instanceTypes, func(i *cloudprovider.InstanceType, _ int) bool {
		return reqs.Compatible(i.Requirements, v1alpha2.AllowUndefinedLabels) == nil &&
			len(i.Offerings.Requirements(reqs).Available()) > 0 &&
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
		return i.Name == string(*instance.Properties.HardwareProfile.VMSize)
	})
	return instanceType, nil
}

func (c *CloudProvider) resolveNodePoolFromInstance(ctx context.Context, instance *armcompute.VirtualMachine) (*corev1beta1.NodePool, error) {
	nodePoolName, ok := instance.Tags[corev1beta1.NodePoolLabelKey]
	if ok && *nodePoolName != "" {
		nodePool := &corev1beta1.NodePool{}
		if err := c.kubeClient.Get(ctx, types.NamespacedName{Name: *nodePoolName}, nodePool); err != nil {
			return nil, err
		}
		return nodePool, nil
	}

	return nil, errors.NewNotFound(schema.GroupResource{Group: corev1beta1.Group, Resource: "NodePool"}, "")
}

func (c *CloudProvider) instanceToNodeClaim(ctx context.Context, vm *armcompute.VirtualMachine, instanceType *cloudprovider.InstanceType) (*corev1beta1.NodeClaim, error) {
	nodeClaim := &corev1beta1.NodeClaim{}
	labels := map[string]string{}
	annotations := map[string]string{}

	if instanceType != nil {
		labels = instance.GetAllSingleValuedRequirementLabels(instanceType)
		nodeClaim.Status.Capacity = functional.FilterMap(instanceType.Capacity, func(_ v1.ResourceName, v resource.Quantity) bool { return !resources.IsZero(v) })
		nodeClaim.Status.Allocatable = functional.FilterMap(instanceType.Allocatable(), func(_ v1.ResourceName, v resource.Quantity) bool { return !resources.IsZero(v) })
	}

	if zoneID, err := instance.GetZoneID(vm); err != nil {
		logging.FromContext(ctx).Warnf("Failed to get zone for VM %s, %v", *vm.Name, err)
	} else {
		zone := makeZone(*vm.Location, zoneID)
		// aks-node-validating-webhook protects v1.LabelTopologyZone, will be set elsewhere, so we use a different label
		labels[v1alpha2.AlternativeLabelTopologyZone] = zone
	}

	labels[corev1beta1.CapacityTypeLabelKey] = instance.GetCapacityType(vm)

	// TODO: v1beta1 new kes/labels
	if tag, ok := vm.Tags[instance.NodePoolTagKey]; ok {
		labels[corev1beta1.NodePoolLabelKey] = *tag
	}

	inPlaceUpdateHash, err := inplaceupdate.HashFromVM(vm)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate in place update hash, %w", err)
	}
	annotations[v1alpha2.AnnotationInPlaceUpdateHash] = inPlaceUpdateHash

	nodeClaim.Name = GenerateNodeClaimName(*vm.Name)
	nodeClaim.Labels = labels
	nodeClaim.Annotations = annotations
	nodeClaim.CreationTimestamp = metav1.Time{Time: *vm.Properties.TimeCreated}
	nodeClaim.Status.ProviderID = utils.ResourceIDToProviderID(ctx, *vm.ID)
	return nodeClaim, nil
}

func GenerateNodeClaimName(vmName string) string {
	return strings.TrimLeft("aks-", vmName)
}

// makeZone returns the zone value in format of <region>-<zone-id>.
func makeZone(location string, zoneID string) string {
	if zoneID == "" {
		return ""
	}
	return fmt.Sprintf("%s-%s", strings.ToLower(location), zoneID)
}
