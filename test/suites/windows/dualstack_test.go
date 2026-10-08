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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"
)

const expectDualStackEnvVar = "E2E_EXPECT_DUAL_STACK"

func requireDualStackCluster() {
	GinkgoHelper()
	if os.Getenv(expectDualStackEnvVar) != "true" {
		Skip(fmt.Sprintf("Windows dual-stack validation requires %s=true", expectDualStackEnvVar))
	}
}

var _ = Describe("Windows DualStack", func() {
	BeforeEach(func() {
		requireDualStackCluster()
	})

	for _, settings := range windowsImageFamilies() {
		It(fmt.Sprintf("should provision %s with functional IPv4 and IPv6 networking", settings.family), func() {
			requireSupportedWindowsImageFamily(settings)

			nodeClass := windowsNodeClass(settings)
			nodePool := env.WindowsNodePool(nodeClass)
			configureRegionalNodePool(nodePool)
			deployment := windowsServerDeployment("windows-dualstack", 1)
			service := common.DualStackServiceForDeployment(deployment, windowsServicePort)

			env.ExpectCreated(nodeClass, nodePool)
			resolvedImages := expectResolvedWindowsImages(nodeClass)
			env.ExpectCreated(deployment, service)

			pods := env.EventuallyExpectHealthyDeploymentWithTimeout(25*time.Minute, deployment)
			Expect(common.HasIPv4AndIPv6PodIPs(pods[0].Status.PodIPs)).To(BeTrue(), "expected pod %s/%s to have IPv4 and IPv6 PodIPs, got %v", pods[0].Namespace, pods[0].Name, pods[0].Status.PodIPs)
			env.ExpectCreatedNodeCount("==", 1)

			var node *corev1.Node
			Eventually(func(g Gomega) {
				node = env.GetNode(pods[0].Spec.NodeName)
				g.Expect(common.HasIPv4AndIPv6NodeInternalIPs(node)).To(BeTrue(), "expected node %s to have IPv4 and IPv6 InternalIPs, got %v", node.Name, node.Status.Addresses)
			}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
			Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelOSStable, string(corev1.Windows)))
			Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelOSSKU, settings.expectedSKU))
			Expect(node.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))

			env.EventuallyExpectDualStackServiceConnectivity(
				service,
				pods[0],
				windowsServicePort,
				10*time.Minute,
				windowsDualStackProbeOptions(),
			)
			expectWindowsProvisioningRelationships(settings, resolvedImages, nodePool, pods, 1, 1)
		})
	}
})

func windowsDualStackProbeOptions() test.PodOptions {
	return test.PodOptions{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"app": "dualstack-service-probe-windows"},
		},
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
	}
}
