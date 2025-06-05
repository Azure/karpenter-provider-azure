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

package sku_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
)

var env *azure.Environment
var nodeClass *v1beta1.AKSNodeClass
var nodePool *karpv1.NodePool
var ctx context.Context

// Static list of SKUs for testing
var testSKUs = []string{
	"Standard_A2_v2",       // A-series
	"Standard_B2s",         // B-series
	"Standard_D2s_v3",      // D-series
	"Standard_E2s_v3",      // E-series
	"Standard_F2s_v2",      // F-series
	"Standard_G2",          // G-series
	"Standard_H8",          // H-series
	"Standard_L4s",         // L-series
	"Standard_M8-2ms",      // M-series
	"Standard_NC4as_T4_v3", // NC-series
	"Standard_NV4as_v4",    // NV-series
}

func TestSKU(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
		ctx = env.Context
	})
	AfterSuite(func() {
		env.Stop()
	})
	RunSpecs(t, "SKU")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	// Ensure the AKSNodeClass is created in the cluster
	env.ExpectCreated(nodeClass)
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("SKU Scaling", func() {
	It("should scale up and down with different SKUs from each family", func() {
		// Verify that we have SKUs to test
		Expect(testSKUs).ToNot(BeEmpty(), "No SKUs found to test")

		for _, skuName := range testSKUs {
			// Create a NodePool with specific SKU requirement
			skuNodePool := test.NodePool(karpv1.NodePool{
				Spec: karpv1.NodePoolSpec{
					Template: karpv1.NodeClaimTemplate{
						Spec: karpv1.NodeClaimTemplateSpec{
							NodeClassRef: &karpv1.NodeClassReference{
								Name:  nodeClass.Name,
								Kind:  "AKSNodeClass",
								Group: "karpenter.azure.com",
							},
							Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
								{
									NodeSelectorRequirement: corev1.NodeSelectorRequirement{
										Key:      v1beta1.LabelSKUName,
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{skuName},
									},
								},
							},
						},
					},
				},
			})

			// Create a pod that requires the specific SKU
			pod := test.Pod(test.PodOptions{
				NodeSelector: map[string]string{
					v1beta1.LabelSKUName: skuName,
				},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("1"),
					},
				},
			})

			// Apply the NodePool and pod
			env.ExpectCreated(skuNodePool, pod)

			// Wait for pod to be scheduled
			env.EventuallyExpectHealthy(pod)

			// Verify the node was created with correct SKU
			node := env.ExpectCreatedNodeCount("==", 1)[0]
			Expect(node.Labels[v1beta1.LabelSKUName]).To(Equal(skuName))

			// Clean up
			env.ExpectDeleted(pod, skuNodePool)
			env.EventuallyExpectNotFound(pod, skuNodePool)
			env.EventuallyExpectNotFound(node)
		}
	})
})
