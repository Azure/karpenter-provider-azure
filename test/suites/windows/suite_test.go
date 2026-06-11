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
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
)

var env *azure.Environment

// windowsPauseImage is a multi-arch pause image with a Windows Server 2022 (ltsc2022,
// build 20348) variant, matching the Windows2022 node we provision.
const windowsPauseImage = "mcr.microsoft.com/oss/kubernetes/pause:3.9"

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

var _ = BeforeEach(func() { env.BeforeEach() })
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("Windows", func() {
	BeforeEach(func() {
		// Windows nodes are only provisionable via the AKS Machine API provision mode.
		if !env.IsAKSMachineAPIMode() {
			Skip("Windows node provisioning is only supported in AKS Machine API provision mode")
		}
		// The AKS Machine API only allows usable (<=12 char) Windows machine names in the
		// reserved, NAP-managed agent pool ("aksmanagedap"); custom/self-hosted machine pools
		// are capped at 5 chars, which Karpenter's generated names cannot satisfy. So this
		// test is only meaningful against a NAP-managed cluster.
		if env.MachineAgentPoolName != "aksmanagedap" {
			Skip("Windows machines require the reserved NAP-managed agent pool (aksmanagedap); skipping on custom machine pool")
		}
	})

	It("should provision a Windows node and run a Windows pod", func() {
		nodeClass := env.WindowsNodeClass()
		nodePool := env.WindowsNodePool(nodeClass)
		// Keep the test as simple and portable as possible: pin to the regional (zoneless)
		// offering so it provisions in subscriptions/regions without availability-zone support.
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      corev1.LabelTopologyZone,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{zones.Regional},
		})

		deployment := test.Deployment(test.DeploymentOptions{
			Replicas: 1,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "windows-inflate"},
				},
				Image: windowsPauseImage,
				NodeSelector: map[string]string{
					corev1.LabelOSStable: string(corev1.Windows),
				},
				// Tolerate the OS taint Karpenter may surface during Windows node registration.
				Tolerations: []corev1.Toleration{{
					Key:      corev1.LabelOSStable,
					Operator: corev1.TolerationOpEqual,
					Value:    string(corev1.Windows),
					Effect:   corev1.TaintEffectNoSchedule,
				}},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
			},
		})

		env.ExpectCreated(nodeClass, nodePool, deployment)

		// Windows nodes take noticeably longer to provision and pull images than Linux.
		pods := env.EventuallyExpectHealthyDeploymentWithTimeout(25*time.Minute, deployment)
		env.ExpectCreatedNodeCount("==", 1)

		node := env.GetNode(pods[0].Spec.NodeName)
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelOSStable, string(corev1.Windows)))
		Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelOSSKU, v1beta1.OSSKUWindows2022))
		Expect(node.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))
	})
})
