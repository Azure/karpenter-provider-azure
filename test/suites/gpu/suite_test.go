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

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
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
		Label("GPU"),
		func(nodeClass *v1beta1.AKSNodeClass) {
			// Enable NodeRepair feature gate if running in-cluster
			if env.InClusterController {
				// Have Node Repair enabled to validate it does not interfere with
				// provisioning of GPU VMs, which can take longer to become Ready.
				// The assertions on healthy workload and created node count
				// already rule out node repair scenario.
				env.ExpectSettingsOverridden(corev1.EnvVar{Name: "FEATURE_GATES", Value: "NodeRepair=True"})
			}
			nodePool := env.DefaultNodePool(nodeClass)
			test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1beta1.LabelSKUFamily,
				Operator: corev1.NodeSelectorOpExists,
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
		Entry("should provision one GPU Node and one GPU Pod (Ubuntu)", func() *v1beta1.AKSNodeClass { // This ensures the case statement for GPU Filtering covers the generic Ubuntu Image family
			nodeClass := env.DefaultAKSNodeClass()
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.UbuntuImageFamily)
			return nodeClass

		}()),
		Entry("should provision one GPU Node and one GPU Pod (Ubuntu2204)", func() *v1beta1.AKSNodeClass {
			nodeClass := env.DefaultAKSNodeClass()
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)
			return nodeClass
		}()),
		Entry("should provision one GPU Node and one GPU Pod (Ubuntu2404)", func() *v1beta1.AKSNodeClass {
			nodeClass := env.DefaultAKSNodeClass()
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2404ImageFamily)
			return nodeClass
		}()),
	)

	It("should provision a GPU node with mode None",
		Label("GPU"),
		func() {
			noneMode := v1beta1.GPUModeNone
			nodeClass := env.DefaultAKSNodeClass()
			nodeClass.Spec.GPU = &v1beta1.GPU{Mode: &noneMode}

			nodePool := env.DefaultNodePool(nodeClass)
			// Override the default requirements to force Karpenter off D-series
			// (non-GPU) SKUs and onto an actual GPU SKU family.
			test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1beta1.LabelSKUFamily,
				Operator: corev1.NodeSelectorOpExists,
			})

			nodePool.Spec.Limits = karpv1.Limits{
				corev1.ResourceCPU:                    resource.MustParse("25"),
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
			}

			// Deploy a workload that targets GPU nodes via a node selector on
			// the GPU count label. We do NOT request nvidia.com/gpu resources
			// because mode: None means no device plugin is
			// installed, so the GPU resource is not advertised. Instead, the
			// node selector forces Karpenter to provision a GPU SKU.
			podOptions := test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gpu-none-driver-test",
					Labels: map[string]string{
						"app": "gpu-none-driver-test",
					},
				},
				NodeSelector: map[string]string{
					v1beta1.LabelSKUGPUCount: "1",
				},
			}
			deployment := test.Deployment(test.DeploymentOptions{
				Replicas:   1,
				PodOptions: podOptions,
			})

			env.ExpectCreated(nodeClass, nodePool, deployment)

			// Verify the GPU node is provisioned, initialized, and the
			// workload pod becomes healthy. With mode: None,
			// Karpenter provisions the node without installing GPU drivers.
			// The node should still come up and be ready.
			env.EventuallyExpectHealthyPodCountWithTimeout(
				time.Minute*15,
				labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels),
				int(*deployment.Spec.Replicas),
			)
			env.ExpectCreatedNodeCount("==", int(*deployment.Spec.Replicas))

			// Verify the node does NOT advertise nvidia.com/gpu in its allocatable
			// resources, confirming that no GPU driver was installed (mode: None).
			nodes := env.Monitor.CreatedNodes()
			Expect(nodes).To(HaveLen(int(*deployment.Spec.Replicas)))
			for _, node := range nodes {
				_, hasGPUResource := node.Status.Allocatable[corev1.ResourceName("nvidia.com/gpu")]
				Expect(hasGPUResource).To(BeFalse(), "node %s should not advertise nvidia.com/gpu with mode: None", node.Name)
			}
		},
	)

	It("should provision a GPU node with managed mode (nvidia.managementMode=Managed)",
		Label("GPU"),
		func() {
			// Managed mode: AKS installs and manages the NVIDIA stack (device
			// plugin + DCGM metrics) on top of the driver. Unlike the default
			// table above, we deliberately do NOT deploy a device plugin
			// DaemonSet ourselves — the managed experience must advertise
			// nvidia.com/gpu on its own. A healthy pod that requests
			// nvidia.com/gpu therefore proves the managed device plugin was
			// installed by the platform.
			//
			// Prerequisite: the cluster subscription must have the managed GPU
			// preview feature registered; otherwise the AKS machine API rejects
			// managementMode=Managed and the NodeClaim surfaces an error.
			nodeClass := env.DefaultAKSNodeClass()
			nodeClass.Spec.GPU = &v1beta1.GPU{
				Mode:   lo.ToPtr(v1beta1.GPUModeDriver),
				Nvidia: &v1beta1.NvidiaGPU{ManagementMode: lo.ToPtr(v1beta1.ManagementModeManaged)},
			}

			nodePool := env.DefaultNodePool(nodeClass)
			test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1beta1.LabelSKUFamily,
				Operator: corev1.NodeSelectorOpExists,
			})
			nodePool.Spec.Limits = karpv1.Limits{
				corev1.ResourceCPU:                    resource.MustParse("25"),
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
			}

			deployment := test.Deployment(test.DeploymentOptions{
				Replicas: 1,
				PodOptions: test.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "managed-gpu-test",
						Labels: map[string]string{"app": "managed-gpu-test"},
					},
					ResourceRequirements: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("1"),
						},
					},
				},
			})

			// Note: no device plugin DaemonSet is created here on purpose.
			env.ExpectCreated(nodeClass, nodePool, deployment)

			env.EventuallyExpectHealthyPodCountWithTimeout(
				time.Minute*15,
				labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels),
				int(*deployment.Spec.Replicas),
			)
			env.ExpectCreatedNodeCount("==", int(*deployment.Spec.Replicas))

			// The managed device plugin must have advertised the GPU resource
			// without any user-deployed device plugin.
			nodes := env.Monitor.CreatedNodes()
			Expect(nodes).To(HaveLen(int(*deployment.Spec.Replicas)))
			for _, node := range nodes {
				_, hasGPUResource := node.Status.Allocatable[corev1.ResourceName("nvidia.com/gpu")]
				Expect(hasGPUResource).To(BeTrue(), "node %s should advertise nvidia.com/gpu under managed GPU mode", node.Name)
			}
		},
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
