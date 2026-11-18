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

// Tests verifying that the fake's batch error behavior adheres to the batch error
// contract defined in design doc 0010-aks-machines-batch-creation.md.
//
// The batch API returns per-machine errors in two formats:
//   - BatchMachineClientError (400): details[] at top level
//   - BatchMachineInternalServerError (500): details[] JSON-encoded in message field
//
// These tests cover all error combinations: partial failures, all-fail, mixed
// client+internal, single non-batch error, and all-success.

package fake

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/aksmachinesheaderbatch"
	"github.com/onsi/gomega"
	"github.com/samber/lo"
)

// These are based on the contract defined in design doc 0010-aks-machines-batch-creation.md.

func setupBatchFake() (*AKSMachinesAPI, context.Context) {
	storage := NewAKSDataStorage()
	storage.AgentPools.Store(MkAgentPoolID("rg", "cluster", "pool"), armcontainerservice.AgentPool{})
	return NewAKSMachinesAPI(storage), context.Background()
}

func callBatch(t *testing.T, api *AKSMachinesAPI, ctx context.Context, names []string) error {
	t.Helper()
	entries := make([]aksmachinesheaderbatch.MachineEntry, len(names))
	for i, n := range names {
		entries[i] = aksmachinesheaderbatch.MachineEntry{MachineName: n, Zones: []string{"1"}}
	}
	ctx = aksmachinesheaderbatch.WithFakeBatchEntries(ctx, entries)
	template := armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: lo.ToPtr("Standard_D4s_v3")},
		},
	}
	_, err := api.BeginCreateOrUpdate(ctx, "rg", "cluster", "pool", names[0], template, nil)
	return err
}

func requireBatchClientError(t *testing.T, err error) (targets map[string]string) {
	t.Helper()
	g := gomega.NewWithT(t)

	var respErr *azcore.ResponseError
	g.Expect(errors.As(err, &respErr)).To(gomega.BeTrue())

	g.Expect(respErr.ErrorCode).To(gomega.Equal("BatchMachineClientError"))
	g.Expect(respErr.StatusCode).To(gomega.Equal(400))

	body, _ := io.ReadAll(respErr.RawResponse.Body)
	var parsed struct {
		Code    string `json:"code"`
		Details []struct {
			Code   string `json:"code"`
			Target string `json:"target"`
		} `json:"details"`
	}
	g.Expect(json.Unmarshal(body, &parsed)).ToNot(gomega.HaveOccurred())

	g.Expect(parsed.Code).To(gomega.Equal("BatchMachineClientError"))

	targets = map[string]string{}
	for _, d := range parsed.Details {
		targets[d.Target] = d.Code
	}
	return targets
}

func requireBatchInternalServerError(t *testing.T, err error) (targets map[string]string) {
	t.Helper()
	g := gomega.NewWithT(t)

	var respErr *azcore.ResponseError
	g.Expect(errors.As(err, &respErr)).To(gomega.BeTrue())

	g.Expect(respErr.ErrorCode).To(gomega.Equal("BatchMachineInternalServerError"))
	g.Expect(respErr.StatusCode).To(gomega.Equal(500))

	body, _ := io.ReadAll(respErr.RawResponse.Body)
	var topLevel struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details"`
	}
	g.Expect(json.Unmarshal(body, &topLevel)).ToNot(gomega.HaveOccurred())

	g.Expect(topLevel.Code).To(gomega.Equal("BatchMachineInternalServerError"))
	g.Expect(topLevel.Details == nil || string(topLevel.Details) == "null").To(gomega.BeTrue(),
		"InternalServerError must have no top-level details")

	var inner struct {
		Details []struct {
			Code   string `json:"code"`
			Target string `json:"target"`
		} `json:"details"`
	}
	g.Expect(json.Unmarshal([]byte(topLevel.Message), &inner)).ToNot(gomega.HaveOccurred(),
		"message must be valid JSON with details[]")

	targets = map[string]string{}
	for _, d := range inner.Details {
		targets[d.Target] = d.Code
	}
	return targets
}

func assertMachineExists(t *testing.T, api *AKSMachinesAPI, ctx context.Context, name string) {
	t.Helper()
	g := gomega.NewWithT(t)
	_, err := api.Get(ctx, "rg", "cluster", "pool", name, nil)
	g.Expect(err).ToNot(gomega.HaveOccurred(), "machine %q should exist in storage", name)
}

func assertMachineNotExists(t *testing.T, api *AKSMachinesAPI, ctx context.Context, name string) {
	t.Helper()
	g := gomega.NewWithT(t)
	_, err := api.Get(ctx, "rg", "cluster", "pool", name, nil)
	g.Expect(err).To(gomega.HaveOccurred(), "machine %q should NOT exist in storage", name)
}

