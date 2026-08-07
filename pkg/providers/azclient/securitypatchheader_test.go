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
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	. "github.com/onsi/gomega"
)

// captureHeaderPolicy is a terminal test policy that records a header value and
// short-circuits the pipeline with a synthetic response.
type captureHeaderPolicy struct {
	header string
	out    *string
}

func (c *captureHeaderPolicy) Do(req *policy.Request) (*http.Response, error) {
	*c.out = req.Raw().Header.Get(c.header)
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req.Raw()}, nil
}

func TestSecurityPatchOnlyPolicy_SetsHeader(t *testing.T) {
	g := NewWithT(t)
	req, err := runtime.NewRequest(t.Context(), http.MethodGet, "https://management.azure.com/nodeImageVersions")
	g.Expect(err).ToNot(HaveOccurred())

	var seen string
	pipeline := runtime.NewPipeline("test", "v1.0.0", runtime.PipelineOptions{
		PerCall: []policy.Policy{
			&securityPatchOnlyPolicy{},
			&captureHeaderPolicy{header: securityPatchOnlyHeader, out: &seen},
		},
	}, nil)

	_, err = pipeline.Do(req)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(seen).To(Equal("true"))
}

// TestNoSecurityPatchOnlyPolicy_HeaderAbsent guards the default path: without the policy attached the
// header must not be present, so standard node images are returned.
func TestNoSecurityPatchOnlyPolicy_HeaderAbsent(t *testing.T) {
	g := NewWithT(t)
	req, err := runtime.NewRequest(t.Context(), http.MethodGet, "https://management.azure.com/nodeImageVersions")
	g.Expect(err).ToNot(HaveOccurred())

	var seen string
	pipeline := runtime.NewPipeline("test", "v1.0.0", runtime.PipelineOptions{
		PerCall: []policy.Policy{
			&captureHeaderPolicy{header: securityPatchOnlyHeader, out: &seen},
		},
	}, nil)

	_, err = pipeline.Do(req)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(seen).To(BeEmpty())
}
