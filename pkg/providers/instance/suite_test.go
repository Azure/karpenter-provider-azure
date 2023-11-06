// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package instance_test

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	"github.com/Azure/karpenter/pkg/apis"
	"github.com/Azure/karpenter/pkg/apis/settings"
	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter/pkg/cloudprovider"
	"github.com/Azure/karpenter/pkg/fake"
	"github.com/Azure/karpenter/pkg/test"
	"github.com/aws/karpenter-core/pkg/apis/v1alpha5"
	"github.com/aws/karpenter-core/pkg/events"

	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	corecloudprovider "github.com/aws/karpenter-core/pkg/cloudprovider"
	"github.com/aws/karpenter-core/pkg/operator/scheme"

	. "github.com/aws/karpenter-core/pkg/test/expectations"
	. "knative.dev/pkg/logging/testing"

	coreoptions "github.com/aws/karpenter-core/pkg/operator/options"
	coretest "github.com/aws/karpenter-core/pkg/test"
)

var ctx context.Context
var stop context.CancelFunc
var env *coretest.Environment
var azureEnv *test.Environment
var cloudProvider *cloudprovider.CloudProvider

func TestAzure(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Provider/Azure")
}

var _ = BeforeSuite(func() {
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	// ctx = options.ToContext(ctx, test.Options())
	ctx = settings.ToContext(ctx, test.Settings())

	env = coretest.NewEnvironment(scheme.Scheme, coretest.WithCRDs(apis.CRDs...))

	ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider)
})

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = Describe("InstanceProvider", func() {

	var nodeClass *v1alpha2.AKSNodeClass
	var nodePool *corev1beta1.NodePool
	var nodeClaim *corev1beta1.NodeClaim

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
		nodePool = coretest.NodePool(corev1beta1.NodePool{
			Spec: corev1beta1.NodePoolSpec{
				Template: corev1beta1.NodeClaimTemplate{
					Spec: corev1beta1.NodeClaimSpec{
						NodeClassRef: &corev1beta1.NodeClassReference{
							Name: nodeClass.Name,
						},
					},
				},
			},
		})
		nodeClaim = coretest.NodeClaim(corev1beta1.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					corev1beta1.NodePoolLabelKey: nodePool.Name,
				},
			},
			Spec: corev1beta1.NodeClaimSpec{
				NodeClassRef: &corev1beta1.NodeClassReference{
					Name: nodeClass.Name,
				},
			},
		})
	})

	It("should return an ICE error when all attempted instance types return an ICE error", func() {
		ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)
		zones := []string{"1", "2", "3"}
		for _, zone := range zones {
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", fmt.Sprintf("%s-%s", fake.Region, zone), v1alpha5.CapacityTypeSpot)
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", fmt.Sprintf("%s-%s", fake.Region, zone), v1alpha5.CapacityTypeOnDemand)
		}
		instanceTypes, err := cloudProvider.GetInstanceTypes(ctx, nodePool)
		Expect(err).ToNot(HaveOccurred())

		// Filter down to a single instance type
		instanceTypes = lo.Filter(instanceTypes, func(i *corecloudprovider.InstanceType, _ int) bool { return i.Name == "Standard_D2_v2" })

		// Since all the offerings are unavailable, this should return back an ICE error
		instance, err := azureEnv.InstanceProvider.Create(ctx, nodeClass, nodeClaim, instanceTypes)
		Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
		Expect(instance).To(BeNil())
	})
})
