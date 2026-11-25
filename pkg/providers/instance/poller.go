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

package instance

import (
	"context"
	"errors"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// Eight retries (nine attempts) allow roughly 3-4 minutes of default SDK backoff for a throttled poll.
const provisioningPollMaxRetries = 8

func pollProvisioningUntilDone[T any](ctx context.Context, poller *runtime.Poller[T]) (T, error) {
	pollCtx := policy.WithRetryOptions(ctx, policy.RetryOptions{MaxRetries: provisioningPollMaxRetries})
	result, err := poller.PollUntilDone(pollCtx, defaultPollerOptions())
	// Poll succeeded, return
	if err == nil {
		return result, nil
	}
	// Poll failed
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return result, &LROPollingError{Err: err}
	}
	if poller.Done() {
		// Done is also true when the final GET fails. Poll on a completed
		// poller returns its cached terminal response without another HTTP request.
		// If that HTTP response matches the error in err, it means the provisioning operation failed.
		// If it DOESN'T match, it means the GET failed and we don't actually know if the operation succeeded
		// or failed.
		terminalResponse, _ := poller.Poll(pollCtx)
		var responseErr *azcore.ResponseError
		if errors.As(err, &responseErr) && responseErr.RawResponse == terminalResponse {
			return result, err
		}
	}
	return result, &LROPollingError{Err: err}
}

// defaultPollerOptions returns a PollUntilDoneOptions with a default polling frequency of 3 seconds.
// We want to poll faster than the default of 30 seconds in order to provision VMs more quickly.
// 3s seems a good compromise between responsiveness and avoiding excessive API calls.
func defaultPollerOptions() *runtime.PollUntilDoneOptions {
	return &runtime.PollUntilDoneOptions{
		Frequency: 3 * time.Second,
	}
}
