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

package remediation_test

import (
	"context"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/controllers/node/remediation"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/karpenter/pkg/apis"
	v1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/operator/options"
	"sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"
)

var ctx context.Context
var remediationController *remediation.Controller
var env *test.Environment

func TestAPIs(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Remediation")
}

var _ = BeforeSuite(func() {
	env = test.NewEnvironment(test.WithCRDs(apis.CRDs...), test.WithCRDs(v1alpha1.CRDs...))
	ctx = options.ToContext(ctx, test.Options())

	remediationController = remediation.NewController(env.Client)
})

var _ = AfterSuite(func() {
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = AfterEach(func() {
	ExpectCleanedUp(ctx, env.Client)
})

var _ = Describe("Remediation", func() {
	DescribeTable(
		"Azure CNI Overlay Label",
		func(isManaged bool, hadLabel bool) {

			labels := map[string]string{}
			if hadLabel {
				labels["kubernetes.azure.com/azure-cni-overlay"] = "true"
			}
			if isManaged {
				labels[v1.NodePoolLabelKey] = "default"
			}

			node := test.Node(test.NodeOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
			})

			ExpectApplied(ctx, env.Client, node)
			ExpectObjectReconciled(ctx, env.Client, remediationController, node)

			node = ExpectExists(ctx, env.Client, node)
			value, ok := node.Labels["kubernetes.azure.com/azure-cni-overlay"]

			Expect(ok).To(Equal(isManaged || hadLabel))
			if isManaged || hadLabel {
				Expect(value).To(Equal("true"))
			}
		},
		Entry("should add  label to   managed node without label", true, false),
		Entry("should keep label on   managed node with    label", true, true),
		Entry("should keep label on unmanaged node with    label", false, true),
		Entry("should ignore        unmanaged node without label", false, false),
	)
})
