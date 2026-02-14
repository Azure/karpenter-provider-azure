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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient"
	"github.com/samber/lo"
)

type DiskEncryptionSetsAPI struct {
	GetFunc func(
		ctx context.Context,
		resourceGroupName string,
		diskEncryptionSetName string,
		options *armcompute.DiskEncryptionSetsClientGetOptions,
	) (armcompute.DiskEncryptionSetsClientGetResponse, error)
}

var _ azclient.DiskEncryptionSetsAPI = &DiskEncryptionSetsAPI{}

func (d *DiskEncryptionSetsAPI) Get(
	ctx context.Context,
	resourceGroupName string,
	diskEncryptionSetName string,
	options *armcompute.DiskEncryptionSetsClientGetOptions,
) (armcompute.DiskEncryptionSetsClientGetResponse, error) {
	if d.GetFunc != nil {
		return d.GetFunc(ctx, resourceGroupName, diskEncryptionSetName, options)
	}
	// Default: return success as if the DES exists and is accessible
	return armcompute.DiskEncryptionSetsClientGetResponse{
		DiskEncryptionSet: armcompute.DiskEncryptionSet{
			Name:     lo.ToPtr(diskEncryptionSetName),
			Location: lo.ToPtr("eastus"),
		},
	}, nil
}

func (d *DiskEncryptionSetsAPI) Reset() {
	d.GetFunc = nil
}
