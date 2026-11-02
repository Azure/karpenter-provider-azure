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

package test

import (
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestGeneratedNodeClaimNames(t *testing.T) {
	g := NewWithT(t)
	names := map[string]bool{}
	for range 1000 {
		nodeClaim := NodeClaim()
		g.Expect(len(nodeClaim.Name)).To(BeNumerically("<=", 34))
		g.Expect(names).ToNot(HaveKey(nodeClaim.Name))
		names[nodeClaim.Name] = true

		nodePool := NodePoolWithShortName()
		// Provisioning appends a hyphen and Kubernetes' five-character generated suffix.
		g.Expect(len(nodePool.Name) + 6).To(BeNumerically("<=", 34))
		g.Expect(names).ToNot(HaveKey(nodePool.Name))
		names[nodePool.Name] = true
	}
}

func TestShortNameFactoriesPreserveOverrides(t *testing.T) {
	g := NewWithT(t)
	nodeClaim := NodeClaim(karpv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "first", Labels: map[string]string{"custom": "value"}},
	}, karpv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "second"},
	})
	g.Expect(nodeClaim.Name).To(Equal("second"))
	g.Expect(nodeClaim.Labels).To(HaveKeyWithValue("custom", "value"))
	g.Expect(nodeClaim.Spec.NodeClassRef).ToNot(BeNil())

	nodePool := NodePoolWithShortName(karpv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "first", Labels: map[string]string{"custom": "value"}},
	}, karpv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "second"},
	})
	g.Expect(nodePool.Name).To(Equal("second"))
	g.Expect(nodePool.Labels).To(HaveKeyWithValue("custom", "value"))
	g.Expect(nodePool.Spec.Template.Spec.NodeClassRef).ToNot(BeNil())
}
