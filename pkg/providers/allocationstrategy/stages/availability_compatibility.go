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

package stages

import (
	"context"

	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/samber/lo"
)

type availabilityCompatibilityFilterStage struct {
	requirements scheduling.Requirements
}

func NewAvailabilityCompatibilityFilterStage(requirements scheduling.Requirements) Stage {
	return &availabilityCompatibilityFilterStage{
		requirements: requirements,
	}
}

func (s *availabilityCompatibilityFilterStage) Process(_ context.Context, instanceOfferings []InstanceOffering) []InstanceOffering {
	return lo.FilterMap(instanceOfferings, func(instanceOffering InstanceOffering, _ int) (InstanceOffering, bool) {
		instanceOffering.Offerings = instanceOffering.Offerings.Available().Compatible(s.requirements)
		return instanceOffering, len(instanceOffering.Offerings) > 0
	})
}
