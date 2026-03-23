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

package allocationstrategy

import (
	allocationstrategystages "github.com/Azure/karpenter-provider-azure/pkg/providers/allocationstrategy/stages"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type InstanceOffering = allocationstrategystages.InstanceOffering

func NewInstanceOfferings(instanceTypes []*corecloudprovider.InstanceType) []InstanceOffering {
	instanceOfferings := make([]InstanceOffering, 0, len(instanceTypes))
	for _, instanceType := range instanceTypes {
		if instanceType == nil {
			instanceOfferings = append(instanceOfferings, InstanceOffering{})
			continue
		}
		instanceOfferings = append(instanceOfferings, InstanceOffering{
			InstanceType: instanceType,
			Offerings:    append(corecloudprovider.Offerings{}, instanceType.Offerings...),
		})
	}
	return instanceOfferings
}
