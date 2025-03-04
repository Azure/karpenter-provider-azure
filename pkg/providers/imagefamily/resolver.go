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
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/metrics"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	template "github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/samber/lo"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// Resolver is able to fill-in dynamic launch template parameters
type Resolver struct {
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

// New constructs a new launch template Resolver
func New(_ client.Client, imageProvider *Provider) *Resolver {
	return &Resolver{
		imageProvider: imageProvider,
	}
}

// Resolve fills in dynamic launch template parameters
func (r Resolver) Resolve(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass, nodeClaim *karpv1.NodeClaim, instanceType *cloudprovider.InstanceType,
	staticParameters *template.StaticParameters) (*template.Parameters, error) {
	imageFamily := getImageFamily(nodeClass.Spec.ImageFamily, staticParameters)
	imageDistro, imageID, err := r.imageProvider.Get(ctx, nodeClass, instanceType, imageFamily)
	if err != nil {
		metrics.ImageSelectionErrorCount.WithLabelValues(imageFamily.Name()).Inc()
		return nil, err
	}

	logging.FromContext(ctx).Infof("Resolved image %s for instance type %s", imageID, instanceType.Name)

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
			prepareKubeletConfiguration(instanceType, nodeClass),
			allTaints,
			staticParameters.Labels,
			staticParameters.CABundle,
			instanceType,
		),
		CustomScriptsNodeBootstrapping: imageFamily.CustomScriptsNodeBootstrapping(
			prepareKubeletConfiguration(instanceType, nodeClass),
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

func prepareKubeletConfiguration(instanceType *cloudprovider.InstanceType, nodeClass *v1alpha2.AKSNodeClass) *bootstrap.KubeletConfiguration {
	kubeletConfig := &bootstrap.KubeletConfiguration{}

	if nodeClass.Spec.Kubelet != nil {
		kubeletConfig.KubeletConfiguration = *nodeClass.Spec.Kubelet
	}

	// TODO: make default maxpods dependent on CNI
	if nodeClass.Spec.MaxPods != nil {
		kubeletConfig.MaxPods = *nodeClass.Spec.MaxPods
	} else {
		kubeletConfig.MaxPods = consts.DefaultKubernetesMaxPods
	}

	// TODO: revisit computeResources implementation
	kubeletConfig.KubeReserved = utils.StringMap(instanceType.Overhead.KubeReserved)
	kubeletConfig.SystemReserved = utils.StringMap(instanceType.Overhead.SystemReserved)
	kubeletConfig.EvictionHard = map[string]string{instancetype.MemoryAvailable: instanceType.Overhead.EvictionThreshold.Memory().String()}
	return kubeletConfig
}

func getImageFamily(familyName *string, parameters *template.StaticParameters) ImageFamily {
	switch lo.FromPtr(familyName) {
	case v1alpha2.Ubuntu2204ImageFamily:
		return &Ubuntu2204{Options: parameters}
	case v1alpha2.AzureLinuxImageFamily:
		return &AzureLinux{Options: parameters}
	default:
		return &Ubuntu2204{Options: parameters}
	}
}

func getEphemeralMaxSizeGB(instanceType *cloudprovider.InstanceType) int32 {
	reqs := instanceType.Requirements.Get(v1alpha2.LabelSKUStorageEphemeralOSMaxSize).Values()
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
func useEphemeralDisk(instanceType *cloudprovider.InstanceType, nodeClass *v1alpha2.AKSNodeClass) bool {
	// use ephemeral disk if it is large enough
	return *nodeClass.Spec.OSDiskSizeGB <= getEphemeralMaxSizeGB(instanceType)
}
