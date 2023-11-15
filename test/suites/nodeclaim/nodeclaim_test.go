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

package nodeclaim_test

import (
	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"github.com/aws/karpenter-core/pkg/test"
	"github.com/aws/karpenter-core/pkg/utils/resources"
)

var _ = Describe("StandaloneNodeClaim", func() {
	It("should create a standard NodeClaim within the 'D' sku family", func() {
		nodeClaim := test.NodeClaim(corev1beta1.NodeClaim{
			Spec: corev1beta1.NodeClaimSpec{
				Requirements: []v1.NodeSelectorRequirement{
					{
						Key:      v1alpha2.LabelSKUFamily,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"D"},
					},
					{
						Key:      corev1beta1.CapacityTypeLabelKey,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{corev1beta1.CapacityTypeOnDemand},
					},
				},
				NodeClassRef: &corev1beta1.NodeClassReference{
					Name: nodeClass.Name,
				},
			},
		})
		env.ExpectCreated(nodeClass, nodeClaim)
		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		nodeClaim = env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]
		Expect(node.Labels).To(HaveKeyWithValue(v1alpha2.LabelSKUFamily, "D"))
		env.EventuallyExpectNodeClaimsReady(nodeClaim)
	})
	It("should create a standard NodeClaim based on resource requests", func() {
		nodeClaim := test.NodeClaim(corev1beta1.NodeClaim{
			Spec: corev1beta1.NodeClaimSpec{
				Resources: corev1beta1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("1"),
						v1.ResourceMemory: resource.MustParse("16Gi"),
					},
				},
				NodeClassRef: &corev1beta1.NodeClassReference{
					Name: nodeClass.Name,
				},
			},
		})
		env.ExpectCreated(nodeClass, nodeClaim)
		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		nodeClaim = env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]
		Expect(resources.Fits(nodeClaim.Spec.Resources.Requests, node.Status.Allocatable))
		env.EventuallyExpectNodeClaimsReady(nodeClaim)
	})
	It("should create a NodeClaim propagating all the NodeClaim spec details", func() {
		nodeClaim := test.NodeClaim(corev1beta1.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"custom-annotation": "custom-value",
				},
				Labels: map[string]string{
					"custom-label": "custom-value",
				},
			},
			Spec: corev1beta1.NodeClaimSpec{
				Taints: []v1.Taint{
					{
						Key:    "custom-taint",
						Effect: v1.TaintEffectNoSchedule,
						Value:  "custom-value",
					},
					{
						Key:    "other-custom-taint",
						Effect: v1.TaintEffectNoExecute,
						Value:  "other-custom-value",
					},
				},
				NodeClassRef: &corev1beta1.NodeClassReference{
					Name: nodeClass.Name,
				},
			},
		})
		env.ExpectCreated(nodeClass, nodeClaim)
		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		Expect(node.Annotations).To(HaveKeyWithValue("custom-annotation", "custom-value"))
		Expect(node.Labels).To(HaveKeyWithValue("custom-label", "custom-value"))
		Expect(node.Spec.Taints).To(ContainElements(
			v1.Taint{
				Key:    "custom-taint",
				Effect: v1.TaintEffectNoSchedule,
				Value:  "custom-value",
			},
			v1.Taint{
				Key:    "other-custom-taint",
				Effect: v1.TaintEffectNoExecute,
				Value:  "other-custom-value",
			},
		))
		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
		env.EventuallyExpectNodeClaimsReady(nodeClaim)
	})
	It("should remove the cloudProvider NodeClaim when the cluster NodeClaim is deleted", func() {
		nodeClaim := test.NodeClaim(corev1beta1.NodeClaim{
			Spec: corev1beta1.NodeClaimSpec{
				Requirements: []v1.NodeSelectorRequirement{
					{
						Key:      v1alpha2.LabelSKUFamily,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"D"},
					},
					{
						Key:      corev1beta1.CapacityTypeLabelKey,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{corev1beta1.CapacityTypeOnDemand},
					},
				},
				NodeClassRef: &corev1beta1.NodeClassReference{
					Name: nodeClass.Name,
				},
			},
		})
		env.ExpectCreated(nodeClass, nodeClaim)
		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		nodeClaim = env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]

		// Node is deleted and now should be not found
		env.ExpectDeleted(nodeClaim)
		env.EventuallyExpectNotFound(nodeClaim, node)
	})
})
