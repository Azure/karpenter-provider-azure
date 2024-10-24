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

package termination_test

import (
	"context"
	"testing"
	"time"

	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"

	//used for launch template tests until they are migrated

	"github.com/awslabs/operatorpkg/object"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/termination"
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
var terminationController *termination.Controller

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

	terminationController = termination.NewController(env.Client, events.NewRecorder(&record.FakeRecorder{}))
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

var _ = Describe("NodeClass Termination", func() {
	var nodeClass *v1alpha2.AKSNodeClass
	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
	})

	It("should not delete the AKSNodeClass until all associated NodeClaims are terminated", func() {
		var nodeClaims []*karpv1.NodeClaim
		for i := 0; i < 2; i++ {
			nc := coretest.NodeClaim(karpv1.NodeClaim{
				Spec: karpv1.NodeClaimSpec{
					NodeClassRef: &karpv1.NodeClassReference{
						Group: object.GVK(nodeClass).Group,
						Kind:  object.GVK(nodeClass).Kind,
						Name:  nodeClass.Name,
					},
				},
			})
			ExpectApplied(ctx, env.Client, nc)
			nodeClaims = append(nodeClaims, nc)
		}
		controllerutil.AddFinalizer(nodeClass, v1alpha2.TerminationFinalizer)
		ExpectApplied(ctx, env.Client, nodeClass)
		ExpectObjectReconciled(ctx, env.Client, terminationController, nodeClass)

		Expect(env.Client.Delete(ctx, nodeClass)).To(Succeed())
		res := ExpectObjectReconciled(ctx, env.Client, terminationController, nodeClass)
		Expect(res.RequeueAfter).To(Equal(time.Minute * 10))
		ExpectExists(ctx, env.Client, nodeClass)

		// Delete one of the NodeClaims
		// The NodeClass should still not delete
		ExpectDeleted(ctx, env.Client, nodeClaims[0])
		res = ExpectObjectReconciled(ctx, env.Client, terminationController, nodeClass)
		Expect(res.RequeueAfter).To(Equal(time.Minute * 10))
		ExpectExists(ctx, env.Client, nodeClass)

		// Delete the last NodeClaim
		// The NodeClass should now delete
		ExpectDeleted(ctx, env.Client, nodeClaims[1])
		ExpectObjectReconciled(ctx, env.Client, terminationController, nodeClass)
		ExpectNotFound(ctx, env.Client, nodeClass)
	})
})
