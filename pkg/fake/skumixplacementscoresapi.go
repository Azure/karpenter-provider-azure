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

package fake

import (
	"context"

	armrecommender "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armrecommender"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/capacityrecommendation"
)

type SKUMixPlacementScoresPostInput struct {
	Location string
	Request  armrecommender.SKUMixPlacementRequest
	Options  *armrecommender.SKUMixPlacementScoresClientPostOptions
}

type SKUMixPlacementScoresBehavior struct {
	PostBehavior MockedFunction[SKUMixPlacementScoresPostInput, armrecommender.SKUMixPlacementScoresClientPostResponse]
}

var _ capacityrecommendation.SKUMixPlacementScoresAPI = &SKUMixPlacementScoresAPI{}

type SKUMixPlacementScoresAPI struct {
	SKUMixPlacementScoresBehavior
}

func (f *SKUMixPlacementScoresAPI) Post(_ context.Context, location string, request armrecommender.SKUMixPlacementRequest, options *armrecommender.SKUMixPlacementScoresClientPostOptions) (armrecommender.SKUMixPlacementScoresClientPostResponse, error) {
	input := &SKUMixPlacementScoresPostInput{
		Location: location,
		Request:  request,
		Options:  options,
	}
	return f.PostBehavior.Invoke(input, func(*SKUMixPlacementScoresPostInput) (armrecommender.SKUMixPlacementScoresClientPostResponse, error) {
		return armrecommender.SKUMixPlacementScoresClientPostResponse{}, nil
	})
}

func (f *SKUMixPlacementScoresAPI) Reset() {
	f.PostBehavior.Reset()
}
