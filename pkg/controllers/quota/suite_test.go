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

package quota_test

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	quotacontroller "github.com/Azure/karpenter-provider-azure/pkg/controllers/quota"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"
)

var ctx context.Context
var env *coretest.Environment
var azureEnv *test.Environment
var controller *quotacontroller.Controller

func TestController(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "QuotaController")
}

var _ = BeforeSuite(func() {
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, test.Options())
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...))
	azureEnv = test.NewEnvironment(ctx, env)
	controller = quotacontroller.NewController(azureEnv.QuotaProvider)
})

var _ = AfterSuite(func() {
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = BeforeEach(func() {
	azureEnv.Reset()
})

var _ = AfterEach(func() {
	ExpectCleanedUp(ctx, env.Client)
})

var _ = Describe("Quota Controller", func() {
	It("should return a requeue interval of 10 minutes", func() {
		result := ExpectSingletonReconciled(ctx, controller)
		Expect(result.RequeueAfter).To(Equal(quotacontroller.RefreshInterval))
	})

	It("should update quota data after reconciliation", func() {
		azureEnv.UsageAPI.Usages.Append(
			&armcompute.Usage{
				Name:         &armcompute.UsageName{Value: lo.ToPtr("standardBSFamily"), LocalizedValue: lo.ToPtr("Standard BS Family vCPUs")},
				CurrentValue: lo.ToPtr[int32](10),
				Limit:        lo.ToPtr[int64](100),
				Unit:         lo.ToPtr("Count"),
			},
			&armcompute.Usage{
				Name:         &armcompute.UsageName{Value: lo.ToPtr("cores"), LocalizedValue: lo.ToPtr("Total Regional vCPUs")},
				CurrentValue: lo.ToPtr[int32](50),
				Limit:        lo.ToPtr[int64](500),
				Unit:         lo.ToPtr("Count"),
			},
		)

		ExpectSingletonReconciled(ctx, controller)

		found, usage := azureEnv.QuotaProvider.GetUsage("standardBSFamily")
		Expect(found).To(BeTrue())
		Expect(*usage.CurrentValue).To(Equal(int32(10)))
		Expect(*usage.Limit).To(Equal(int64(100)))

		found, usage = azureEnv.QuotaProvider.GetTotalRegionalUsage()
		Expect(found).To(BeTrue())
		Expect(*usage.CurrentValue).To(Equal(int32(50)))
	})

	It("should fail reconciliation when the usage API returns an error", func() {
		azureEnv.UsageAPI.Error = fmt.Errorf("simulated usage API failure")

		err := ExpectSingletonReconcileFailed(ctx, controller)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("simulated usage API failure"))
	})
})
