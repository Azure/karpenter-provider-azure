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
	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	"github.com/Azure/skewer"
	"github.com/jongio/azidext/go/azidext"
)

func NewSkuClient(subscriptionID string, cred azcore.TokenCredential, env cloud.Configuration) skewer.ResourceClient {
	resourceManagerEndpoint := env.Services[cloud.ResourceManager].Endpoint
	authorizer := azidext.NewTokenCredentialAdapter(cred, []string{auth.TokenScope(env)})
	skuClient := compute.NewResourceSkusClientWithBaseURI(resourceManagerEndpoint, subscriptionID)
	skuClient.Authorizer = authorizer
	return skuClient
}
