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
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"github.com/aws/karpenter-core/pkg/test"

	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter/test/pkg/environment/azure"
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
	It("should provision one GPU node and one GPU Pod", func() {

		nodeClass := env.DefaultAKSNodeClass()
		nodePool := env.DefaultNodePool(nodeClass)

		// relax default SKU family selector to allow for GPU nodes
		test.ReplaceRequirements(nodePool, v1.NodeSelectorRequirement{
			Key:      v1alpha2.LabelSKUFamily,
			Operator: v1.NodeSelectorOpExists,
		})
		test.ReplaceRequirements(nodePool, v1.NodeSelectorRequirement{
			Key:      v1.LabelInstanceTypeStable,
			Operator: v1.NodeSelectorOpExists,
			Values:   []string{"Standard_NC4as_T4_v3"},
		})
		// exclude some of the more expensive GPU SKUs
		nodePool.Spec.Limits = corev1beta1.Limits{
			v1.ResourceCPU:                    resource.MustParse("25"),
			v1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
		}

		minstPodOptions := test.PodOptions{
			ObjectMeta: metav1.ObjectMeta{
				Name: "samples-fake-minst",
				Labels: map[string]string{
					"app": "samples-tf-mnist-demo",
				},
			},
			Image: "mcr.microsoft.com/oss/kubernetes/pause:3.6",
			ResourceRequirements: v1.ResourceRequirements{
				Limits: v1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				},
			},
		}
		deployment := test.Deployment(test.DeploymentOptions{
			Replicas:   1,
			PodOptions: minstPodOptions,
		})

		devicePlugin := &appsv1.DaemonSet{
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
				Template: v1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"name": "nvidia-device-plugin-ds",
						},
					},
					Spec: v1.PodSpec{
						Tolerations: []v1.Toleration{
							{
								Key:      "nvidia.com/gpu",
								Operator: v1.TolerationOpExists,
								Effect:   v1.TaintEffectNoSchedule,
							},
						},
						PriorityClassName: "system-node-critical",
						Volumes: []v1.Volume{
							{
								Name: "device-plugin",
								VolumeSource: v1.VolumeSource{
									HostPath: &v1.HostPathVolumeSource{
										Path: "/var/lib/kubelet/device-plugins",
									},
								},
							},
						},
						Containers: []v1.Container{
							{
								Name:  "nvidia-device-plugin-ctr",
								Image: "nvcr.io/nvidia/k8s-device-plugin:v0.14.1",
								SecurityContext: &v1.SecurityContext{
									AllowPrivilegeEscalation: lo.ToPtr(false),
									Capabilities: &v1.Capabilities{
										Drop: []v1.Capability{
											"ALL",
										},
									},
								},
								VolumeMounts: []v1.VolumeMount{
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
		env.ExpectCreated(nodeClass, nodePool, deployment, devicePlugin)
		// This test exercises the full lifecycle of the GPU Node, and validates it can successfully schedule GPU Resources.
		// the Time.Minute 45 gives the test 3 tries to provision and bootstrap the node successfully
		env.EventuallyExpectHealthyPodCountWithTimeout(time.Minute*45, labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
		env.ExpectCreatedNodeCount("==", int(*deployment.Spec.Replicas))

	})
})
