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

package utils

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadResponseBody_SDKConstructedError(t *testing.T) {
	t.Parallel()
	bodyContent := `{"code":"TestError","message":"test message"}`
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader([]byte(bodyContent))),
		Request:    &http.Request{URL: &url.URL{Scheme: "https", Host: "test"}},
	}

	err := runtime.NewResponseError(resp)
	var respErr *azcore.ResponseError
	require.ErrorAs(t, err, &respErr)

	body, readErr := ReadResponseBody(respErr)
	require.NoError(t, readErr)
	assert.Contains(t, string(body), "TestError")
}

func TestReadResponseBody_NilRawResponse(t *testing.T) {
	t.Parallel()
	respErr := &azcore.ResponseError{ErrorCode: "Test"}
	_, err := ReadResponseBody(respErr)
	require.Error(t, err)
}

func TestReadResponseBody_NilBody(t *testing.T) {
	t.Parallel()
	respErr := &azcore.ResponseError{
		ErrorCode:   "Test",
		RawResponse: &http.Response{Body: nil},
	}
	_, err := ReadResponseBody(respErr)
	require.Error(t, err)
}

func TestReadResponseBody_EmptyBody(t *testing.T) {
	t.Parallel()
	respErr := &azcore.ResponseError{
		ErrorCode: "Test",
		RawResponse: &http.Response{
			Body: io.NopCloser(bytes.NewReader([]byte{})),
		},
	}
	_, err := ReadResponseBody(respErr)
	require.Error(t, err)
}
