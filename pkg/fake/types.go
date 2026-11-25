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

package fake

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type MockedFunction[I any, O any] struct {
	Output          AtomicPtr[O]      // Output to return on call to this function
	CalledWithInput AtomicPtrStack[I] // Stack used to keep track of passed input to this function
	Error           AtomicError       // Error to return a certain number of times defined by custom error options

	successfulCalls     atomic.Int32 // Internal construct to keep track of the number of times this function has successfully been called
	failedCalls         atomic.Int32 // Internal construct to keep track of the number of times this function has failed (with error)
	customTransformerMu sync.RWMutex
	customTransformer   func(*I, func(*I) (O, error)) (O, error) // Optional hook; can modify input and call next, or return its own result or error to short-circuit
}

// SetCustomTransformer sets a wrapper that returns a per-call result or delegates to next.
// next returns configured Output when present, otherwise it calls the default transformer.
// Configured Error takes precedence over the wrapper. Wrappers may run concurrently.
func (m *MockedFunction[I, O]) SetCustomTransformer(fn func(*I, func(*I) (O, error)) (O, error)) {
	m.customTransformerMu.Lock()
	defer m.customTransformerMu.Unlock()
	m.customTransformer = fn
}

func (m *MockedFunction[I, O]) getCustomTransformer() func(*I, func(*I) (O, error)) (O, error) {
	m.customTransformerMu.RLock()
	defer m.customTransformerMu.RUnlock()
	return m.customTransformer
}

// Reset must be called between tests otherwise tests will pollute
// each other.
func (m *MockedFunction[I, O]) Reset() {
	m.Output.Reset()
	m.CalledWithInput.Reset()
	m.Error.Reset()
	m.SetCustomTransformer(nil)

	m.successfulCalls.Store(0)
	m.failedCalls.Store(0)
}

func (m *MockedFunction[I, O]) Invoke(input *I, defaultTransformer func(*I) (O, error)) (O, error) {
	m.CalledWithInput.Add(input)
	err := m.Error.Get()
	if err != nil {
		m.failedCalls.Add(1)
		return *new(O), err
	}

	next := func(input *I) (O, error) {
		if !m.Output.IsNil() {
			return *m.Output.Clone(), nil
		}
		return defaultTransformer(input)
	}
	invoke := next
	if ct := m.getCustomTransformer(); ct != nil {
		invoke = func(input *I) (O, error) {
			return ct(input, next)
		}
	}
	out, err := invoke(input)
	if err != nil {
		m.failedCalls.Add(1)
	} else {
		m.successfulCalls.Add(1)
	}
	return out, err
}

func (m *MockedFunction[I, O]) Calls() int {
	return m.SuccessfulCalls() + m.FailedCalls()
}

func (m *MockedFunction[I, O]) SuccessfulCalls() int {
	return int(m.successfulCalls.Load())
}

func (m *MockedFunction[I, O]) FailedCalls() int {
	return int(m.failedCalls.Load())
}

type MockedLRO[I any, O any] struct {
	MockedFunction[I, O]
	BeginError AtomicError // Error to return a certain number of times defined by custom error options (for Begin)

	customTransformerMu sync.RWMutex
	customTransformer   func(*I, func(*I) (*runtime.Poller[O], error)) (*runtime.Poller[O], error)
}

// SetCustomTransformer sets a concurrent wrapper around poller creation. Return an error
// directly to fail the begin call, or delegate to next to retain default polling behavior.
// BeginError and Error take precedence; next uses Output before the default transformer.
func (m *MockedLRO[I, O]) SetCustomTransformer(fn func(*I, func(*I) (*runtime.Poller[O], error)) (*runtime.Poller[O], error)) {
	m.customTransformerMu.Lock()
	defer m.customTransformerMu.Unlock()
	m.customTransformer = fn
}

func (m *MockedLRO[I, O]) getCustomTransformer() func(*I, func(*I) (*runtime.Poller[O], error)) (*runtime.Poller[O], error) {
	m.customTransformerMu.RLock()
	defer m.customTransformerMu.RUnlock()
	return m.customTransformer
}

// Reset must be called between tests otherwise tests will pollute each other.
func (m *MockedLRO[I, O]) Reset() {
	m.MockedFunction.Reset()
	m.BeginError.Reset()
	m.SetCustomTransformer(nil)
}

func (m *MockedLRO[I, O]) Invoke(input *I, defaultTransformer func(*I) (*O, error)) (*runtime.Poller[O], error) {
	m.CalledWithInput.Add(input)

	if err := m.BeginError.Get(); err != nil {
		m.failedCalls.Add(1)
		return nil, err
	}
	if err := m.Error.Get(); err != nil {
		m.failedCalls.Add(1)
		return newMockPoller[O](nil, err)
	}

	var pollErr error
	next := func(input *I) (*runtime.Poller[O], error) {
		if !m.Output.IsNil() {
			return newMockPoller(m.Output.Clone(), nil)
		}
		out, err := defaultTransformer(input)
		pollErr = err
		return newMockPoller(out, err)
	}
	invoke := next
	if ct := m.getCustomTransformer(); ct != nil {
		invoke = func(input *I) (*runtime.Poller[O], error) {
			return ct(input, next)
		}
	}
	poller, err := invoke(input)
	if err != nil || pollErr != nil {
		m.failedCalls.Add(1)
	} else {
		m.successfulCalls.Add(1)
	}
	return poller, err
}

func (m *MockedLRO[I, O]) Calls() int {
	return m.SuccessfulCalls() + m.FailedCalls()
}

func (m *MockedLRO[I, O]) SuccessfulCalls() int {
	return int(m.successfulCalls.Load())
}

func (m *MockedLRO[I, O]) FailedCalls() int {
	return int(m.failedCalls.Load())
}

// MockHandler returns a pre-defined result or error.
type MockHandler[T any] struct {
	result *T
	err    error
}

// Done returns true if the LRO has reached a terminal state. TrivialHandler is always done.
func (h MockHandler[T]) Done() bool {
	return true
}

// Poll fetches the latest state of the LRO.
func (h MockHandler[T]) Poll(context.Context) (*http.Response, error) {
	if h.err != nil {
		return nil, h.err
	}
	return nil, nil
}

// Result is called once the LRO has reached a terminal state. It populates the out parameter
// with the result of the operation.
func (h MockHandler[T]) Result(_ context.Context, result *T) error {
	if h.err != nil {
		return h.err
	}
	*result = *h.result // TODO: may need to deep copy
	return nil
}

// newMockPoller returns a poller with a mock handler that returns the given result and error.
func newMockPoller[T any](result *T, err error) (*runtime.Poller[T], error) {
	// http.Response and Pipeline are not used
	return runtime.NewPoller(nil, runtime.Pipeline{}, &runtime.NewPollerOptions[T]{
		Handler: MockHandler[T]{
			result: result,
			err:    err,
		},
		// Response: &result at the poller level is not needed, result from handler is always used
	})
}
