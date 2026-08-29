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

package nodeclaim_test

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

func requireTrustedLaunchBoundaryInstanceType() {
	GinkgoHelper()
	// The maximum eligible Ephemeral OS placement is 50 GiB. Trusted Launch
	// makes a 50-GiB disk too large and leaves exactly enough room for 49 GiB.
	test.ReplaceRequirements(nodePool,
		karpv1.NodeSelectorRequirementWithMinValues{
			Key:      v1beta1.LabelSKUFamily,
			Operator: corev1.NodeSelectorOpExists,
		},
		karpv1.NodeSelectorRequirementWithMinValues{
			Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{"50"},
		},
	)
}

var _ = Describe("Ephemeral OS Disk", func() {
	It("should use a node with an ephemeral os disk", func() {
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
			Operator: corev1.NodeSelectorOpGt,
			// NOTE: this is the size of our nodeclass OSDiskSizeGB.
			// If the size of the ephemeral disk requested is lower than AKSNodeClass OSDiskGB
			// we fallback to managed disks, honoring OSDiskSizeGB
			Values: []string{"50"},
		})

		deployment := test.Deployment(test.DeploymentOptions{Replicas: 1})
		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](50)
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)
		env.ExpectCreatedNodeCount("==", 1)

		vm := env.GetVM(pods[0].Spec.NodeName)
		Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).ToNot(BeNil())
		// We should be specifying os disk placement now
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement).ToNot(BeNil())
		Expect(string(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option))).To(Equal("Local"))
	})
	It("should select resource disk when cache is too small and resource disk fits", func() {
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      corev1.LabelInstanceTypeStable,
			Operator: corev1.NodeSelectorOpIn,
			// Every candidate supports both placements with less than 128 GiB of cache
			// and at least 128 GiB on the resource disk. Multiple families tolerate
			// subscription-specific SKU restrictions without weakening the boundary.
			Values: []string{
				"Standard_B16ms",
				"Standard_B20ms",
				"Standard_D4ds_v4",
				"Standard_D4pds_v5",
				"Standard_D4plds_v5",
				"Standard_DC2ds_v3",
				"Standard_E4-2ds_v4",
				"Standard_E4ds_v4",
				"Standard_E4pds_v5",
				"Standard_HC44-16rs",
				"Standard_HC44-32rs",
				"Standard_HC44rs",
				"Standard_NV8as_v4",
			},
		})
		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](128)

		deployment := test.Deployment(test.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)
		vm := env.GetVM(pods[0].Spec.NodeName)

		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement).ToNot(BeNil())
		Expect(*vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement).To(Equal(armcompute.DiffDiskPlacementResourceDisk))
	})
	It("should use managed disk when Trusted Launch consumes exact-fit local storage", func() {
		requireTrustedLaunchBoundaryInstanceType()
		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](50)
		nodeClass.Spec.Security = &v1beta1.Security{
			TrustedLaunch: &v1beta1.TrustedLaunch{VTPM: lo.ToPtr(true)},
		}

		deployment := test.Deployment(test.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)
		vm := env.GetVM(pods[0].Spec.NodeName)

		Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).ToNot(BeNil())
		Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(50)))
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).To(BeNil())
		Expect(vm.Properties.SecurityProfile).ToNot(BeNil())
		Expect(vm.Properties.SecurityProfile.SecurityType).ToNot(BeNil())
		Expect(*vm.Properties.SecurityProfile.SecurityType).To(Equal(armcompute.SecurityTypesTrustedLaunch))
	})
	It("should select resource disk when Trusted Launch consumes an exact-fit cache boundary", func() {
		test.ReplaceRequirements(nodePool,
			karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1beta1.LabelSKUFamily,
				Operator: corev1.NodeSelectorOpExists,
			},
			karpv1.NodeSelectorRequirementWithMinValues{
				Key:      corev1.LabelInstanceTypeStable,
				Operator: corev1.NodeSelectorOpIn,
				// Each candidate has a 50-GiB CacheDisk and 75-GiB ResourceDisk.
				Values: []string{"Standard_D2ds_v4", "Standard_DC1ds_v3", "Standard_E2ds_v4"},
			},
		)
		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](50)
		nodeClass.Spec.Security = &v1beta1.Security{
			TrustedLaunch: &v1beta1.TrustedLaunch{VTPM: lo.ToPtr(true)},
		}

		deployment := test.Deployment(test.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)
		vm := env.GetVM(pods[0].Spec.NodeName)

		Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).ToNot(BeNil())
		Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(50)))
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement).ToNot(BeNil())
		Expect(*vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement).To(Equal(armcompute.DiffDiskPlacementResourceDisk))
		Expect(vm.Properties.SecurityProfile).ToNot(BeNil())
		Expect(vm.Properties.SecurityProfile.SecurityType).ToNot(BeNil())
		Expect(*vm.Properties.SecurityProfile.SecurityType).To(Equal(armcompute.SecurityTypesTrustedLaunch))
	})
	It("should use an ephemeral disk when Trusted Launch has one GiB of headroom", func() {
		requireTrustedLaunchBoundaryInstanceType()
		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](49)
		nodeClass.Spec.Security = &v1beta1.Security{
			TrustedLaunch: &v1beta1.TrustedLaunch{VTPM: lo.ToPtr(true), SecureBoot: lo.ToPtr(true)},
		}

		deployment := test.Deployment(test.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)
		vm := env.GetVM(pods[0].Spec.NodeName)

		Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).ToNot(BeNil())
		Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(49)))
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement).ToNot(BeNil())
		Expect(vm.Properties.SecurityProfile).ToNot(BeNil())
		Expect(vm.Properties.SecurityProfile.SecurityType).ToNot(BeNil())
		Expect(*vm.Properties.SecurityProfile.SecurityType).To(Equal(armcompute.SecurityTypesTrustedLaunch))
	})
	It("should provision VM with SKU that does not support ephemeral OS disk", func() {
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
			Operator: corev1.NodeSelectorOpDoesNotExist,
		})

		deployment := test.Deployment(test.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)
		env.ExpectCreatedNodeCount("==", 1)
		vm := env.GetVM(pods[0].Spec.NodeName)
		Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).To(BeNil())
	})
	It("should provision VM with SKU that does not support ephemeral OS disk, even if OS disk fits on cache disk", func() {
		test.ReplaceRequirements(nodePool,
			karpv1.NodeSelectorRequirementWithMinValues{
				Key:      corev1.LabelArchStable,
				Operator: corev1.NodeSelectorOpExists, // relax to allow arm
			},
			karpv1.NodeSelectorRequirementWithMinValues{
				Key:      corev1.LabelInstanceTypeStable,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{"Standard_D2pls_v5"}, // 53GB cache disk, does not support ephemeral OS disk
			},
		)

		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](40) // < 53GB cache disk

		deployment := test.Deployment(test.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)
		env.ExpectCreatedNodeCount("==", 1)
		vm := env.GetVM(pods[0].Spec.NodeName)
		Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).To(BeNil())
	})
})
