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

package byok_test

import (
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

var _ = Describe("Disk Encryption BYOK", Serial, func() {
	var diskEncryptionSetID string

	BeforeEach(func() {
		// Check if BYOK is configured for this test run
		diskEncryptionSetID = os.Getenv("AZURE_DISK_ENCRYPTION_SET_ID")
		if diskEncryptionSetID == "" {
			Skip("Skipping BYOK tests: AZURE_DISK_ENCRYPTION_SET_ID environment variable not set")
		}
	})

	Context("when DiskEncryptionSetID is configured", func() {
		It("should create VMs with encrypted managed OS disks", func() {
			nodeClass := env.DefaultAKSNodeClass()
			nodePool := env.DefaultNodePool(nodeClass)

			// Ensure we select SKUs that only support managed disks (no ephemeral OS disk support)
			test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"0"}, // 0 means no ephemeral OS disk support
				}})

			pod := test.Pod()
			env.ExpectCreated(nodeClass, nodePool, pod)
			env.EventuallyExpectHealthy(pod)
			env.ExpectCreatedNodeCount("==", 1)

			// Verify VM has encrypted managed OS disk
			vm := env.GetVM(pod.Spec.NodeName)
			Expect(vm).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk).NotTo(BeNil())
			
			// Verify it's a managed disk (no ephemeral settings)
			Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).To(BeNil())
			
			// Verify encryption is configured
			Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).NotTo(BeNil())
			Expect(*vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).To(Equal(diskEncryptionSetID))
		})

		It("should create VMs with encrypted ephemeral OS disks", func() {
			nodeClass := env.DefaultAKSNodeClass()
			nodePool := env.DefaultNodePool(nodeClass)

			// Configure for ephemeral OS disk and ensure we can provision it
			test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
					Operator: corev1.NodeSelectorOpGt,
					Values:   []string{"50"},
				}})
			nodeClass.Spec.OSDiskType = lo.ToPtr(v1beta1.OSDiskTypeEphemeral)
			nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](50)

			pod := test.Pod()
			env.ExpectCreated(nodeClass, nodePool, pod)
			env.EventuallyExpectHealthy(pod)
			env.ExpectCreatedNodeCount("==", 1)

			// Verify VM has encrypted ephemeral OS disk
			vm := env.GetVM(pod.Spec.NodeName)
			Expect(vm).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk).NotTo(BeNil())

			// Verify ephemeral disk settings
			Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option).To(Equal(lo.ToPtr(armcompute.DiffDiskOptionsLocal)))

			// Verify encryption is configured
			Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).NotTo(BeNil())
			Expect(*vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).To(Equal(diskEncryptionSetID))
		})

		It("should work with persistent volume claims using BYOK", func() {
			nodeClass := env.DefaultAKSNodeClass()
			nodePool := env.DefaultNodePool(nodeClass)

			// Create a storage class that uses the same disk encryption set
			storageClass := &storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "encrypted-storage-test",
				},
				Provisioner: "disk.csi.azure.com",
				Parameters: map[string]string{
					"skuName":              "Premium_LRS",
					"diskEncryptionSetID":  diskEncryptionSetID,
				},
				VolumeBindingMode: lo.ToPtr(storagev1.VolumeBindingWaitForFirstConsumer),
			}

			pvc := test.PersistentVolumeClaim(test.PersistentVolumeClaimOptions{
				ObjectMeta: metav1.ObjectMeta{
					Name: "encrypted-pvc-test",
				},
				StorageClassName: &storageClass.Name,
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			})

			pod := test.Pod(test.PodOptions{
				PersistentVolumeClaims: []string{pvc.Name},
			})

			env.ExpectCreated(nodeClass, nodePool, storageClass, pvc, pod)
			env.EventuallyExpectHealthy(pod)
			env.ExpectCreatedNodeCount("==", 1)

			// Verify VM has encrypted OS disk (from cluster-level DES configuration)
			vm := env.GetVM(pod.Spec.NodeName)
			Expect(vm).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).NotTo(BeNil())
			Expect(*vm.Properties.StorageProfile.OSDisk.ManagedDisk.DiskEncryptionSet.ID).To(Equal(diskEncryptionSetID))

			// The PVC itself should also be encrypted, but we can't easily verify that in the VM properties
			// since data disks are managed separately. The important part is that the storage class
			// includes the diskEncryptionSetID parameter.
			env.ExpectDeleted(pod)
		})
	})

	Context("when invalid DiskEncryptionSetID is provided", func() {
		It("should handle invalid disk encryption set ID gracefully", func() {
			// This test would require setting up the cluster with an invalid DES ID
			// For now, we skip it as it would require specific infrastructure setup
			Skip("Test for invalid DiskEncryptionSetID requires specific infrastructure setup")
		})
	})
})