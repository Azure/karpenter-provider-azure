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
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IsLocalDNSEnabled", func() {
	var nodeClass *v1alpha2.AKSNodeClass

	BeforeEach(func() {
		nodeClass = &v1alpha2.AKSNodeClass{}
	})

	DescribeTable("reads from Status.LocalDNSState",
		func(state *v1alpha2.LocalDNSState, expected bool) {
			nodeClass.Status.LocalDNSState = state
			Expect(nodeClass.IsLocalDNSEnabled()).To(Equal(expected))
		},
		Entry("nil state", (*v1alpha2.LocalDNSState)(nil), false),
		Entry("Enabled state", lo.ToPtr(v1alpha2.LocalDNSStateEnabled), true),
		Entry("Disabled state", lo.ToPtr(v1alpha2.LocalDNSStateDisabled), false),
	)
})
