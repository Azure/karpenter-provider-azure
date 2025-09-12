package byok_test

import (
	"context"
	"testing"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *azure.Environment

func TestBYOK(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	AfterSuite(func() {
		env.Stop()
	})

	RunSpecs(t, "BYOK Suite")
}

var _ = BeforeEach(func() { env.BeforeEach() })
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("BYOK", func() {
	BeforeEach(func() {
		if !env.InClusterController {
			Skip("replacing the env vars is only supported for in cluster controller")
		}
	})
	It("should provision a VM with customer-managed key disk encryption", func() {
		ctx := context.Background()
		diskEncryptionSetID := env.CreateKeyVaultAndDiskEncryptionSet(ctx)
		env.ExpectSettingsOverridden(corev1.EnvVar{Name: "NODE_OSDISK_DISKENCRYPTIONSET_ID", Value: diskEncryptionSetID})

		nodeClass := env.DefaultAKSNodeClass()
		nodePool := env.DefaultNodePool(nodeClass)

		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)

		vm := env.GetVM(pod.Spec.NodeName)
		Expect(vm.Properties).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).ToNot(BeNil())
		Expect(*vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).To(Equal(diskEncryptionSetID))
	})

	It("should provision a VM with ephemeral OS disk and customer-managed key disk encryption", func() {
		ctx := context.Background()

		diskEncryptionSetID := env.CreateKeyVaultAndDiskEncryptionSet(ctx)
		env.ExpectSettingsOverridden(corev1.EnvVar{Name: "NODE_OSDISK_DISKENCRYPTIONSET_ID", Value: diskEncryptionSetID})
		nodeClass := env.DefaultAKSNodeClass()
		nodePool := env.DefaultNodePool(nodeClass)

		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
				Operator: corev1.NodeSelectorOpGt,
				Values:   []string{"50"},
			}})

		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](50)

		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)

		vm := env.GetVM(pod.Spec.NodeName)
		Expect(vm.Properties).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())

		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option).ToNot(BeNil())
		Expect(string(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option))).To(Equal("Local"))

		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).ToNot(BeNil())
		Expect(*vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).To(Equal(diskEncryptionSetID))
	})
})
