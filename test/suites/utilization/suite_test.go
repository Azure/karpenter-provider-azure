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

package utilization_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
)

var env *azure.Environment

func TestUtilization(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	RunSpecs(t, "Utilization")
}

var _ = BeforeEach(func() { env.BeforeEach() })
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("Utilization", func() {
	It("should provision one pod per node (AzureLinux, amd64)", func() {
		ExpectProvisionPodPerNode(env.AZLinuxNodeClass, env.DefaultNodePool)
	})
	It("should provision one pod per node (AzureLinux, arm64)", func() {
		if imagefamily.UseAzureLinux3(env.K8sVersion()) && env.InClusterController {
			Skip("AzureLinux3 ARM64 VHD is not available in CIG")
		}
		ExpectProvisionPodPerNode(env.AZLinuxNodeClass, env.ArmNodepool)
	})
	It("should provision one pod per node (Ubuntu, amd64)", func() {
		ExpectProvisionPodPerNode(env.DefaultAKSNodeClass, env.DefaultNodePool)
	})
	It("should provision one pod per node (Ubuntu, arm64)", func() {
		ExpectProvisionPodPerNode(env.DefaultAKSNodeClass, env.ArmNodepool)
	})
})

func ExpectProvisionPodPerNode(getNodeClass func() *v1beta1.AKSNodeClass, getNodePool func(*v1beta1.AKSNodeClass) *karpv1.NodePool) {
	GinkgoHelper()
	nodeClass := getNodeClass()
	nodePool := getNodePool(nodeClass)
	test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
		NodeSelectorRequirement: v1.NodeSelectorRequirement{
			Key:      v1beta1.LabelSKUCPU,
			Operator: v1.NodeSelectorOpLt,
			Values:   []string{"3"},
		}})

	deployment := test.Deployment(test.DeploymentOptions{
		Replicas: 10,
		PodOptions: test.PodOptions{
			ResourceRequirements: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceCPU: resource.MustParse("1.1"),
				},
			},
		},
	})

	env.ExpectCreated(nodePool, nodeClass, deployment)
	env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
	env.ExpectCreatedNodeCount("==", int(*deployment.Spec.Replicas)) // One pod per node enforced by instance size
}
