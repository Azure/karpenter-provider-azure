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

package instancetype_test

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	instancetypecontroller "github.com/Azure/karpenter-provider-azure/pkg/controllers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
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
var controller *instancetypecontroller.Controller

func TestController(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "InstanceTypeController")
}

var _ = BeforeSuite(func() {
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, test.Options())
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...))
	azureEnv = test.NewEnvironment(ctx, env)
	controller = instancetypecontroller.NewController(azureEnv.InstanceTypesProvider)
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

var _ = Describe("InstanceType Controller", func() {
	It("should return a requeue interval of 12 hours", func() {
		result := ExpectSingletonReconciled(ctx, controller)
		Expect(result.RequeueAfter).To(Equal(instancetypecontroller.InstanceTypesRefreshInterval))
	})

	It("should List after reconciliation", func() {
		// Flush the cache to simulate a cold start
		azureEnv.InstanceTypesProvider.Reset()

		// Reconcile to populate instance types
		ExpectSingletonReconciled(ctx, controller)

		nodeClass := test.AKSNodeClass()
		instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
		Expect(err).To(BeNil())
		Expect(instanceTypes).NotTo(BeEmpty())
	})

	It("should fail reconciliation when the SKU API returns an error", func() {
		azureEnv.SKUsAPI.Error = fmt.Errorf("simulated SKU API failure")

		err := ExpectSingletonReconcileFailed(ctx, controller)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("fetching SKUs using skewer"))
	})

	It("should update instance types on subsequent reconciliations", func() {
		// First reconcile
		ExpectSingletonReconciled(ctx, controller)

		nodeClass := test.AKSNodeClass()
		instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
		Expect(err).To(BeNil())
		Expect(instanceTypes).NotTo(BeEmpty())
		Expect(instanceTypes).ToNot(ContainElement(HaveField("Name", Equal("Standard_D64s_v6"))))

		// create a copy of the slice so we can revert it to its old state afterwards
		copy := append([]compute.ResourceSku{}, fake.ResourceSkus[fake.Region]...)
		fake.ResourceSkus[fake.Region] = append(fake.ResourceSkus[fake.Region],
			compute.ResourceSku{
				Name:         lo.ToPtr("Standard_D64s_v6"),
				Tier:         lo.ToPtr("Stanadard"),
				Kind:         lo.ToPtr(""),
				Size:         lo.ToPtr("D64s_v6"),
				Family:       lo.ToPtr("standardD64s_v6Family"),
				ResourceType: lo.ToPtr("virtualMachines"),
				APIVersions:  &[]string{},
				Costs:        &[]compute.ResourceSkuCosts{},
				Restrictions: &[]compute.ResourceSkuRestrictions{},
				Capabilities: &[]compute.ResourceSkuCapabilities{
					{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("64")},
					{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("64")},
					{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("x64")},
					{Name: lo.ToPtr("vCPUsAvailable"), Value: lo.ToPtr("64")},
				},
				Locations:    &[]string{"southcentralus"},
				LocationInfo: &[]compute.ResourceSkuLocationInfo{{Location: lo.ToPtr("southcentralus"), Zones: &[]string{}}},
			},
		)
		defer func() {
			fake.ResourceSkus[fake.Region] = copy
		}()

		// Second reconcile should succeed and have new cached data
		ExpectSingletonReconciled(ctx, controller)
		instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
		Expect(err).To(BeNil())
		Expect(instanceTypes).NotTo(BeEmpty())
		Expect(instanceTypes).To(ContainElement(HaveField("Name", Equal("Standard_D64s_v6"))))
	})
})
