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
	"testing"
	"time"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

func newStatusTestNodeClass() *v1beta1.AKSNodeClass {
	return &v1beta1.AKSNodeClass{
		ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
		Spec: v1beta1.AKSNodeClassSpec{
			VNETSubnetID: lo.ToPtr("subnet-id"),
			OSDiskSizeGB: lo.ToPtr(int32(30)),
			ImageFamily:  lo.ToPtr("Ubuntu2204"),
			Tags: map[string]string{
				"keyTag-1": "valueTag-1",
				"keyTag-2": "valueTag-2",
			},
			Kubelet: &v1beta1.KubeletConfiguration{
				CPUManagerPolicy:            lo.ToPtr("static"),
				CPUCFSQuota:                 lo.ToPtr(true),
				CPUCFSQuotaPeriod:           metav1.Duration{Duration: lo.Must(time.ParseDuration("100ms"))},
				ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
				ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
				TopologyManagerPolicy:       lo.ToPtr("none"),
				AllowedUnsafeSysctls:        []string{"net.core.somaxconn"},
				ContainerLogMaxSize:         lo.ToPtr("10Mi"),
				ContainerLogMaxFiles:        lo.ToPtr(int32(10)),
			},
			MaxPods: lo.ToPtr(int32(100)),
		},
		Status: v1beta1.AKSNodeClassStatus{
			Conditions: []status.Condition{
				{
					Type:               v1beta1.ConditionTypeImagesReady,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             "ImagesReady",
					Message:            "Images are ready for use",
					ObservedGeneration: 1,
				},
				{
					Type:               v1beta1.ConditionTypeKubernetesVersionReady,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             "KubernetesVersionReady",
					Message:            "Kubernetes version is ready for use",
					ObservedGeneration: 1,
				},
			},
			KubernetesVersion: lo.ToPtr("1.31.0"),
			Images: []v1beta1.NodeImage{
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
}

//nolint:gocyclo
func TestStatusAccessors(t *testing.T) {
	t.Run("should return conditions", func(t *testing.T) {
		nodeClass := newStatusTestNodeClass()
		conditions := nodeClass.GetConditions()
		if conditions == nil {
			t.Fatal("expected conditions to not be nil")
		}
		if len(conditions) != 2 {
			t.Fatalf("expected 2 conditions, got %d", len(conditions))
		}
		if conditions[0].Type != v1beta1.ConditionTypeImagesReady {
			t.Errorf("expected condition type %s, got %s", v1beta1.ConditionTypeImagesReady, conditions[0].Type)
		}
		if conditions[0].Status != metav1.ConditionTrue {
			t.Errorf("expected condition status %s, got %s", metav1.ConditionTrue, conditions[0].Status)
		}
		if time.Since(conditions[0].LastTransitionTime.Time) > time.Second {
			t.Errorf("expected LastTransitionTime to be within 1 second of now")
		}
		if conditions[0].Reason != "ImagesReady" {
			t.Errorf("expected reason ImagesReady, got %s", conditions[0].Reason)
		}
		if conditions[0].Message != "Images are ready for use" {
			t.Errorf("expected message 'Images are ready for use', got %s", conditions[0].Message)
		}
	})

	t.Run("should return status conditions", func(t *testing.T) {
		nodeClass := newStatusTestNodeClass()
		conditionSet := nodeClass.StatusConditions()
		// KubernetesVersionReady, SubnetReady, ImagesReady, ValidationSucceeded, Ready
		if got := len(conditionSet.List()); got != 5 {
			t.Errorf("expected 5 status conditions, got %d", got)
		}
		if conditionSet.Root().Type != status.ConditionReady {
			t.Errorf("expected root condition type %s, got %s", status.ConditionReady, conditionSet.Root().Type)
		}
	})

	t.Run("should return kubernetes version", func(t *testing.T) {
		nodeClass := newStatusTestNodeClass()
		kubernetesVersion, err := nodeClass.GetKubernetesVersion()
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if kubernetesVersion != "1.31.0" {
			t.Errorf("expected kubernetes version 1.31.0, got %s", kubernetesVersion)
		}
	})

	t.Run("should return image", func(t *testing.T) {
		nodeClass := newStatusTestNodeClass()
		images, err := nodeClass.GetImages()
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(images) != 1 {
			t.Fatalf("expected 1 image, got %d", len(images))
		}
		expectedID := "/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2containerd/versions/202501.02.0"
		if images[0].ID != expectedID {
			t.Errorf("expected image ID %s, got %s", expectedID, images[0].ID)
		}
		if len(images[0].Requirements) != 1 {
			t.Fatalf("expected 1 requirement, got %d", len(images[0].Requirements))
		}
		if images[0].Requirements[0].Key != corev1.LabelArchStable {
			t.Errorf("expected requirement key %s, got %s", corev1.LabelArchStable, images[0].Requirements[0].Key)
		}
		if images[0].Requirements[0].Operator != corev1.NodeSelectorOperator("In") {
			t.Errorf("expected requirement operator In, got %s", images[0].Requirements[0].Operator)
		}
		if len(images[0].Requirements[0].Values) != 1 || images[0].Requirements[0].Values[0] != "amd64" {
			t.Errorf("expected requirement values [amd64], got %v", images[0].Requirements[0].Values)
		}
	})

	t.Run("should return the expected errors", func(t *testing.T) {
		// nil nodeClass
		var errNodeClass *v1beta1.AKSNodeClass
		kubernetesVersion, err := errNodeClass.GetKubernetesVersion()
		if err == nil {
			t.Fatal("expected error for nil nodeClass")
		}
		if kubernetesVersion != "" {
			t.Errorf("expected empty kubernetes version, got %s", kubernetesVersion)
		}
		if err.Error() != "NodeClass is nil, condition KubernetesVersionReady is not true" {
			t.Errorf("unexpected error: %s", err.Error())
		}

		// nodeClass with ImagesReady=False, no KubernetesVersionReady condition
		errNodeClass = &v1beta1.AKSNodeClass{
			ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
			Spec: v1beta1.AKSNodeClassSpec{
				VNETSubnetID: lo.ToPtr("subnet-id"),
				OSDiskSizeGB: lo.ToPtr(int32(30)),
				ImageFamily:  lo.ToPtr("Ubuntu2204"),
				Tags: map[string]string{
					"keyTag-1": "valueTag-1",
					"keyTag-2": "valueTag-2",
				},
				Kubelet: &v1beta1.KubeletConfiguration{
					CPUManagerPolicy:            lo.ToPtr("static"),
					CPUCFSQuota:                 lo.ToPtr(true),
					CPUCFSQuotaPeriod:           metav1.Duration{Duration: lo.Must(time.ParseDuration("100ms"))},
					ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
					TopologyManagerPolicy:       lo.ToPtr("none"),
					AllowedUnsafeSysctls:        []string{"net.core.somaxconn"},
					ContainerLogMaxSize:         lo.ToPtr("10Mi"),
					ContainerLogMaxFiles:        lo.ToPtr(int32(10)),
				},
				MaxPods: lo.ToPtr(int32(100)),
			},
			Status: v1beta1.AKSNodeClassStatus{
				Conditions: []status.Condition{
					{
						Type:               v1beta1.ConditionTypeImagesReady,
						Status:             metav1.ConditionFalse,
						LastTransitionTime: metav1.Now(),
						Reason:             "Unknown",
						Message:            "Images are not ready for use",
					},
				},
				KubernetesVersion: lo.ToPtr("1.31.0"),
			},
		}
		kubernetesVersion, err = errNodeClass.GetKubernetesVersion()
		if kubernetesVersion != "" {
			t.Errorf("expected empty kubernetes version, got %s", kubernetesVersion)
		}
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "NodeClass condition KubernetesVersionReady, is in Ready=Unknown, object is awaiting reconciliation" {
			t.Errorf("unexpected error: %s", err.Error())
		}
		images, err := errNodeClass.GetImages()
		if err == nil {
			t.Fatal("expected error for GetImages")
		}
		if len(images) != 0 {
			t.Errorf("expected empty images, got %d", len(images))
		}
		if err.Error() != "NodeClass condition ImagesReady, is in Ready=False, Images are not ready for use" {
			t.Errorf("unexpected error: %s", err.Error())
		}

		// nodeClass with ObservedGeneration mismatch
		errNodeClass = &v1beta1.AKSNodeClass{
			ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
			Spec: v1beta1.AKSNodeClassSpec{
				VNETSubnetID: lo.ToPtr("subnet-id"),
				OSDiskSizeGB: lo.ToPtr(int32(30)),
				ImageFamily:  lo.ToPtr("Ubuntu2204"),
				Tags: map[string]string{
					"keyTag-1": "valueTag-1",
					"keyTag-2": "valueTag-2",
				},
				Kubelet: &v1beta1.KubeletConfiguration{
					CPUManagerPolicy:            lo.ToPtr("static"),
					CPUCFSQuota:                 lo.ToPtr(true),
					CPUCFSQuotaPeriod:           metav1.Duration{Duration: lo.Must(time.ParseDuration("100ms"))},
					ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
					TopologyManagerPolicy:       lo.ToPtr("none"),
					AllowedUnsafeSysctls:        []string{"net.core.somaxconn"},
					ContainerLogMaxSize:         lo.ToPtr("10Mi"),
					ContainerLogMaxFiles:        lo.ToPtr(int32(10)),
				},
				MaxPods: lo.ToPtr(int32(100)),
			},
			Status: v1beta1.AKSNodeClassStatus{
				Conditions: []status.Condition{
					{
						Type:               v1beta1.ConditionTypeImagesReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "ImagesReady",
						Message:            "Images are ready for use",
					},
					{
						Type:               v1beta1.ConditionTypeKubernetesVersionReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "KubernetesVersionReady",
						Message:            "Kubernetes version is ready for use",
					},
				},
				KubernetesVersion: lo.ToPtr("1.31.0"),
			},
		}
		kubernetesVersion, err = errNodeClass.GetKubernetesVersion()
		if err == nil {
			t.Fatal("expected error")
		}
		if kubernetesVersion != "" {
			t.Errorf("expected empty kubernetes version, got %s", kubernetesVersion)
		}
		if err.Error() != "NodeClass condition KubernetesVersionReady ObservedGeneration 0 does not match the NodeClass Generation 1" {
			t.Errorf("unexpected error: %s", err.Error())
		}
		images, err = errNodeClass.GetImages()
		if err == nil {
			t.Fatal("expected error for GetImages")
		}
		if len(images) != 0 {
			t.Errorf("expected empty images, got %d", len(images))
		}
		if err.Error() != "NodeClass condition ImagesReady ObservedGeneration 0 does not match the NodeClass Generation 1" {
			t.Errorf("unexpected error: %s", err.Error())
		}

		// nodeClass with empty KubernetesVersion
		errNodeClass = &v1beta1.AKSNodeClass{
			ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
			Spec: v1beta1.AKSNodeClassSpec{
				VNETSubnetID: lo.ToPtr("subnet-id"),
				OSDiskSizeGB: lo.ToPtr(int32(30)),
				ImageFamily:  lo.ToPtr("Ubuntu2204"),
				Tags: map[string]string{
					"keyTag-1": "valueTag-1",
					"keyTag-2": "valueTag-2",
				},
				Kubelet: &v1beta1.KubeletConfiguration{
					CPUManagerPolicy:            lo.ToPtr("static"),
					CPUCFSQuota:                 lo.ToPtr(true),
					CPUCFSQuotaPeriod:           metav1.Duration{Duration: lo.Must(time.ParseDuration("100ms"))},
					ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
					TopologyManagerPolicy:       lo.ToPtr("none"),
					AllowedUnsafeSysctls:        []string{"net.core.somaxconn"},
					ContainerLogMaxSize:         lo.ToPtr("10Mi"),
					ContainerLogMaxFiles:        lo.ToPtr(int32(10)),
				},
				MaxPods: lo.ToPtr(int32(100)),
			},
			Status: v1beta1.AKSNodeClassStatus{
				Conditions: []status.Condition{
					{
						Type:               v1beta1.ConditionTypeImagesReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "ImagesReady",
						Message:            "Images are ready for use",
					},
					{
						Type:               v1beta1.ConditionTypeKubernetesVersionReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "KubernetesVersionReady",
						Message:            "Kubernetes version is ready for use",
						ObservedGeneration: 1,
					},
				},
				KubernetesVersion: lo.ToPtr(""),
			},
		}
		kubernetesVersion, err = errNodeClass.GetKubernetesVersion()
		if err == nil {
			t.Fatal("expected error")
		}
		if kubernetesVersion != "" {
			t.Errorf("expected empty kubernetes version, got %s", kubernetesVersion)
		}
		if err.Error() != "NodeClass KubernetesVersion is uninitialized" {
			t.Errorf("unexpected error: %s", err.Error())
		}
	})
}
