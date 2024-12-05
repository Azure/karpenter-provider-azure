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
	"k8s.io/apimachinery/pkg/labels"
	"knative.dev/pkg/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

var _ = Describe("Emptiness", func() {
	// TODO: add budget tests
	It("should terminate an empty node", func() {
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("10s")

		const numPods = 1
		deployment := test.Deployment(test.DeploymentOptions{Replicas: numPods})

		By("kicking off provisioning for a deployment")
		env.ExpectCreated(nodeClass, nodePool, deployment)
		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), numPods)

		By("making the nodeclaim empty")
		persisted := deployment.DeepCopy()
		deployment.Spec.Replicas = ptr.Int32(0)
		Expect(env.Client.Patch(env, deployment, client.MergeFrom(persisted))).To(Succeed())

		env.EventuallyExpectConsolidatable(nodeClaim)

		By("waiting for the nodeclaim to deprovision when past its ConsolidateAfter timeout of 0")
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
		env.ExpectUpdated(nodePool)

		env.EventuallyExpectNotFound(nodeClaim, node)
	})
})
