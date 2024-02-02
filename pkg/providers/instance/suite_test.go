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

package instance_test

import (
	"context"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clock "k8s.io/utils/clock/testing"

	"k8s.io/client-go/tools/record"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/aws/karpenter-core/pkg/apis/v1alpha5"
	"github.com/aws/karpenter-core/pkg/controllers/provisioning"
	"github.com/aws/karpenter-core/pkg/controllers/state"
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
var azureEnvNonZonal *test.Environment
var cloudProvider *cloudprovider.CloudProvider
var cloudProviderNonZonal *cloudprovider.CloudProvider
var fakeClock *clock.FakeClock
var cluster *state.Cluster
var coreProvisioner *provisioning.Provisioner

func TestAzure(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)

	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, test.Options())
	env = coretest.NewEnvironment(scheme.Scheme, coretest.WithCRDs(apis.CRDs...))

	ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider)
	cloudProviderNonZonal = cloudprovider.New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider)
	fakeClock = &clock.FakeClock{}
	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	coreProvisioner = provisioning.NewProvisioner(env.Client, env.KubernetesInterface.CoreV1(), events.NewRecorder(&record.FakeRecorder{}), cloudProvider, cluster)
	RunSpecs(t, "Provider/Azure")
}

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
		azureEnv.Reset()
		azureEnvNonZonal.Reset()
	})

	var _ = AfterEach(func() {
		ExpectCleanedUp(ctx, env.Client)
	})

	var ZonalAndNonZonalRegions = []TableEntry{
		Entry("zonal", azureEnv, cloudProvider),
		Entry("non-zonal", azureEnvNonZonal, cloudProviderNonZonal),
	}

	DescribeTable("should return an ICE error when all attempted instance types return an ICE error",
		func(azEnv *test.Environment, cp *cloudprovider.CloudProvider) {
			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)
			for _, zone := range azEnv.Zones() {
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, v1alpha5.CapacityTypeSpot)
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, v1alpha5.CapacityTypeOnDemand)
			}
			instanceTypes, err := cp.GetInstanceTypes(ctx, nodePool)
			Expect(err).ToNot(HaveOccurred())

			// Filter down to a single instance type
			instanceTypes = lo.Filter(instanceTypes, func(i *corecloudprovider.InstanceType, _ int) bool { return i.Name == "Standard_D2_v2" })

			// Since all the offerings are unavailable, this should return back an ICE error
			instance, err := azEnv.InstanceProvider.Create(ctx, nodeClass, nodeClaim, instanceTypes)
			Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
			Expect(instance).To(BeNil())
		},
		ZonalAndNonZonalRegions,
	)

	It("should create VM with valid ARM tags", func() {
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)
		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
		ExpectScheduled(ctx, env.Client, pod)
		Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		vmName := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
		vm, err := azureEnv.InstanceProvider.Get(ctx, vmName)
		Expect(err).To(BeNil())
		tags := vm.Tags
		Expect(lo.FromPtr(tags[instance.NodePoolTagKey])).To(Equal(nodePool.Name))
		Expect(lo.PickBy(tags, func(key string, value *string) bool {
			return strings.Contains(key, "/") // ARM tags can't contain '/'
		})).To(HaveLen(0))
	})
})
