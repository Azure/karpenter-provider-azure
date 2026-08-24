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

const windowsPauseImage = "mcr.microsoft.com/oss/kubernetes/pause:3.9"
const windows2025Image = "mcr.microsoft.com/windows/servercore:ltsc2025"

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
		// Windows machine names are bounded by the Windows NetBIOS computer-name limit once the AKS RP
		// composes the VM name from the pool and machine names. The reserved NAP pool ("aksmanagedap")
		// uses a 12-character pool-hash plus NodeClaim-suffix name. Custom pools use only the
		// five-character NodeClaim suffix and additionally require the pool name to be <= 6 chars.
		if env.MachineAgentPoolName != "aksmanagedap" && len(env.MachineAgentPoolName) > 6 {
			Skip(fmt.Sprintf("Windows machines require the reserved aksmanagedap pool or a custom machines pool name <= 6 chars; got %q (%d chars)",
				env.MachineAgentPoolName, len(env.MachineAgentPoolName)))
		}
	})

	It("should provision a Windows node and run a Windows pod", func() {
		imageFamily := os.Getenv("WINDOWS_IMAGE_FAMILY")
		if imageFamily == "" {
			imageFamily = v1beta1.Windows2022ImageFamily
		}

		var (
			containerImage string
			command        []string
			expectedOSSKU  string
		)
		switch imageFamily {
		case v1beta1.Windows2022ImageFamily:
			containerImage = windowsPauseImage
			expectedOSSKU = v1beta1.OSSKUWindows2022
		case v1beta1.Windows2025ImageFamily:
			containerImage = windows2025Image
			command = []string{"cmd", "/S", "/C", "ping -t 127.0.0.1"}
			expectedOSSKU = v1beta1.OSSKUWindows2025
		default:
			Fail(fmt.Sprintf("unsupported WINDOWS_IMAGE_FAMILY %q", imageFamily))
		}

		nodeClass := env.WindowsNodeClass(imageFamily)
		nodePool := env.WindowsNodePool(nodeClass)
		// Keep the test as simple and portable as possible: pin to the regional (zoneless)
		// offering so it provisions in subscriptions/regions without availability-zone support.
		// The SKU itself is intentionally left unconstrained: in AKS Machine API mode Karpenter
		// requests a Gen2 Windows image (UseWindowsGen2VM) whenever the selected SKU supports it,
		// so Windows provisions on any Hyper-V generation, including Gen2-only sizes.
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
				Image:   containerImage,
				Command: command,
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
		Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelOSSKU, expectedOSSKU))
		Expect(node.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))
		if imageFamily == v1beta1.Windows2025ImageFamily {
			Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelFIPSEnabled, "true"))
		}
	})
})
