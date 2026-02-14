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

/*
Context utilities for passing batch metadata through the call stack.
Allows downstream code to check if a request was batched and access batch info.
*/
package batch

import "context"

type batchMetadataKey struct{}
type skipBatchingKey struct{}
type fakeBatchEntriesKey struct{}

// BatchMetadata is attached to context after batch execution.
type BatchMetadata struct {
	IsBatched   bool
	MachineName string
	BatchID     string
}

// FromContext retrieves BatchMetadata if present, nil otherwise.
func FromContext(ctx context.Context) *BatchMetadata {
	if meta, ok := ctx.Value(batchMetadataKey{}).(*BatchMetadata); ok {
		return meta
	}
	return nil
}

// WithBatchMetadata attaches BatchMetadata to a context.
func WithBatchMetadata(ctx context.Context, meta *BatchMetadata) context.Context {
	return context.WithValue(ctx, batchMetadataKey{}, meta)
}

// WithSkipBatching marks a context to bypass batching (e.g., for retries).
func WithSkipBatching(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipBatchingKey{}, true)
}

// ShouldSkipBatching checks if context is marked to bypass batching.
func ShouldSkipBatching(ctx context.Context) bool {
	if skip, ok := ctx.Value(skipBatchingKey{}).(bool); ok {
		return skip
	}
	return false
}

// WithFakeBatchEntries attaches per-machine entries to a context so the
// fake/test API client can see which machines are being created in this batch.
// This has NO production significance — the real Azure API reads per-machine
// data from the BatchPutMachine HTTP header, not from context.
//
// Why this exists:
// policy.WithHTTPHeader stores the header using an unexported context key
// (internal/shared.CtxWithHTTPHeaderKey{}) and the SDK provides no public
// getter. Our fake implements the Go interface directly (no HTTP pipeline),
// so the header never materializes into an http.Request the fake could
// inspect. This context key mirrors the same []MachineEntry data so that
// in-process fakes can access it.
func WithFakeBatchEntries(ctx context.Context, entries []MachineEntry) context.Context {
	return context.WithValue(ctx, fakeBatchEntriesKey{}, entries)
}

// FakeBatchEntriesFromContext retrieves per-machine batch entries if present.
// Only used by fakes/tests — see WithFakeBatchEntries.
func FakeBatchEntriesFromContext(ctx context.Context) []MachineEntry {
	if entries, ok := ctx.Value(fakeBatchEntriesKey{}).([]MachineEntry); ok {
		return entries
	}
	return nil
}
