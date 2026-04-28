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

	"sigs.k8s.io/controller-runtime/pkg/log"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/logging"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/allocationstrategy/stages"
)

type Provider interface {
	// Allocate selects a single instance type and offering for a NodeClaim.
	// Returns nil when no compatible offering is available.
	//
	// This interface models client-side allocation: the provider chooses one
	// concrete offering and the caller is responsible for provisioning. It
	// accommodates future client-side strategies that consult external Azure
	// APIs (e.g. placement-score, capacity advice) to inform the decision,
	// since the contract here is just "given candidates, return a choice."
	//
	// It does NOT accommodate future server-side APIs that combine decision
	// and provisioning into a single call (Fleet-like APIs): such APIs do
	// not return a separable Selection that the caller then provisions, so
	// they would replace this provider rather than implement it.
	Allocate(ctx context.Context, instanceTypes []*corecloudprovider.InstanceType, requirements scheduling.Requirements) *Selection
}

var _ Provider = &DefaultProvider{}

type DefaultProvider struct{}

func NewProvider() *DefaultProvider {
	return &DefaultProvider{}
}

func (p *DefaultProvider) Allocate(ctx context.Context, instanceTypes []*corecloudprovider.InstanceType, requirements scheduling.Requirements) *Selection {
	candidates := p.FilterInstanceOfferings(ctx, NewInstanceOfferings(instanceTypes), requirements)
	if len(candidates) == 0 {
		return nil
	}
	best := candidates[0]
	if best.InstanceType == nil || len(best.Offerings) == 0 {
		return nil
	}
	log.FromContext(ctx).Info("selected instance type", logging.InstanceType, best.InstanceType.Name)
	return &Selection{
		InstanceType: best.InstanceType,
		Offering:     best.Offerings[0],
	}
}

func (p *DefaultProvider) FilterInstanceOfferings(ctx context.Context, instanceOfferings []InstanceOffering, requirements scheduling.Requirements) []InstanceOffering {
	stages := []stages.Stage{
		stages.NewAvailabilityCompatibilityFilterStage(requirements),
		// Keep offering ranking in a single stage so future customizable allocation strategy work can swap or parameterize the ranker
		// without introducing multiple reorder stages where the last reorder wins.
		stages.NewDefaultOfferingRankStage(),
	}
	for _, stage := range stages {
		instanceOfferings = stage.Process(ctx, instanceOfferings)
	}
	return instanceOfferings
}
