// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package v1alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const Group = "karpenter.azure.com"

var (
	SchemeGroupVersion = schema.GroupVersion{Group: Group, Version: "v1alpha2"}
	SchemeBuilder      = runtime.NewSchemeBuilder(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion,
			&AKSNodeClass{},
			&AKSNodeClassList{},
		)
		metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
		return nil
	})
)
