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

package offerings

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/cache"
)

// Just a sanity check that both error handlers are kept in sync.
// More details in comments in CloudErrorHandler.
func TestErrorHandlerEquivalency(t *testing.T) {
	unavailableOfferings := cache.NewUnavailableOfferings()

	cloudHandler := NewCloudErrorHandler(unavailableOfferings)
	responseHandler := NewResponseErrorHandler(unavailableOfferings)

	if len(cloudHandler.HandlerEntries) != len(responseHandler.HandlerEntries) {
		t.Errorf("Handler entry count mismatch: CloudErrorHandler has %d entries, ResponseErrorHandler has %d entries",
			len(cloudHandler.HandlerEntries), len(responseHandler.HandlerEntries))
	}

	for i := 0; i < len(cloudHandler.HandlerEntries) && i < len(responseHandler.HandlerEntries); i++ {
		cloudHandleFunc := runtime.FuncForPC(reflect.ValueOf(cloudHandler.HandlerEntries[i].handle).Pointer())
		responseHandleFunc := runtime.FuncForPC(reflect.ValueOf(responseHandler.HandlerEntries[i].handle).Pointer())

		if cloudHandleFunc.Name() != responseHandleFunc.Name() {
			t.Errorf("Handler function mismatch at index %d: CloudErrorHandler uses %s, ResponseErrorHandler uses %s",
				i, cloudHandleFunc.Name(), responseHandleFunc.Name())
		}
	}
}
