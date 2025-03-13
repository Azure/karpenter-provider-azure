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

package status_test

import (
	"fmt"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/test"

	corev1 "k8s.io/api/core/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
)

func getExpectedTestCommunityImages() []v1alpha2.NodeImage {
	return []v1alpha2.NodeImage{
		{
			ID: "/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2containerd/versions/",
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      "kubernetes.io/arch",
					Operator: "In",
					Values:   []string{"amd64"},
				},
				{
					Key:      "karpenter.azure.com/sku-hyperv-generation",
					Operator: "In",
					Values:   []string{"2"},
				},
			},
		},
		{
			ID: "/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204containerd/versions/",
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      "kubernetes.io/arch",
					Operator: "In",
					Values:   []string{"amd64"},
				},
				{
					Key:      "karpenter.azure.com/sku-hyperv-generation",
					Operator: "In",
					Values:   []string{"1"},
				},
			},
		},
		{
			ID: "/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2arm64containerd/versions/",
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      "kubernetes.io/arch",
					Operator: "In",
					Values:   []string{"arm64"},
				},
				{
					Key:      "karpenter.azure.com/sku-hyperv-generation",
					Operator: "In",
					Values:   []string{"2"},
				},
			},
		},
	}
}

func getTestCommunityOlderImages() []v1alpha2.NodeImage {
	images := getExpectedTestCommunityImages()
	for _, image := range images {
		image.ID = fmt.Sprintf("%s/versions/%s", status.TrimVersionSuffix(image.ID), "202410.09.0")
	}
	return images
}

var _ = Describe("NodeClass NodeImage Status Controller", func() {
	var nodeClass *v1alpha2.AKSNodeClass
	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
	})

	It("should init NodeImages and its readiness on AKSNodeClass", func() {
		ExpectApplied(ctx, env.Client, nodeClass)
		ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(len(nodeClass.Status.NodeImages)).To(Equal(3))
		Expect(nodeClass.Status.NodeImages).To(ContainElements(getExpectedTestCommunityImages()))
		Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeNodeImageReady)).To(BeTrue())
	})

	It("should update NodeImages and its readiness on AKSNodeClass when in an open MW", func() {
		// TODO: once MW support is added we need to actually add test code here causing it to be open.
		nodeClass.Status.NodeImages = getTestCommunityOlderImages()
		nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeNodeImageReady)

		ExpectApplied(ctx, env.Client, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(nodeClass.Status.NodeImages).To(ContainElements(getTestCommunityOlderImages()))
		Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeNodeImageReady)).To(BeTrue())

		ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(len(nodeClass.Status.NodeImages)).To(Equal(3))
		Expect(nodeClass.Status.NodeImages).To(ContainElements(getExpectedTestCommunityImages()))
		Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeNodeImageReady)).To(BeTrue())
	})

	// TODO: Handle test cases where MW is not open, but other update conditions trigger an update, once MWs are supported.
})
