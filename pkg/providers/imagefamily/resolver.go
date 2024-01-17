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

	core "k8s.io/api/core/v1"
	"knative.dev/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter/pkg/metrics"
	"github.com/Azure/karpenter/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter/pkg/providers/instancetype"
	template "github.com/Azure/karpenter/pkg/providers/launchtemplate/parameters"
	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"github.com/aws/karpenter-core/pkg/cloudprovider"
	"github.com/samber/lo"
)

const (
	networkPluginAzureCNIOverlay = "overlay"
	networkPluginKubenet         = "kubenet"

	// defaultKubernetesMaxPodsAzureCNIOverlay is the maximum number of pods to run on a node for Azure CNI Overlay.
	defaultKubernetesMaxPodsAzureCNIOverlay = 250
	// defaultKubernetesMaxPodsKubenet is the maximum number of pods to run on a node for Kubenet.
	defaultKubernetesMaxPodsKubenet = 100
	// defaultKubernetesMaxPods is the maximum number of pods on a node.
	defaultKubernetesMaxPods = 110
)

// Resolver is able to fill-in dynamic launch template parameters
type Resolver struct {
	imageProvider *Provider
}

// ImageFamily can be implemented to override the default logic for generating dynamic launch template parameters
type ImageFamily interface {
	UserData(
		kubeletConfig *corev1beta1.KubeletConfiguration,
		taints []core.Taint,
		labels map[string]string,
		caBundle *string,
		instanceType *cloudprovider.InstanceType,
	) bootstrap.Bootstrapper
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
func (r Resolver) Resolve(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass, nodeClaim *corev1beta1.NodeClaim, instanceType *cloudprovider.InstanceType,
	staticParameters *template.StaticParameters) (*template.Parameters, error) {
	imageFamily := getImageFamily(nodeClass.Spec.ImageFamily, staticParameters)
	imageID, err := r.imageProvider.Get(ctx, nodeClass, instanceType, imageFamily)
	if err != nil {
		metrics.ImageSelectionErrorCount.WithLabelValues(imageFamily.Name()).Inc()
		return nil, err
	}

	kubeletConfig := nodeClaim.Spec.Kubelet
	if kubeletConfig == nil {
		kubeletConfig = &corev1beta1.KubeletConfiguration{}
	}

	// TODO: revist computeResources and maxPods implementation
	kubeletConfig.KubeReserved = instanceType.Overhead.KubeReserved
	kubeletConfig.SystemReserved = instanceType.Overhead.SystemReserved
	kubeletConfig.EvictionHard = map[string]string{
		instancetype.MemoryAvailable: instanceType.Overhead.EvictionThreshold.Memory().String()}
	kubeletConfig.MaxPods = lo.ToPtr(getMaxPods(staticParameters.NetworkPlugin))

	logging.FromContext(ctx).Infof("Resolved image %s for instance type %s", imageID, instanceType.Name)
	template := &template.Parameters{
		StaticParameters: staticParameters,
		UserData: imageFamily.UserData(
			kubeletConfig,
			append(nodeClaim.Spec.Taints, nodeClaim.Spec.StartupTaints...),
			staticParameters.Labels,
			staticParameters.CABundle,
			instanceType,
		),
		ImageID: imageID,
	}

	return template, nil
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

func getMaxPods(networkPlugin string) int32 {
	if networkPlugin == networkPluginAzureCNIOverlay {
		return defaultKubernetesMaxPodsAzureCNIOverlay
	} else if networkPlugin == networkPluginKubenet {
		return defaultKubernetesMaxPodsKubenet
	}
	return defaultKubernetesMaxPods
}