// =====================================================================
// Batch error scenarios
// =====================================================================

// 1 client error + 2 valid → 400 BatchMachineClientError, 1 detail, successes omitted
func TestBatchFake_1ClientError_2Valid(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	api, ctx := setupBatchFake()
	api.BatchMachineErrorFunc = func(n string) (string, string) {
		if n == "m-bad" {
			return "InvalidParameter", "simulated client error for machine m-bad"
		}
		return "", ""
	}

	err := callBatch(t, api, ctx, []string{"m-ok1", "m-bad", "m-ok2"})
	g.Expect(err).To(gomega.HaveOccurred())

	targets := requireBatchClientError(t, err)
	g.Expect(targets).To(gomega.Equal(map[string]string{"m-bad": "InvalidParameter"}))

	assertMachineExists(t, api, ctx, "m-ok1")
	assertMachineExists(t, api, ctx, "m-ok2")
	assertMachineNotExists(t, api, ctx, "m-bad")
}

// 1 internal error + 2 valid → 500 BatchMachineInternalServerError, 1 detail, successes omitted
func TestBatchFake_1InternalError_2Valid(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	api, ctx := setupBatchFake()
	api.BatchMachineErrorFunc = func(n string) (string, string) {
		if n == "m-bad" {
			return "InternalOperationError", "simulated internal error for machine m-bad"
		}
		return "", ""
	}

	err := callBatch(t, api, ctx, []string{"m-ok1", "m-bad", "m-ok2"})
	g.Expect(err).To(gomega.HaveOccurred())

	targets := requireBatchInternalServerError(t, err)
	g.Expect(targets).To(gomega.Equal(map[string]string{"m-bad": "InternalOperationError"}))

	assertMachineExists(t, api, ctx, "m-ok1")
	assertMachineExists(t, api, ctx, "m-ok2")
	assertMachineNotExists(t, api, ctx, "m-bad")
}

// 2 client errors + 1 valid → 400 BatchMachineClientError, 2 details
func TestBatchFake_2ClientErrors_1Valid(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	api, ctx := setupBatchFake()
	api.BatchMachineErrorFunc = func(n string) (string, string) {
		if n == "m-bad1" || n == "m-bad2" {
			return "InvalidParameter", "simulated client error for machine " + n
		}
		return "", ""
	}

	err := callBatch(t, api, ctx, []string{"m-ok", "m-bad1", "m-bad2"})
	g.Expect(err).To(gomega.HaveOccurred())

	targets := requireBatchClientError(t, err)
	g.Expect(targets).To(gomega.HaveLen(2))
	g.Expect(targets["m-bad1"]).To(gomega.Equal("InvalidParameter"))
	g.Expect(targets["m-bad2"]).To(gomega.Equal("InvalidParameter"))
	g.Expect(targets).ToNot(gomega.HaveKey("m-ok"))

	assertMachineExists(t, api, ctx, "m-ok")
	assertMachineNotExists(t, api, ctx, "m-bad1")
	assertMachineNotExists(t, api, ctx, "m-bad2")
}

// 2 internal errors + 1 valid → 500 BatchMachineInternalServerError, 2 details
func TestBatchFake_2InternalErrors_1Valid(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	api, ctx := setupBatchFake()
	api.BatchMachineErrorFunc = func(n string) (string, string) {
		if n == "m-bad1" || n == "m-bad2" {
			return "InternalOperationError", "simulated internal error for machine " + n
		}
		return "", ""
	}

	err := callBatch(t, api, ctx, []string{"m-ok", "m-bad1", "m-bad2"})
	g.Expect(err).To(gomega.HaveOccurred())

	targets := requireBatchInternalServerError(t, err)
	g.Expect(targets).To(gomega.HaveLen(2))
	g.Expect(targets["m-bad1"]).To(gomega.Equal("InternalOperationError"))
	g.Expect(targets["m-bad2"]).To(gomega.Equal("InternalOperationError"))
	g.Expect(targets).ToNot(gomega.HaveKey("m-ok"))

	assertMachineExists(t, api, ctx, "m-ok")
}

