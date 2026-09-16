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

package v1alpha2_test

import (
	"context"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
)

var ctx context.Context
var env *coretest.Environment
var azureEnv *test.Environment

func TestAPIs(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Validation")
}

func TestV1Alpha2IsUnserved(t *testing.T) {
	g := NewWithT(t)
	found := false
	for _, crd := range apis.CRDs {
		for i := range crd.Spec.Versions {
			if crd.Spec.Versions[i].Name == "v1alpha2" {
				found = true
				g.Expect(crd.Spec.Versions[i].Served).To(BeFalse())
			}
		}
	}
	g.Expect(found).To(BeTrue())
}

var _ = BeforeSuite(func() {
	ctx = options.ToContext(ctx, test.Options())
	env = coretest.NewEnvironment(coretest.WithCRDs(servedCRDsForLegacyTests()...), coretest.WithCRDs(v1alpha1.CRDs...))
	azureEnv = test.NewEnvironment(ctx, env)
})

func servedCRDsForLegacyTests() []*apiextensionsv1.CustomResourceDefinition {
	crds := make([]*apiextensionsv1.CustomResourceDefinition, 0, len(apis.CRDs))
	for _, crd := range apis.CRDs {
		crd = crd.DeepCopy()
		for i := range crd.Spec.Versions {
			if crd.Spec.Versions[i].Name == "v1alpha2" {
				crd.Spec.Versions[i].Served = true
			}
		}
		crds = append(crds, crd)
	}
	return crds
}

var _ = AfterEach(func() {
	ExpectCleanedUp(ctx, env.Client)
})

var _ = AfterSuite(func() {
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})
