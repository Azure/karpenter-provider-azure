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
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/onsi/gomega"
	"github.com/samber/lo"
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
	g := gomega.NewWithT(t)
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
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(details).To(gomega.HaveLen(2))
	g.Expect(lo.FromPtr(details[0].Code)).To(gomega.Equal("InvalidParameter"))
	g.Expect(lo.FromPtr(details[0].Target)).To(gomega.Equal("m-1"))
	g.Expect(lo.FromPtr(details[1].Code)).To(gomega.Equal("SkuNotAvailable"))
	g.Expect(lo.FromPtr(details[1].Target)).To(gomega.Equal("m-2"))
}

func TestParsePerMachineDetails_BatchMachineInternalServerError(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
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
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(details).To(gomega.HaveLen(1))
	g.Expect(lo.FromPtr(details[0].Code)).To(gomega.Equal("InternalOperationError"))
	g.Expect(lo.FromPtr(details[0].Target)).To(gomega.Equal("m-1"))
}

func TestParsePerMachineDetails_InternalServerError_PlainTextMessage(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	body, _ := json.Marshal(map[string]any{
		"code":    "BatchMachineInternalServerError",
		"message": "the following machines failed with InternalError: m-1",
	})
	respErr := makeResponseError("BatchMachineInternalServerError", 500, string(body))

	_, err := parsePerMachineDetails(respErr)
	g.Expect(err).To(gomega.HaveOccurred(), "plain text message should fail")
	g.Expect(err.Error()).To(gomega.ContainSubstring("failed to parse BatchMachineInternalServerError message JSON"))
}

func TestParsePerMachineDetails_MalformedBody(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	respErr := makeResponseError("BatchMachineClientError", 400, "not json")

	_, err := parsePerMachineDetails(respErr)
	g.Expect(err).To(gomega.HaveOccurred())
}

func TestParsePerMachineDetails_NoRawResponse(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	respErr := &azcore.ResponseError{ErrorCode: "BatchMachineClientError", StatusCode: 400}

	_, err := parsePerMachineDetails(respErr)
	g.Expect(err).To(gomega.HaveOccurred())
}

// =====================================================================
// parseTopLevelError — direct tests
// =====================================================================

func TestParseTopLevelError_WrappedARMFormat(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	body := `{"error":{"code":"ResourceGroupNotFound","message":"Resource group 'rg' could not be found."}}`
	respErr := makeResponseError("ResourceGroupNotFound", 404, body)

	he, err := parseTopLevelError(respErr)
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(he.Code).To(gomega.Equal("ResourceGroupNotFound"))
	g.Expect(he.Message).To(gomega.ContainSubstring("could not be found"))
}

func TestParseTopLevelError_FlatAKSFormat(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	body := `{"code":"BadRequest","message":"Agent pool 'np' is not in 'Machines' mode.","subcode":"","details":null}`
	respErr := makeResponseError("BadRequest", 400, body)

	he, err := parseTopLevelError(respErr)
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(he.Code).To(gomega.Equal("BadRequest"))
	g.Expect(he.Message).To(gomega.ContainSubstring("Machines"))
}

func TestParseTopLevelError_MalformedBody(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	respErr := makeResponseError("SomeError", 500, "not json")

	_, err := parseTopLevelError(respErr)
	g.Expect(err).To(gomega.HaveOccurred())
}

func TestParseTopLevelError_NoRawResponse(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	respErr := &azcore.ResponseError{ErrorCode: "SomeError", StatusCode: 500}

	_, err := parseTopLevelError(respErr)
	g.Expect(err).To(gomega.HaveOccurred())
}

func TestParseTopLevelError_FlatWithDetails(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	body := `{"code":"NotFound","message":"Could not find the agentpool","details":[{"code":"Unspecified","message":"rpc error"}],"subcode":"GetAgentPool_NotFound"}`
	respErr := makeResponseError("NotFound", 404, body)

	he, err := parseTopLevelError(respErr)
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(he.Code).To(gomega.Equal("NotFound"))
	g.Expect(he.Message).To(gomega.ContainSubstring("agentpool"))
}

// =====================================================================
// extractPerMachineErrors — integration tests (calls parse* internally)
// =====================================================================

