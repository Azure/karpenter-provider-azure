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

// spotSystemNodePolicy is an Azure SDK per-call policy that appends the
// AllowSpotVMSystemNode feature flag to the AKSHTTPCustomFeatures header
// on every PUT request sent through the AKS Machines client.
//
// This appends to the existing header value rather than replacing it,
// to preserve any features already set by the SDK/infrastructure
// (e.g., UseCustomizedOSImage).
type spotSystemNodePolicy struct{}

const spotFeature = "Microsoft.ContainerService/AllowSpotVMSystemNode"

func (p *spotSystemNodePolicy) Do(req *policy.Request) (*http.Response, error) {
	if req.Raw().Method == http.MethodPut {
		if existing := req.Raw().Header.Get("AKSHTTPCustomFeatures"); existing != "" {
			req.Raw().Header.Set("AKSHTTPCustomFeatures", existing+","+spotFeature)
		} else {
			req.Raw().Header.Set("AKSHTTPCustomFeatures", spotFeature)
		}
	}
	return req.Next()
}
