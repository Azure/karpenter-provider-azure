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

package kubernetesupgrade_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v4"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	coretest "sigs.k8s.io/karpenter/pkg/test"
)

var env *azure.Environment
var nodeClass *v1alpha2.AKSNodeClass
var nodePool *karpv1.NodePool

func TestDrift(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	RunSpecs(t, "KubernetesUpgrade")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("KubernetesUpgrade", func() {
	// NOTE: This Describe has only one test, and no BeforeEach, as this test performs modifications to the managed cluster in a way that can not be undone,
	// meaning the environment can not be reset for any additional tests.
	//
	// This is actually a test of Drift. However, testing kubernetes upgrade has to be isolated due to its modification of the managed cluster environment.
	It("should drift and replace all nodes when the cluster's kubernetes version upgrades", func() {
		numPods := 1
		// Add pods with a do-not-disrupt annotation so that we can check node metadata before we disrupt
		dep := coretest.Deployment(coretest.DeploymentOptions{
			Replicas: int32(numPods),
			PodOptions: coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "my-app",
					},
					Annotations: map[string]string{
						karpv1.DoNotDisruptAnnotationKey: "true",
					},
				},
				TerminationGracePeriodSeconds: lo.ToPtr[int64](0),
			},
		})
		selector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)

		env.ExpectCreated(dep, nodeClass, nodePool)
		pod := env.EventuallyExpectHealthyPodCount(selector, numPods)[0]
		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]
		node := env.ExpectCreatedNodeCount("==", 1)[0]

		By("getting latest available kubernetes version upgrade")
		availableKubernetesUpgrades := env.ExpectSuccessfulGetOfAvailableKubernetesVersionUpgradesForManagedCluster()
		kubernetesUpgradeVersion := *lo.MaxBy(availableKubernetesUpgrades, func(a, b *containerservice.ManagedClusterPoolUpgradeProfileUpgradesItem) bool {
			return *a.KubernetesVersion > *b.KubernetesVersion
		}).KubernetesVersion

		By(fmt.Sprintf("upgrading the managed cluster to kubernetes version: %s", kubernetesUpgradeVersion))
		env.ExpectSuccessfulUpgradeOfManagedCluster(kubernetesUpgradeVersion)

		By("replacing nodes via Drift on kubernetes version")
		// Raising the timeout for Drift detection.
		// Based off KubernetesVersionTTL. However, not directly referenced, as if KubernetesVersionTTL is increased we don't want this test
		// runtime to balloon out of control.
		SetDefaultEventuallyTimeout(15 * time.Minute)
		env.EventuallyExpectDrifted(nodeClaim)
		SetDefaultEventuallyTimeout(5 * time.Minute)

		delete(pod.Annotations, karpv1.DoNotDisruptAnnotationKey)
		env.ExpectUpdated(pod)

		env.EventuallyExpectNotFound(pod, node)
		env.EventuallyExpectHealthyPodCount(selector, numPods)

		By(fmt.Sprintf("new nodes having upgraded kubernetes version: %s", kubernetesUpgradeVersion))
		node = env.ExpectCreatedNodeCount("==", 1)[0]
		Expect(strings.TrimPrefix(node.Status.NodeInfo.KubeletVersion, "v")).To(Equal(kubernetesUpgradeVersion))
	})
})
