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

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/logging"
	"github.com/Azure/karpenter-provider-azure/pkg/metrics"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	types "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	template "github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/samber/lo"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

type Resolver interface {
	Resolve(
		ctx context.Context,
		nodeClass *v1beta1.AKSNodeClass,
		nodeClaim *karpv1.NodeClaim,
		instanceType *cloudprovider.InstanceType,
		staticParameters *template.StaticParameters) (*template.Parameters, error)
	ResolveNodeImageFromNodeClass(nodeClass *v1beta1.AKSNodeClass, instanceType *cloudprovider.InstanceType) (string, error)
}

// assert that defaultResolver implements Resolver interface
var _ Resolver = &defaultResolver{}

// defaultResolver is able to fill-in dynamic launch template parameters
// ATTENTION!!!: changes here may NOT be effective on AKS machine nodes (ProvisionModeAKSMachineAPI); See aksmachineinstance.go/aksmachineinstancehelpers.go.
// Refactoring for code unification is not being invested immediately.
type defaultResolver struct {
	nodeBootstrappingProvider types.NodeBootstrappingAPI
	imageProvider             *provider
	instanceTypeProvider      instancetype.Provider
}

// ImageFamily can be implemented to override the default logic for generating dynamic launch template parameters
// ATTENTION!!!: changes here may NOT be effective on AKS machine nodes (ProvisionModeAKSMachineAPI); See aksmachineinstance.go/aksmachineinstancehelpers.go.
// Refactoring for code unification is not being invested immediately.
type ImageFamily interface {
	ScriptlessCustomData(
		kubeletConfig *bootstrap.KubeletConfiguration,
		taints []corev1.Taint,
		labels map[string]string,
		caBundle *string,
		instanceType *cloudprovider.InstanceType,
	) bootstrap.Bootstrapper
	CustomScriptsNodeBootstrapping(
		kubeletConfig *bootstrap.KubeletConfiguration,
		taints []corev1.Taint,
		startupTaints []corev1.Taint,
		labels map[string]string,
		instanceType *cloudprovider.InstanceType,
		imageDistro string,
		storageProfile string,
		nodeBootstrappingClient types.NodeBootstrappingAPI,
		fipsMode *v1beta1.FIPSMode,
		localDNS *v1beta1.LocalDNS,
	) customscriptsbootstrap.Bootstrapper
	Name() string
	// DefaultImages returns a list of default CommunityImage definitions for this ImageFamily.
	// Our Image Selection logic relies on the ordering of the default images to be ordered from most preferred to least, then we will select the latest image version available for that CommunityImage definition.
	// Our Release pipeline ensures all images are released together within 24 hours of each other for community image gallery, so selecting based on image feature priorities, then by date, and not vice-versa is acceptable.
	// If fipsMode is FIPSModeFIPS, only FIPS-enabled images will be returned
	DefaultImages(useSIG bool, fipsMode *v1beta1.FIPSMode) []types.DefaultImageOutput
}

// NewDefaultResolver constructs a new launch template Resolver
func NewDefaultResolver(_ client.Client, imageProvider *provider, instanceTypeProvider instancetype.Provider, nodeBootstrappingClient types.NodeBootstrappingAPI) *defaultResolver {
	return &defaultResolver{
		imageProvider:             imageProvider,
		nodeBootstrappingProvider: nodeBootstrappingClient,
		instanceTypeProvider:      instanceTypeProvider,
	}
}

