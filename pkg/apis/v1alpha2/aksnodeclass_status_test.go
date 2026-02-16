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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("Status, successful outcomes", func() {
	var nodeClass *v1alpha2.AKSNodeClass
	BeforeEach(func() {
		nodeClass = &v1alpha2.AKSNodeClass{
			ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
			Spec: v1alpha2.AKSNodeClassSpec{
				VNETSubnetID: lo.ToPtr("subnet-id"),
				OSDiskSizeGB: lo.ToPtr(int32(30)),
				ImageFamily:  lo.ToPtr("Ubuntu2204"),
				Tags: map[string]string{
					"keyTag-1": "valueTag-1",
					"keyTag-2": "valueTag-2",
				},
				Kubelet: &v1alpha2.KubeletConfiguration{
					CPUManagerPolicy:            to.Ptr("static"),
					CPUCFSQuota:                 lo.ToPtr(true),
					CPUCFSQuotaPeriod:           metav1.Duration{Duration: lo.Must(time.ParseDuration("100ms"))},
					ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
					TopologyManagerPolicy:       "none",
					AllowedUnsafeSysctls:        []string{"net.core.somaxconn"},
					ContainerLogMaxSize:         to.Ptr("10Mi"),
					ContainerLogMaxFiles:        lo.ToPtr(int32(10)),
				},
				MaxPods: lo.ToPtr(int32(100)),
			},
			Status: v1alpha2.AKSNodeClassStatus{
				Conditions: []status.Condition{
					{
						Type:               v1alpha2.ConditionTypeImagesReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "ImagesReady",
						Message:            "Images are ready for use",
						ObservedGeneration: 1,
					},
					{
						Type:               v1alpha2.ConditionTypeKubernetesVersionReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "KubernetesVersionReady",
						Message:            "Kubernetes version is ready for use",
						ObservedGeneration: 1,
					},
				},
				KubernetesVersion: to.Ptr("1.31.0"),
				Images: []v1alpha2.NodeImage{
					{
						ID: "/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2containerd/versions/202501.02.0",
						Requirements: []corev1.NodeSelectorRequirement{
							{
								Key:      corev1.LabelArchStable,
								Operator: "In",
								Values:   []string{"amd64"},
							},
						},
					},
				},
			},
		}
	})
	It("should return conditions", func() {
		conditions := nodeClass.GetConditions()
		Expect(conditions).ToNot(BeNil())
		Expect(conditions).To(HaveLen(2))
		Expect(conditions[0].Type).To(Equal(v1alpha2.ConditionTypeImagesReady))
		Expect(conditions[0].Status).To(Equal(metav1.ConditionTrue))
		Expect(conditions[0].LastTransitionTime.UTC()).To(BeTemporally("~", metav1.Now().Time, time.Second))
		Expect(conditions[0].Reason).To(Equal("ImagesReady"))
		Expect(conditions[0].Message).To(Equal("Images are ready for use"))
	})
	It("should return status conditions", func() {
		conditionSet := nodeClass.StatusConditions()
		Expect(conditionSet).ToNot(BeNil())
		Expect(conditionSet.List()).To(HaveLen(4)) // KubernetesVersionReady, ImagesReady, SubnetReady, Ready
		Expect(conditionSet.Root().Type).To(Equal(status.ConditionReady))
	})
	It("should return kubernetes version", func() {
		kubernetesVersion, err := nodeClass.GetKubernetesVersion()
		Expect(err).To(BeNil())
		Expect(kubernetesVersion).To(Equal("1.31.0"))
	})
	It("should return image", func() {
		images, err := nodeClass.GetImages()
		Expect(err).To(BeNil())
		Expect(images).To(HaveLen(1))
		Expect(images[0].ID).To(Equal("/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2containerd/versions/202501.02.0"))
		Expect(images[0].Requirements).To(HaveLen(1))
		Expect(images[0].Requirements[0].Key).To(Equal(corev1.LabelArchStable))
		Expect(images[0].Requirements[0].Operator).To(Equal(corev1.NodeSelectorOperator("In")))
		Expect(images[0].Requirements[0].Values).To(Equal([]string{"amd64"}))
	})
	It("should return the expected errors", func() {
		var errNodeClass *v1alpha2.AKSNodeClass
		kubernetesVersion, err := errNodeClass.GetKubernetesVersion()
		Expect(err).To(HaveOccurred())
		Expect(kubernetesVersion).To(Equal(""))
		Expect(err.Error()).To(Equal("NodeClass is nil, condition KubernetesVersionReady is not true"))
		errNodeClass = &v1alpha2.AKSNodeClass{
			ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
			Spec: v1alpha2.AKSNodeClassSpec{
				VNETSubnetID: lo.ToPtr("subnet-id"),
				OSDiskSizeGB: lo.ToPtr(int32(30)),
				ImageFamily:  lo.ToPtr("Ubuntu2204"),
				Tags: map[string]string{
					"keyTag-1": "valueTag-1",
					"keyTag-2": "valueTag-2",
				},
				Kubelet: &v1alpha2.KubeletConfiguration{
					CPUManagerPolicy:            to.Ptr("static"),
					CPUCFSQuota:                 lo.ToPtr(true),
					CPUCFSQuotaPeriod:           metav1.Duration{Duration: lo.Must(time.ParseDuration("100ms"))},
					ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
					TopologyManagerPolicy:       "none",
					AllowedUnsafeSysctls:        []string{"net.core.somaxconn"},
					ContainerLogMaxSize:         to.Ptr("10Mi"),
					ContainerLogMaxFiles:        lo.ToPtr(int32(10)),
				},
				MaxPods: lo.ToPtr(int32(100)),
			},
			Status: v1alpha2.AKSNodeClassStatus{
				Conditions: []status.Condition{
					{
						Type:               v1alpha2.ConditionTypeImagesReady,
						Status:             metav1.ConditionFalse,
						LastTransitionTime: metav1.Now(),
						Reason:             "Unknown",
						Message:            "Images are not ready for use",
					},
				},
				KubernetesVersion: to.Ptr("1.31.0"),
			},
		}
		kubernetesVersion, err = errNodeClass.GetKubernetesVersion()
		Expect(kubernetesVersion).To(Equal(""))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(Equal("NodeClass condition KubernetesVersionReady, is in Ready=Unknown, object is awaiting reconciliation"))
		images, err := errNodeClass.GetImages()
		Expect(err).To(HaveOccurred())
		Expect(images).To(Equal([]v1alpha2.NodeImage{}))
		Expect(err.Error()).To(Equal("NodeClass condition ImagesReady, is in Ready=False, Images are not ready for use"))
		errNodeClass = &v1alpha2.AKSNodeClass{
			ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
			Spec: v1alpha2.AKSNodeClassSpec{
				VNETSubnetID: lo.ToPtr("subnet-id"),
				OSDiskSizeGB: lo.ToPtr(int32(30)),
				ImageFamily:  lo.ToPtr("Ubuntu2204"),
				Tags: map[string]string{
					"keyTag-1": "valueTag-1",
					"keyTag-2": "valueTag-2",
				},
				Kubelet: &v1alpha2.KubeletConfiguration{
					CPUManagerPolicy:            to.Ptr("static"),
					CPUCFSQuota:                 lo.ToPtr(true),
					CPUCFSQuotaPeriod:           metav1.Duration{Duration: lo.Must(time.ParseDuration("100ms"))},
					ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
					TopologyManagerPolicy:       "none",
					AllowedUnsafeSysctls:        []string{"net.core.somaxconn"},
					ContainerLogMaxSize:         to.Ptr("10Mi"),
					ContainerLogMaxFiles:        lo.ToPtr(int32(10)),
				},
				MaxPods: lo.ToPtr(int32(100)),
			},
			Status: v1alpha2.AKSNodeClassStatus{
				Conditions: []status.Condition{
					{
						Type:               v1alpha2.ConditionTypeImagesReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "ImagesReady",
						Message:            "Images are ready for use",
					},
					{
						Type:               v1alpha2.ConditionTypeKubernetesVersionReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "KubernetesVersionReady",
						Message:            "Kubernetes version is ready for use",
					},
				},
				KubernetesVersion: to.Ptr("1.31.0"),
			},
		}
		kubernetesVersion, err = errNodeClass.GetKubernetesVersion()
		Expect(err).To(HaveOccurred())
		Expect(kubernetesVersion).To(Equal(""))
		Expect(err.Error()).To(Equal("NodeClass condition KubernetesVersionReady ObservedGeneration 0 does not match the NodeClass Generation 1"))
		images, err = errNodeClass.GetImages()
		Expect(err).To(HaveOccurred())
		Expect(images).To(Equal([]v1alpha2.NodeImage{}))
		Expect(err.Error()).To(Equal("NodeClass condition ImagesReady ObservedGeneration 0 does not match the NodeClass Generation 1"))
		errNodeClass = &v1alpha2.AKSNodeClass{
			ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
			Spec: v1alpha2.AKSNodeClassSpec{
				VNETSubnetID: lo.ToPtr("subnet-id"),
				OSDiskSizeGB: lo.ToPtr(int32(30)),
				ImageFamily:  lo.ToPtr("Ubuntu2204"),
				Tags: map[string]string{
					"keyTag-1": "valueTag-1",
					"keyTag-2": "valueTag-2",
				},
				Kubelet: &v1alpha2.KubeletConfiguration{
					CPUManagerPolicy:            to.Ptr("static"),
					CPUCFSQuota:                 lo.ToPtr(true),
					CPUCFSQuotaPeriod:           metav1.Duration{Duration: lo.Must(time.ParseDuration("100ms"))},
					ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
					TopologyManagerPolicy:       "none",
					AllowedUnsafeSysctls:        []string{"net.core.somaxconn"},
					ContainerLogMaxSize:         to.Ptr("10Mi"),
					ContainerLogMaxFiles:        lo.ToPtr(int32(10)),
				},
				MaxPods: lo.ToPtr(int32(100)),
			},
			Status: v1alpha2.AKSNodeClassStatus{
				Conditions: []status.Condition{
					{
						Type:               v1alpha2.ConditionTypeImagesReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "ImagesReady",
						Message:            "Images are ready for use",
					},
					{
						Type:               v1alpha2.ConditionTypeKubernetesVersionReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "KubernetesVersionReady",
						Message:            "Kubernetes version is ready for use",
						ObservedGeneration: 1,
					},
				},
				KubernetesVersion: to.Ptr(""),
			},
		}
		kubernetesVersion, err = errNodeClass.GetKubernetesVersion()
		Expect(err).To(HaveOccurred())
		Expect(kubernetesVersion).To(Equal(""))
		Expect(err.Error()).To(Equal("NodeClass KubernetesVersion is uninitialized"))
	})
})
