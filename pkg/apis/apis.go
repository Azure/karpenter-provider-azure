// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package apis contains Kubernetes API groups.
package apis

import (
	_ "embed"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/aws/karpenter-core/pkg/operator/scheme"

	"github.com/samber/lo"

	"github.com/Azure/karpenter/pkg/apis/settings"
	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	"github.com/aws/karpenter-core/pkg/apis"
	coresettings "github.com/aws/karpenter-core/pkg/apis/settings"
	"github.com/aws/karpenter-core/pkg/utils/functional"
)

var (
	// Builder includes all types within the apis package
	Builder = runtime.NewSchemeBuilder(
		v1alpha2.SchemeBuilder.AddToScheme,
	)
	// AddToScheme may be used to add all resources defined in the project to a Scheme
	AddToScheme = Builder.AddToScheme
	Settings    = []coresettings.Injectable{&settings.Settings{}}
)

//go:generate controller-gen crd object:headerFile="../../hack/boilerplate.go.txt" paths="./..." output:crd:artifacts:config=crds
var (
	//go:embed crds/karpenter.azure.com_aksnodeclasses.yaml
	AKSNodeClassCRD []byte
	CRDs            = append(apis.CRDs, lo.Must(functional.Unmarshal[v1.CustomResourceDefinition](AKSNodeClassCRD)))
)

func init() {
	lo.Must0(AddToScheme(scheme.Scheme))
}
