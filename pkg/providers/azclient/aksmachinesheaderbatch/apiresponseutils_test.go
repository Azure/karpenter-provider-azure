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

package aksmachinesheaderbatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =====================================================================
// Helpers
// =====================================================================

func makeResponseError(code string, statusCode int, body string) *azcore.ResponseError {
	return &azcore.ResponseError{
		ErrorCode:  code,
		StatusCode: statusCode,
		RawResponse: &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		},
	}
}

// =====================================================================
// parsePerMachineDetails — direct tests
// =====================================================================

func TestParsePerMachineDetails_BatchMachineClientError(t *testing.T) {
	t.Parallel()
	body, _ := json.Marshal(map[string]any{
		"code":    "BatchMachineClientError",
		"message": "client errors",
		"details": []map[string]any{
			{"code": "InvalidParameter", "message": "bad param", "target": "m-1"},
			{"code": "SkuNotAvailable", "message": "no sku", "target": "m-2"},
		},
	})
	respErr := makeResponseError("BatchMachineClientError", 400, string(body))

	details, err := parsePerMachineDetails(respErr)
	require.NoError(t, err)
	require.Len(t, details, 2)
	assert.Equal(t, "InvalidParameter", lo.FromPtr(details[0].Code))
	assert.Equal(t, "m-1", lo.FromPtr(details[0].Target))
	assert.Equal(t, "SkuNotAvailable", lo.FromPtr(details[1].Code))
	assert.Equal(t, "m-2", lo.FromPtr(details[1].Target))
}

func TestParsePerMachineDetails_BatchMachineInternalServerError(t *testing.T) {
	t.Parallel()
	innerJSON, _ := json.Marshal(map[string]any{
		"details": []map[string]any{
			{"code": "InternalOperationError", "message": "fail", "target": "m-1"},
		},
	})
	body, _ := json.Marshal(map[string]any{
		"code":    "BatchMachineInternalServerError",
		"message": string(innerJSON),
	})
	respErr := makeResponseError("BatchMachineInternalServerError", 500, string(body))

	details, err := parsePerMachineDetails(respErr)
	require.NoError(t, err)
	require.Len(t, details, 1)
	assert.Equal(t, "InternalOperationError", lo.FromPtr(details[0].Code))
	assert.Equal(t, "m-1", lo.FromPtr(details[0].Target))
}

func TestParsePerMachineDetails_InternalServerError_PlainTextMessage(t *testing.T) {
	t.Parallel()
	body, _ := json.Marshal(map[string]any{
		"code":    "BatchMachineInternalServerError",
		"message": "the following machines failed with InternalError: m-1",
	})
	respErr := makeResponseError("BatchMachineInternalServerError", 500, string(body))

	_, err := parsePerMachineDetails(respErr)
	require.Error(t, err, "plain text message should fail")
	assert.Contains(t, err.Error(), "failed to parse BatchMachineInternalServerError message JSON")
}

func TestParsePerMachineDetails_MalformedBody(t *testing.T) {
	t.Parallel()
	respErr := makeResponseError("BatchMachineClientError", 400, "not json")

	_, err := parsePerMachineDetails(respErr)
	require.Error(t, err)
}

func TestParsePerMachineDetails_NoRawResponse(t *testing.T) {
	t.Parallel()
	respErr := &azcore.ResponseError{ErrorCode: "BatchMachineClientError", StatusCode: 400}

	_, err := parsePerMachineDetails(respErr)
	require.Error(t, err)
}

// =====================================================================
// parseTopLevelError — direct tests
// =====================================================================

func TestParseTopLevelError_WrappedARMFormat(t *testing.T) {
	t.Parallel()
	body := `{"error":{"code":"ResourceGroupNotFound","message":"Resource group 'rg' could not be found."}}`
	respErr := makeResponseError("ResourceGroupNotFound", 404, body)

	he, err := parseTopLevelError(respErr)
	require.NoError(t, err)
	assert.Equal(t, "ResourceGroupNotFound", he.Code)
	assert.Contains(t, he.Message, "could not be found")
}

func TestParseTopLevelError_FlatAKSFormat(t *testing.T) {
	t.Parallel()
	body := `{"code":"BadRequest","message":"Agent pool 'np' is not in 'Machines' mode.","subcode":"","details":null}`
	respErr := makeResponseError("BadRequest", 400, body)

	he, err := parseTopLevelError(respErr)
	require.NoError(t, err)
	assert.Equal(t, "BadRequest", he.Code)
	assert.Contains(t, he.Message, "Machines")
}

func TestParseTopLevelError_MalformedBody(t *testing.T) {
	t.Parallel()
	respErr := makeResponseError("SomeError", 500, "not json")

	_, err := parseTopLevelError(respErr)
	require.Error(t, err)
}

func TestParseTopLevelError_NoRawResponse(t *testing.T) {
	t.Parallel()
	respErr := &azcore.ResponseError{ErrorCode: "SomeError", StatusCode: 500}

	_, err := parseTopLevelError(respErr)
	require.Error(t, err)
}

