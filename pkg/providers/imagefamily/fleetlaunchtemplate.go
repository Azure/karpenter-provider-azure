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

package imagefamily

import (
	"context"
	"fmt"
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/logging"
	"github.com/Azure/karpenter-provider-azure/pkg/metrics"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	template "github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/samber/lo"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// FleetResolver fills in dynamic launch template parameters for a Fleet template shared by multiple SKUs.
type FleetResolver interface {
	ResolveFleet(
		ctx context.Context,
		nodeClass *v1beta1.AKSNodeClass,
		nodeClaim *karpv1.NodeClaim,
		instanceType *cloudprovider.InstanceType,
		staticParameters *template.StaticParameters,
	) (*template.Parameters, error)
}

var _ FleetResolver = &defaultResolver{}

// ResolveFleet is the Fleet counterpart of Resolve. It is kept separate so that changes for Fleet
// do not affect the single-SKU path. The instance type is only used to resolve the image and OS disk
// profile shared by all Fleet candidates; kubelet settings are not derived from its capacity, and
// only scriptless bootstrapping is produced.
func (r *defaultResolver) ResolveFleet(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceType *cloudprovider.InstanceType,
	staticParameters *template.StaticParameters,
) (*template.Parameters, error) {
	kubernetesVersion, err := nodeClass.GetKubernetesVersion()
	if err != nil {
		return nil, err
	}

	if nodeClass.IsKataEnabled() && !options.FromContext(ctx).SupportsWorkloadRuntime() {
		return nil, fmt.Errorf("workloadRuntime %q is not supported with provision-mode %q",
			nodeClass.GetWorkloadRuntime(), options.FromContext(ctx).ProvisionMode)
	}

	imageFamily := GetImageFamily(nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, nodeClass.IsTrustedLaunchEnabled(), kubernetesVersion, staticParameters)
	imageID, err := r.ResolveNodeImageFromNodeClass(nodeClass, instanceType)
	if err != nil {
		metrics.ImageSelectionErrorCount.WithLabelValues(imageFamily.Name()).Inc()
		return nil, err
	}

	log.FromContext(ctx).Info("resolved fleet image",
		logging.ImageID, imageID,
		logging.InstanceType, instanceType.Name,
	)

	useSIG := options.FromContext(ctx).UseSIG
	imageDistro, err := mapToImageDistro(imageID, nodeClass.Spec.FIPSMode, imageFamily, useSIG, nodeClass.IsTrustedLaunchEnabled(), nodeClass.IsKataEnabled())
	if err != nil {
		return nil, err
	}

	generalTaints, startupTaints := utils.ExtractTaints(nodeClaim)
	allTaints := slices.Concat(generalTaints, startupTaints)

	osDiskProfile, err := instancetype.ResolveOSDiskProfileFromInstanceType(
		ctx,
		r.instanceTypeProvider,
		instanceType.Name,
		nodeClass.Spec.OSDiskSizeGB,
		lo.FromPtr(nodeClass.Spec.OSDiskType),
		nodeClass.IsTrustedLaunchEnabled(),
	)
	if err != nil {
		return nil, err
	}
	diskType := lo.Ternary(osDiskProfile.Type == armcontainerservice.OSDiskTypeEphemeral, consts.StorageProfileEphemeral, consts.StorageProfileManagedDisks)

	return &template.Parameters{
		StaticParameters: staticParameters,
		ScriptlessCustomData: imageFamily.ScriptlessCustomData(
			prepareFleetKubeletConfiguration(ctx, nodeClass),
			allTaints,
			staticParameters.Labels,
			staticParameters.CABundle,
			instanceType,
		),
		StorageProfileDiskType:    diskType,
		StorageProfileIsEphemeral: osDiskProfile.Type == armcontainerservice.OSDiskTypeEphemeral,
		StorageProfilePlacement:   lo.FromPtr(osDiskProfile.Placement),
		StorageProfileSizeGB:      osDiskProfile.SizeGB,
		ImageID:                   imageID,
		EnableFIPS1403Encryption:  requiresFIPS1403Encryption(imageDistro),
		IsWindows:                 false, // TODO(Windows)
	}, nil
}

// prepareFleetKubeletConfiguration builds kubelet settings without SKU-derived reservations, since one
// Fleet template is shared by VMs of different sizes.
func prepareFleetKubeletConfiguration(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) *bootstrap.KubeletConfiguration {
	kubeletConfig := &bootstrap.KubeletConfiguration{}
	if nodeClass.Spec.Kubelet != nil {
		kubeletConfig.KubeletConfiguration = *nodeClass.Spec.Kubelet
	}
	opts := options.FromContext(ctx)
	kubeletConfig.MaxPods = utils.GetMaxPods(nodeClass, opts.NetworkPlugin, opts.NetworkPluginMode)
	kubeletConfig.ClusterDNSServiceIP = opts.DNSServiceIP
	return kubeletConfig
}
