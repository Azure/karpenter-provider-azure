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
	"strings"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/blang/semver/v4"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Node image pinning", func() {
	It("should provision a node one Kubernetes version behind the control plane", func() {
		serverVersion := lo.Must(env.KubeClient.Discovery().ServerVersion())
		requestedVersion := lo.Must(semver.ParseTolerant(serverVersion.GitVersion))
		Expect(requestedVersion.Patch).To(BeNumerically(">", 0))
		requestedVersion.Patch--

		nodeClassObject := lo.Must(runtime.DefaultUnstructuredConverter.ToUnstructured(nodeClass))
		unstructuredNodeClass := &unstructured.Unstructured{Object: nodeClassObject}
		Expect(unstructured.SetNestedField(unstructuredNodeClass.Object, requestedVersion.String(), "spec", "versions", "kubernetesVersion")).To(Succeed())

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(unstructuredNodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)

		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(unstructuredNodeClass), unstructuredNodeClass)).To(Succeed())
			effectiveVersion, found, err := unstructured.NestedString(unstructuredNodeClass.Object, "status", "kubernetesVersion")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(found).To(BeTrue())
			g.Expect(effectiveVersion).To(Equal(requestedVersion.String()))
		}).Should(Succeed())

		node := env.GetNode(pods[0].Spec.NodeName)
		Expect(strings.TrimPrefix(node.Status.NodeInfo.KubeletVersion, "v")).To(Equal(requestedVersion.String()))
	})

	It("should pin the current Kubernetes and node image versions", func() {
		env.ExpectCreated(nodeClass, nodePool)

		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
			g.Expect(nodeClass.Status.KubernetesVersion).ToNot(BeNil())
			g.Expect(nodeClass.Status.Images).ToNot(BeEmpty())
			g.Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeKubernetesVersionReady)).To(BeTrue())
			g.Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeImagesReady)).To(BeTrue())
		}).Should(Succeed())

		currentKubernetesVersion := *nodeClass.Status.KubernetesVersion
		imageIDParts := strings.Split(nodeClass.Status.Images[0].ID, "/")
		currentImageVersion := imageIDParts[len(imageIDParts)-1]

		nodeClassObject := lo.Must(runtime.DefaultUnstructuredConverter.ToUnstructured(nodeClass))
		unstructuredNodeClass := &unstructured.Unstructured{Object: nodeClassObject}
		Expect(unstructured.SetNestedField(unstructuredNodeClass.Object, currentKubernetesVersion, "spec", "versions", "kubernetesVersion")).To(Succeed())
		Expect(unstructured.SetNestedField(unstructuredNodeClass.Object, currentImageVersion, "spec", "versions", "nodeImageVersion")).To(Succeed())
		env.ExpectUpdated(unstructuredNodeClass)

		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
			g.Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeKubernetesVersionReady)).To(BeTrue())
			g.Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeImagesReady)).To(BeTrue())
			g.Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady).ObservedGeneration).To(Equal(nodeClass.Generation))
		}).Should(Succeed())

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)

		node := env.GetNode(pods[0].Spec.NodeName)
		Expect(strings.TrimPrefix(node.Status.NodeInfo.KubeletVersion, "v")).To(Equal(currentKubernetesVersion))
	})
})
