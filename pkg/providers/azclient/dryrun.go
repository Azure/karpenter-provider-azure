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

var _ policy.Policy = &dryRunPolicy{}

// dryRunPolicy is an Azure SDK per-call policy that appends the dryRun=true
// query parameter to PUT requests. This tells the AKS RP Machine API to run
// the full validation pipeline without enqueuing or persisting any state.
//
// Used by the ValidationReconciler to pre-validate Machine API templates
// against RP's real validator, catching validation drift without maintaining
// a parallel copy of RP validation rules.
type dryRunPolicy struct{}

func (p *dryRunPolicy) Do(req *policy.Request) (*http.Response, error) {
	if req.Raw().Method == http.MethodPut {
		q := req.Raw().URL.Query()
		q.Set("dryRun", "true")
		req.Raw().URL.RawQuery = q.Encode()
	}
	return req.Next()
}
