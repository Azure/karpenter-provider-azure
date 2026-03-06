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

package cloudprovider

// This file contains VM-specific (AKSScriptless mode) end-to-end tests that
// provision VMs and inspect bootstrap script generation, customData contents,
// and VM-specific configurations. These tests exercise the full CloudProvider
// orchestration flow and validate VM implementation details.

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
)

var _ = Describe("CloudProvider - VM Bootstrap (AKSScriptless Mode)", func() {
	// These tests verify VM-specific bootstrap script generation and customData inspection.
	// They are end-to-end tests that provision nodes via CloudProvider and validate the
	// bootstrap configuration in the resulting VMs.

	Context("ProvisionMode = BootstrappingClient", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode: lo.ToPtr(consts.ProvisionModeBootstrappingClient),
			})
			ctx = options.ToContext(ctx, testOptions)
			azureEnv = test.NewEnvironment(ctx, env)
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)

			// Setup NSG for VM mode
			nsg := test.MakeNetworkSecurityGroup(options.FromContext(ctx).NodeResourceGroup, fmt.Sprintf("aks-agentpool-%s-nsg", options.FromContext(ctx).ClusterID))
			azureEnv.NetworkSecurityGroupAPI.NSGs.Store(nsg.ID, nsg)
		})

		AfterEach(func() {
			cloudProvider.WaitForInstancePromises()
			cluster.Reset()
			azureEnv.Reset()
		})

		It("should provision the node and CSE", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectCSEProvisioned(azureEnv)
			ExpectScheduled(ctx, env.Client, pod)
		})

		It("should not reattempt creation of a vm thats been created before, and also not CSE", func() {
			nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"karpenter.sh/nodepool": nodePool.Name},
				},
				Spec: karpv1.NodeClaimSpec{NodeClassRef: &karpv1.NodeClassReference{Name: nodeClass.Name}},
			})
			vmName := instance.GenerateResourceName(nodeClaim.Name)
			vm := &armcompute.VirtualMachine{
				Name:     lo.ToPtr(vmName),
				ID:       lo.ToPtr(fake.MkVMID(options.FromContext(ctx).NodeResourceGroup, vmName)),
				Location: lo.ToPtr(fake.Region),
				Zones:    []*string{lo.ToPtr("fantasy-zone")},
				Properties: &armcompute.VirtualMachineProperties{
					TimeCreated: lo.ToPtr(time.Now()),
					HardwareProfile: &armcompute.HardwareProfile{
						VMSize: lo.ToPtr(armcompute.VirtualMachineSizeTypesBasicA3),
					},
				},
			}
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			_, err := cloudProvider.Create(ctx, nodeClaim)
			Expect(err).ToNot(HaveOccurred())

			ExpectCSENotProvisioned(azureEnv)
		})
	})

	Context("ProvisionMode = AKSScriptless", func() {
		BeforeEach(func() { setupVMMode() })
		AfterEach(func() { teardownProvisionMode() })

		Context("Subnet & CNI Configuration", func() {
			It("should use the VNET_SUBNET_ID", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
				nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(nic).NotTo(BeNil())
				Expect(lo.FromPtr(nic.Interface.Properties.IPConfigurations[0].Properties.Subnet.ID)).To(Equal("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/aks-vnet-12345678/subnets/aks-subnet"))
			})

			It("should produce all required azure cni labels", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				decodedString := ExpectDecodedCustomData(azureEnv)
				Expect(decodedString).To(SatisfyAll(
					ContainSubstring("kubernetes.azure.com/ebpf-dataplane=cilium"),
					ContainSubstring("kubernetes.azure.com/network-subnet=aks-subnet"),
					ContainSubstring("kubernetes.azure.com/nodenetwork-vnetguid=a519e60a-cac0-40b2-b883-084477fe6f5c"),
					ContainSubstring("kubernetes.azure.com/podnetwork-type=overlay"),
					ContainSubstring("kubernetes.azure.com/azure-cni-overlay=true"),
				))
			})

			It("should include stateless CNI label for kubernetes 1.34+ set to true", func() {
				nodeClass.Status.KubernetesVersion = lo.ToPtr("1.34.0")
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				decodedString := ExpectDecodedCustomData(azureEnv)
				Expect(decodedString).To(ContainSubstring("kubernetes.azure.com/network-stateless-cni=true"))
			})

			It("should include stateless CNI label for kubernetes < 1.34 set to false", func() {
				nodeClass.Status.KubernetesVersion = lo.ToPtr("1.33.0")
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
				decodedString := ExpectDecodedCustomData(azureEnv)
				Expect(decodedString).To(ContainSubstring("kubernetes.azure.com/network-stateless-cni=false"))
			})

			It("should not include cilium or azure cni vnet labels when using kubenet", func() {
				originalOptions := options.FromContext(ctx)
				ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
					NetworkPlugin: lo.ToPtr("kubenet"),
				}))
				defer func() { ctx = options.ToContext(ctx, originalOptions) }()

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
				decodedString := ExpectDecodedCustomData(azureEnv)
				Expect(decodedString).ToNot(ContainSubstring("kubernetes.azure.com/ebpf-dataplane=cilium"))
				Expect(decodedString).ToNot(ContainSubstring("kubernetes.azure.com/azure-cni-overlay=true"))
			})
		})

		Context("VM Creation Failures", func() {
			It("should delete the network interface on failure to create the vm", func() {
				ErrMsg := "test error"
				ErrCode := fmt.Sprint(http.StatusNotFound)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{
						ErrorCode: ErrCode,
						RawResponse: &http.Response{
							Body: createVMSDKErrorBody(ErrCode, ErrMsg),
						},
					},
				)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(nic).NotTo(BeNil())
				_, ok := azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Load(nic.Interface.ID)
				Expect(ok).To(BeFalse())

				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
				pod = coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
			})
		})

		Context("Custom DNS", func() {
			It("should support provisioning with custom DNS server from options", func() {
				ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
					ClusterDNSServiceIP: lo.ToPtr("10.244.0.1"),
				}))

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				customData := ExpectDecodedCustomData(azureEnv)
				ExpectKubeletFlags(azureEnv, customData, map[string]string{
					"cluster-dns": "10.244.0.1",
				})
			})
		})

		Context("Community Image Gallery (CIG)", func() {
			var kubernetesVersion string
			var expectUseAzureLinux3 bool
			var azureLinuxGen2ImageDefinition string
			var azureLinuxGen1ImageDefinition string
			var azureLinuxGen2ArmImageDefinition string

			BeforeEach(func() {
				kubernetesVersion = lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
				expectUseAzureLinux3 = imagefamily.UseAzureLinux3(kubernetesVersion)
				azureLinuxGen2ImageDefinition = lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ImageDefinition, imagefamily.AzureLinuxGen2ImageDefinition)
				azureLinuxGen1ImageDefinition = lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen1ImageDefinition, imagefamily.AzureLinuxGen1ImageDefinition)
				azureLinuxGen2ArmImageDefinition = lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ArmImageDefinition, imagefamily.AzureLinuxGen2ArmImageDefinition)
			})

			DescribeTable("should select the right Community Image Gallery image for a given instance type",
				func(instanceType string, imgFamily string, expectedImageDefinition string, expectedGalleryURL string) {
					localStatusController := status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID)
					if expectUseAzureLinux3 && expectedImageDefinition == azureLinuxGen2ArmImageDefinition {
						Skip("AzureLinux3 ARM64 VHD is not available in CIG")
					}
					nodeClass.Spec.ImageFamily = lo.ToPtr(imgFamily)
					coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
						NodeSelectorRequirement: v1.NodeSelectorRequirement{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{instanceType},
						}})
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					ExpectObjectReconciled(ctx, env.Client, localStatusController, nodeClass)
					pod := coretest.UnschedulablePod(coretest.PodOptions{})
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
					Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
					Expect(vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID).ToNot(BeNil())
					parts := strings.Split(*vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID, "/")
					Expect(parts[2]).To(Equal(expectedGalleryURL))
					Expect(parts[4]).To(Equal(expectedImageDefinition))

					cluster.Reset()
					azureEnv.Reset()
				},
				Entry("Gen2 instance type with AKSUbuntu image family",
					"Standard_D2_v5", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ImageDefinition, imagefamily.AKSUbuntuPublicGalleryURL),
				Entry("Gen1 instance type with AKSUbuntu image family",
					"Standard_D2_v3", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen1ImageDefinition, imagefamily.AKSUbuntuPublicGalleryURL),
				Entry("ARM instance type with AKSUbuntu image family",
					"Standard_D16plds_v5", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ArmImageDefinition, imagefamily.AKSUbuntuPublicGalleryURL),
				Entry("Gen2 instance type with AzureLinux image family",
					"Standard_D2_v5", v1beta1.AzureLinuxImageFamily, azureLinuxGen2ImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
				Entry("Gen1 instance type with AzureLinux image family",
					"Standard_D2_v3", v1beta1.AzureLinuxImageFamily, azureLinuxGen1ImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
				Entry("ARM instance type with AzureLinux image family",
					"Standard_D16plds_v5", v1beta1.AzureLinuxImageFamily, azureLinuxGen2ArmImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
			)
		})

		Context("VM Profile", func() {
			It("should have OS disk and network interface set to auto-delete", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Properties).ToNot(BeNil())
				Expect(vm.Properties.StorageProfile).ToNot(BeNil())
				Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())
				osDiskDeleteOption := vm.Properties.StorageProfile.OSDisk.DeleteOption
				Expect(osDiskDeleteOption).ToNot(BeNil())
				Expect(lo.FromPtr(osDiskDeleteOption)).To(Equal(armcompute.DiskDeleteOptionTypesDelete))

				for _, nic := range vm.Properties.NetworkProfile.NetworkInterfaces {
					nicDeleteOption := nic.Properties.DeleteOption
					Expect(nicDeleteOption).To(Not(BeNil()))
					Expect(lo.FromPtr(nicDeleteOption)).To(Equal(armcompute.DeleteOptionsDelete))
				}
			})

			It("should not create unneeded secondary ips for azure cni with overlay", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Properties).ToNot(BeNil())
				Expect(len(vm.Properties.NetworkProfile.NetworkInterfaces)).To(Equal(1))
				Expect(lo.FromPtr(vm.Properties.NetworkProfile.NetworkInterfaces[0].Properties.Primary)).To(BeTrue())

				Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
				Expect(nic.Properties).ToNot(BeNil())
				Expect(len(nic.Properties.IPConfigurations)).To(Equal(1))
			})
		})

		Context("Bootstrap Script", func() {
			var (
				kubeletFlags          string
				customData            string
				minorVersion          uint64
				credentialProviderURL string
			)
			BeforeEach(func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
				customData = ExpectDecodedCustomData(azureEnv)
				kubeletFlags = ExpectKubeletFlagsPassed(customData)

				k8sVersion, err := azureEnv.KubernetesVersionProvider.KubeServerVersion(ctx)
				Expect(err).To(BeNil())
				minorVersion = semver.MustParse(k8sVersion).Minor
				credentialProviderURL = bootstrap.CredentialProviderURL(k8sVersion, "amd64")
			})

			It("should include or exclude --keep-terminated-pod-volumes based on kubelet version", func() {
				if minorVersion < 31 {
					Expect(kubeletFlags).To(ContainSubstring("--keep-terminated-pod-volumes"))
				} else {
					Expect(kubeletFlags).ToNot(ContainSubstring("--keep-terminated-pod-volumes"))
				}
			})

			It("should include correct flags and credential provider URL when CredentialProviderURL is not empty", func() {
				if credentialProviderURL != "" {
					Expect(kubeletFlags).ToNot(ContainSubstring("--azure-container-registry-config"))
					Expect(kubeletFlags).To(ContainSubstring("--image-credential-provider-config=/var/lib/kubelet/credential-provider-config.yaml"))
					Expect(kubeletFlags).To(ContainSubstring("--image-credential-provider-bin-dir=/var/lib/kubelet/credential-provider"))
					Expect(customData).To(ContainSubstring(credentialProviderURL))
				}
			})

			It("should include correct flags when CredentialProviderURL is empty", func() {
				if credentialProviderURL == "" {
					Expect(kubeletFlags).To(ContainSubstring("--azure-container-registry-config"))
					Expect(kubeletFlags).ToNot(ContainSubstring("--image-credential-provider-config"))
					Expect(kubeletFlags).ToNot(ContainSubstring("--image-credential-provider-bin-dir"))
				}
			})

			It("should include karpenter.sh/unregistered taint", func() {
				Expect(kubeletFlags).To(ContainSubstring("--register-with-taints=" + karpv1.UnregisteredNoExecuteTaint.ToString()))
			})
		})

		DescribeTable("Azure CNI node labels and agentbaker network plugin", func(
			networkPlugin, networkPluginMode, networkDataplane, expectedAgentBakerNetPlugin string,
			expectedNodeLabels sets.Set[string]) {
			options := test.Options(test.OptionsFields{
				NetworkPlugin:     lo.ToPtr(networkPlugin),
				NetworkPluginMode: lo.ToPtr(networkPluginMode),
				NetworkDataplane:  lo.ToPtr(networkDataplane),
			})
			ctx = options.ToContext(ctx)

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)
			customData := ExpectDecodedCustomData(azureEnv)

			Expect(customData).To(ContainSubstring(fmt.Sprintf("NETWORK_PLUGIN=%s", expectedAgentBakerNetPlugin)))

			for label := range expectedNodeLabels {
				Expect(customData).To(ContainSubstring(label))
			}
		},
			Entry("Azure CNI V1",
				"azure", "", "",
				"azure", sets.New[string]()),
			Entry("Azure CNI w Overlay",
				"azure", "overlay", "",
				"none",
				sets.New(
					"kubernetes.azure.com/azure-cni-overlay=true",
					"kubernetes.azure.com/network-subnet=aks-subnet",
					"kubernetes.azure.com/nodenetwork-vnetguid=a519e60a-cac0-40b2-b883-084477fe6f5c",
					"kubernetes.azure.com/podnetwork-type=overlay",
				)),
			Entry("Network Plugin none",
				"none", "", "", "none",
				sets.New[string]()),
			Entry("Azure CNI w Overlay w Cilium",
				"azure", "overlay", "cilium",
				"none",
				sets.New(
					"kubernetes.azure.com/azure-cni-overlay=true",
					"kubernetes.azure.com/network-subnet=aks-subnet",
					"kubernetes.azure.com/nodenetwork-vnetguid=a519e60a-cac0-40b2-b883-084477fe6f5c",
					"kubernetes.azure.com/podnetwork-type=overlay",
					"kubernetes.azure.com/ebpf-dataplane=cilium",
				)),
			Entry("Cilium w feature flag Microsoft.ContainerService/EnableCiliumNodeSubnet",
				"azure", "", "cilium",
				"none",
				sets.New("kubernetes.azure.com/ebpf-dataplane=cilium")),
		)

		Context("LoadBalancer", func() {
			resourceGroup := "test-resourceGroup"

			It("should include loadbalancer backend pools for allocated VMs", func() {
				standardLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBName, true)
				internalLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.InternalSLBName, false)

				azureEnv.LoadBalancersAPI.LoadBalancers.Store(standardLB.ID, standardLB)
				azureEnv.LoadBalancersAPI.LoadBalancers.Store(internalLB.ID, internalLB)

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				iface := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface

				Expect(iface.Properties.IPConfigurations).ToNot(BeEmpty())
				Expect(lo.FromPtr(iface.Properties.IPConfigurations[0].Properties.Primary)).To(Equal(true))

				backendPools := iface.Properties.IPConfigurations[0].Properties.LoadBalancerBackendAddressPools
				Expect(backendPools).To(HaveLen(3))
				Expect(lo.FromPtr(backendPools[0].ID)).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-resourceGroup/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/kubernetes"))
				Expect(lo.FromPtr(backendPools[1].ID)).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-resourceGroup/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/aksOutboundBackendPool"))
				Expect(lo.FromPtr(backendPools[2].ID)).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-resourceGroup/providers/Microsoft.Network/loadBalancers/kubernetes-internal/backendAddressPools/kubernetes"))
			})
		})
	})
})

