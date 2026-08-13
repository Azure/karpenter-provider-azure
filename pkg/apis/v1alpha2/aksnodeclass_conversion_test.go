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

package v1alpha2_test

import (
	"strings"

	"github.com/Pallinder/go-randomdata"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The CRD serves both v1alpha2 and v1beta1 with no conversion strategy, so the two schemas must
// carry the same spec fields. A field present in only one version is pruned when the object is
// written through the other, which silently reverts it to its default.
var _ = Describe("Version round-trip", func() {
	It("should preserve workloadRuntime across a v1alpha2 read-modify-write", func() {
		name := strings.ToLower(randomdata.SillyName())
		Expect(env.Client.Create(ctx, &v1beta1.AKSNodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1beta1.AKSNodeClassSpec{
				ImageFamily:     lo.ToPtr(v1beta1.AzureLinuxImageFamily),
				WorkloadRuntime: lo.ToPtr(v1beta1.WorkloadRuntimeKataVMIsolation),
			},
		})).To(Succeed())

		older := &v1alpha2.AKSNodeClass{}
		Expect(env.Client.Get(ctx, client.ObjectKey{Name: name}, older)).To(Succeed())
		Expect(lo.FromPtr(older.Spec.WorkloadRuntime)).To(Equal(v1alpha2.WorkloadRuntimeKataVMIsolation))

		older.Spec.OSDiskSizeGB = lo.ToPtr(int32(256))
		Expect(env.Client.Update(ctx, older)).To(Succeed())

		newer := &v1beta1.AKSNodeClass{}
		Expect(env.Client.Get(ctx, client.ObjectKey{Name: name}, newer)).To(Succeed())
		Expect(lo.FromPtr(newer.Spec.WorkloadRuntime)).To(Equal(v1beta1.WorkloadRuntimeKataVMIsolation))
	})
})