// Resolve fills in dynamic launch template parameters.
// The name "imageFamilyResolver.Resolve()" is potentially misleading here.
// Suggestion: refactor would help, but this won't be used by PROVISION_MODE=aksmachineapi anyway. May not be worth it.
// ATTENTION!!!: changes here may NOT be effective on AKS machine nodes (ProvisionModeAKSMachineAPI); See aksmachineinstance.go/aksmachineinstancehelpers.go.
// Refactoring for code unification is not being invested immediately.
func (r *defaultResolver) Resolve(
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

	imageFamily := GetImageFamily(nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, kubernetesVersion, staticParameters)
	imageID, err := r.ResolveNodeImageFromNodeClass(nodeClass, instanceType)
	if err != nil {
		metrics.ImageSelectionErrorCount.WithLabelValues(imageFamily.Name()).Inc()
		return nil, err
	}

	log.FromContext(ctx).Info("resolved image",
		logging.ImageID, imageID,
		logging.InstanceType, instanceType.Name,
	)

	// TODO: as ProvisionModeBootstrappingClient path develops, we will eventually be able to drop the retrieval of imageDistro here.
	useSIG := options.FromContext(ctx).UseSIG
	imageDistro, err := mapToImageDistro(imageID, nodeClass.Spec.FIPSMode, imageFamily, useSIG)
	if err != nil {
		return nil, err
	}

	generalTaints := nodeClaim.Spec.Taints
	startupTaints := nodeClaim.Spec.StartupTaints
	allTaints := lo.Flatten([][]corev1.Taint{
		generalTaints,
		startupTaints,
	})

	// Ensure UnregisteredNoExecuteTaint is present
	if _, found := lo.Find(allTaints, func(t corev1.Taint) bool { // Allow UnregisteredNoExecuteTaint to be in non-startup taints(?)
		return t.MatchTaint(&karpv1.UnregisteredNoExecuteTaint)
	}); !found {
		startupTaints = append(startupTaints, karpv1.UnregisteredNoExecuteTaint)
		allTaints = append(allTaints, karpv1.UnregisteredNoExecuteTaint)
	}

	diskType, placement, err := r.getStorageProfile(ctx, instanceType, nodeClass)
	if err != nil {
		return nil, err
	}

	// ATTENTION!!!: changes here will NOT be effective on AKS machine nodes (ProvisionModeAKSMachineAPI); See aksmachineinstance.go/aksmachineinstancehelpers.go.
	// Refactoring for code unification is not being invested immediately.
	template := &template.Parameters{
		StaticParameters: staticParameters,
		ScriptlessCustomData: imageFamily.ScriptlessCustomData(
			prepareKubeletConfiguration(ctx, instanceType, nodeClass),
			allTaints,
			staticParameters.Labels,
			staticParameters.CABundle,
			instanceType,
		),
		CustomScriptsNodeBootstrapping: imageFamily.CustomScriptsNodeBootstrapping(
			prepareKubeletConfiguration(ctx, instanceType, nodeClass),
			generalTaints,
			startupTaints,
			staticParameters.Labels,
			instanceType,
			imageDistro,
			diskType,
			r.nodeBootstrappingProvider,
			nodeClass.Spec.FIPSMode,
			nodeClass.Spec.LocalDNS,
		),
		StorageProfileDiskType:    diskType,
		StorageProfileIsEphemeral: diskType == consts.StorageProfileEphemeral,
		StorageProfilePlacement:   lo.FromPtr(placement),

		// TODO: We could potentially use the instance type to do defaulting like
		// traditional AKS, so putting this here along with the other settings
		StorageProfileSizeGB: lo.FromPtr(nodeClass.Spec.OSDiskSizeGB),
		ImageID:              imageID,
		IsWindows:            false, // TODO(Windows)
	}

	return template, nil
}

func (r *defaultResolver) getStorageProfile(ctx context.Context, instanceType *cloudprovider.InstanceType, nodeClass *v1beta1.AKSNodeClass) (diskType string, placement *armcompute.DiffDiskPlacement, err error) {
	sku, err := r.instanceTypeProvider.Get(ctx, nodeClass, instanceType.Name)
	if err != nil {
		return "", nil, err
	}

	_, placement = instancetype.FindMaxEphemeralSizeGBAndPlacement(sku)

	if instancetype.UseEphemeralDisk(sku, nodeClass) {
		return consts.StorageProfileEphemeral, placement, nil
	}
	return consts.StorageProfileManagedDisks, placement, nil
}

func mapToImageDistro(imageID string, fipsMode *v1beta1.FIPSMode, imageFamily ImageFamily, useSIG bool) (string, error) {
	var imageInfo types.DefaultImageOutput
	imageInfo.PopulateImageTraitsFromID(imageID)
	for _, defaultImage := range imageFamily.DefaultImages(useSIG, fipsMode) {
		if defaultImage.ImageDefinition == imageInfo.ImageDefinition {
			return defaultImage.Distro, nil
		}
	}
	return "", fmt.Errorf("no distro found for image id %s", imageID)
}

