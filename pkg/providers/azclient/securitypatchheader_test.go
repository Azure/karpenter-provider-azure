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
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	. "github.com/onsi/gomega"
)

// captureHeaderPolicy is a terminal test policy that records a header value and
// short-circuits the pipeline with a synthetic response.
type captureHeaderPolicy struct {
	header         string
	out            *string
	responseHeader string
	status         int
}

func (c *captureHeaderPolicy) Do(req *policy.Request) (*http.Response, error) {
	*c.out = req.Raw().Header.Get(c.header)
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	header := http.Header{}
	header.Set(capturedImagesOnlyHeader, c.responseHeader)
	return &http.Response{StatusCode: status, Header: header, Body: http.NoBody, Request: req.Raw()}, nil
}

func TestSecurityPatchOnlyPolicy_SetsHeader(t *testing.T) {
	g := NewWithT(t)
	req, err := runtime.NewRequest(imagefamily.WithSecurityPatchCatalog(t.Context()), http.MethodGet, "https://management.azure.com/nodeImageVersions")
	g.Expect(err).ToNot(HaveOccurred())

	var seen string
	pipeline := runtime.NewPipeline("test", "v1.0.0", runtime.PipelineOptions{
		PerCall: []policy.Policy{
			&securityPatchOnlyPolicy{},
			&captureHeaderPolicy{header: securityPatchOnlyHeader, out: &seen, responseHeader: "true"},
		},
	}, nil)

	_, err = pipeline.Do(req)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(seen).To(Equal("true"))
	g.Expect(req.Raw().Header.Get(capturedImagesOnlyHeader)).To(Equal("true"))
}

func TestCapturedCatalogAcknowledgement(t *testing.T) {
	for _, tc := range []struct {
		name, ack string
		marked    bool
		status    int
		wantError bool
	}{
		{"acknowledged", "true", true, 200, false},
		{"case insensitive", "TRUE", true, 200, false},
		{"missing", "", true, 200, true},
		{"false", "false", true, 200, true},
		{"unknown", "v2", true, 200, true},
		{"standard request", "", false, 200, false},
		{"preserve HTTP error", "", true, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := t.Context()
			if tc.marked {
				ctx = imagefamily.WithSecurityPatchCatalog(ctx)
			}
			req, err := runtime.NewRequest(ctx, http.MethodGet, "https://management.azure.com/nodeImageVersions")
			g.Expect(err).ToNot(HaveOccurred())
			var seen string
			pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{PerCall: []policy.Policy{
				&securityPatchOnlyPolicy{}, &captureHeaderPolicy{header: capturedImagesOnlyHeader, out: &seen, responseHeader: tc.ack, status: tc.status},
			}}, nil)
			resp, err := pipeline.Do(req)
			if tc.wantError {
				g.Expect(err).To(MatchError(imagefamily.ErrCapturedImagesOnlyNotAcknowledged))
				g.Expect(resp).To(BeNil())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(resp.StatusCode).To(Equal(tc.status))
			}
		})
	}
}

type capturedCatalogCredential struct{}

func (capturedCatalogCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test"}, nil
}

type capturedCatalogTransport func(*http.Request) (*http.Response, error)

func (f capturedCatalogTransport) Do(req *http.Request) (*http.Response, error) { return f(req) }

func TestCapturedCatalogRequiresAcknowledgementOnEveryPage(t *testing.T) {
	for _, acknowledgeSecond := range []bool{false, true} {
		t.Run(fmt.Sprint(acknowledgeSecond), func(t *testing.T) {
			g := NewWithT(t)
			calls := 0
			opts := &arm.ClientOptions{}
			opts.PerCallPolicies = []policy.Policy{&securityPatchOnlyPolicy{}}
			opts.Transport = capturedCatalogTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				g.Expect(req.Header.Get(capturedImagesOnlyHeader)).To(Equal("true"))
				header := http.Header{}
				if calls == 1 || acknowledgeSecond {
					header.Set(capturedImagesOnlyHeader, "true")
				}
				body := `{"value":[{"os":"AKSUbuntu","sku":"2404gen2containerd","version":"202609.23.0-2026.09.25"}]}`
				if calls == 1 {
					body = strings.TrimSuffix(body, "}") + `,"nextLink":"https://management.azure.com/next"}`
				}
				return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})
			client, err := imagefamily.NewNodeImageVersionsClient("subscription", capturedCatalogCredential{}, opts)
			g.Expect(err).ToNot(HaveOccurred())
			images, err := client.List(imagefamily.WithSecurityPatchCatalog(t.Context()), "eastus")
			g.Expect(calls).To(Equal(2))
			if acknowledgeSecond {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(images).To(HaveLen(1))
			} else {
				g.Expect(err).To(MatchError(imagefamily.ErrCapturedImagesOnlyNotAcknowledged))
				g.Expect(images).To(BeEmpty())
			}
		})
	}
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
			&securityPatchOnlyPolicy{},
			&captureHeaderPolicy{header: securityPatchOnlyHeader, out: &seen},
		},
	}, nil)

	_, err = pipeline.Do(req)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(seen).To(BeEmpty())
	g.Expect(req.Raw().Header.Get(capturedImagesOnlyHeader)).To(BeEmpty())
}
