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

package arm_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"

	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"github.com/aws/karpenter-core/pkg/test"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/karpenter/test/pkg/environment/azure"
)

var env *azure.Environment

func TestArm(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	RunSpecs(t, "Arm")
}

var _ = BeforeEach(func() { env.BeforeEach() })
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("Arm", func() {
	It("should provision one arm64 node and one Pod (Ubuntu2204)", func() {
		nodeClass := env.DefaultAKSNodeClass()
		nodePool := env.DefaultNodePool(nodeClass)
		test.ReplaceRequirements(nodePool, v1.NodeSelectorRequirement{
			Key:      v1.LabelArchStable,
			Operator: v1.NodeSelectorOpIn,
			Values:   []string{corev1beta1.ArchitectureArm64},
		})
		deployment := test.Deployment(test.DeploymentOptions{
			Replicas:   1,
			PodOptions: test.PodOptions{ResourceRequirements: v1.ResourceRequirements{Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("1.1")}}, Image: "mcr.microsoft.com/oss/kubernetes/pause:3.6"}})

		env.ExpectCreated(nodePool, nodeClass, deployment)
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))

	})

	It("should provision one arm64 node and one Pod (AzureLinux)", func() {
		nodeClass := env.DefaultAKSNodeClass()
		nodeClass.Spec.ImageFamily = to.Ptr("AzureLinux")
		nodePool := env.DefaultNodePool(nodeClass)
		test.ReplaceRequirements(nodePool, v1.NodeSelectorRequirement{
			Key:      v1.LabelArchStable,
			Operator: v1.NodeSelectorOpIn,
			Values:   []string{corev1beta1.ArchitectureArm64},
		})
		deployment := test.Deployment(test.DeploymentOptions{
			Replicas:   1,
			PodOptions: test.PodOptions{ResourceRequirements: v1.ResourceRequirements{Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("1.1")}}, Image: "mcr.microsoft.com/oss/kubernetes/pause:3.6"}})

		env.ExpectCreated(nodePool, nodeClass, deployment)
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
		env.ExpectCreatedNodeCount("==", int(*deployment.Spec.Replicas))

	})

})