// 2 client + 2 internal → 400 BatchMachineClientError (client error presence wins), all 4 in details
func TestBatchFake_2Client_2Internal_AllFail(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	api, ctx := setupBatchFake()
	api.BatchMachineErrorFunc = func(n string) (string, string) {
		switch n {
		case "m-client1", "m-client2":
			return "InvalidParameter", "simulated client error for machine " + n
		case "m-internal1", "m-internal2":
			return "InternalOperationError", "simulated internal error for machine " + n
		}
		return "", ""
	}

	err := callBatch(t, api, ctx, []string{"m-client1", "m-internal1", "m-client2", "m-internal2"})
	g.Expect(err).To(gomega.HaveOccurred())

	targets := requireBatchClientError(t, err)
	g.Expect(targets).To(gomega.HaveLen(4), "all 4 errors in details")
	g.Expect(targets["m-client1"]).To(gomega.Equal("InvalidParameter"))
	g.Expect(targets["m-client2"]).To(gomega.Equal("InvalidParameter"))
	g.Expect(targets["m-internal1"]).To(gomega.Equal("InternalOperationError"))
	g.Expect(targets["m-internal2"]).To(gomega.Equal("InternalOperationError"))

	for _, n := range []string{"m-client1", "m-internal1", "m-client2", "m-internal2"} {
		assertMachineNotExists(t, api, ctx, n)
	}
}

// 2 client + 1 internal → 400 BatchMachineClientError, all 3 in details
func TestBatchFake_2Client_1Internal_AllFail(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	api, ctx := setupBatchFake()
	api.BatchMachineErrorFunc = func(n string) (string, string) {
		switch n {
		case "m-client1", "m-client2":
			return "InvalidParameter", "simulated client error for machine " + n
		case "m-internal":
			return "InternalOperationError", "simulated internal error for machine " + n
		}
		return "", ""
	}

	err := callBatch(t, api, ctx, []string{"m-client1", "m-internal", "m-client2"})
	g.Expect(err).To(gomega.HaveOccurred())

	targets := requireBatchClientError(t, err)
	g.Expect(targets).To(gomega.HaveLen(3))
	g.Expect(targets["m-client1"]).To(gomega.Equal("InvalidParameter"))
	g.Expect(targets["m-internal"]).To(gomega.Equal("InternalOperationError"))
	g.Expect(targets["m-client2"]).To(gomega.Equal("InvalidParameter"))
}

// Single machine error, no batch header → direct error (not wrapped in batch error code)
func TestBatchFake_SingleError_NoBatchHeader(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	api, ctx := setupBatchFake()

	api.AKSMachineCreateOrUpdateBehavior.BeginError.Set(AKSMachineAPIErrorFromAKSMachineImmutablePropertyChangeAttempted)

	template := armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: lo.ToPtr("Standard_D4s_v3")},
		},
	}

	// No batch entries in context → single machine path
	_, err := api.BeginCreateOrUpdate(ctx, "rg", "cluster", "pool", "m-1", template, nil)
	g.Expect(err).To(gomega.HaveOccurred())

	var respErr *azcore.ResponseError
	g.Expect(errors.As(err, &respErr)).To(gomega.BeTrue())
	g.Expect(respErr.ErrorCode).ToNot(gomega.Equal("BatchMachineClientError"),
		"non-batch error should NOT be wrapped in batch error code")
	g.Expect(respErr.ErrorCode).ToNot(gomega.Equal("BatchMachineInternalServerError"),
		"non-batch error should NOT be wrapped in batch error code")
}

// All success → nil error, all machines created
func TestBatchFake_AllSuccess(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	api, ctx := setupBatchFake()

	err := callBatch(t, api, ctx, []string{"m-1", "m-2", "m-3"})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	assertMachineExists(t, api, ctx, "m-1")
	assertMachineExists(t, api, ctx, "m-2")
	assertMachineExists(t, api, ctx, "m-3")
}

// 3 internal errors → 500 BatchMachineInternalServerError, all 3 in details
func TestBatchFake_3InternalErrors_AllFail(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	api, ctx := setupBatchFake()
	api.BatchMachineErrorFunc = func(n string) (string, string) {
		switch n {
		case "m-1", "m-2", "m-3":
			return "InternalOperationError", "simulated internal error for machine " + n
		}
		return "", ""
	}

	err := callBatch(t, api, ctx, []string{"m-1", "m-2", "m-3"})
	g.Expect(err).To(gomega.HaveOccurred())

	targets := requireBatchInternalServerError(t, err)
	g.Expect(targets).To(gomega.HaveLen(3))
	g.Expect(targets["m-1"]).To(gomega.Equal("InternalOperationError"))
	g.Expect(targets["m-2"]).To(gomega.Equal("InternalOperationError"))
	g.Expect(targets["m-3"]).To(gomega.Equal("InternalOperationError"))

	for _, n := range []string{"m-1", "m-2", "m-3"} {
		assertMachineNotExists(t, api, ctx, n)
	}
}
