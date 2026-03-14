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
	"github.com/samber/lo"
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
var nodeClass *v1beta1.AKSNodeClass
var nodePool *karpv1.NodePool

func TestUtilization(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	RunSpecs(t, "Utilization")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})

var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("Utilization", func() {
	DescribeTable("should provision one pod per node",
		func(imageFamily string, arch string) {
			nodeClass := env.DefaultAKSNodeClass()
			nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
			nodePool := lo.Ternary(arch == karpv1.ArchitectureAmd64, env.DefaultNodePool(nodeClass), env.ArmNodepool(nodeClass))
			ExpectProvisionPodPerNode(nodeClass, nodePool)
		},

		// AzureLinux
		Entry("AzureLinux, amd64", v1beta1.AzureLinuxImageFamily, karpv1.ArchitectureAmd64),
		// Covered below due to conditional logic, arm64 is not available in CIG
		// Entry("AzureLinux, arm64", env.AZLinuxNodeClass().Spec.ImageFamily, karpv1.ArchitectureArm64,

		// Ubuntu
		Entry("Ubuntu, amd64", v1beta1.UbuntuImageFamily, karpv1.ArchitectureAmd64),
		Entry("Ubuntu, arm64", v1beta1.UbuntuImageFamily, karpv1.ArchitectureArm64),
		Entry("Ubuntu2204, amd64", v1beta1.Ubuntu2204ImageFamily, karpv1.ArchitectureAmd64),
		Entry("Ubuntu2204, arm64", v1beta1.Ubuntu2204ImageFamily, karpv1.ArchitectureArm64),
		Entry("Ubuntu2404, amd64", v1beta1.Ubuntu2404ImageFamily, karpv1.ArchitectureAmd64),
		Entry("Ubuntu2404, arm64", v1beta1.Ubuntu2404ImageFamily, karpv1.ArchitectureArm64),
	)

	It("should provision one pod per node (AzureLinux, arm64)", func() {
		if imagefamily.UseAzureLinux3(env.K8sVersion()) && env.InClusterController {
			Skip("AzureLinux3 ARM64 VHD is not available in CIG")
		}
		nc := env.AZLinuxNodeClass()
		ExpectProvisionPodPerNode(nc, env.ArmNodepool(nc))
	})
})

func ExpectProvisionPodPerNode(nodeClass *v1beta1.AKSNodeClass, nodePool *karpv1.NodePool) {
	GinkgoHelper()
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
