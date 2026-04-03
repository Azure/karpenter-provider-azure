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

	"dario.cat/mergo"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha1"
	opstatus "github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

const (
	DefaultAzureNodeClassImageID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/galleries/testgallery/images/testimage/versions/1.0.0"
)

func AzureNodeClass(overrides ...v1alpha1.AzureNodeClass) *v1alpha1.AzureNodeClass {
	options := v1alpha1.AzureNodeClass{}
	for _, override := range overrides {
		if err := mergo.Merge(&options, override, mergo.WithOverride); err != nil {
			panic(fmt.Sprintf("Failed to merge settings: %s", err))
		}
	}
	// Apply defaults for fields that the API server would normally default via webhooks
	if options.Spec.ImageID == nil {
		options.Spec.ImageID = lo.ToPtr(DefaultAzureNodeClassImageID)
	}
	if options.Spec.OSDiskSizeGB == nil {
		options.Spec.OSDiskSizeGB = lo.ToPtr[int32](128)
	}
	return &v1alpha1.AzureNodeClass{
		ObjectMeta: coretest.ObjectMeta(options.ObjectMeta),
		Spec:       options.Spec,
		Status:     options.Status,
	}
}

// ApplyDefaultAzureNodeClassStatus sets default status conditions on an AzureNodeClass
// for use in tests. This sets ValidationSucceeded to true and the Ready condition.
func ApplyDefaultAzureNodeClassStatus(azureNodeClass *v1alpha1.AzureNodeClass) {
	azureNodeClass.StatusConditions().SetTrue(v1alpha1.ConditionTypeValidationSucceeded)
	azureNodeClass.StatusConditions().SetTrue(opstatus.ConditionReady)

	conditions := []opstatus.Condition{}
	for _, condition := range azureNodeClass.GetConditions() {
		// Set ObservedGeneration to 1 to match the expected generation in testing,
		// same pattern as ApplyDefaultStatus for AKSNodeClass.
		condition.ObservedGeneration = 1
		conditions = append(conditions, condition)
	}
	azureNodeClass.SetConditions(conditions)
}