func TestExtractPerMachineErrors_BatchError_EmptyDetails(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	body, _ := json.Marshal(map[string]any{"code": "BatchMachineClientError", "message": "no details", "details": []any{}})
	err := makeResponseError("BatchMachineClientError", 400, string(body))

	perMachine := map[string]*offerings.HandlableError{"m-1": nil}
	extractErr := extractPerMachineErrors(err, perMachine)
	g.Expect(extractErr).To(gomega.HaveOccurred(), "empty details for batch error code should be operational error")
	g.Expect(extractErr.Error()).To(gomega.ContainSubstring("no per-machine details"))
}

func TestExtractPerMachineErrors_BatchError_DetailMissingTarget(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	body, _ := json.Marshal(map[string]any{
		"code":    "BatchMachineClientError",
		"message": "has detail without target",
		"details": []map[string]any{
			{"code": "InvalidParameter", "message": "bad thing"},
		},
	})
	err := makeResponseError("BatchMachineClientError", 400, string(body))

	perMachine := map[string]*offerings.HandlableError{"m-1": nil}
	extractErr := extractPerMachineErrors(err, perMachine)
	g.Expect(extractErr).To(gomega.HaveOccurred(), "detail without target should be operational error")
	g.Expect(extractErr.Error()).To(gomega.ContainSubstring("missing target"))
}

func TestExtractPerMachineErrors_BatchError_DetailTargetsUnknownMachine(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	body, _ := json.Marshal(map[string]any{
		"code":    "BatchMachineClientError",
		"message": "bad target",
		"details": []map[string]any{
			{"code": "InvalidParameter", "message": "error", "target": "unknown-machine"},
		},
	})
	err := makeResponseError("BatchMachineClientError", 400, string(body))

	perMachine := map[string]*offerings.HandlableError{"m-1": nil, "m-2": nil}
	extractErr := extractPerMachineErrors(err, perMachine)
	g.Expect(extractErr).To(gomega.HaveOccurred(), "detail targeting unknown machine should be operational error")
	g.Expect(extractErr.Error()).To(gomega.ContainSubstring("unknown-machine"))
	g.Expect(extractErr.Error()).To(gomega.ContainSubstring("not in the batch"))
}

func TestExtractPerMachineErrors_NonBatch_WrappedARMFormat(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	body := `{"error":{"code":"ResourceGroupNotFound","message":"Resource group 'rg' could not be found."}}`
	err := makeResponseError("ResourceGroupNotFound", 404, body)

	perMachine := map[string]*offerings.HandlableError{"m-1": nil, "m-2": nil}
	extractErr := extractPerMachineErrors(err, perMachine)
	g.Expect(extractErr).ToNot(gomega.HaveOccurred())

	for name, he := range perMachine {
		g.Expect(he).ToNot(gomega.BeNil(), "machine %s should have error", name)
		g.Expect(he.Code).To(gomega.Equal("ResourceGroupNotFound"))
	}
}

func TestExtractPerMachineErrors_NonBatch_FlatAKSFormat(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	body := `{"code":"BadRequest","message":"Agent pool 'np' is not in 'Machines' mode.","subcode":"","details":null}`
	err := makeResponseError("BadRequest", 400, body)

	perMachine := map[string]*offerings.HandlableError{"m-1": nil, "m-2": nil}
	extractErr := extractPerMachineErrors(err, perMachine)
	g.Expect(extractErr).ToNot(gomega.HaveOccurred())

	for name, he := range perMachine {
		g.Expect(he).ToNot(gomega.BeNil(), "machine %s should have error", name)
		g.Expect(he.Code).To(gomega.Equal("BadRequest"))
	}
}

func TestExtractPerMachineErrors_NonResponseError_ContextCanceled(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	perMachine := map[string]*offerings.HandlableError{"m-1": nil}
	extractErr := extractPerMachineErrors(context.Canceled, perMachine)
	g.Expect(extractErr).To(gomega.HaveOccurred())
}

func TestExtractPerMachineErrors_NonResponseError_ContextDeadlineExceeded(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	perMachine := map[string]*offerings.HandlableError{"m-1": nil}
	extractErr := extractPerMachineErrors(context.DeadlineExceeded, perMachine)
	g.Expect(extractErr).To(gomega.HaveOccurred())
}

func TestExtractPerMachineErrors_NonResponseError_PlainError(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	perMachine := map[string]*offerings.HandlableError{"m-1": nil}
	extractErr := extractPerMachineErrors(fmt.Errorf("network error"), perMachine)
	g.Expect(extractErr).To(gomega.HaveOccurred())
}
