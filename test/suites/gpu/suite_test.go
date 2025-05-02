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

package gpu_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
)

var env *azure.Environment

func TestGPU(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	RunSpecs(t, "GPU")
}

var _ = BeforeEach(func() { env.BeforeEach() })
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("GPU", func() {
	DescribeTable("should provision one GPU node and one GPU Pod",
		func(nodeClass *v1alpha2.AKSNodeClass) {
			// Enable NodeRepair feature gate if running in-cluster
			if env.InClusterController {
				// One scenario we want to test, is that our node-auto-repair isn't too aggressive.
				// If a gpu vm joins the cluster at 3 minutes, and takes 10 minutes to get ready, we don't want
				// to GC the vm for node auto repair.
				// By enabling the controller, the below assertions that the gpu workload eventually gets scheduled
				// and runs means we didnt remove the not-ready vm
				env.ExpectSettingsOverridden(corev1.EnvVar{Name: "FEATURE_GATES", Value: "NodeRepair=True"})
			}
			nodePool := env.DefaultNodePool(nodeClass)
			test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      v1alpha2.LabelSKUFamily,
					Operator: corev1.NodeSelectorOpExists,
				},
			})

			nodePool.Spec.Limits = karpv1.Limits{
				corev1.ResourceCPU:                    resource.MustParse("25"),
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
			}

			minstPodOptions := test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Name: "samples-fake-minst",
					Labels: map[string]string{
						"app": "samples-tf-mnist-demo",
					},
				},
				ResourceRequirements: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
			}
			deployment := test.Deployment(test.DeploymentOptions{
				Replicas:   1,
				PodOptions: minstPodOptions,
			})

			devicePlugin := createNVIDIADevicePluginDaemonSet()
			env.ExpectCreated(nodeClass, nodePool, deployment, devicePlugin)

			env.EventuallyExpectHealthyPodCountWithTimeout(
				time.Minute*15,
				labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels),
				int(*deployment.Spec.Replicas),
			)
			env.ExpectCreatedNodeCount("==", int(*deployment.Spec.Replicas))
		},
		Entry("should provision one GPU Node and one GPU Pod (AzureLinux)", env.AZLinuxNodeClass()),
		Entry("should provision one GPU Node and one GPU Pod (Ubuntu2204)", env.DefaultAKSNodeClass()),
	)
})

func createNVIDIADevicePluginDaemonSet() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nvidia-device-plugin-daemonset",
			Namespace: "kube-system",
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "nvidia-device-plugin-ds",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name": "nvidia-device-plugin-ds",
					},
				},
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{
						{
							Key:      "nvidia.com/gpu",
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
					PriorityClassName: "system-node-critical",
					Volumes: []corev1.Volume{
						{
							Name: "device-plugin",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/kubelet/device-plugins",
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "nvidia-device-plugin-ctr",
							Image: "nvcr.io/nvidia/k8s-device-plugin:v0.14.1",
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: lo.ToPtr(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{
										"ALL",
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "device-plugin",
									MountPath: "/var/lib/kubelet/device-plugins",
								},
							},
						},
					},
				},
			},
		},
	}
}
