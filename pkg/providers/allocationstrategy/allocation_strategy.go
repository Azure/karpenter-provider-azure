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
	"context"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/allocationstrategy/stages"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

type Provider interface {
	FilterInstanceOfferings(ctx context.Context, instanceOfferings []InstanceOffering, requirements scheduling.Requirements) []InstanceOffering
}

var _ Provider = &DefaultProvider{}

type DefaultProvider struct{}

func NewProvider() *DefaultProvider {
	return &DefaultProvider{}
}

func (p *DefaultProvider) FilterInstanceOfferings(ctx context.Context, instanceOfferings []InstanceOffering, requirements scheduling.Requirements) []InstanceOffering {
	stages := []stages.Stage{
		// TODO: One of these filters may need to evolve to be a "scoring" filter, which takes into account a variety of inputs (e.g. price, availability, performance)
		// to determine the best instance types to launch. They're separate stages for now because that's what we've historically been doing, but as we consider more
		// inputs re-ordering the list over and over becomes problematic (last reorder wins?).
		stages.NewAvailabilityCompatibilityFilterStage(requirements),
		stages.NewPriceSortStage(),
	}
	for _, stage := range stages {
		instanceOfferings = stage.Process(ctx, instanceOfferings)
	}
	return instanceOfferings
}
