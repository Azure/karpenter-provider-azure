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
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
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
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...))

	ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider)
	cloudProviderNonZonal = cloudprovider.New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider)
	fakeClock = &clock.FakeClock{}
	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	coreProvisioner = provisioning.NewProvisioner(env.Client, events.NewRecorder(&record.FakeRecorder{}), cloudProvider, cluster, fakeClock)
	RunSpecs(t, "Provider/Azure")
}

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = Describe("VMInstanceProvider", func() {
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
			instance, err := azEnv.VMInstanceProvider.BeginCreate(ctx, nodeClass, nodeClaim, instanceTypes)
			Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
			Expect(instance).To(BeNil())
		},
		ZonalAndNonZonalRegions,
	)

	When("getting the auxiliary token", func() {
		var originalOptions *options.Options
		var originalEnv *test.Environment
		var originalCloudProvider *cloudprovider.CloudProvider
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
				azureEnv.VMInstanceProvider,
				events.NewRecorder(&record.FakeRecorder{}),
				env.Client,
				azureEnv.ImageProvider,
			)
			test.ApplyDefaultStatus(nodeClass, env, newOptions.UseSIG)
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
				Expect(azureEnv.AuxiliaryTokenServer.AuxiliaryTokenDoBehavior.CalledWithInput.Len()).To(Equal(1)) // init token
			})
		})

		Context("token is cached by previous vmClient call", func() {
			BeforeEach(func() {
				_ = azureEnv.VirtualMachinesAPI.UseAuxiliaryTokenPolicy()
			})
			It("should use cached auxiliary token when still valid", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AuxiliaryTokenServer.AuxiliaryTokenDoBehavior.CalledWithInput.Len()).To(Equal(1)) // init token
				Expect(azureEnv.VirtualMachinesAPI.AuxiliaryTokenPolicy.Token).ToNot(BeNil())
			})

			It("should refresh auxiliary token if about to expire", func() {
				azureEnv.VirtualMachinesAPI.AuxiliaryTokenPolicy.Token.ExpiresOn = time.Now().Add(4 * time.Minute)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AuxiliaryTokenServer.AuxiliaryTokenDoBehavior.CalledWithInput.Len()).To(Equal(2)) // init + refresh token
			})

			It("should refresh auxiliary token if after RefreshOn", func() {
				azureEnv.VirtualMachinesAPI.AuxiliaryTokenPolicy.Token.RefreshOn = time.Now().Add(-1 * time.Second)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AuxiliaryTokenServer.AuxiliaryTokenDoBehavior.CalledWithInput.Len()).To(Equal(2)) // init + refresh token
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
		vm, err := azureEnv.VMInstanceProvider.Get(ctx, vmName)
		Expect(err).To(BeNil())
		tags := vm.Tags
		Expect(lo.FromPtr(tags[launchtemplate.NodePoolTagKey])).To(Equal(nodePool.Name))
		Expect(lo.PickBy(tags, func(key string, value *string) bool {
			return strings.Contains(key, "/") // ARM tags can't contain '/'
		})).To(HaveLen(0))

		Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
		Expect(nic).ToNot(BeNil())
		nicTags := nic.Tags
		Expect(lo.FromPtr(nicTags[launchtemplate.NodePoolTagKey])).To(Equal(nodePool.Name))
		Expect(lo.PickBy(nicTags, func(key string, value *string) bool {
			return strings.Contains(key, "/") // ARM tags can't contain '/'
		})).To(HaveLen(0))
	})

	It("should not allow the user to override Karpenter-managed tags", func() {
		nodeClass.Spec.Tags = map[string]string{
			"karpenter.azure.com/cluster": "my-override-cluster",
			"karpenter.sh/nodepool":       "my-override-nodepool",
		}
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
		ExpectScheduled(ctx, env.Client, pod)

		Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		vmName := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
		vm, err := azureEnv.VMInstanceProvider.Get(ctx, vmName)
		Expect(err).To(BeNil())
		tags := vm.Tags
		Expect(lo.FromPtr(tags[launchtemplate.NodePoolTagKey])).To(Equal(nodePool.Name))
		Expect(lo.FromPtr(tags[launchtemplate.KarpenterManagedTagKey])).To(Equal(testOptions.ClusterName))

		Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
		Expect(nic).ToNot(BeNil())
		nicTags := nic.Tags
		Expect(lo.FromPtr(nicTags[launchtemplate.NodePoolTagKey])).To(Equal(nodePool.Name))
		Expect(lo.FromPtr(nicTags[launchtemplate.KarpenterManagedTagKey])).To(Equal(testOptions.ClusterName))
	})

	It("should list nic from karpenter provisioning request", func() {
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)
		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
		ExpectScheduled(ctx, env.Client, pod)
		interfaces, err := azureEnv.VMInstanceProvider.ListNics(ctx)
		Expect(err).To(BeNil())
		Expect(len(interfaces)).To(Equal(1))
	})
	It("should only list nics that belong to karpenter", func() {
		managedNic := test.Interface(test.InterfaceOptions{NodepoolName: nodePool.Name})
		unmanagedNic := test.Interface(test.InterfaceOptions{Tags: map[string]*string{"kubernetes.io/cluster/test-cluster": lo.ToPtr("random-aks-vm")}})

		azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(managedNic.ID), *managedNic)
		azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(unmanagedNic.ID), *unmanagedNic)
		interfaces, err := azureEnv.VMInstanceProvider.ListNics(ctx)
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

	Context("Update", func() {
		It("should update only VM when no tags are included", func() {
			// Ensure that the VM already exists in the fake environment
			vmName := nodeClaim.Name
			vm := armcompute.VirtualMachine{
				ID:   lo.ToPtr(fake.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
				Name: lo.ToPtr(vmName),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}

			azureEnv.VirtualMachinesAPI.Instances.Store(*vm.ID, vm)

			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)

			// Update the VM identities
			err := azureEnv.VMInstanceProvider.Update(ctx, vmName, armcompute.VirtualMachineUpdate{
				Identity: &armcompute.VirtualMachineIdentity{
					UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.ManagedIdentity/userAssignedIdentities/aks-agentpool-00000000-identity": {},
					},
				},
			})
			Expect(err).ToNot(HaveOccurred())

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			update := azureEnv.VirtualMachinesAPI.VirtualMachineUpdateBehavior.CalledWithInput.Pop().Updates
			Expect(update).ToNot(BeNil())
			Expect(update.Identity).ToNot(BeNil())
			Expect(update.Identity.UserAssignedIdentities).To(HaveLen(1))

			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesUpdateTagsBehavior.CalledWithInput.Len()).To(Equal(0))
		})

		It("should update only VM, NIC, and Extensions when tags are included", func() {
			// Ensure that the VM already exists in the fake environment
			vmName := nodeClaim.Name
			vm := armcompute.VirtualMachine{
				ID:   lo.ToPtr(fake.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
				Name: lo.ToPtr(vmName),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			// Ensure that the NIC already exists in the fake environment
			azureEnv.VirtualMachinesAPI.Instances.Store(*vm.ID, vm)
			nic := armnetwork.Interface{
				ID:   lo.ToPtr(fake.MakeNetworkInterfaceID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
				Name: lo.ToPtr(vmName),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(*nic.ID, nic)

			// Ensure that the two VM extensions already exist in the fake environment
			billingExt := armcompute.VirtualMachineExtension{
				ID:   lo.ToPtr(fake.MakeVMExtensionID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName, "computeAksLinuxBilling")),
				Name: lo.ToPtr("computeAksLinuxBilling"),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			cseExt := armcompute.VirtualMachineExtension{
				ID:   lo.ToPtr(fake.MakeVMExtensionID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName, "cse-agent-karpenter")),
				Name: lo.ToPtr("cse-agent-karpenter"),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(*billingExt.ID, billingExt)
			azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(*cseExt.ID, cseExt)

			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)

			// Update the VM tags
			err := azureEnv.VMInstanceProvider.Update(ctx, vmName, armcompute.VirtualMachineUpdate{
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
					"test-tag":                    lo.ToPtr("test-value"),
				},
			})
			Expect(err).ToNot(HaveOccurred())

			ExpectInstanceResourcesHaveTags(ctx, vmName, azureEnv, map[string]*string{
				"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				"test-tag":                    lo.ToPtr("test-value"),
			})
		})

		It("should ignore NotFound errors for computeAksLinuxBilling extension update", func() {
			// Ensure that the VM already exists in the fake environment
			vmName := nodeClaim.Name
			vm := armcompute.VirtualMachine{
				ID:   lo.ToPtr(fake.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
				Name: lo.ToPtr(vmName),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			// Ensure that the NIC already exists in the fake environment
			azureEnv.VirtualMachinesAPI.Instances.Store(*vm.ID, vm)
			nic := armnetwork.Interface{
				ID:   lo.ToPtr(fake.MakeNetworkInterfaceID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
				Name: lo.ToPtr(vmName),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(*nic.ID, nic)

			// Ensure that only one extension exists in the env
			cseExt := armcompute.VirtualMachineExtension{
				ID:   lo.ToPtr(fake.MakeVMExtensionID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName, "cse-agent-karpenter")),
				Name: lo.ToPtr("cse-agent-karpenter"),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(*cseExt.ID, cseExt)
			// TODO: This only works because this extension happens to be first in the list of extensions. If it were second it wouldn't work
			azureEnv.VirtualMachineExtensionsAPI.VirtualMachineExtensionsUpdateBehavior.BeginError.Set(&azcore.ResponseError{StatusCode: http.StatusNotFound}, fake.MaxCalls(1))

			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)

			// Update the VM tags
			err := azureEnv.VMInstanceProvider.Update(ctx, vmName, armcompute.VirtualMachineUpdate{
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
					"test-tag":                    lo.ToPtr("test-value"),
				},
			})
			Expect(err).ToNot(HaveOccurred())

			ExpectInstanceResourcesHaveTags(ctx, vmName, azureEnv, map[string]*string{
				"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				"test-tag":                    lo.ToPtr("test-value"),
			})
		})
	})

	Context("EncryptionAtHost", func() {
		It("should create VM with EncryptionAtHost enabled when specified in AKSNodeClass", func() {
			if nodeClass.Spec.Security == nil {
				nodeClass.Spec.Security = &v1beta1.Security{}
			}
			nodeClass.Spec.Security.EncryptionAtHost = lo.ToPtr(true)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM

			Expect(vm.Properties.SecurityProfile).ToNot(BeNil())
			Expect(vm.Properties.SecurityProfile.EncryptionAtHost).ToNot(BeNil())
			Expect(lo.FromPtr(vm.Properties.SecurityProfile.EncryptionAtHost)).To(BeTrue())
		})

		It("should create VM with EncryptionAtHost disabled when specified in AKSNodeClass", func() {
			if nodeClass.Spec.Security == nil {
				nodeClass.Spec.Security = &v1beta1.Security{}
			}
			nodeClass.Spec.Security.EncryptionAtHost = lo.ToPtr(false)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM

			Expect(vm.Properties.SecurityProfile).ToNot(BeNil())
			Expect(vm.Properties.SecurityProfile.EncryptionAtHost).ToNot(BeNil())
			Expect(lo.FromPtr(vm.Properties.SecurityProfile.EncryptionAtHost)).To(BeFalse())
		})

		It("should create VM without SecurityProfile when EncryptionAtHost is not specified in AKSNodeClass", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM

			Expect(vm.Properties.SecurityProfile).To(BeNil())
		})
	})
})
