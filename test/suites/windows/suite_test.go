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

package windows_test

import (
	"fmt"
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
	"k8s.io/apimachinery/pkg/util/intstr"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"
)

var env *azure.Environment

const windowsServicePort int32 = 8080

type windowsImageSettings struct {
	family               string
	expectedSKU          string
	expectedImagePattern string
	expectedFIPS         bool
}

func windowsImageFamilies() []windowsImageSettings {
	return []windowsImageSettings{
		{
			family:               v1beta1.Windows2022ImageFamily,
			expectedSKU:          v1beta1.OSSKUWindows2022,
			expectedImagePattern: `^AKSWindows-2022-`,
		},
		{
			family:               v1beta1.Windows2025ImageFamily,
			expectedSKU:          v1beta1.OSSKUWindows2025,
			expectedImagePattern: `^AKSWindows-2025-`,
			expectedFIPS:         true,
		},
	}
}

func requireWindowsMachineAPICluster() {
	GinkgoHelper()
	if !env.IsMachineModeOrNPS() {
		Skip("Windows node provisioning requires AKS Machine API or managed NAP")
	}
	if env.MachineAgentPoolName != "aksmanagedap" && len(env.MachineAgentPoolName) > 6 {
		Skip(fmt.Sprintf("Windows machines require the reserved aksmanagedap pool or a custom machines pool name <= 6 chars; got %q (%d chars)",
			env.MachineAgentPoolName, len(env.MachineAgentPoolName)))
	}
}

func requireSupportedWindowsImageFamily(settings windowsImageSettings) {
	GinkgoHelper()
	if settings.family == v1beta1.Windows2025ImageFamily && env.K8sMinorVersion() < 32 {
		Skip(fmt.Sprintf("%s requires Kubernetes 1.32 or newer", settings.family))
	}
}

func TestWindows(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	AfterSuite(func() {
		env.Stop()
	})
	RunSpecs(t, "Windows")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	requireWindowsMachineAPICluster()
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("Windows", func() {
	for _, settings := range windowsImageFamilies() {
		It(fmt.Sprintf("should provision %s, serve Windows traffic, and report image and FIPS state", settings.family), func() {
			requireSupportedWindowsImageFamily(settings)

			nodeClass := windowsNodeClass(settings)
			nodePool := env.WindowsNodePool(nodeClass)
			configureRegionalWindowsNodePool(nodePool)

			deployment := windowsServerDeployment("windows-service", 1)
			service := serviceForWindowsDeployment(deployment)
			clientPod := windowsServiceClient(service)

			env.ExpectCreated(nodeClass, nodePool, deployment, service, clientPod)

			// Windows nodes take noticeably longer to provision and pull images than Linux.
			pods := env.EventuallyExpectHealthyDeploymentWithTimeout(25*time.Minute, deployment)
			env.EventuallyExpectHealthyWithTimeout(25*time.Minute, clientPod)
			env.ExpectCreatedNodeCount("==", 1)

			expectWindowsProvisioningRelationships(settings, nodePool, append(pods, clientPod), 1)
		})
	}

	It("should scale Windows2022 across two nodes", func() {
		settings := windowsImageFamilies()[0]
		nodeClass := windowsNodeClass(settings)
		nodePool := env.WindowsNodePool(nodeClass)
		configureRegionalWindowsNodePool(nodePool)

		deployment := test.Deployment(test.DeploymentOptions{
			Replicas: 2,
			PodOptions: windowsPodOptions(map[string]string{
				"app": "windows-scale-out",
			}),
		})
		deployment.Spec.Template.Spec.Containers[0].Command = []string{"/agnhost", "pause"}
		deployment.Spec.Template.Spec.Affinity = &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
					LabelSelector: deployment.Spec.Selector,
					TopologyKey:   corev1.LabelHostname,
				}},
			},
		}

		env.ExpectCreated(nodeClass, nodePool, deployment)

		pods := env.EventuallyExpectHealthyDeploymentWithTimeout(25*time.Minute, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectUniqueNodeNames(selector, 2)
		env.ExpectCreatedNodeCount("==", 2)
		expectWindowsProvisioningRelationships(settings, nodePool, pods, 2)
	})
})

func windowsNodeClass(settings windowsImageSettings) *v1beta1.AKSNodeClass {
	nodeClass := env.WindowsNodeClass(settings.family)
	if settings.expectedFIPS {
		nodeClass.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeFIPS)
	}
	return nodeClass
}

func configureRegionalWindowsNodePool(nodePool *karpv1.NodePool) {
	// Keep the test portable across subscriptions and regions without availability-zone support.
	// The SKU is intentionally unconstrained: AKS Machine API requests a Gen2 Windows image when
	// the selected SKU supports it, including for Gen2-only sizes.
	test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
		Key:      corev1.LabelTopologyZone,
		Operator: corev1.NodeSelectorOpIn,
		Values:   []string{zones.Regional},
	})
}

func windowsServerDeployment(app string, replicas int32) *appsv1.Deployment {
	options := windowsPodOptions(map[string]string{"app": app})
	options.Command = common.NetexecCommand(windowsServicePort)
	options.ReadinessProbe = common.TCPReadinessProbe(windowsServicePort)
	return test.Deployment(test.DeploymentOptions{
		Replicas:   replicas,
		PodOptions: options,
	})
}

func windowsPodOptions(podLabels map[string]string) test.PodOptions {
	return test.PodOptions{
		ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
		Image:      common.AgnHostTestImage,
		NodeSelector: map[string]string{
			corev1.LabelOSStable: string(corev1.Windows),
		},
		Tolerations: windowsTolerations(),
		ResourceRequirements: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		},
	}
}

func serviceForWindowsDeployment(deployment *appsv1.Deployment) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: test.NamespacedObjectMeta(),
		Spec: corev1.ServiceSpec{
			Selector: deployment.Spec.Selector.MatchLabels,
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Protocol:   corev1.ProtocolTCP,
				Port:       windowsServicePort,
				TargetPort: intstr.FromInt32(windowsServicePort),
			}},
		},
	}
}

func windowsServiceClient(service *corev1.Service) *corev1.Pod {
	return env.Pod(test.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"app": "windows-service-client"},
		},
		Image:   common.AgnHostTestImage,
		Command: []string{"/agnhost", "pause"},
		InitContainers: []corev1.Container{{
			Image: common.AgnHostTestImage,
			Command: []string{
				"/agnhost", "connect", "--timeout=30s", fmt.Sprintf("%s:%d", service.Name, windowsServicePort),
			},
		}},
		NodeSelector: map[string]string{
			corev1.LabelOSStable: string(corev1.Windows),
		},
		Tolerations: windowsTolerations(),
		ResourceRequirements: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
	})
}

func windowsTolerations() []corev1.Toleration {
	return []corev1.Toleration{{
		Key:      corev1.LabelOSStable,
		Operator: corev1.TolerationOpEqual,
		Value:    string(corev1.Windows),
		Effect:   corev1.TaintEffectNoSchedule,
	}}
}
