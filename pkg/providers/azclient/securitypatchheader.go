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

const securityPatchOnlyHeader = "SecurityPatchOnly"

var _ policy.Policy = &securityPatchOnlyPolicy{}

// securityPatchOnlyPolicy is an Azure SDK per-call policy that sets the
// SecurityPatchOnly header on ListNodeImageVersions requests. When set, the
// service returns node image versions from the security-patch lineage instead
// of the standard node images, so clusters on the SecurityPatch node OS upgrade
// channel receive security-patched images.
//
// The policy is only attached when the cluster is on the SecurityPatch channel,
// so it can set the header unconditionally on every request the client makes.
type securityPatchOnlyPolicy struct{}

func (p *securityPatchOnlyPolicy) Do(req *policy.Request) (*http.Response, error) {
	req.Raw().Header.Set(securityPatchOnlyHeader, "true")
	return req.Next()
}
