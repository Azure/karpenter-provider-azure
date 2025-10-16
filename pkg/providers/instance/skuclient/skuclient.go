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

package skuclient

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/skewer/v2"
)

func NewSkuClient(subscriptionID string, cred azcore.TokenCredential, opts *arm.ClientOptions) (skewer.ResourceClient, error) {
	skuClient, err := armcompute.NewResourceSKUsClient(subscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}
	// authorizer := azidext.NewTokenCredentialAdapter(cred, []string{auth.TokenScope(env)})
	return skuClient, nil
}
