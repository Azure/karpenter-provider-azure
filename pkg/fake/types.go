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
	"sync/atomic"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type MockedFunction[I any, O any] struct {
	Output          AtomicPtr[O]      // Output to return on call to this function
	CalledWithInput AtomicPtrStack[I] // Stack used to keep track of passed input to this function
	Error           AtomicError       // Error to return a certain number of times defined by custom error options

	successfulCalls atomic.Int32 // Internal construct to keep track of the number of times this function has successfully been called
	failedCalls     atomic.Int32 // Internal construct to keep track of the number of times this function has failed (with error)
}

// Reset must be called between tests otherwise tests will pollute
// each other.
func (m *MockedFunction[I, O]) Reset() {
	m.Output.Reset()
	m.CalledWithInput.Reset()
	m.Error.Reset()

	m.successfulCalls.Store(0)
	m.failedCalls.Store(0)
}

func (m *MockedFunction[I, O]) Invoke(input *I, defaultTransformer func(*I) (O, error)) (O, error) {
	err := m.Error.Get()
	if err != nil {
		m.failedCalls.Add(1)
		return *new(O), err
	}
	m.CalledWithInput.Add(input)

	if !m.Output.IsNil() {
		m.successfulCalls.Add(1)
		return *m.Output.Clone(), nil
	}
	out, err := defaultTransformer(input)
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
}

// Reset must be called between tests otherwise tests will pollute each other.
func (m *MockedLRO[I, O]) Reset() {
	m.Output.Reset()
	m.CalledWithInput.Reset()
	m.BeginError.Reset()
	m.Error.Reset()

	m.successfulCalls.Store(0)
	m.failedCalls.Store(0)
}

func (m *MockedLRO[I, O]) Invoke(input *I, defaultTransformer func(*I) (*O, error)) (*runtime.Poller[O], error) {
	if err := m.BeginError.Get(); err != nil {
		m.failedCalls.Add(1)
		return nil, err
	}
	if err := m.Error.Get(); err != nil {
		m.failedCalls.Add(1)
		return newMockPoller[O](nil, err)
	}

	m.CalledWithInput.Add(input)

	if !m.Output.IsNil() {
		m.successfulCalls.Add(1)
		return newMockPoller(m.Output.Clone(), nil)
	}
	out, err := defaultTransformer(input)
	if err != nil {
		m.failedCalls.Add(1)
	} else {
		m.successfulCalls.Add(1)
	}
	return newMockPoller(out, err)
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