// ATTENTION!!!: changes here may NOT be effective on AKS machine nodes (ProvisionModeAKSMachineAPI); See aksmachineinstance.go/aksmachineinstancehelpers.go.
// Refactoring for code unification is not being invested immediately.
func prepareKubeletConfiguration(ctx context.Context, instanceType *cloudprovider.InstanceType, nodeClass *v1beta1.AKSNodeClass) *bootstrap.KubeletConfiguration {
	kubeletConfig := &bootstrap.KubeletConfiguration{}

	if nodeClass.Spec.Kubelet != nil {
		kubeletConfig.KubeletConfiguration = *nodeClass.Spec.Kubelet
	}

	kubeletConfig.MaxPods = utils.GetMaxPods(nodeClass, options.FromContext(ctx).NetworkPlugin, options.FromContext(ctx).NetworkPluginMode)
	kubeletConfig.ClusterDNSServiceIP = options.FromContext(ctx).DNSServiceIP

	// TODO: revisit computeResources implementation
	kubeletConfig.KubeReserved = utils.StringMap(instanceType.Overhead.KubeReserved)
	kubeletConfig.SystemReserved = utils.StringMap(instanceType.Overhead.SystemReserved)
	kubeletConfig.EvictionHard = map[string]string{instancetype.MemoryAvailable: instanceType.Overhead.EvictionThreshold.Memory().String()}
	return kubeletConfig
}

func getSupportedImages(familyName *string, fipsMode *v1beta1.FIPSMode, kubernetesVersion string, useSIG bool) []types.DefaultImageOutput {
	// TODO: Options aren't used within DefaultImages, so safe to be using nil here. Refactor so we don't actually need to pass in Options for getting DefaultImage.
	imageFamily := GetImageFamily(familyName, fipsMode, kubernetesVersion, nil)
	return imageFamily.DefaultImages(useSIG, fipsMode)
}

func GetImageFamily(familyName *string, fipsMode *v1beta1.FIPSMode, kubernetesVersion string, parameters *template.StaticParameters) ImageFamily {
	switch lo.FromPtr(familyName) {
	case v1beta1.Ubuntu2204ImageFamily:
		return &Ubuntu2204{Options: parameters}
	case v1beta1.Ubuntu2404ImageFamily:
		return &Ubuntu2404{Options: parameters}
	case v1beta1.AzureLinuxImageFamily:
		if UseAzureLinux3(kubernetesVersion) {
			return &AzureLinux3{Options: parameters}
		}
		return &AzureLinux{Options: parameters}
	case v1beta1.UbuntuImageFamily:
		fallthrough
	default:
		return defaultUbuntu(fipsMode, kubernetesVersion, parameters)
	}
}

func defaultUbuntu(fipsMode *v1beta1.FIPSMode, kubernetesVersion string, parameters *template.StaticParameters) ImageFamily {
	if lo.FromPtr(fipsMode) == v1beta1.FIPSModeFIPS {
		return &Ubuntu2004{Options: parameters}
	}
	if UseUbuntu2404(kubernetesVersion) {
		return &Ubuntu2404{Options: parameters}
	}
	return &Ubuntu2204{Options: parameters}
}

// ResolveNodeImageFromNodeClass resolves Distro and image ID for the given node class and instance type. Images may vary due to architecture, accelerator, etc
func (r *defaultResolver) ResolveNodeImageFromNodeClass(nodeClass *v1beta1.AKSNodeClass, instanceType *cloudprovider.InstanceType) (string, error) {
	// ASSUMPTION: nodeImages in a NodeClass are always sorted by priority order.
	nodeImages, err := nodeClass.GetImages()
	if err != nil {
		return "", err
	}
	for _, availableImage := range nodeImages {
		if err := instanceType.Requirements.Compatible(
			scheduling.NewNodeSelectorRequirements(availableImage.Requirements...),
			v1beta1.AllowUndefinedWellKnownAndRestrictedLabels,
		); err == nil {
			return availableImage.ID, nil
		}
	}
	return "", fmt.Errorf("no compatible images found for instance type %s", instanceType.Name)
}
