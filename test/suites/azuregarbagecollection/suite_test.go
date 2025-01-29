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

package azuregarbagecollection

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

var env *azure.Environment
var nodeClass *v1alpha2.AKSNodeClass
var nodePool *karpv1.NodePool
var pauseImage string

func TestAcr(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	RunSpecs(t, "Azure Garbage Collection")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("gc", func() {
	It("should garbage collect network interfaces created by karpenter", func() {
		deployment := test.Deployment(test.DeploymentOptions{
			Replicas: 1,
			PodOptions: test.PodOptions{
				ResourceRequirements: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("1.1"),
					},
				},
				Image: "mcr.microsoft.com/oss/kubernetes/pause:3.6",
			},
		})

		env.ExpectCreated(nodePool, nodeClass, deployment)
		env.EventuallyExpectHealthyPodCountWithTimeout(time.Minute*15, labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))

		// check number of nics + number of nodeclaims
		// if number of nics is greater than number of nodeclaims, then we have a leak, and we expect they are eventually cleaned up
		// if number of nics is equal to number of nodeclaims, then we need to create a network interface that will get picked up
		// by garbage collection.
		// its rather simple to create a fake karpenter network interface all it requires is we have the nodepool tag on the network interface.

	})
})
