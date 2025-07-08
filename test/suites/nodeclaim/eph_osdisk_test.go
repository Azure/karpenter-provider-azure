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
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

var _ = Describe("Ephemeral OS Disk", func() {
	It("should use a node with an ephemeral os disk", func() {
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
				Operator: corev1.NodeSelectorOpGt,
				// NOTE: this is the size of our nodeclass OSDiskSizeGB.
				// If the size of the ephemeral disk requested is lower than AKSNodeClass OSDiskGB
				// we fallback to managed disks, honoring OSDiskSizeGB
				Values: []string{"50"},
			}})

		pod := test.Pod()
		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](50)
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)

		vm := env.GetVM(pod.Spec.NodeName)
		Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).ToNot(BeNil())
		// We should be specifying os disk placement now
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement).ToNot(BeNil())
		Expect(string(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option))).To(Equal("Local"))
	})
	It("should provision VM with SKU that does not support ephemeral OS disk", func() {
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
				Operator: corev1.NodeSelectorOpDoesNotExist,
			}})

		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)
		vm := env.GetVM(pod.Spec.NodeName)
		Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).To(BeNil())
	})
	It("should provision VM with SKU that does not support ephemeral OS disk, even if OS disk fits on cache disk", func() {
		test.ReplaceRequirements(nodePool,
			karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      corev1.LabelArchStable,
					Operator: corev1.NodeSelectorOpExists, // relax to allow arm
				}},
			karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      corev1.LabelInstanceTypeStable,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"Standard_D2pls_v5"}, // 53GB cache disk, does not support ephemeral OS disk
				}},
		)

		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](40) // < 53GB cache disk

		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)
		vm := env.GetVM(pod.Spec.NodeName)
		Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())
		Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).To(BeNil())
	})
})
