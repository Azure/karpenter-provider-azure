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

import "context"

type fakeBatchEntriesKey struct{}

// WithFakeBatchEntries attaches per-machine entries to a context so the
// fake/test API client can see which machines are being created in this aksmachinesheaderbatch.
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

type fakeUseWindowsGen2VMKey struct{}

// WithFakeUseWindowsGen2VM mirrors the per-batch UseWindowsGen2VM request flag into a context so
// fakes/tests can observe it. Like WithFakeBatchEntries, this has NO production significance: the
// real Azure API reads the UseWindowsGen2VM HTTP header (set via policy.WithHTTPHeader, whose
// context key is unexported), not from this context value.
func WithFakeUseWindowsGen2VM(ctx context.Context, useWindowsGen2VM bool) context.Context {
	return context.WithValue(ctx, fakeUseWindowsGen2VMKey{}, useWindowsGen2VM)
}

// FakeUseWindowsGen2VMFromContext retrieves the mirrored UseWindowsGen2VM flag if present.
// Only used by fakes/tests — see WithFakeUseWindowsGen2VM.
func FakeUseWindowsGen2VMFromContext(ctx context.Context) bool {
	if v, ok := ctx.Value(fakeUseWindowsGen2VMKey{}).(bool); ok {
		return v
	}
	return false
}
