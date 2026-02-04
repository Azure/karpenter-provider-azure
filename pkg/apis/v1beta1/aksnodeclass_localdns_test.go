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
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IsLocalDNSEnabled", func() {
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		nodeClass = &v1beta1.AKSNodeClass{
			ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
			Spec: v1beta1.AKSNodeClassSpec{
				VNETSubnetID: lo.ToPtr("subnet-id"),
			},
		}
	})

	Context("when LocalDNS is nil", func() {
		It("should return false", func() {
			nodeClass.Spec.LocalDNS = nil
			Expect(nodeClass.IsLocalDNSEnabled()).To(BeFalse())
		})
	})

	Context("when LocalDNS Mode is empty", func() {
		It("should return false", func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: ""}
			Expect(nodeClass.IsLocalDNSEnabled()).To(BeFalse())
		})
	})

	Context("when LocalDNS Mode is Required", func() {
		It("should return true regardless of Kubernetes version", func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeRequired}
			Expect(nodeClass.IsLocalDNSEnabled()).To(BeTrue())
		})
	})

	Context("when LocalDNS Mode is Disabled", func() {
		It("should return false regardless of Kubernetes version", func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeDisabled}
			Expect(nodeClass.IsLocalDNSEnabled()).To(BeFalse())
		})
	})

	Context("when LocalDNS Mode is Preferred", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
		})

		It("should return false when Kubernetes version is not set", func() {
			Expect(nodeClass.IsLocalDNSEnabled()).To(BeFalse())
		})

		It("should return false when Kubernetes version is below 1.35", func() {
			nodeClass.Status.KubernetesVersion = "1.34.0"
			nodeClass.Status.Conditions = []status.Condition{{
				Type:               v1beta1.ConditionTypeKubernetesVersionReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: nodeClass.Generation,
			}}
			Expect(nodeClass.IsLocalDNSEnabled()).To(BeFalse())
		})

		It("should return true when Kubernetes version is 1.35", func() {
			nodeClass.Status.KubernetesVersion = "1.35.0"
			nodeClass.Status.Conditions = []status.Condition{{
				Type:               v1beta1.ConditionTypeKubernetesVersionReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: nodeClass.Generation,
			}}
			Expect(nodeClass.IsLocalDNSEnabled()).To(BeTrue())
		})

		It("should return true when Kubernetes version is 1.35 with v prefix", func() {
			nodeClass.Status.KubernetesVersion = "v1.35.0"
			nodeClass.Status.Conditions = []status.Condition{{
				Type:               v1beta1.ConditionTypeKubernetesVersionReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: nodeClass.Generation,
			}}
			Expect(nodeClass.IsLocalDNSEnabled()).To(BeTrue())
		})

		It("should return true when Kubernetes version is above 1.35", func() {
			nodeClass.Status.KubernetesVersion = "1.36.0"
			nodeClass.Status.Conditions = []status.Condition{{
				Type:               v1beta1.ConditionTypeKubernetesVersionReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: nodeClass.Generation,
			}}
			Expect(nodeClass.IsLocalDNSEnabled()).To(BeTrue())
		})

		It("should return true when Kubernetes version is 1.35 with patch version", func() {
			nodeClass.Status.KubernetesVersion = "1.35.5"
			nodeClass.Status.Conditions = []status.Condition{{
				Type:               v1beta1.ConditionTypeKubernetesVersionReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: nodeClass.Generation,
			}}
			Expect(nodeClass.IsLocalDNSEnabled()).To(BeTrue())
		})

		It("should return false when Kubernetes version is 1.34 with high patch version", func() {
			nodeClass.Status.KubernetesVersion = "1.34.99"
			nodeClass.Status.Conditions = []status.Condition{{
				Type:               v1beta1.ConditionTypeKubernetesVersionReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: nodeClass.Generation,
			}}
			Expect(nodeClass.IsLocalDNSEnabled()).To(BeFalse())
		})
	})
})
