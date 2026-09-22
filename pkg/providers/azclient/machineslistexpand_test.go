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
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capturingTransport struct {
	request *http.Request
}

type fakeCredential struct{}

func (fakeCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "token",
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}

func (t *capturingTransport) Do(req *http.Request) (*http.Response, error) {
	t.request = req.Clone(req.Context())
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("{}")),
		Request:    req,
	}, nil
}

func TestNewAKSMachinesClientIncludesListExpansion(t *testing.T) {
	t.Parallel()

	transport := &capturingTransport{}
	options := &arm.ClientOptions{}
	options.Transport = transport

	client, err := newAKSMachinesClient("subscription", fakeCredential{}, options)
	require.NoError(t, err)

	_, err = client.NewListPager("resource-group", "cluster", "pool", nil).NextPage(context.Background())
	require.NoError(t, err)
	require.NotNil(t, transport.request)
	assert.Equal(t, "instanceView", transport.request.URL.Query().Get("$expand"))
	assert.Empty(t, options.PerCallPolicies)
}

func TestMachinesListExpandPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		method         string
		url            string
		expectedExpand string
	}{
		{
			name:           "adds instance view expansion to a machine list request",
			method:         http.MethodGet,
			url:            "https://management.azure.com/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/cluster/agentPools/pool/machines?api-version=2026-06-02-preview",
			expectedExpand: "instanceView",
		},
		{
			name:           "adds instance view expansion to a continuation request",
			method:         http.MethodGet,
			url:            "https://management.azure.com/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/cluster/agentPools/pool/machines?api-version=2026-06-02-preview&skipToken=token",
			expectedExpand: "instanceView",
		},
		{
			name:   "does not expand a machine get request",
			method: http.MethodGet,
			url:    "https://management.azure.com/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/cluster/agentPools/pool/machines/machine?api-version=2026-06-02-preview",
		},
		{
			name:   "does not expand a machine create request",
			method: http.MethodPut,
			url:    "https://management.azure.com/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/cluster/agentPools/pool/machines?api-version=2026-06-02-preview",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &capturingTransport{}
			pipeline := runtime.NewPipeline("", "", runtime.PipelineOptions{}, &policy.ClientOptions{
				Transport:       transport,
				PerCallPolicies: []policy.Policy{&machinesListExpandPolicy{}},
			})
			req, err := runtime.NewRequest(context.Background(), tt.method, tt.url)
			require.NoError(t, err)

			_, err = pipeline.Do(req)
			require.NoError(t, err)
			require.NotNil(t, transport.request)
			assert.Equal(t, tt.expectedExpand, transport.request.URL.Query().Get("$expand"))
			assert.Equal(t, "2026-06-02-preview", transport.request.URL.Query().Get("api-version"))
		})
	}
}
