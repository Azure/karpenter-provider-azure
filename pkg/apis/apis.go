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

// Package apis contains Kubernetes API groups.
package apis

import (
	_ "embed"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"sigs.k8s.io/karpenter/pkg/apis"
	"sigs.k8s.io/karpenter/pkg/utils/functional"
)

var (
	// Builder includes all types within the apis package
	Builder = runtime.NewSchemeBuilder(
		v1alpha2.SchemeBuilder.AddToScheme,
	)
	// AddToScheme may be used to add all resources defined in the project to a Scheme
	AddToScheme = Builder.AddToScheme
)

//go:generate controller-gen crd object:headerFile="../../hack/boilerplate.go.txt" paths="./..." output:crd:artifacts:config=crds
var (
	//go:embed crds/karpenter.azure.com_aksnodeclasses.yaml
	AKSNodeClassCRD []byte
	CRDs            = append(apis.CRDs, lo.Must(functional.Unmarshal[v1.CustomResourceDefinition](AKSNodeClassCRD)))
)
