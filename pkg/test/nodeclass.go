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

package test

import (
	"context"
	"fmt"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	opstatus "github.com/awslabs/operatorpkg/status"
	"github.com/imdario/mergo"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

func AKSNodeClass(overrides ...v1alpha2.AKSNodeClass) *v1alpha2.AKSNodeClass {
	options := v1alpha2.AKSNodeClass{}
	for _, override := range overrides {
		if err := mergo.Merge(&options, override, mergo.WithOverride); err != nil {
			panic(fmt.Sprintf("Failed to merge settings: %s", err))
		}
	}
	// In reality, these default values will be set via the defaulting done by the API server. The reason we provide them here is
	// we sometimes reference a test.AKSNodeClass without applying it, and in that case we need to set the default values ourselves
	if options.Spec.OSDiskSizeGB == nil {
		options.Spec.OSDiskSizeGB = lo.ToPtr[int32](128)
	}
	if options.Spec.ImageFamily == nil {
		options.Spec.ImageFamily = lo.ToPtr(v1alpha2.Ubuntu2204ImageFamily)
	}
	return &v1alpha2.AKSNodeClass{
		ObjectMeta: coretest.ObjectMeta(options.ObjectMeta),
		Spec:       options.Spec,
		Status:     options.Status,
	}
}

func ApplyDefaultStatus(nodeClass *v1alpha2.AKSNodeClass, env *coretest.Environment) {
	cigImageVersion := "202501.02.0"
	nodeClass.Status.NodeImages = []v1alpha2.NodeImage{
		{
			ID: fmt.Sprintf("/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2containerd/versions/%s", cigImageVersion),
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      "kubernetes.io/arch",
					Operator: "In",
					Values:   []string{"amd64"},
				},
				{
					Key:      "karpenter.azure.com/sku-hyperv-generation",
					Operator: "In",
					Values:   []string{"2"},
				},
			},
		},
		{
			ID: fmt.Sprintf("/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204containerd/versions/%s", cigImageVersion),
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      "kubernetes.io/arch",
					Operator: "In",
					Values:   []string{"amd64"},
				},
				{
					Key:      "karpenter.azure.com/sku-hyperv-generation",
					Operator: "In",
					Values:   []string{"1"},
				},
			},
		},
		{
			ID: fmt.Sprintf("/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2arm64containerd/versions/%s", cigImageVersion),
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      "kubernetes.io/arch",
					Operator: "In",
					Values:   []string{"arm64"},
				},
				{
					Key:      "karpenter.azure.com/sku-hyperv-generation",
					Operator: "In",
					Values:   []string{"2"},
				},
			},
		},
	}
	nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeNodeImagesReady)
	nodeClass.StatusConditions().SetTrue(opstatus.ConditionReady)

	conditions := []opstatus.Condition{}
	for _, condition := range nodeClass.GetConditions() {
		// Using the magic number 1, as it appears the Generation is always equal to 1 on the NodeClass in testing. If that appears to not be the case,
		// than we should add some function for allows bumps as needed to match.
		condition.ObservedGeneration = 1
		conditions = append(conditions, condition)
	}
	nodeClass.SetConditions(conditions)
}

func AKSNodeClassFieldIndexer(ctx context.Context) func(cache.Cache) error {
	return func(c cache.Cache) error {
		return c.IndexField(ctx, &karpv1.NodeClaim{}, "spec.nodeClassRef.name", func(obj client.Object) []string {
			nc := obj.(*karpv1.NodeClaim)
			if nc.Spec.NodeClassRef == nil {
				return []string{""}
			}
			return []string{nc.Spec.NodeClassRef.Name}
		})
	}
}
