// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package test

import (
	"context"
	"fmt"

	"github.com/imdario/mergo"

	"github.com/aws/karpenter-core/pkg/test"

	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
)

func AKSNodeClass(overrides ...v1alpha2.AKSNodeClass) *v1alpha2.AKSNodeClass {
	options := v1alpha2.AKSNodeClass{}
	for _, override := range overrides {
		if err := mergo.Merge(&options, override, mergo.WithOverride); err != nil {
			panic(fmt.Sprintf("Failed to merge settings: %s", err))
		}
	}

	nc := &v1alpha2.AKSNodeClass{
		ObjectMeta: test.ObjectMeta(options.ObjectMeta),
		Spec:       options.Spec,
		Status:     options.Status,
	}
	nc.SetDefaults(context.Background())
	return nc
}
