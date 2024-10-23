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

package drift

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"

	corev1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"

	"sigs.k8s.io/karpenter/pkg/test"
)

var env *azure.Environment
var nodeClass *v1alpha2.AKSNodeClass
var nodePool *corev1beta1.NodePool

func TestDrift(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	RunSpecs(t, "Drift")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("Drift", func() {

	var pod *v1.Pod

	BeforeEach(func() {
		env.ExpectSettingsOverridden(v1.EnvVar{Name: "FEATURE_GATES", Value: "Drift=true"})

		test.ReplaceRequirements(nodePool, corev1beta1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: v1.NodeSelectorRequirement{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_DS2_v2"},
			}})

		// Add a do-not-disrupt pod so that we can check node metadata before we disrupt
		pod = test.Pod(test.PodOptions{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					corev1beta1.DoNotDisruptAnnotationKey: "true",
				},
			},
			ResourceRequirements: v1.ResourceRequirements{Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("0.5")}},
			Image:                "mcr.microsoft.com/oss/kubernetes/pause:3.6",
		})
	})
	It("should deprovision nodes that have drifted due to labels", func() {

		By(fmt.Sprintf("creating pod %s, nodepool %s, and nodeclass %s", pod.Name, nodePool.Name, nodeClass.Name))
		env.ExpectCreated(pod, nodeClass, nodePool)

		By(fmt.Sprintf("expect pod %s to be healthy", pod.Name))
		env.EventuallyExpectHealthy(pod)

		By("expect created node count to be 1")
		env.ExpectCreatedNodeCount("==", 1)

		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]
		node := env.EventuallyExpectNodeCount("==", 1)[0]

		By(fmt.Sprintf("waiting for nodepool %s update", nodePool.Name))
		nodePool.Spec.Template.Labels["triggerdrift"] = "value"
		env.ExpectCreatedOrUpdated(nodePool)

		By(fmt.Sprintf("waiting for nodeclaim %s to be marked as drifted", nodeClaim.Name))
		env.EventuallyExpectDrifted(nodeClaim)

		By(fmt.Sprintf("waiting for pod %s to to update", pod.Name))
		delete(pod.Annotations, corev1beta1.DoNotDisruptAnnotationKey)
		env.ExpectUpdated(pod)

		By(fmt.Sprintf("expect pod %s, nodeclaim %s, and node %s to eventually not exist", pod.Name, nodeClaim.Name, node.Name))
		SetDefaultEventuallyTimeout(10 * time.Minute)
		env.EventuallyExpectNotFound(pod, nodeClaim, node)
		SetDefaultEventuallyTimeout(5 * time.Minute)
	})
	It("should mark the nodeclaim as drifted for SubnetDrift if AKSNodeClass subnet id changes", func() {
		env.ExpectCreated(pod, nodeClass, nodePool)

		By(fmt.Sprintf("expect pod %s to be healthy", pod.Name))
		env.EventuallyExpectHealthy(pod)

		By("expect created node count to be 1")
		env.ExpectCreatedNodeCount("==", 1)

		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]
		By("triggering subnet drift")
		// TODO: Introduce azure clients to the tests to get values dynamically and be able to create azure resources inside of tests rather than using a fake id.
		// this will fail to actually create a new nodeclaim for the drift replacement but should still test that we are marking the nodeclaim as drifted.
		nodeClass.Spec.VNETSubnetID = lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpenter/subnets/nodeclassSubnet2")
		env.ExpectCreatedOrUpdated(nodeClass)

		By(fmt.Sprintf("waiting for nodeclaim %s to be marked as drifted", nodeClaim.Name))
		env.EventuallyExpectDrifted(nodeClaim)
	})
})