// Helper functions for VM bootstrap tests

func ExpectKubeletFlagsPassed(customData string) string {
	GinkgoHelper()
	return customData[strings.Index(customData, "KUBELET_FLAGS=")+len("KUBELET_FLAGS=") : strings.Index(customData, "KUBELET_NODE_LABELS")]
}

func ExpectKubeletNodeLabelsPassed(customData string) string {
	GinkgoHelper()
	startIdx := strings.Index(customData, "KUBELET_NODE_LABELS=") + len("KUBELET_NODE_LABELS=")
	endIdx := strings.Index(customData[startIdx:], "\n")
	if endIdx == -1 {
		// If no newline found, take to the end
		return customData[startIdx:]
	}
	return customData[startIdx : startIdx+endIdx]
}

func ExpectKubeletNodeLabelsInCustomData(vm *armcompute.VirtualMachine, key string, value string) {
	GinkgoHelper()

	Expect(vm.Properties).ToNot(BeNil())
	Expect(vm.Properties.OSProfile).ToNot(BeNil())
	Expect(vm.Properties.OSProfile.CustomData).ToNot(BeNil())

	customData := *vm.Properties.OSProfile.CustomData
	Expect(customData).ToNot(BeNil())

	decodedBytes, err := base64.StdEncoding.DecodeString(customData)
	Expect(err).To(Succeed())
	decodedString := string(decodedBytes[:])

	// Extract and check KUBELET_NODE_LABELS contains the expected label
	kubeletNodeLabels := ExpectKubeletNodeLabelsPassed(decodedString)
	Expect(kubeletNodeLabels).To(ContainSubstring(fmt.Sprintf("%s=%s", key, value)))
}

func ExpectKubeletNodeLabelsNotInCustomData(vm *armcompute.VirtualMachine, key string, value string) {
	GinkgoHelper()

	Expect(vm.Properties).ToNot(BeNil())
	Expect(vm.Properties.OSProfile).ToNot(BeNil())
	Expect(vm.Properties.OSProfile.CustomData).ToNot(BeNil())

	customData := *vm.Properties.OSProfile.CustomData
	Expect(customData).ToNot(BeNil())

	decodedBytes, err := base64.StdEncoding.DecodeString(customData)
	Expect(err).To(Succeed())
	decodedString := string(decodedBytes[:])

	// Extract and check KUBELET_NODE_LABELS contains the expected label
	kubeletNodeLabels := ExpectKubeletNodeLabelsPassed(decodedString)
	Expect(kubeletNodeLabels).ToNot(ContainSubstring(fmt.Sprintf("%s=%s", key, value)))
}
