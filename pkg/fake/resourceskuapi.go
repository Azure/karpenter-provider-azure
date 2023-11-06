// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fake

import (
	"context"

	//nolint SA1019 - deprecated package
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2022-08-01/compute"
	"github.com/Azure/skewer"
)

// TODO: consider using fakes from skewer itself

type MockSkuClientSingleton struct {
	SKUClient *ResourceSKUsAPI
}

func (sc *MockSkuClientSingleton) GetInstance() skewer.ResourceClient {
	return sc.SKUClient
}

func (sc *MockSkuClientSingleton) Reset() {
	sc.SKUClient.Reset()
}

// assert that the fake implements the interface
var _ skewer.ResourceClient = &ResourceSKUsAPI{}

type ResourceSKUsAPI struct {
	// skewer.ResourceClient
}

// Reset must be called between tests otherwise tests will pollute each other.
func (s *ResourceSKUsAPI) Reset() {
	//c.ResourceSKUsBehavior.Reset()
}

func (s *ResourceSKUsAPI) ListComplete(_ context.Context, _, _ string) (compute.ResourceSkusResultIterator, error) {
	return compute.NewResourceSkusResultIterator(
		compute.NewResourceSkusResultPage(
			// cur
			compute.ResourceSkusResult{
				Value: &ResourceSkus,
			},
			// fn
			func(ctx context.Context, result compute.ResourceSkusResult) (compute.ResourceSkusResult, error) {
				return compute.ResourceSkusResult{
					Value:    nil, // end of iterator
					NextLink: nil,
				}, nil
			},
		),
	), nil
}
