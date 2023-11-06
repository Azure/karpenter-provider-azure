// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package instancetype_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	. "knative.dev/pkg/logging/testing"

	coreoptions "github.com/aws/karpenter-core/pkg/operator/options"

	"github.com/aws/karpenter-core/pkg/apis/v1alpha5"
	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	corecloudprovider "github.com/aws/karpenter-core/pkg/cloudprovider"
	"github.com/aws/karpenter-core/pkg/controllers/provisioning"
	"github.com/aws/karpenter-core/pkg/controllers/state"
	"github.com/aws/karpenter-core/pkg/events"
	"github.com/aws/karpenter-core/pkg/operator/scheme"
	coretest "github.com/aws/karpenter-core/pkg/test"
	. "github.com/aws/karpenter-core/pkg/test/expectations"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/karpenter/pkg/apis"
	"github.com/Azure/karpenter/pkg/apis/settings"
	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter/pkg/cloudprovider"
	"github.com/Azure/karpenter/pkg/fake"
	"github.com/Azure/karpenter/pkg/providers/instancetype"
	"github.com/Azure/karpenter/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter/pkg/test"
	"github.com/Azure/karpenter/pkg/utils"
)

var ctx context.Context
var stop context.CancelFunc
var env *coretest.Environment
var azureEnv *test.Environment
var fakeClock *clock.FakeClock
var coreProvisioner *provisioning.Provisioner
var cluster *state.Cluster
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

	fakeClock = &clock.FakeClock{}
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider)
	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	coreProvisioner = provisioning.NewProvisioner(env.Client, env.KubernetesInterface.CoreV1(), events.NewRecorder(&record.FakeRecorder{}), cloudProvider, cluster)
})

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = Describe("InstanceType Provider", func() {

	var nodeClass *v1alpha2.AKSNodeClass
	var nodePool *corev1beta1.NodePool

	BeforeEach(func() {
		os.Setenv("AZURE_VNET_GUID", "test-vnet-guid")
		os.Setenv("AZURE_VNET_NAME", "aks-vnet-00000000")
		os.Setenv("AZURE_SUBNET_NAME", "test-subnet-name")

		nodeClass = test.AKSNodeClass()
		// Sometimes we use nodeClass without applying it, when simulating the List() call.
		// In that case, we need to set the default values for the node class.
		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](128)
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

		cluster.Reset()
		azureEnv.Reset()
	})

	AfterEach(func() {
		ExpectCleanedUp(ctx, env.Client)
	})

	Context("Filtering in InstanceType Provider List", func() {
		It("should filter out skus that are explicitly marked as restricted", func() {
			instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, &corev1beta1.KubeletConfiguration{}, nodeClass)
			Expect(err).NotTo(HaveOccurred())
			for _, instanceType := range instanceTypes {
				// We should not see any instance types in the restricted list
				Expect(instancetype.RestrictedVMSizes.Has(instanceType.Name)).To(BeFalse())
			}
		})
	})

	Context("Ephemeral Disk", func() {
		It("should use ephemeral disk if supported, and has space of at least 128GB by default", func() {
			// Create a Provisioner that selects a sku that supports ephemeral
			// SKU Standard_D64s_v3 has 1600GB of CacheDisk space, so we expect we can create an ephemeral disk with size 128GB

			np := coretest.NodePool()
			np.Spec.Template.Spec.Requirements = append(np.Spec.Template.Spec.Requirements, v1.NodeSelectorRequirement{
				Key:      "node.kubernetes.io/instance-type",
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D64s_v3"},
			})
			np.Spec.Template.Spec.NodeClassRef = &corev1beta1.NodeClassReference{
				Name: nodeClass.Name,
			}

			ExpectApplied(ctx, env.Client, np, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).NotTo(BeNil())
			Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(128)))
			// should have local disk attached
			Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
			Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option)).To(Equal(armcompute.DiffDiskOptionsLocal))
		})

		It("should use ephemeral disk if supported, and set disk size to OSDiskSizeGB from node class", func() {
			// Create a Provisioner that selects a sku that supports ephemeral
			// SKU Standard_D64s_v3 has 1600GB of CacheDisk space, so we expect we can create an ephemeral disk with size 256GB
			provider := test.AKSNodeClass()
			provider.Spec.OSDiskSizeGB = lo.ToPtr[int32](256)
			np := coretest.NodePool()
			np.Spec.Template.Spec.Requirements = append(np.Spec.Template.Spec.Requirements, v1.NodeSelectorRequirement{
				Key:      "node.kubernetes.io/instance-type",
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D64s_v3"},
			})
			np.Spec.Template.Spec.NodeClassRef = &corev1beta1.NodeClassReference{
				Name: provider.Name,
			}

			ExpectApplied(ctx, env.Client, np, provider)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).NotTo(BeNil())
			Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(256)))
			Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option)).To(Equal(armcompute.DiffDiskOptionsLocal))
		})
		It("if ephemeral is supported, but we don't have enough space, we should not use ephemeral disk", func() {
			// Create a Provisioner that selects a sku that supports ephemeral Standard_D2s_v3
			// Standard_D2s_V3 has 53GB Of CacheDisk space,
			// and has 16GB of Temp Disk Space.
			// With our rule of 100GB being the minimum OSDiskSize, this VM should be created without local disk
			np := coretest.NodePool()
			np.Spec.Template.Spec.Requirements = append(np.Spec.Template.Spec.Requirements, v1.NodeSelectorRequirement{
				Key:      "node.kubernetes.io/instance-type",
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D2s_v3"},
			})
			np.Spec.Template.Spec.NodeClassRef = &corev1beta1.NodeClassReference{
				Name: nodeClass.Name,
			}

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).NotTo(BeNil())
			Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(128)))
			Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).To(BeNil())
		})
	})

	Context("Provisioner with KubeletConfig", func() {
		kubeletConfig := &corev1beta1.KubeletConfiguration{
			PodsPerCore: lo.ToPtr(int32(110)),
			EvictionSoft: map[string]string{
				instancetype.MemoryAvailable: "1Gi",
			},
			EvictionSoftGracePeriod: map[string]metav1.Duration{
				instancetype.MemoryAvailable: {Duration: 10 * time.Second},
			},
			EvictionMaxPodGracePeriod:   lo.ToPtr(int32(15)),
			ImageGCHighThresholdPercent: lo.ToPtr(int32(30)),
			ImageGCLowThresholdPercent:  lo.ToPtr(int32(20)),
			CPUCFSQuota:                 lo.ToPtr(true),
		}

		It("should support provisioning with kubeletConfig, computeResources & maxPods not specified", func() {

			nodePool.Spec.Template.Spec.Kubelet = kubeletConfig
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			customData := *vm.Properties.OSProfile.CustomData
			Expect(customData).ToNot(BeNil())
			decodedBytes, err := base64.StdEncoding.DecodeString(customData)
			Expect(err).To(Succeed())
			decodedString := string(decodedBytes[:])
			kubeletFlags := decodedString[strings.Index(decodedString, "KUBELET_FLAGS=")+len("KUBELET_FLAGS="):]

			Expect(kubeletFlags).To(SatisfyAny(
				ContainSubstring("--system-reserved=cpu=0,memory=0"),
				ContainSubstring("--system-reserved=memory=0,cpu=0"),
			))
			Expect(kubeletFlags).To(SatisfyAny(
				ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
				ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
			))

			Expect(kubeletFlags).To(ContainSubstring("--eviction-hard=memory.available<750Mi"))
			Expect(kubeletFlags).To(ContainSubstring("--eviction-soft=memory.available<1Gi"))
			Expect(kubeletFlags).To(ContainSubstring("--eviction-soft-grace-period=memory.available=10s"))
			Expect(kubeletFlags).To(ContainSubstring("--max-pods=100")) // kubenet
			Expect(kubeletFlags).To(ContainSubstring("--pods-per-core=110"))
			Expect(kubeletFlags).To(ContainSubstring("--image-gc-low-threshold=20"))
			Expect(kubeletFlags).To(ContainSubstring("--image-gc-high-threshold=30"))
			Expect(kubeletFlags).To(ContainSubstring("--cpu-cfs-quota=true"))
		})

		It("should support provisioning with kubeletConfig, computeResources and maxPods specified", func() {
			kubeletConfig.SystemReserved = v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("200m"),
				v1.ResourceMemory: resource.MustParse("1Gi"),
			}
			kubeletConfig.KubeReserved = v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("100m"),
				v1.ResourceMemory: resource.MustParse("500Mi"),
			}
			kubeletConfig.EvictionHard = map[string]string{
				instancetype.MemoryAvailable: "10Mi",
			}
			kubeletConfig.MaxPods = lo.ToPtr(int32(15))

			nodePool.Spec.Template.Spec.Kubelet = kubeletConfig

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			customData := *vm.Properties.OSProfile.CustomData
			Expect(customData).ToNot(BeNil())
			decodedBytes, err := base64.StdEncoding.DecodeString(customData)
			Expect(err).To(Succeed())
			decodedString := string(decodedBytes[:])
			kubeletFlags := decodedString[strings.Index(decodedString, "KUBELET_FLAGS=")+len("KUBELET_FLAGS="):]

			Expect(kubeletFlags).To(SatisfyAny(
				ContainSubstring("--system-reserved=cpu=0,memory=0"),
				ContainSubstring("--system-reserved=memory=0,cpu=0"),
			))
			Expect(kubeletFlags).To(SatisfyAny(
				ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
				ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
			))

			Expect(kubeletFlags).To(ContainSubstring("--eviction-hard=memory.available<750Mi"))
			Expect(kubeletFlags).To(ContainSubstring("--eviction-soft=memory.available<1Gi"))
			Expect(kubeletFlags).To(ContainSubstring("--eviction-soft-grace-period=memory.available=10s"))
			Expect(kubeletFlags).To(ContainSubstring("--max-pods=100")) // kubenet
			Expect(kubeletFlags).To(ContainSubstring("--pods-per-core=110"))
			Expect(kubeletFlags).To(ContainSubstring("--image-gc-low-threshold=20"))
			Expect(kubeletFlags).To(ContainSubstring("--image-gc-high-threshold=30"))
			Expect(kubeletFlags).To(ContainSubstring("--cpu-cfs-quota=true"))
		})
	})

	Context("Provisioner with VnetNodeLabel", func() {
		It("should support provisioning with Vnet node labels", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			customData := *vm.Properties.OSProfile.CustomData
			Expect(customData).ToNot(BeNil())
			decodedBytes, err := base64.StdEncoding.DecodeString(customData)
			Expect(err).To(Succeed())
			decodedString := string(decodedBytes[:])
			Expect(decodedString).To(SatisfyAll(
				ContainSubstring("kubernetes.azure.com/ebpf-dataplane=cilium"),
				ContainSubstring("kubernetes.azure.com/network-name=aks-vnet-00000000"),
				ContainSubstring("kubernetes.azure.com/network-subnet=test-subnet-name"),
				ContainSubstring("kubernetes.azure.com/network-subscription=test-subscription"),
				ContainSubstring("kubernetes.azure.com/nodenetwork-vnetguid=test-vnet-guid"),
				ContainSubstring("kubernetes.azure.com/podnetwork-type=overlay"),
			))
		})
	})

	Context("Unavailable Offerings", func() {
		It("should not allocate a vm in a zone marked as unavailable", func() {
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fmt.Sprintf("%s-1", fake.Region), corev1beta1.CapacityTypeSpot)
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fmt.Sprintf("%s-1", fake.Region), corev1beta1.CapacityTypeOnDemand)
			coretest.ReplaceRequirements(nodePool, v1.NodeSelectorRequirement{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D2_v2"},
			},
			)

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			// Try this 100 times to make sure we don't get a node in eastus-1,
			// we pick from 3 zones so the likelihood of this test passing by chance is 1/3^100
			for i := 0; i < 100; i++ {
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)
				nodes := &v1.NodeList{}
				Expect(env.Client.List(ctx, nodes)).To(Succeed())
				for _, node := range nodes.Items {
					Expect(node.Labels["karpenter.k8s.azure/zone"]).ToNot(Equal(fmt.Sprintf("%s-1", fake.Region)))
					Expect(node.Labels["node.kubernetes.io/instance-type"]).To(Equal("Standard_D2_v2"))

				}
			}

		})

		It("Should not return unavailable offerings", func() {
			zones := []string{"1", "2", "3"}
			for _, zone := range zones {

				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", fmt.Sprintf("%s-%s", fake.Region, zone), corev1beta1.CapacityTypeSpot)
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", fmt.Sprintf("%s-%s", fake.Region, zone), corev1beta1.CapacityTypeOnDemand)
			}
			instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, &corev1beta1.KubeletConfiguration{}, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			seeUnavailable := false
			for _, instanceType := range instanceTypes {
				if instanceType.Name == "Standard_D2_v2" {
					// We want to validate we see the offering in the list,
					// but we also expect it to not have any available offerings
					seeUnavailable = true
					Expect(len(instanceType.Offerings.Available())).To(Equal(0))
				} else {
					Expect(len(instanceType.Offerings.Available())).To(Not(Equal(0)))
				}
			}
			// we should see the unavailable offering in the list
			Expect(seeUnavailable).To(BeTrue())
		})

		It("should launch instances in a different zone than preferred", func() {
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fmt.Sprintf("%s-1", fake.Region), v1alpha5.CapacityTypeOnDemand)
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fmt.Sprintf("%s-1", fake.Region), v1alpha5.CapacityTypeSpot)

			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod(coretest.PodOptions{
				NodeSelector: map[string]string{v1.LabelInstanceTypeStable: "Standard_D2_v2"},
			})
			pod.Spec.Affinity = &v1.Affinity{NodeAffinity: &v1.NodeAffinity{PreferredDuringSchedulingIgnoredDuringExecution: []v1.PreferredSchedulingTerm{
				{
					Weight: 1, Preference: v1.NodeSelectorTerm{MatchExpressions: []v1.NodeSelectorRequirement{
						{Key: v1.LabelTopologyZone, Operator: v1.NodeSelectorOpIn, Values: []string{fmt.Sprintf("%s-1", fake.Region)}},
					}},
				},
			}}}
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels["karpenter.k8s.azure/zone"]).ToNot(Equal(fmt.Sprintf("%s-1", fake.Region)))
			Expect(node.Labels["node.kubernetes.io/instance-type"]).To(Equal("Standard_D2_v2"))
		})
		It("should launch smaller instances than optimal if larger instance launch results in Insufficient Capacity Error", func() {
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_F16s_v2", fmt.Sprintf("%s-1", fake.Region), v1alpha5.CapacityTypeOnDemand)
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_F16s_v2", fmt.Sprintf("%s-1", fake.Region), v1alpha5.CapacityTypeSpot)
			coretest.ReplaceRequirements(nodePool, v1.NodeSelectorRequirement{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_DS2_v2", "Standard_F16s_v2"},
			})
			pods := []*v1.Pod{}
			for i := 0; i < 2; i++ {
				pods = append(pods, coretest.UnschedulablePod(coretest.PodOptions{
					ResourceRequirements: v1.ResourceRequirements{
						Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("1")},
					},
					NodeSelector: map[string]string{
						v1.LabelTopologyZone: fmt.Sprintf("%s-1", fake.Region),
					},
				}))
			}
			// Provisions 2 smaller instances since larger was ICE'd
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pods...)
			nodeNames := sets.New[string]()
			for _, pod := range pods {
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_DS2_v2"))
				nodeNames.Insert(node.Name)
			}
			Expect(nodeNames.Len()).To(Equal(2))
		})
		It("should launch instances on later reconciliation attempt with Insufficient Capacity Error Cache expiry", func() {
			zones := []string{"1", "2", "3"}
			for _, zone := range zones {
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", fmt.Sprintf("%s-%s", fake.Region, zone), v1alpha5.CapacityTypeSpot)
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", fmt.Sprintf("%s-%s", fake.Region, zone), v1alpha5.CapacityTypeOnDemand)
			}

			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod(coretest.PodOptions{
				NodeSelector: map[string]string{v1.LabelInstanceTypeStable: "Standard_D2_v2"},
			})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectNotScheduled(ctx, env.Client, pod)
			// capacity shortage is over - expire the items from the cache and try again
			azureEnv.UnavailableOfferingsCache.Flush()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels).To(HaveKeyWithValue(v1.LabelInstanceTypeStable, "Standard_D2_v2"))
		})
	})
	Context("Provider List", func() {
		var instanceTypes corecloudprovider.InstanceTypes
		var err error

		BeforeEach(func() {
			// disable VM memory overhead for simpler capacity testing
			ctx = settings.ToContext(ctx, test.Settings(test.SettingOptions{
				VMMemoryOverheadPercent: lo.ToPtr[float64](0),
			}))
			instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, &corev1beta1.KubeletConfiguration{}, nodeClass)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should have all the requirements on every sku", func() {
			for _, instanceType := range instanceTypes {
				reqs := instanceType.Requirements

				Expect(reqs.Has(v1.LabelArchStable)).To(BeTrue())
				Expect(reqs.Has(v1.LabelOSStable)).To(BeTrue())
				Expect(reqs.Has(v1.LabelInstanceTypeStable)).To(BeTrue())

				Expect(reqs.Has(v1alpha2.LabelSKUName)).To(BeTrue())

				Expect(reqs.Has(v1alpha2.LabelSKUStoragePremiumCapable)).To(BeTrue())
				Expect(reqs.Has(v1alpha2.LabelSKUEncryptionAtHostSupported)).To(BeTrue())
				Expect(reqs.Has(v1alpha2.LabelSKUAcceleratedNetworking)).To(BeTrue())
				Expect(reqs.Has(v1alpha2.LabelSKUHyperVGeneration)).To(BeTrue())
				Expect(reqs.Has(v1alpha2.LabelSKUStorageEphemeralOSMaxSize)).To(BeTrue())
			}
		})

		It("should have all compute capacity", func() {
			for _, instanceType := range instanceTypes {
				capList := instanceType.Capacity
				Expect(capList).To(HaveKey(v1.ResourceCPU))
				Expect(capList).To(HaveKey(v1.ResourceMemory))
				Expect(capList).To(HaveKey(v1.ResourcePods))
				Expect(capList).To(HaveKey(v1.ResourceEphemeralStorage))
			}
		})

		It("should propagate all values to requirements from skewer", func() {
			var gpuNode *corecloudprovider.InstanceType
			var normalNode *corecloudprovider.InstanceType

			for _, instanceType := range instanceTypes {
				if instanceType.Name == "Standard_D2_v2" {
					normalNode = instanceType
				}
				// #nosec G101
				if instanceType.Name == "Standard_NC24ads_A100_v4" {
					gpuNode = instanceType
				}
			}

			Expect(normalNode.Name).To(Equal("Standard_D2_v2"))
			Expect(gpuNode.Name).To(Equal("Standard_NC24ads_A100_v4"))

			Expect(normalNode.Requirements.Get(v1alpha2.LabelSKUName).Values()).To(ConsistOf("Standard_D2_v2"))
			Expect(gpuNode.Requirements.Get(v1alpha2.LabelSKUName).Values()).To(ConsistOf("Standard_NC24ads_A100_v4"))

			Expect(normalNode.Requirements.Get(v1alpha2.LabelSKUHyperVGeneration).Values()).To(ConsistOf(v1alpha2.HyperVGenerationV1))
			Expect(gpuNode.Requirements.Get(v1alpha2.LabelSKUHyperVGeneration).Values()).To(ConsistOf(v1alpha2.HyperVGenerationV2))

			Expect(gpuNode.Requirements.Get(v1alpha2.LabelSKUAccelerator).Values()).To(ConsistOf("A100"))

			Expect(normalNode.Requirements.Get(v1alpha2.LabelSKUVersion).Values()).To(ConsistOf("2"))
			Expect(gpuNode.Requirements.Get(v1alpha2.LabelSKUVersion).Values()).To(ConsistOf("4"))

			// CPU (requirements and capacity)
			Expect(normalNode.Requirements.Get(v1alpha2.LabelSKUCPU).Values()).To(ConsistOf("2"))
			Expect(normalNode.Capacity.Cpu().Value()).To(Equal(int64(2)))
			Expect(gpuNode.Requirements.Get(v1alpha2.LabelSKUCPU).Values()).To(ConsistOf("24"))
			Expect(gpuNode.Capacity.Cpu().Value()).To(Equal(int64(24)))

			// Memory (requirements and capacity)
			Expect(normalNode.Requirements.Get(v1alpha2.LabelSKUMemory).Values()).To(ConsistOf(fmt.Sprint(7 * 1024))) // 7GiB in MiB
			Expect(normalNode.Capacity.Memory().Value()).To(Equal(int64(7 * 1024 * 1024 * 1024)))                     // 7GiB in bytes
			Expect(gpuNode.Requirements.Get(v1alpha2.LabelSKUMemory).Values()).To(ConsistOf(fmt.Sprint(220 * 1024)))  // 220GiB in MiB
			Expect(gpuNode.Capacity.Memory().Value()).To(Equal(int64(220 * 1024 * 1024 * 1024)))                      // 220GiB in bytes

			// GPU -- Number of GPUs
			gpuQuantity, ok := gpuNode.Capacity["nvidia.com/gpu"]
			Expect(ok).To(BeTrue(), "Expected nvidia.com/gpu to be present in capacity")
			Expect(gpuQuantity.Value()).To(Equal(int64(1)))

			gpuQuanityNonGPU, ok := normalNode.Capacity["nvidia.com/gpu"]
			Expect(ok).To(BeTrue(), "Expected nvidia.com/gpu to be present in capacity, and be zero")
			Expect(gpuQuanityNonGPU.Value()).To(Equal(int64(0)))
		})

		It("should not include SKUs with constrained CPUs, but include unconstrained ones", func() {
			getName := func(instanceType *corecloudprovider.InstanceType) string { return instanceType.Name }
			Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_M8-2ms"))))
			Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D2_v2"))))
		})
	})

	Context("Instance Types", func() {
		It("should support provisioning with no labels", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)
		})
		Context("VM profile", func() {
			It("should have OS disk and network interface set to auto-delete", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Properties).ToNot(BeNil())

				Expect(vm.Properties.StorageProfile).ToNot(BeNil())
				Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())
				osDiskDeleteOption := vm.Properties.StorageProfile.OSDisk.DeleteOption
				Expect(osDiskDeleteOption).ToNot(BeNil())
				Expect(lo.FromPtr(osDiskDeleteOption)).To(Equal(armcompute.DiskDeleteOptionTypesDelete))

				Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())

				for _, nic := range vm.Properties.NetworkProfile.NetworkInterfaces {
					nicDeleteOption := nic.Properties.DeleteOption
					Expect(nicDeleteOption).To(Not(BeNil()))
					Expect(lo.FromPtr(nicDeleteOption)).To(Equal(armcompute.DeleteOptionsDelete))
				}
			})
		})

		It("should have VM identity set", func() {
			ctx = settings.ToContext(
				ctx,
				test.Settings(test.SettingOptions{
					NodeIdentities: []string{
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
					},
				}))

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm.Identity).ToNot(BeNil())

			Expect(lo.FromPtr(vm.Identity.Type)).To(Equal(armcompute.ResourceIdentityTypeUserAssigned))
			Expect(vm.Identity.UserAssignedIdentities).ToNot(BeNil())
			Expect(vm.Identity.UserAssignedIdentities).To(HaveLen(2))
			Expect(vm.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1"))
			Expect(vm.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2"))
		})
	})

	Context("GPU workloads and Nodes", func() {
		It("should schedule non-GPU pod onto the cheapest non-GPU capable node", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm.Properties).ToNot(BeNil())
			Expect(vm.Properties.HardwareProfile).ToNot(BeNil())
			Expect(utils.IsNvidiaEnabledSKU(string(*vm.Properties.HardwareProfile.VMSize))).To(BeFalse())

			clusterNodes := cluster.Nodes()
			node := clusterNodes[0]
			if node.Name() == pod.Spec.NodeName {
				nodeLabels := node.GetLabels()
				Expect(nodeLabels).To(HaveKeyWithValue("karpenter.k8s.azure/sku-gpu-count", "0"))
			}
		})

		It("should schedule GPU pod on GPU capable node", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Name: "samples-tf-mnist-demo",
					Labels: map[string]string{
						"app": "samples-tf-mnist-demo",
					},
				},
				Image: "mcr.microsoft.com/azuredocs/samples-tf-mnist-demo:gpu",
				ResourceRequirements: v1.ResourceRequirements{
					Limits: v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
				RestartPolicy: v1.RestartPolicy("OnFailure"),
				Tolerations: []v1.Toleration{
					{
						Key:      "sku",
						Operator: v1.TolerationOpEqual,
						Value:    "gpu",
						Effect:   v1.TaintEffectNoSchedule,
					},
				},
			})

			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Verify that the node has the GPU label set that the pod was scheduled on
			clusterNodes := cluster.Nodes()
			Expect(clusterNodes).ToNot(BeEmpty())
			Expect(len(clusterNodes)).To(Equal(1))
			node := clusterNodes[0]
			Expect(node.Node.Status.Allocatable).To(HaveKeyWithValue(v1.ResourceName("nvidia.com/gpu"), resource.MustParse("1")))

			if node.Name() == pod.Spec.NodeName {
				nodeLabels := node.GetLabels()

				Expect(nodeLabels).To(HaveKeyWithValue("karpenter.k8s.azure/sku-gpu-name", "A100"))
				Expect(nodeLabels).To(HaveKeyWithValue("karpenter.k8s.azure/sku-gpu-manufacturer", v1alpha2.ManufacturerNvidia))
				Expect(nodeLabels).To(HaveKeyWithValue("karpenter.k8s.azure/sku-gpu-count", "1"))

			}
		})

		Context("Provisioner with KubeletConfig", func() {
			It("Should support provisioning with kubeletConfig, computeResources and maxPods not specified", func() {

				nodePool.Spec.Template.Spec.Kubelet = &corev1beta1.KubeletConfiguration{
					PodsPerCore: lo.ToPtr(int32(110)),
					EvictionSoft: map[string]string{
						instancetype.MemoryAvailable: "1Gi",
					},
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						instancetype.MemoryAvailable: {Duration: 10 * time.Second},
					},
					EvictionMaxPodGracePeriod:   lo.ToPtr(int32(15)),
					ImageGCHighThresholdPercent: lo.ToPtr(int32(30)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(20)),
					CPUCFSQuota:                 lo.ToPtr(true),
				}

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				customData := *vm.Properties.OSProfile.CustomData
				Expect(customData).ToNot(BeNil())
				decodedBytes, err := base64.StdEncoding.DecodeString(customData)
				Expect(err).To(Succeed())
				decodedString := string(decodedBytes[:])
				kubeletFlags := decodedString[strings.Index(decodedString, "KUBELET_FLAGS=")+len("KUBELET_FLAGS="):]

				Expect(kubeletFlags).To(SatisfyAny( // AKS default
					ContainSubstring("--system-reserved=cpu=0,memory=0"),
					ContainSubstring("--system-reserved=memory=0,cpu=0"),
				))
				Expect(kubeletFlags).To(SatisfyAny( // AKS calculation based on cpu and memory
					ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
					ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
				))
				Expect(kubeletFlags).To(ContainSubstring("--eviction-hard=memory.available<750Mi")) // AKS default
				Expect(kubeletFlags).To(ContainSubstring("--eviction-soft=memory.available<1Gi"))
				Expect(kubeletFlags).To(ContainSubstring("--eviction-soft-grace-period=memory.available=10s"))
				Expect(kubeletFlags).To(ContainSubstring("--max-pods=100")) // kubenet
				Expect(kubeletFlags).To(ContainSubstring("--pods-per-core=110"))
				Expect(kubeletFlags).To(ContainSubstring("--image-gc-low-threshold=20"))
				Expect(kubeletFlags).To(ContainSubstring("--image-gc-high-threshold=30"))
				Expect(kubeletFlags).To(ContainSubstring("--cpu-cfs-quota=true"))
			})
			It("Should support provisioning with kubeletConfig, computeResources and maxPods specified", func() {

				nodePool.Spec.Template.Spec.Kubelet = &corev1beta1.KubeletConfiguration{
					PodsPerCore: lo.ToPtr(int32(110)),
					EvictionSoft: map[string]string{
						instancetype.MemoryAvailable: "1Gi",
					},
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						instancetype.MemoryAvailable: {Duration: 10 * time.Second},
					},
					EvictionMaxPodGracePeriod:   lo.ToPtr(int32(15)),
					ImageGCHighThresholdPercent: lo.ToPtr(int32(30)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(20)),
					CPUCFSQuota:                 lo.ToPtr(true),

					SystemReserved: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("200m"),
						v1.ResourceMemory: resource.MustParse("1Gi"),
					},
					KubeReserved: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("100m"),
						v1.ResourceMemory: resource.MustParse("500Mi"),
					},
					EvictionHard: map[string]string{
						instancetype.MemoryAvailable: "10Mi",
					},
					MaxPods: lo.ToPtr(int32(15)),
				}

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				customData := *vm.Properties.OSProfile.CustomData
				Expect(customData).ToNot(BeNil())
				decodedBytes, err := base64.StdEncoding.DecodeString(customData)
				Expect(err).To(Succeed())
				decodedString := string(decodedBytes[:])
				kubeletFlags := decodedString[strings.Index(decodedString, "KUBELET_FLAGS=")+len("KUBELET_FLAGS="):]

				Expect(kubeletFlags).To(SatisfyAny( // AKS default
					ContainSubstring("--system-reserved=cpu=0,memory=0"),
					ContainSubstring("--system-reserved=memory=0,cpu=0"),
				))
				Expect(kubeletFlags).To(SatisfyAny( // AKS calculation based on cpu and memory
					ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
					ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
				))
				Expect(kubeletFlags).To(ContainSubstring("--eviction-hard=memory.available<750Mi")) // AKS default
				Expect(kubeletFlags).To(ContainSubstring("--eviction-soft=memory.available<1Gi"))
				Expect(kubeletFlags).To(ContainSubstring("--eviction-soft-grace-period=memory.available=10s"))
				Expect(kubeletFlags).To(ContainSubstring("--max-pods=100")) // kubenet
				Expect(kubeletFlags).To(ContainSubstring("--pods-per-core=110"))
				Expect(kubeletFlags).To(ContainSubstring("--image-gc-low-threshold=20"))
				Expect(kubeletFlags).To(ContainSubstring("--image-gc-high-threshold=30"))
				Expect(kubeletFlags).To(ContainSubstring("--cpu-cfs-quota=true"))
			})
		})
	})

	Context("LoadBalancer backend pools", func() {
		resourceGroup := "test-resourceGroup"

		It("should include loadbalancer backend pools the allocated VMs", func() {
			standardLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBName, true)
			internalLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.InternalSLBName, false)

			azureEnv.LoadBalancersAPI.LoadBalancers.Store(standardLB.ID, standardLB)
			azureEnv.LoadBalancersAPI.LoadBalancers.Store(internalLB.ID, internalLB)

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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

var _ = Describe("Tax Calculator", func() {
	Context("KubeReservedResources", func() {
		It("4 cores, 7GiB", func() {
			cpus := int64(4) // 4 cores
			memory := 7.0    // 7 GiB
			expectedCPU := "140m"
			expectedMemory := "1638Mi"

			resources := instancetype.KubeReservedResources(cpus, memory)
			gotCPU := resources[v1.ResourceCPU]
			gotMemory := resources[v1.ResourceMemory]

			Expect(gotCPU.String()).To(Equal(expectedCPU))
			Expect(gotMemory.String()).To(Equal(expectedMemory))
		})

		It("2 cores, 8GiB", func() {
			cpus := int64(2) // 2 cores
			memory := 8.0    // 8 GiB
			expectedCPU := "100m"
			expectedMemory := "1843Mi"

			resources := instancetype.KubeReservedResources(cpus, memory)
			gotCPU := resources[v1.ResourceCPU]
			gotMemory := resources[v1.ResourceMemory]

			Expect(gotCPU.String()).To(Equal(expectedCPU))
			Expect(gotMemory.String()).To(Equal(expectedMemory))
		})

		It("3 cores, 64GiB", func() {
			cpus := int64(3) // 3 cores
			memory := 64.0   // 64 GiB
			expectedCPU := "120m"
			expectedMemory := "5611Mi"

			resources := instancetype.KubeReservedResources(cpus, memory)
			gotCPU := resources[v1.ResourceCPU]
			gotMemory := resources[v1.ResourceMemory]

			Expect(gotCPU.String()).To(Equal(expectedCPU))
			Expect(gotMemory.String()).To(Equal(expectedMemory))
		})
	})
})
