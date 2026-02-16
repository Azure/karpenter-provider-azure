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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/awslabs/operatorpkg/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IsLocalDNSEnabled", func() {
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
		nodeClass.Status = v1beta1.AKSNodeClassStatus{
			Conditions: []status.Condition{{
				Type:               v1beta1.ConditionTypeKubernetesVersionReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: nodeClass.Generation,
			}},
		}
	})

	DescribeTable("should return correct value based on LocalDNS mode and Kubernetes version",
		func(mode v1beta1.LocalDNSMode, kubernetesVersion string, expected bool) {
			if mode != "" {
				nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: mode}
			}
			if kubernetesVersion != "" {
				nodeClass.Status.KubernetesVersion = to.Ptr(kubernetesVersion)
			}
			Expect(nodeClass.IsLocalDNSEnabled()).To(Equal(expected))
		},
		Entry("LocalDNS is nil", v1beta1.LocalDNSMode(""), "", false),
		Entry("Mode is Required", v1beta1.LocalDNSModeRequired, "", true),
		Entry("Mode is Disabled", v1beta1.LocalDNSModeDisabled, "", false),
		Entry("Mode is Preferred, no k8s version", v1beta1.LocalDNSModePreferred, "", false),
		Entry("Mode is Preferred, k8s 1.34.0", v1beta1.LocalDNSModePreferred, "1.34.0", false),
		Entry("Mode is Preferred, k8s 1.35.0", v1beta1.LocalDNSModePreferred, "1.35.0", true),
		Entry("Mode is Preferred, k8s v1.35.0", v1beta1.LocalDNSModePreferred, "v1.35.0", true),
		Entry("Mode is Preferred, k8s 1.36.0", v1beta1.LocalDNSModePreferred, "1.36.0", true),
		Entry("Mode is Preferred, k8s 1.35.5", v1beta1.LocalDNSModePreferred, "1.35.5", true),
		Entry("Mode is Preferred, k8s 1.34.99", v1beta1.LocalDNSModePreferred, "1.34.99", false),
	)
})
