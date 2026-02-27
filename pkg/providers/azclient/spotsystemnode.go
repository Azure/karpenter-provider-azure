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

package azclient

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

var _ policy.Policy = &spotSystemNodePolicy{}

// spotSystemNodePolicy is an Azure SDK per-call policy that adds the
// AKSHTTPCustomFeatures header on every PUT request sent through the
// AKS Machines client, enabling AKS-side feature flags.
//
// Features:
//   - UseCustomizedOSImage: Required for custom OS image support. This is
//     normally set later behind the scenes by the SDK/infrastructure, but our policy
//     uses Header.Set which replaces the entire header value, unintentionally
//     dropping it. Re-added here to preserve it alongside our custom feature.
//   - AllowSpotVMSystemNode: Required for spot VM support on system node pools.
type spotSystemNodePolicy struct{}

func (p *spotSystemNodePolicy) Do(req *policy.Request) (*http.Response, error) {
	if req.Raw().Method == http.MethodPut {
		req.Raw().Header.Set("AKSHTTPCustomFeatures",
			"Microsoft.ContainerService/UseCustomizedOSImage,Microsoft.ContainerService/AllowSpotVMSystemNode")
	}
	return req.Next()
}
