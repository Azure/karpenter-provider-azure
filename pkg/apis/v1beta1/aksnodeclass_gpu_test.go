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

package v1beta1_test

import (
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GPU managed mode helpers", func() {
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
	})

	DescribeTable("GetManagementMode defaults to Unmanaged when unset",
		func(gpu *v1beta1.GPU, expected v1beta1.ManagementMode) {
			nodeClass.Spec.GPU = gpu
			Expect(nodeClass.GetManagementMode()).To(Equal(expected))
		},
		Entry("gpu nil", (*v1beta1.GPU)(nil), v1beta1.ManagementModeUnmanaged),
		Entry("gpu set, nvidia nil", &v1beta1.GPU{}, v1beta1.ManagementModeUnmanaged),
		Entry("nvidia set, managementMode nil", &v1beta1.GPU{Nvidia: &v1beta1.NvidiaGPU{}}, v1beta1.ManagementModeUnmanaged),
		Entry("managementMode Unmanaged", &v1beta1.GPU{Nvidia: &v1beta1.NvidiaGPU{ManagementMode: lo.ToPtr(v1beta1.ManagementModeUnmanaged)}}, v1beta1.ManagementModeUnmanaged),
		Entry("managementMode Managed", &v1beta1.GPU{Nvidia: &v1beta1.NvidiaGPU{ManagementMode: lo.ToPtr(v1beta1.ManagementModeManaged)}}, v1beta1.ManagementModeManaged),
	)

	DescribeTable("IsManagedGPUEnabled",
		func(gpu *v1beta1.GPU, expected bool) {
			nodeClass.Spec.GPU = gpu
			Expect(nodeClass.IsManagedGPUEnabled()).To(Equal(expected))
		},
		Entry("gpu nil", (*v1beta1.GPU)(nil), false),
		Entry("nvidia nil", &v1beta1.GPU{}, false),
		Entry("managementMode nil", &v1beta1.GPU{Nvidia: &v1beta1.NvidiaGPU{}}, false),
		Entry("Unmanaged", &v1beta1.GPU{Nvidia: &v1beta1.NvidiaGPU{ManagementMode: lo.ToPtr(v1beta1.ManagementModeUnmanaged)}}, false),
		Entry("Managed", &v1beta1.GPU{Nvidia: &v1beta1.NvidiaGPU{ManagementMode: lo.ToPtr(v1beta1.ManagementModeManaged)}}, true),
	)
})
