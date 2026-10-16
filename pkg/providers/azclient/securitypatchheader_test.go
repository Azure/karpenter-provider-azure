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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	. "github.com/onsi/gomega"
)

type captureIncludeHeaderPolicy struct {
	out *string
}

func TestOptionsForNodeImageVersions(t *testing.T) {
	g := NewWithT(t)
	original := &arm.ClientOptions{}
	configured := optionsForNodeImageVersions(original, true)

	g.Expect(configured.PerCallPolicies).To(HaveLen(1))
	g.Expect(configured.PerCallPolicies[0]).To(BeAssignableToTypeOf(&includeSecurityPatchImagesPolicy{}))
	g.Expect(original.PerCallPolicies).To(BeEmpty())

	standard := optionsForNodeImageVersions(original, false)
	g.Expect(standard.PerCallPolicies).To(BeEmpty())
}

func (c *captureIncludeHeaderPolicy) Do(req *policy.Request) (*http.Response, error) {
	*c.out = req.Raw().Header.Get(includeSecurityPatchImagesHeader)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody, Request: req.Raw()}, nil
}

func TestIncludeSecurityPatchImagesPolicy(t *testing.T) {
	g := NewWithT(t)
	req, err := runtime.NewRequest(t.Context(), http.MethodGet, "https://example.com")
	g.Expect(err).ToNot(HaveOccurred())
	var seen string
	pipeline := runtime.NewPipeline("test", "v0", runtime.PipelineOptions{PerCall: []policy.Policy{
		&includeSecurityPatchImagesPolicy{}, &captureIncludeHeaderPolicy{out: &seen},
	}}, nil)

	_, err = pipeline.Do(req)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(seen).To(Equal("true"))
}
