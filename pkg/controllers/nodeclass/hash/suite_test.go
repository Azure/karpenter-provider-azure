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

package hash_test

import (
	"context"
	"testing"

	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"

	"github.com/awslabs/operatorpkg/object"
	"github.com/imdario/mergo"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/hash"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"
)

var ctx context.Context
var env *coretest.Environment
var awsEnv *test.Environment
var hashController *hash.Controller

func TestAPIs(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "AKSNodeClass")
}

var _ = BeforeSuite(func() {
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...), coretest.WithFieldIndexers(test.AKSNodeClassFieldIndexer(ctx)))
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, test.Options())
	awsEnv = test.NewEnvironment(ctx, env)

	hashController = hash.NewController(env.Client)
})

var _ = AfterSuite(func() {
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = BeforeEach(func() {
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	awsEnv.Reset()
})

var _ = AfterEach(func() {
	ExpectCleanedUp(ctx, env.Client)
})

var _ = Describe("NodeClass Hash Controller", func() {
	var nodeClass *v1beta1.AKSNodeClass
	var nodePool *karpv1.NodePool
	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
		nodePool = coretest.NodePool(karpv1.NodePool{
			Spec: karpv1.NodePoolSpec{
				Template: karpv1.NodeClaimTemplate{
					Spec: karpv1.NodeClaimTemplateSpec{
						NodeClassRef: &karpv1.NodeClassReference{
							Group: object.GVK(nodeClass).Group,
							Kind:  object.GVK(nodeClass).Kind,
							Name:  nodeClass.Name,
						},
					},
				},
			},
		})
	})
	DescribeTable("should update the drift hash when static field is updated", func(changes *v1beta1.AKSNodeClass) {
		ExpectApplied(ctx, env.Client, nodeClass)
		ExpectObjectReconciled(ctx, env.Client, hashController, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		expectedHash := nodeClass.Hash()
		Expect(nodeClass.ObjectMeta.Annotations[v1beta1.AnnotationAKSNodeClassHash]).To(Equal(expectedHash))

		Expect(mergo.Merge(nodeClass, changes, mergo.WithOverride)).To(Succeed())

		ExpectApplied(ctx, env.Client, nodeClass)
		ExpectObjectReconciled(ctx, env.Client, hashController, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		expectedHashTwo := nodeClass.Hash()
		Expect(nodeClass.Annotations[v1beta1.AnnotationAKSNodeClassHash]).To(Equal(expectedHashTwo))
		Expect(expectedHash).ToNot(Equal(expectedHashTwo))

	},
		Entry("ImageFamily Drift", &v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{ImageFamily: lo.ToPtr("AzureLinux")}}),
		Entry("OSDiskSizeGB Drift", &v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{OSDiskSizeGB: lo.ToPtr(int32(30))}}),
	)
	It("should update aksnodeclass-hash-version annotation when the aksnodeclass-hash-version on the NodeClass does not match with the controller hash version", func() {
		nodeClass.Annotations = map[string]string{
			v1beta1.AnnotationAKSNodeClassHash:        "abceduefed",
			v1beta1.AnnotationAKSNodeClassHashVersion: "test",
		}
		ExpectApplied(ctx, env.Client, nodeClass)

		ExpectObjectReconciled(ctx, env.Client, hashController, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		expectedHash := nodeClass.Hash()
		// Expect aksnodeclass-hash on the NodeClass to be updated
		Expect(nodeClass.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSNodeClassHash, expectedHash))
		Expect(nodeClass.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSNodeClassHashVersion, v1beta1.AKSNodeClassHashVersion))
	})
	It("should update aksnodeclass-hash-versions on all NodeClaims when the aksnodeclass-hash-version does not match with the controller hash version", func() {
		nodeClass.Annotations = map[string]string{
			v1beta1.AnnotationAKSNodeClassHash:        "abceduefed",
			v1beta1.AnnotationAKSNodeClassHashVersion: "test",
		}
		nodeClaimOne := coretest.NodeClaim(karpv1.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
				Annotations: map[string]string{
					v1beta1.AnnotationAKSNodeClassHash:        "123456",
					v1beta1.AnnotationAKSNodeClassHashVersion: "test",
				},
			},
			Spec: karpv1.NodeClaimSpec{
				NodeClassRef: &karpv1.NodeClassReference{
					Group: object.GVK(nodeClass).Group,
					Kind:  object.GVK(nodeClass).Kind,
					Name:  nodeClass.Name,
				},
			},
		})
		nodeClaimTwo := coretest.NodeClaim(karpv1.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
				Annotations: map[string]string{
					v1beta1.AnnotationAKSNodeClassHash:        "123456",
					v1beta1.AnnotationAKSNodeClassHashVersion: "test",
				},
			},
			Spec: karpv1.NodeClaimSpec{
				NodeClassRef: &karpv1.NodeClassReference{
					Group: object.GVK(nodeClass).Group,
					Kind:  object.GVK(nodeClass).Kind,
					Name:  nodeClass.Name,
				},
			},
		})

		ExpectApplied(ctx, env.Client, nodeClass, nodeClaimOne, nodeClaimTwo, nodePool)

		ExpectObjectReconciled(ctx, env.Client, hashController, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)
		nodeClaimOne = ExpectExists(ctx, env.Client, nodeClaimOne)
		nodeClaimTwo = ExpectExists(ctx, env.Client, nodeClaimTwo)

		expectedHash := nodeClass.Hash()
		// Expect aksnodeclass-hash on the NodeClaims to be updated
		Expect(nodeClaimOne.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSNodeClassHash, expectedHash))
		Expect(nodeClaimOne.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSNodeClassHashVersion, v1beta1.AKSNodeClassHashVersion))
		Expect(nodeClaimTwo.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSNodeClassHash, expectedHash))
		Expect(nodeClaimTwo.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSNodeClassHashVersion, v1beta1.AKSNodeClassHashVersion))
	})
	It("should not update aksnodeclass-hash on all NodeClaims when the aksnodeclass-hash-version matches the controller hash version", func() {
		nodeClass.Annotations = map[string]string{
			v1beta1.AnnotationAKSNodeClassHash:        "abceduefed",
			v1beta1.AnnotationAKSNodeClassHashVersion: "test-version",
		}
		nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
				Annotations: map[string]string{
					v1beta1.AnnotationAKSNodeClassHash:        "1234564654",
					v1beta1.AnnotationAKSNodeClassHashVersion: v1beta1.AKSNodeClassHashVersion,
				},
			},
			Spec: karpv1.NodeClaimSpec{
				NodeClassRef: &karpv1.NodeClassReference{
					Group: object.GVK(nodeClass).Group,
					Kind:  object.GVK(nodeClass).Kind,
					Name:  nodeClass.Name,
				},
			},
		})
		ExpectApplied(ctx, env.Client, nodeClass, nodeClaim, nodePool)

		ExpectObjectReconciled(ctx, env.Client, hashController, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)
		nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)

		expectedHash := nodeClass.Hash()

		// Expect aksnodeclass-hash on the NodeClass to be updated
		Expect(nodeClass.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSNodeClassHash, expectedHash))
		Expect(nodeClass.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSNodeClassHashVersion, v1beta1.AKSNodeClassHashVersion))
		// Expect aksnodeclass-hash on the NodeClaims to stay the same
		Expect(nodeClaim.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSNodeClassHash, "1234564654"))
		Expect(nodeClaim.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSNodeClassHashVersion, v1beta1.AKSNodeClassHashVersion))
	})
	It("should not update aksnodeclass-hash on the NodeClaim if it's drifted and the aksnodeclass-hash-version does not match the controller hash version", func() {
		nodeClass.Annotations = map[string]string{
			v1beta1.AnnotationAKSNodeClassHash:        "abceduefed",
			v1beta1.AnnotationAKSNodeClassHashVersion: "test",
		}
		nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
				Annotations: map[string]string{
					v1beta1.AnnotationAKSNodeClassHash:        "123456",
					v1beta1.AnnotationAKSNodeClassHashVersion: "test",
				},
			},
			Spec: karpv1.NodeClaimSpec{
				NodeClassRef: &karpv1.NodeClassReference{
					Group: object.GVK(nodeClass).Group,
					Kind:  object.GVK(nodeClass).Kind,
					Name:  nodeClass.Name,
				},
			},
		})
		nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeDrifted)
		ExpectApplied(ctx, env.Client, nodeClass, nodeClaim, nodePool)

		ExpectObjectReconciled(ctx, env.Client, hashController, nodeClass)
		nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)

		// Expect aksnodeclass-hash on the NodeClaims to stay the same
		Expect(nodeClaim.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSNodeClassHash, "123456"))
		Expect(nodeClaim.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSNodeClassHashVersion, v1beta1.AKSNodeClassHashVersion))
	})
})