func TestParseTopLevelError_FlatWithDetails(t *testing.T) {
	t.Parallel()
	// AKS RP error with nested details — parseTopLevelError should still extract top-level code/message only
	body := `{"code":"NotFound","message":"Could not find the agentpool","details":[{"code":"Unspecified","message":"rpc error"}],"subcode":"GetAgentPool_NotFound"}`
	respErr := makeResponseError("NotFound", 404, body)

	he, err := parseTopLevelError(respErr)
	require.NoError(t, err)
	assert.Equal(t, "NotFound", he.Code)
	assert.Contains(t, he.Message, "agentpool")
}

// =====================================================================
// extractPerMachineErrors — integration tests (calls parse* internally)
// =====================================================================

func TestExtractPerMachineErrors_BatchError_EmptyDetails(t *testing.T) {
	t.Parallel()
	body, _ := json.Marshal(map[string]any{"code": "BatchMachineClientError", "message": "no details", "details": []any{}})
	err := makeResponseError("BatchMachineClientError", 400, string(body))

	perMachine := map[string]*HandlableError{"m-1": nil}
	extractErr := extractPerMachineErrors(err, perMachine)
	require.Error(t, extractErr, "empty details for batch error code should be operational error")
	assert.Contains(t, extractErr.Error(), "no per-machine details")
}

func TestExtractPerMachineErrors_BatchError_DetailMissingTarget(t *testing.T) {
	t.Parallel()
	body, _ := json.Marshal(map[string]any{
		"code":    "BatchMachineClientError",
		"message": "has detail without target",
		"details": []map[string]any{
			{"code": "InvalidParameter", "message": "bad thing"},
		},
	})
	err := makeResponseError("BatchMachineClientError", 400, string(body))

	perMachine := map[string]*HandlableError{"m-1": nil}
	extractErr := extractPerMachineErrors(err, perMachine)
	require.Error(t, extractErr, "detail without target should be operational error")
	assert.Contains(t, extractErr.Error(), "missing target")
}

func TestExtractPerMachineErrors_BatchError_DetailTargetsUnknownMachine(t *testing.T) {
	t.Parallel()
	body, _ := json.Marshal(map[string]any{
		"code":    "BatchMachineClientError",
		"message": "bad target",
		"details": []map[string]any{
			{"code": "InvalidParameter", "message": "error", "target": "unknown-machine"},
		},
	})
	err := makeResponseError("BatchMachineClientError", 400, string(body))

	perMachine := map[string]*HandlableError{"m-1": nil, "m-2": nil}
	extractErr := extractPerMachineErrors(err, perMachine)
	require.Error(t, extractErr, "detail targeting unknown machine should be operational error")
	assert.Contains(t, extractErr.Error(), "unknown-machine")
	assert.Contains(t, extractErr.Error(), "not in the batch")
}

func TestExtractPerMachineErrors_NonBatch_WrappedARMFormat(t *testing.T) {
	t.Parallel()
	body := `{"error":{"code":"ResourceGroupNotFound","message":"Resource group 'rg' could not be found."}}`
	err := makeResponseError("ResourceGroupNotFound", 404, body)

	perMachine := map[string]*HandlableError{"m-1": nil, "m-2": nil}
	extractErr := extractPerMachineErrors(err, perMachine)
	require.NoError(t, extractErr)

	for name, he := range perMachine {
		require.NotNil(t, he, "machine %s should have error", name)
		assert.Equal(t, "ResourceGroupNotFound", he.Code)
	}
}

func TestExtractPerMachineErrors_NonBatch_FlatAKSFormat(t *testing.T) {
	t.Parallel()
	body := `{"code":"BadRequest","message":"Agent pool 'np' is not in 'Machines' mode.","subcode":"","details":null}`
	err := makeResponseError("BadRequest", 400, body)

	perMachine := map[string]*HandlableError{"m-1": nil, "m-2": nil}
	extractErr := extractPerMachineErrors(err, perMachine)
	require.NoError(t, extractErr)

	for name, he := range perMachine {
		require.NotNil(t, he, "machine %s should have error", name)
		assert.Equal(t, "BadRequest", he.Code)
	}
}

func TestExtractPerMachineErrors_NonResponseError_ContextCanceled(t *testing.T) {
	t.Parallel()
	perMachine := map[string]*HandlableError{"m-1": nil}
	extractErr := extractPerMachineErrors(context.Canceled, perMachine)
	require.Error(t, extractErr)
}

func TestExtractPerMachineErrors_NonResponseError_ContextDeadlineExceeded(t *testing.T) {
	t.Parallel()
	perMachine := map[string]*HandlableError{"m-1": nil}
	extractErr := extractPerMachineErrors(context.DeadlineExceeded, perMachine)
	require.Error(t, extractErr)
}

func TestExtractPerMachineErrors_NonResponseError_PlainError(t *testing.T) {
	t.Parallel()
	perMachine := map[string]*HandlableError{"m-1": nil}
	extractErr := extractPerMachineErrors(fmt.Errorf("network error"), perMachine)
	require.Error(t, extractErr)
}
