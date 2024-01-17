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
	"fmt"

	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	"github.com/aws/karpenter-core/pkg/test"
	"github.com/imdario/mergo"
	"github.com/samber/lo"
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
	// In reality, these default values will be set via the defaulting done by the API server. The reason we provide them here is
	// we sometimes reference a test.AKSNodeClass without applying it, and in that case we need to set the default values ourselves
	nc.Spec.OSDiskSizeGB = lo.ToPtr[int32](128)
	nc.Spec.ImageFamily = lo.ToPtr(v1alpha2.Ubuntu2204ImageFamily)
	return nc
}
