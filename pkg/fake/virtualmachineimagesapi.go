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
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/samber/lo"

	imagefamilytypes "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
)

// MarketplaceImageVersion is the default marketplace image version returned by the fake.
const MarketplaceImageVersion = "202504.08.0"

// VirtualMachineImagesAPI is a fake for listing Azure Marketplace VM image versions.
// Versions are keyed by "publisher:offer:sku"; keys without an entry fall back to
// DefaultVersions (all skus resolve to the same version list) unless DefaultVersions is empty.
type VirtualMachineImagesAPI struct {
	mu sync.RWMutex

	// Versions maps "publisher:offer:sku" to the image versions available for it.
	Versions map[string][]string
	// DefaultVersions is returned for any key not present in Versions.
	DefaultVersions []string
	Error           AtomicError
}

// assert that the fake implements the interface
var _ imagefamilytypes.VirtualMachineImagesAPI = &VirtualMachineImagesAPI{}

func (c *VirtualMachineImagesAPI) List(_ context.Context, _ string, publisherName string, offer string, skus string, _ *armcompute.VirtualMachineImagesClientListOptions) (armcompute.VirtualMachineImagesClientListResponse, error) {
	if !c.Error.IsNil() {
		return armcompute.VirtualMachineImagesClientListResponse{}, c.Error.Get()
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	versions, ok := c.Versions[fmt.Sprintf("%s:%s:%s", publisherName, offer, skus)]
	if !ok {
		versions = c.DefaultVersions
	}
	return armcompute.VirtualMachineImagesClientListResponse{
		VirtualMachineImageResourceArray: lo.Map(versions, func(version string, _ int) *armcompute.VirtualMachineImageResource {
			return &armcompute.VirtualMachineImageResource{
				Name: lo.ToPtr(version),
			}
		}),
	}, nil
}

func (c *VirtualMachineImagesAPI) SetVersions(publisher, offer, sku string, versions []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Versions == nil {
		c.Versions = map[string][]string{}
	}
	c.Versions[fmt.Sprintf("%s:%s:%s", publisher, offer, sku)] = versions
}

// Reset clears per-test overrides; DefaultVersions is construction-time configuration and is kept.
func (c *VirtualMachineImagesAPI) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Versions = nil
	c.Error.Reset()
}
