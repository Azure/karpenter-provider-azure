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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

var _ policy.Policy = &machinesListExpandPolicy{}

// machinesListExpandPolicy adds the REST option that the generated SDK does not yet expose.
type machinesListExpandPolicy struct{}

func (p *machinesListExpandPolicy) Do(req *policy.Request) (*http.Response, error) {
	rawRequest := req.Raw()
	pathSegments := strings.Split(strings.Trim(rawRequest.URL.Path, "/"), "/")
	// CloudProvider.List currently bypasses the Machine cache. If List starts serving cached entries,
	// expand Machine GETs too so a GET fallback cannot replace an expanded entry with one missing instance-view fields.
	if rawRequest.Method == http.MethodGet &&
		len(pathSegments) >= 3 &&
		pathSegments[len(pathSegments)-1] == "machines" &&
		pathSegments[len(pathSegments)-3] == "agentPools" {
		query := rawRequest.URL.Query()
		query.Set("$expand", "instanceView")
		rawRequest.URL.RawQuery = query.Encode()
	}
	return req.Next()
}
