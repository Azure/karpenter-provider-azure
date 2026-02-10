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
