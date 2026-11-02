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
	"os"
	"strings"
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
	"sigs.k8s.io/controller-runtime/pkg/client"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	azkarptest "github.com/Azure/karpenter-provider-azure/pkg/test"
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
	DescribeTable("should scale managed static MIG from zero and provision another node for overflow",
		Label("GPU", "ManagedMIG"),
		func(strategy v1beta1.MigStrategy, gpuResource corev1.ResourceName) {
			if os.Getenv("E2E_MANAGED_MIG") == "" {
				Skip("managed MIG requires explicit opt-in: E2E_MANAGED_MIG=true; see test/README.md for quota and preview prerequisites")
			}
			Expect(os.Getenv("E2E_MANAGED_MIG")).To(Equal("true"), "E2E_MANAGED_MIG must be true or unset")
			Expect(env.InClusterController).To(BeTrue(), "managed MIG E2E requires self-hosted Karpenter, not NAP")
			Expect(env.IsAKSMachineAPIMode()).To(BeTrue(), "managed MIG E2E requires PROVISION_MODE=aksmachineapi or aksmachineapiheaderbatch")
			controller := env.ExpectActiveKarpenterPod()
			Expect(controller.Spec.Containers).To(HaveLen(1))
			Expect(controller.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{
				Name: "PROVISION_MODE", Value: env.ProvisionMode,
			}), "the running self-hosted controller must match the test's Machine API provision mode")

			const sku = "Standard_NC24ads_A100_v4"
			const slicesPerNode = 7 // One A100 80GB, partitioned into homogeneous MIG1g instances.
			nodeClass := azkarptest.AKSNodeClass(azkarptest.WithImageFamily(v1beta1.Ubuntu2204ImageFamily))
			nodeClass.Spec.GPU = &v1beta1.GPU{
				Mode: lo.ToPtr(v1beta1.GPUModeDriver),
				Nvidia: &v1beta1.NvidiaGPU{
					ManagementMode:     lo.ToPtr(v1beta1.ManagementModeManaged),
					MigStrategy:        lo.ToPtr(strategy),
					GPUInstanceProfile: lo.ToPtr(v1beta1.GPUInstanceProfileMIG1g),
				},
			}
			nodePool := env.DefaultNodePool(nodeClass)
			test.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					Key: v1beta1.LabelSKUFamily, Operator: corev1.NodeSelectorOpExists,
				},
				karpv1.NodeSelectorRequirementWithMinValues{
					Key: corev1.LabelInstanceTypeStable, Operator: corev1.NodeSelectorOpIn, Values: []string{sku},
				},
			)
			nodePool.Spec.Limits = karpv1.Limits{
				corev1.ResourceCPU: resource.MustParse("48"),
				gpuResource:        resource.MustParse("14"),
			}
			deployment := test.Deployment(test.DeploymentOptions{
				Replicas: slicesPerNode,
				PodOptions: test.PodOptions{
					NodeSelector: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
					Tolerations: []corev1.Toleration{{
						Key: "nvidia.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule,
					}},
					Image: "nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0@sha256:7c7540bdf1f942d4fb6db97069fd6c289471b54ac29e3c7fcdf914cf77af7d41",
					// Keep every slice allocated after CUDA succeeds, so the eighth pod cannot reuse one.
					Command: []string{"/bin/sh", "-ec", "nvidia-smi -L && /cuda-samples/sample && touch /tmp/cuda-passed && exec sleep infinity"},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"test", "-f", "/tmp/cuda-passed"}}},
					},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("128Mi"),
							gpuResource: resource.MustParse("1"),
						},
						Limits: corev1.ResourceList{gpuResource: resource.MustParse("1")},
					},
				},
			})
			selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)

			By("starting with no Karpenter nodes, NodeClaims, or machines")
			nodes := &corev1.NodeList{}
			Expect(env.Client.List(env, nodes, client.HasLabels{karpv1.NodePoolLabelKey})).To(Succeed())
			Expect(nodes.Items).To(BeEmpty())
			claims := &karpv1.NodeClaimList{}
			Expect(env.Client.List(env, claims)).To(Succeed())
			Expect(claims.Items).To(BeEmpty())
			env.ExpectNoMachines()

			By("creating unschedulable MIG workloads before making the NodePool available")
			env.ExpectCreated(deployment)
			Eventually(func(g Gomega) {
				pods := env.ExpectPodsMatchingSelector(selector)
				g.Expect(pods).To(HaveLen(slicesPerNode))
				for _, pod := range pods {
					g.Expect(pod.Spec.NodeName).To(BeEmpty())
					g.Expect(pod.Status.Phase).To(Equal(corev1.PodPending))
					g.Expect(pod.Status.Conditions).To(ContainElement(And(
						HaveField("Type", corev1.PodScheduled), HaveField("Status", corev1.ConditionFalse),
						HaveField("Reason", corev1.PodReasonUnschedulable),
					)))
				}
			}).Should(Succeed())
			// Do not install a device plugin: the Machine API managed GPU stack must provide it.
			env.ExpectCreated(nodeClass, nodePool)
			admitted := &v1beta1.AKSNodeClass{ObjectMeta: metav1.ObjectMeta{Name: nodeClass.Name}}
			env.ExpectExists(admitted)
			Expect(admitted.GetManagementMode()).To(Equal(v1beta1.ManagementModeManaged), "managed mode must survive API server admission")
			Expect(admitted.GetMigStrategy()).To(Equal(strategy), "MIG strategy must survive API server admission")
			Expect(admitted.GetGPUInstanceProfile()).To(Equal(v1beta1.GPUInstanceProfileMIG1g), "MIG profile must survive API server admission")

			var firstNodeName string
			for nodeCount := 1; nodeCount <= 2; nodeCount++ {
				podCount := slicesPerNode + nodeCount - 1
				if nodeCount == 2 {
					By("adding an eighth pod while all seven slices on the first node remain allocated")
					deployment.Spec.Replicas = lo.ToPtr(int32(podCount))
					env.ExpectUpdated(deployment)
				}
				Eventually(func(g Gomega) {
					pods := env.ExpectPodsMatchingSelector(selector)
					g.Expect(pods).To(HaveLen(podCount))
					for _, pod := range pods {
						g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
						g.Expect(pod.Status.ContainerStatuses).To(HaveLen(1))
						g.Expect(pod.Status.ContainerStatuses[0].Ready).To(BeTrue(), "CUDA workload must succeed in %s", pod.Name)
						g.Expect(pod.Status.ContainerStatuses[0].RestartCount).To(BeZero())
					}
				}).WithTimeout(20 * time.Minute).Should(Succeed())

				createdNodes := env.ExpectCreatedNodeCount("==", nodeCount)
				if nodeCount == 1 {
					firstNodeName = createdNodes[0].Name
				}
				podsPerNode := map[string]int{}
				for _, pod := range env.ExpectPodsMatchingSelector(selector) {
					podsPerNode[pod.Spec.NodeName]++
					logs := env.EventuallyGetPodLogs(pod)
					Expect(logs).To(ContainSubstring("MIG 1g.10gb"), "pod %s must see an A100 80GB MIG1g device", pod.Name)
					Expect(logs).To(ContainSubstring("Test PASSED"), "pod %s must execute a CUDA kernel successfully", pod.Name)
				}
				Expect(podsPerNode).To(HaveLen(nodeCount))
				Expect(podsPerNode[firstNodeName]).To(Equal(slicesPerNode), "all seven original slices must remain occupied during overflow")
				for _, node := range createdNodes {
					Expect(node.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))
					Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelInstanceTypeStable, sku))
					Expect(node.Labels).To(HaveKeyWithValue("kubernetes.azure.com/dcgm-exporter", "enabled"))
					Expect(podsPerNode[node.Name]).To(BeNumerically(">", 0))
					Expect(podsPerNode[node.Name]).To(BeNumerically("<=", slicesPerNode))
					for _, resources := range []corev1.ResourceList{node.Status.Capacity, node.Status.Allocatable} {
						advertised := map[corev1.ResourceName]int64{}
						for name, quantity := range resources {
							if (name == "nvidia.com/gpu" || strings.HasPrefix(string(name), "nvidia.com/mig-")) && !quantity.IsZero() {
								advertised[name] = quantity.Value()
							}
						}
						Expect(advertised).To(Equal(map[corev1.ResourceName]int64{gpuResource: slicesPerNode}),
							"node %s must advertise exactly seven slices under the correct resource name", node.Name)
					}
				}
				nodeClaims := env.EventuallyExpectRegisteredNodeClaimCount("==", nodeCount)
				env.EventuallyExpectNodeClaimsReady(nodeClaims...)
				env.EventuallyExpectCreatedMachineCount("==", nodeCount)
				for _, claim := range nodeClaims {
					Expect(podsPerNode).To(HaveKey(claim.Status.NodeName))
					machineID := claim.Annotations[v1beta1.AnnotationAKSMachineResourceID]
					Expect(machineID).ToNot(BeEmpty(), "NodeClaim must have been launched via the Machine API")
					machine := env.ExpectMachineByID(machineID)
					Expect(strings.EqualFold(lo.FromPtr(machine.ID), machineID)).To(BeTrue(), "Machine resource ID must match NodeClaim annotation")
				}
			}
		},
		Entry("Single MIG1g", v1beta1.MigStrategySingle, corev1.ResourceName("nvidia.com/gpu")),
		Entry("Mixed MIG1g", v1beta1.MigStrategyMixed, corev1.ResourceName("nvidia.com/mig-1g.10gb")),
	)

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
		Entry("should provision one GPU Node and one GPU Pod (AzureLinux)", azkarptest.AKSNodeClass(azkarptest.WithImageFamily(v1beta1.AzureLinuxImageFamily))),
		Entry("should provision one GPU Node and one GPU Pod (Ubuntu)", azkarptest.AKSNodeClass(azkarptest.WithImageFamily(v1beta1.UbuntuImageFamily))),
		Entry("should provision one GPU Node and one GPU Pod (Ubuntu2204)", azkarptest.AKSNodeClass(azkarptest.WithImageFamily(v1beta1.Ubuntu2204ImageFamily))),
		Entry("should provision one GPU Node and one GPU Pod (Ubuntu2404)", azkarptest.AKSNodeClass(azkarptest.WithImageFamily(v1beta1.Ubuntu2404ImageFamily))),
	)

	It("should provision a GPU node with mode None",
		Label("GPU"),
		func() {
			noneMode := v1beta1.GPUModeNone
			nodeClass := azkarptest.AKSNodeClass()
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
							Image: "nvcr.io/nvidia/k8s-device-plugin:v0.20.0",
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
