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

package integration_test

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

	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"
)

const (
	expectDualStackEnvVar = "E2E_EXPECT_DUAL_STACK"
	dualStackServicePort  = int32(8080)
)

func requireDualStackCluster() {
	GinkgoHelper()
	if os.Getenv(expectDualStackEnvVar) != "true" {
		Skip(fmt.Sprintf("dual-stack validation requires %s=true", expectDualStackEnvVar))
	}
}

var _ = Describe("IPv6 DualStack", func() {
	BeforeEach(func() {
		requireDualStackCluster()
	})

	It("should provision a Linux node with functional IPv4 and IPv6 networking", func() {
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      corev1.LabelTopologyZone,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{zones.Regional},
		})

		deployment := test.Deployment(test.DeploymentOptions{
			Replicas: 1,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "linux-dualstack"},
				},
				Image:          common.AgnHostTestImage,
				Command:        common.NetexecCommand(dualStackServicePort),
				ReadinessProbe: common.TCPReadinessProbe(dualStackServicePort),
				NodeSelector: map[string]string{
					corev1.LabelOSStable: string(corev1.Linux),
				},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
			},
		})
		service := common.DualStackServiceForDeployment(deployment, dualStackServicePort)

		env.ExpectCreated(nodeClass, nodePool, deployment, service)

		pods := env.EventuallyExpectHealthyDeploymentWithTimeout(15*time.Minute, deployment)
		Expect(common.HasIPv4AndIPv6PodIPs(pods[0].Status.PodIPs)).To(BeTrue(), "expected pod %s/%s to have IPv4 and IPv6 PodIPs, got %v", pods[0].Namespace, pods[0].Name, pods[0].Status.PodIPs)
		env.ExpectCreatedNodeCount("==", 1)

		var node *corev1.Node
		Eventually(func(g Gomega) {
			node = env.GetNode(pods[0].Spec.NodeName)
			g.Expect(common.HasIPv4AndIPv6NodeInternalIPs(node)).To(BeTrue(), "expected node %s to have IPv4 and IPv6 InternalIPs, got %v", node.Name, node.Status.Addresses)
		}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelOSStable, string(corev1.Linux)))
		Expect(node.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))

		env.EventuallyExpectDualStackServiceConnectivity(service, pods[0], dualStackServicePort, 10*time.Minute)
	})
})
