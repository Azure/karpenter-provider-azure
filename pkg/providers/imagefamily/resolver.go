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
	"strconv"
	"strings"

	"github.com/blang/semver/v4"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/metrics"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	template "github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/samber/lo"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type Resolver interface {
	Resolve(
		ctx context.Context,
		nodeClass *v1beta1.AKSNodeClass,
		nodeClaim *karpv1.NodeClaim,
		instanceType *cloudprovider.InstanceType,
		staticParameters *template.StaticParameters) (*template.Parameters, error)
}

// assert that defaultResolver implements Resolver interface
var _ Resolver = &defaultResolver{}

// defaultResolver is able to fill-in dynamic launch template parameters
type defaultResolver struct {
	imageProvider *Provider
}

// ImageFamily can be implemented to override the default logic for generating dynamic launch template parameters
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
	) customscriptsbootstrap.Bootstrapper
	Name() string
	// DefaultImages returns a list of default CommunityImage definitions for this ImageFamily.
	// Our Image Selection logic relies on the ordering of the default images to be ordered from most preferred to least, then we will select the latest image version available for that CommunityImage definition.
	// Our Release pipeline ensures all images are released together within 24 hours of each other for community image gallery, so selecting based on image feature priorities, then by date, and not vice-versa is acceptable.
	DefaultImages() []DefaultImageOutput
}

// NewDefaultResolver constructs a new launch template Resolver
func NewDefaultResolver(_ client.Client, imageProvider *Provider) *defaultResolver {
	return &defaultResolver{
		imageProvider: imageProvider,
	}
}

// Resolve fills in dynamic launch template parameters
func (r *defaultResolver) Resolve(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceType *cloudprovider.InstanceType,
	staticParameters *template.StaticParameters,
) (*template.Parameters, error) {
	nodeImages, err := nodeClass.GetImages()
	if err != nil {
		return nil, err
	}
	kubernetesVersion, err := nodeClass.GetKubernetesVersion()
	if err != nil {
		return nil, err
	}

	imageFamily := getImageFamily(nodeClass.Spec.ImageFamily, kubernetesVersion, staticParameters)
	imageID, err := r.resolveNodeImage(nodeImages, instanceType)
	if err != nil {
		metrics.ImageSelectionErrorCount.WithLabelValues(imageFamily.Name()).Inc()
		return nil, err
	}

	log.FromContext(ctx).Info(fmt.Sprintf("Resolved image %s for instance type %s", imageID, instanceType.Name))

	// TODO: as ProvisionModeBootstrappingClient path develops, we will eventually be able to drop the retrieval of imageDistro here.
	imageDistro, err := mapToImageDistro(imageID, imageFamily)
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

	storageProfile := "ManagedDisks"
	if useEphemeralDisk(instanceType, nodeClass) {
		storageProfile = "Ephemeral"
	}

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
			storageProfile,
		),
		ImageID:        imageID,
		StorageProfile: storageProfile,
		IsWindows:      false, // TODO(Windows)
	}

	return template, nil
}

func mapToImageDistro(imageID string, imageFamily ImageFamily) (string, error) {
	var imageInfo DefaultImageOutput
	imageInfo.PopulateImageTraitsFromID(imageID)
	for _, defaultImage := range imageFamily.DefaultImages() {
		if defaultImage.ImageDefinition == imageInfo.ImageDefinition {
			return defaultImage.Distro, nil
		}
	}
	return "", fmt.Errorf("no distro found for image id %s", imageID)
}

func prepareKubeletConfiguration(ctx context.Context, instanceType *cloudprovider.InstanceType, nodeClass *v1beta1.AKSNodeClass) *bootstrap.KubeletConfiguration {
	kubeletConfig := &bootstrap.KubeletConfiguration{}

	if nodeClass.Spec.Kubelet != nil {
		kubeletConfig.KubeletConfiguration = *nodeClass.Spec.Kubelet
	}

	kubeletConfig.MaxPods = utils.GetMaxPods(nodeClass, options.FromContext(ctx).NetworkPlugin, options.FromContext(ctx).NetworkPluginMode)

	// TODO: revisit computeResources implementation
	kubeletConfig.KubeReserved = utils.StringMap(instanceType.Overhead.KubeReserved)
	kubeletConfig.SystemReserved = utils.StringMap(instanceType.Overhead.SystemReserved)
	kubeletConfig.EvictionHard = map[string]string{instancetype.MemoryAvailable: instanceType.Overhead.EvictionThreshold.Memory().String()}
	return kubeletConfig
}

func getSupportedImages(familyName *string, kubernetesVersion string) []DefaultImageOutput {
	// TODO: Options aren't used within DefaultImages, so safe to be using nil here. Refactor so we don't actually need to pass in Options for getting DefaultImage.
	imageFamily := getImageFamily(familyName, kubernetesVersion, nil)
	return imageFamily.DefaultImages()
}

func getImageFamily(familyName *string, kubernetesVersion string, parameters *template.StaticParameters) ImageFamily {
	switch lo.FromPtr(familyName) {
	case v1beta1.Ubuntu2204ImageFamily:
		return &Ubuntu2204{Options: parameters}
	case v1beta1.AzureLinuxImageFamily:
		if useAzureLinux3(kubernetesVersion) {
			return &AzureLinux3{Options: parameters}
		}
		return &AzureLinux{Options: parameters}
	default:
		return &Ubuntu2204{Options: parameters}
	}
}

// useAzureLinux3 checks if the Kubernetes version is 1.32.0 or higher,
// which is when Azure Linux 3 support starts
func useAzureLinux3(kubernetesVersion string) bool {
	// Parse version, stripping any 'v' prefix if present
	version, err := semver.Parse(strings.TrimPrefix(kubernetesVersion, "v"))
	if err != nil {
		// If we can't parse the version, default to AzureLinux (false)
		return false
	}
	return version.GE(semver.Version{Major: 1, Minor: 32})
}

func getEphemeralMaxSizeGB(instanceType *cloudprovider.InstanceType) int32 {
	reqs := instanceType.Requirements.Get(v1beta1.LabelSKUStorageEphemeralOSMaxSize).Values()
	if len(reqs) == 0 || len(reqs) > 1 {
		return 0
	}
	maxSize, err := strconv.ParseFloat(reqs[0], 32)
	if err != nil {
		return 0
	}
	// decimal places are truncated, so we round down
	return int32(maxSize)
}

// setVMPropertiesStorageProfile enables ephemeral os disk for instance types that support it
func useEphemeralDisk(instanceType *cloudprovider.InstanceType, nodeClass *v1beta1.AKSNodeClass) bool {
	// use ephemeral disk if it is large enough
	return *nodeClass.Spec.OSDiskSizeGB <= getEphemeralMaxSizeGB(instanceType)
}
