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

	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const securityPatchOnlyHeader = "SecurityPatchOnly"
const capturedImagesOnlyHeader = "CapturedImagesOnly"

var _ policy.Policy = &securityPatchOnlyPolicy{}

// securityPatchOnlyPolicy sets the SecurityPatchOnly header so the service returns node image
// versions from the security-patch lineage instead of the standard node image lineage.
// New nodes require captured images, rather than abstract targets usable only for upgrades.
type securityPatchOnlyPolicy struct{}

func (p *securityPatchOnlyPolicy) Do(req *policy.Request) (*http.Response, error) {
	capturedOnly := imagefamily.IsSecurityPatchCatalog(req.Raw().Context())
	if capturedOnly {
		req.Raw().Header.Set(securityPatchOnlyHeader, "true")
		req.Raw().Header.Set(capturedImagesOnlyHeader, "true")
	}
	resp, err := req.Next()
	if err != nil || !capturedOnly || resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, err
	}
	// Older endpoints can ignore the request header. Require acknowledgement on
	// every page so the controller can ship first and safely use standard images.
	if !strings.EqualFold(resp.Header.Get(capturedImagesOnlyHeader), "true") {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, imagefamily.ErrCapturedImagesOnlyNotAcknowledged
	}
	return resp, nil
}
