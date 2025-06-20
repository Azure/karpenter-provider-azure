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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/awslabs/operatorpkg/object"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clock "k8s.io/utils/clock/testing"

	"k8s.io/client-go/tools/record"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"

	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"

	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

var ctx context.Context

// var ctxUseSIG context.Context
var stop context.CancelFunc
var env *coretest.Environment
var azureEnv *test.Environment
var azureEnvNonZonal *test.Environment

// var azureEnvUseSIG *test.Environment
var cloudProvider *cloudprovider.CloudProvider
var cloudProviderNonZonal *cloudprovider.CloudProvider

// var cloudProviderUseSIG *cloudprovider.CloudProvider
var fakeClock *clock.FakeClock
var cluster *state.Cluster
var coreProvisioner *provisioning.Provisioner

func TestAzure(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)

	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, test.Options())
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...))

	ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider)
	cloudProviderNonZonal = cloudprovider.New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider)
	fakeClock = &clock.FakeClock{}
	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	coreProvisioner = provisioning.NewProvisioner(env.Client, events.NewRecorder(&record.FakeRecorder{}), cloudProvider, cluster, fakeClock)
	RunSpecs(t, "Provider/Azure")
}

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = Describe("InstanceProvider", func() {
	var nodeClass *v1beta1.AKSNodeClass
	var nodePool *karpv1.NodePool
	var nodeClaim *karpv1.NodeClaim
	testOptions := options.FromContext(ctx)

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
		test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)

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

		nodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					karpv1.NodePoolLabelKey: nodePool.Name,
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

		azureEnv.Reset()
		azureEnvNonZonal.Reset()
		// azureEnvUseSIG.Reset()
		cluster.Reset()
	})

	var _ = AfterEach(func() {
		ExpectCleanedUp(ctx, env.Client)
	})

	ZonalAndNonZonalRegions := []TableEntry{
		Entry("zonal", azureEnv, cloudProvider),
		Entry("non-zonal", azureEnvNonZonal, cloudProviderNonZonal),
	}

	DescribeTable("should return an ICE error when all attempted instance types return an ICE error",
		func(azEnv *test.Environment, cp *cloudprovider.CloudProvider) {
			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)
			for _, zone := range azEnv.Zones() {
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeSpot)
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeOnDemand)
			}
			instanceTypes, err := cp.GetInstanceTypes(ctx, nodePool)
			Expect(err).ToNot(HaveOccurred())

			// Filter down to a single instance type
			instanceTypes = lo.Filter(instanceTypes, func(i *corecloudprovider.InstanceType, _ int) bool { return i.Name == "Standard_D2_v2" })

			// Since all the offerings are unavailable, this should return back an ICE error
			instance, err := azEnv.InstanceProvider.BeginCreate(ctx, nodeClass, nodeClaim, instanceTypes)
			Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
			Expect(instance).To(BeNil())
		},
		ZonalAndNonZonalRegions,
	)

	When("getting the auxiliary token", func() {
		var originalOptions *options.Options
		var originalEnv *test.Environment
		var originalCloudProvider *cloudprovider.CloudProvider
		var serverToken azcore.AccessToken
		newOptions := test.Options(test.OptionsFields{
			UseSIG: lo.ToPtr(true),
		})
		BeforeEach(func() {
			originalOptions = options.FromContext(ctx)
			originalEnv = azureEnv
			originalCloudProvider = cloudProvider
			ctx = options.ToContext(
				ctx,
				newOptions)
			azureEnv = test.NewEnvironment(ctx, env)
			cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider,
				azureEnv.InstanceProvider,
				events.NewRecorder(&record.FakeRecorder{}),
				env.Client,
				azureEnv.ImageProvider,
			)
			test.ApplyDefaultStatus(nodeClass, env, newOptions.UseSIG)
			serverToken = azureEnv.AuxiliaryTokenServer.Token
		})

		AfterEach(func() {
			ctx = options.ToContext(ctx, originalOptions)
			azureEnv = originalEnv
			cloudProvider = originalCloudProvider
			test.ApplyDefaultStatus(nodeClass, env, originalOptions.UseSIG)
		})
		Context("the token is not cached", func() {
			It("should get a new auxiliary token", func() {
				// first call using vm client should get token
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AuxiliaryTokenServer.AuxiliaryTokenDoBehavior.CalledWithInput.Len()).To(Equal(1))
			})
		})

		Context("token is cached by previous vmClient call", func() {
			BeforeEach(func() {
				azureEnv.VirtualMachinesAPI.AuxiliaryTokenPolicy.Token = serverToken
			})
			It("should use cached auxiliary token when still valid", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AuxiliaryTokenServer.AuxiliaryTokenDoBehavior.CalledWithInput.Len()).To(Equal(0))
				Expect(azureEnv.VirtualMachinesAPI.AuxiliaryTokenPolicy.Token).ToNot(BeNil())
			})

			It("should refresh auxiliary token if about to expire", func() {
				prevExpiresOn := time.Now().Add(4 * time.Minute)
				azureEnv.VirtualMachinesAPI.AuxiliaryTokenPolicy.Token.ExpiresOn = prevExpiresOn
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AuxiliaryTokenServer.AuxiliaryTokenDoBehavior.CalledWithInput.Len()).To(Equal(1))
			})

			It("should refresh auxiliary token if after RefreshOn", func() {
				azureEnv.VirtualMachinesAPI.AuxiliaryTokenPolicy.Token.RefreshOn = time.Now().Add(-1 * time.Second)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AuxiliaryTokenServer.AuxiliaryTokenDoBehavior.CalledWithInput.Len()).To(Equal(1))
			})
		})
	})

	Context("AzureCNI V1", func() {
		var originalOptions *options.Options

		BeforeEach(func() {
			originalOptions = options.FromContext(ctx)
			ctx = options.ToContext(
				ctx,
				test.Options(test.OptionsFields{
					NetworkPlugin:     lo.ToPtr(consts.NetworkPluginAzure),
					NetworkPluginMode: lo.ToPtr(consts.NetworkPluginModeNone),
				}))
		})

		AfterEach(func() {
			ctx = options.ToContext(ctx, originalOptions)
		})
		It("should include 30 secondary ips by default for NodeSubnet", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
			Expect(nic).ToNot(BeNil())
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			Expect(len(nic.Properties.IPConfigurations)).To(Equal(30))
			customData := ExpectDecodedCustomData(azureEnv)
			expectedFlags := map[string]string{
				"max-pods": "30",
			}
			ExpectKubeletFlags(azureEnv, customData, expectedFlags)
		})
		It("should include 1 ip config for Azure CNI Overlay", func() {
			ctx = options.ToContext(
				ctx,
				test.Options(test.OptionsFields{
					NetworkPlugin:     lo.ToPtr(consts.NetworkPluginAzure),
					NetworkPluginMode: lo.ToPtr(consts.NetworkPluginModeOverlay),
				}))

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
			Expect(nic).ToNot(BeNil())
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			// Overlay doesn't rely on secondary ips and instead allocates from a
			// virtual address space.
			Expect(len(nic.Properties.IPConfigurations)).To(Equal(1))
			customData := ExpectDecodedCustomData(azureEnv)
			expectedFlags := map[string]string{
				"max-pods": "250",
			}
			ExpectKubeletFlags(azureEnv, customData, expectedFlags)
		})
		It("should set the number of secondary ips equal to max pods (NodeSubnet)", func() {
			nodeClass.Spec.MaxPods = lo.ToPtr(int32(11))
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
			Expect(nic).ToNot(BeNil())
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			Expect(len(nic.Properties.IPConfigurations)).To(Equal(11))
		})
	})

	It("should create VM and NIC with valid ARM tags", func() {
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

		Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
		Expect(nic).ToNot(BeNil())
		nicTags := nic.Tags
		Expect(lo.FromPtr(nicTags[instance.NodePoolTagKey])).To(Equal(nodePool.Name))
		Expect(lo.PickBy(nicTags, func(key string, value *string) bool {
			return strings.Contains(key, "/") // ARM tags can't contain '/'
		})).To(HaveLen(0))
	})
	It("should list nic from karpenter provisioning request", func() {
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)
		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
		ExpectScheduled(ctx, env.Client, pod)
		interfaces, err := azureEnv.InstanceProvider.ListNics(ctx)
		Expect(err).To(BeNil())
		Expect(len(interfaces)).To(Equal(1))
	})
	It("should only list nics that belong to karpenter", func() {
		managedNic := test.Interface(test.InterfaceOptions{NodepoolName: nodePool.Name})
		unmanagedNic := test.Interface(test.InterfaceOptions{Tags: map[string]*string{"kubernetes.io/cluster/test-cluster": lo.ToPtr("random-aks-vm")}})

		azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(managedNic.ID), *managedNic)
		azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(unmanagedNic.ID), *unmanagedNic)
		interfaces, err := azureEnv.InstanceProvider.ListNics(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(len(interfaces)).To(Equal(1))
		Expect(interfaces[0].Name).To(Equal(managedNic.Name))
	})

	It("should create VM with custom Linux admin username", func() {
		customUsername := "customuser"
		ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
			LinuxAdminUsername: lo.ToPtr(customUsername),
		}))

		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
		ExpectScheduled(ctx, env.Client, pod)

		Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM

		// Verify the custom username was propagated
		Expect(vm.Properties.OSProfile.AdminUsername).ToNot(BeNil())
		Expect(*vm.Properties.OSProfile.AdminUsername).To(Equal(customUsername))

		// Verify SSH key path uses the custom username
		Expect(vm.Properties.OSProfile.LinuxConfiguration).ToNot(BeNil())
		Expect(vm.Properties.OSProfile.LinuxConfiguration.SSH).ToNot(BeNil())
		Expect(vm.Properties.OSProfile.LinuxConfiguration.SSH.PublicKeys).To(HaveLen(1))
		expectedPath := "/home/" + customUsername + "/.ssh/authorized_keys"
		Expect(*vm.Properties.OSProfile.LinuxConfiguration.SSH.PublicKeys[0].Path).To(Equal(expectedPath))
	})

	It("should attach nsg to nic when in BYO VNET mode", func() {
		ctx = options.ToContext(
			ctx,
			test.Options(test.OptionsFields{
				SubnetID: lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/aks-vnet-12345678/subnets/aks-subnet"), // different RG
			}))
		nsg := test.MakeNetworkSecurityGroup(options.FromContext(ctx).NodeResourceGroup, "aks-agentpool-00000000-nsg")
		azureEnv.NetworkSecurityGroupAPI.NSGs.Store(nsg.ID, nsg)

		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
		ExpectScheduled(ctx, env.Client, pod)

		Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
		Expect(nic).ToNot(BeNil())
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		expectedNSGID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkSecurityGroups/aks-agentpool-%s-nsg", azureEnv.SubscriptionID, options.FromContext(ctx).NodeResourceGroup, options.FromContext(ctx).ClusterID)
		Expect(nic.Properties.NetworkSecurityGroup).ToNot(BeNil())
		Expect(lo.FromPtr(nic.Properties.NetworkSecurityGroup.ID)).To(Equal(expectedNSGID))
	})

	It("should attach nsg to nic when NodeClass VNET specified", func() {
		nodeClass.Spec.VNETSubnetID = lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/aks-vnet-12345678/subnets/aks-subnet") // different RG

		nsg := test.MakeNetworkSecurityGroup(options.FromContext(ctx).NodeResourceGroup, "aks-agentpool-00000000-nsg")
		azureEnv.NetworkSecurityGroupAPI.NSGs.Store(nsg.ID, nsg)

		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
		ExpectScheduled(ctx, env.Client, pod)

		Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
		Expect(nic).ToNot(BeNil())
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		expectedNSGID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkSecurityGroups/aks-agentpool-%s-nsg", azureEnv.SubscriptionID, options.FromContext(ctx).NodeResourceGroup, options.FromContext(ctx).ClusterID)
		Expect(nic.Properties.NetworkSecurityGroup).ToNot(BeNil())
		Expect(lo.FromPtr(nic.Properties.NetworkSecurityGroup.ID)).To(Equal(expectedNSGID))
	})
})
