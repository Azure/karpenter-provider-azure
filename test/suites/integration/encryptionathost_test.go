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

package integration_test

import (
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/samber/lo"
	"sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("EncryptionAtHost", func() {
	It("should provision a node with encryption at host enabled", func() {
		if nodeClass.Spec.Security == nil {
			nodeClass.Spec.Security = &v1beta1.Security{}
		}
		nodeClass.Spec.Security.EncryptionAtHost = lo.ToPtr(true)

		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)

		vm := env.GetVM(pod.Spec.NodeName)
		Expect(vm.Properties.SecurityProfile).ToNot(BeNil())
		Expect(vm.Properties.SecurityProfile.EncryptionAtHost).ToNot(BeNil())
		Expect(lo.FromPtr(vm.Properties.SecurityProfile.EncryptionAtHost)).To(BeTrue())
	})

})
