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

package aksmachinepoller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type AKSMachineGetter interface {
	Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, options *armcontainerservice.MachinesClientGetOptions) (armcontainerservice.MachinesClientGetResponse, error)
}

// Options contains configuration for polling long-running operations.
type Options struct {
	// PollInterval is the interval between GET requests to check operation state
	PollInterval time.Duration
	// InitialRetryDelay is the initial delay before retrying a failed GET request
	InitialRetryDelay time.Duration
	// MaxRetryDelay is the maximum delay between retries (exponential backoff cap)
	MaxRetryDelay time.Duration
	// MaxRetries is the maximum number of retry attempts for transient GET errors
	MaxRetries int
}

// Provisioning state constants for AKS Machine API
const (
	ProvisioningStateCreating  = "Creating"
	ProvisioningStateUpdating  = "Updating"
	ProvisioningStateDeleting  = "Deleting"
	ProvisioningStateSucceeded = "Succeeded"
	ProvisioningStateFailed    = "Failed"
)

// Poller polls AKS machine instances until they reach a terminal state.
// This follows Azure SDK polling patterns with exponential backoff for transient errors.
type Poller struct {
	config            Options
	client            AKSMachineGetter
	resourceGroupName string
	clusterName       string
	agentPoolName     string
	aksMachineName    string
}

// Compile-time assertion that Poller implements CreatePoller
var _ CreatePoller = (*Poller)(nil)

func NewPoller(
	config Options,
	client AKSMachineGetter,
	resourceGroupName string,
	clusterName string,
	agentPoolName string,
	aksMachineName string,
) *Poller {
	return &Poller{
		config:            config,
		client:            client,
		resourceGroupName: resourceGroupName,
		clusterName:       clusterName,
		agentPoolName:     agentPoolName,
		aksMachineName:    aksMachineName,
	}
}

// PollUntilDone polls the AKS machine instance with GET calls until provisioning state is stabilized.
// If the provisioning is a success, returns nil. If provisioning is a failure, returns provisioning error.
// The function itself will error (second return value) only if the function is not performing as expected.
// E.g., getting a proper provisioning error from AKS machine API is the expected behavior of this function,
// this won't be considered function error.
//
// ASSUMPTION: the AKS machine creation has already begun, and is visible from the API (using GET).
func (p *Poller) PollUntilDone(ctx context.Context) (*armcontainerservice.ErrorDetail, error) {
	var retryAttemptsLeft int
	var currentRetryDelay time.Duration
	p.resetRetryState(&retryAttemptsLeft, &currentRetryDelay)

	// Immediate first poll for fast completion (common in tests with fakes that immediately set Succeeded state).
	if provisioningErr, pollerErr, done := p.pollOnce(ctx, &retryAttemptsLeft, &currentRetryDelay); done {
		return provisioningErr, pollerErr
	}

	ticker := time.NewTicker(p.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled while polling for AKS machine %q", p.aksMachineName)

		case <-ticker.C:
			provisioningErr, pollerErr, done := p.pollOnce(ctx, &retryAttemptsLeft, &currentRetryDelay)
			if done {
				return provisioningErr, pollerErr
			}
		}
	}
}

// pollOnce performs a single GET poll and returns (provisioningErr, pollerErr, done).
func (p *Poller) pollOnce(ctx context.Context, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (*armcontainerservice.ErrorDetail, error, bool) {
	aksMachine, err := p.getAKSMachine(ctx)
	if err != nil {
		return p.handleGetError(ctx, err, retryAttemptsLeft, currentRetryDelay)
	}

	if aksMachine.Properties == nil || aksMachine.Properties.ProvisioningState == nil {
		return p.handleNilProvisioningState(ctx, aksMachine, retryAttemptsLeft, currentRetryDelay)
	}

	return p.handleProvisioningState(ctx, aksMachine, retryAttemptsLeft, currentRetryDelay)
}

// handleGetError processes errors from the GET call during polling.
func (p *Poller) handleGetError(ctx context.Context, err error, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (*armcontainerservice.ErrorDetail, error, bool) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("failed to get AKS machine %q during polling as context is canceled: %w", p.aksMachineName, err), true
	}

	if !isTransientError(err) {
		// Non-transient error (not found, auth, permissions, etc.) - fail immediately
		// Not found is possible if the AKS machine is deleted mid-way.
		// If the deletion takes time, it might appear with provisioning state "Deleting" before this can be reached.
		return nil, fmt.Errorf("failed to get AKS machine %q during polling with non-retryable error: %w", p.aksMachineName, err), true
	}

	log.FromContext(ctx).V(2).Info("Poller: polling for AKS machine failed to get AKS machine, may retry",
		"aksMachineName", p.aksMachineName,
		"error", err,
		"retryAttemptsLeft", *retryAttemptsLeft,
		"retryDelay", *currentRetryDelay,
	)

	shouldRetry, backoffErr := p.retryWithBackoff(ctx, retryAttemptsLeft, currentRetryDelay)
	if backoffErr != nil {
		return nil, backoffErr, true
	}
	if shouldRetry {
		return nil, nil, false
	}
	return nil, fmt.Errorf("failed to get AKS machine %q during polling: %w after exhausting %d retry attempts", p.aksMachineName, err, p.config.MaxRetries), true
}

// handleNilProvisioningState handles the case where the machine's provisioning state is nil.
func (p *Poller) handleNilProvisioningState(ctx context.Context, aksMachine *armcontainerservice.Machine, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (*armcontainerservice.ErrorDetail, error, bool) {
	log.FromContext(ctx).V(1).Info("Poller: warning: polling for AKS machine found nil provisioning state, may retry",
		"aksMachineName", p.aksMachineName,
		"aksMachineID", aksMachine.ID,
		"provisioningState", nil,
		"retryAttemptsLeft", *retryAttemptsLeft,
		"retryDelay", *currentRetryDelay,
	)

	shouldRetry, backoffErr := p.retryWithBackoff(ctx, retryAttemptsLeft, currentRetryDelay)
	if backoffErr != nil {
		return nil, backoffErr, true
	}
	if shouldRetry {
		return nil, nil, false
	}
	return nil, fmt.Errorf("AKS machine %q sees nil provisioning state after exhausting %d retry attempts", p.aksMachineName, p.config.MaxRetries), true
}

// handleProvisioningState processes the machine's provisioning state and returns the appropriate action.
func (p *Poller) handleProvisioningState(ctx context.Context, aksMachine *armcontainerservice.Machine, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (*armcontainerservice.ErrorDetail, error, bool) {
	provisioningState := lo.FromPtr(aksMachine.Properties.ProvisioningState)
	switch provisioningState {
	// Non-terminal state
	case ProvisioningStateCreating, ProvisioningStateUpdating:
		log.FromContext(ctx).V(2).Info("Poller: polling for AKS machine ongoing",
			"aksMachineName", p.aksMachineName,
			"aksMachineID", aksMachine.ID,
			"provisioningState", provisioningState,
		)
		// Reset retry counter on healthy non-terminal state (progress is being made)
		p.resetRetryState(retryAttemptsLeft, currentRetryDelay)
		return nil, nil, false

	// Canceled terminal state
	case ProvisioningStateDeleting:
		// If polling interval is too long/deletion is too fast, then we might get 404 from GET instead of reaching here.
		return nil, fmt.Errorf("AKS machine %q sees canceled provisioning state %s", p.aksMachineName, provisioningState), true

	// Succeeded terminal state
	case ProvisioningStateSucceeded:
		return nil, nil, true

	// Fatal terminal state
	case ProvisioningStateFailed:
		if aksMachine.Properties.Status != nil && aksMachine.Properties.Status.ProvisioningError != nil {
			return aksMachine.Properties.Status.ProvisioningError, nil, true
		}
		return nil, fmt.Errorf("AKS machine %q sees fatal provisioning state %s, but ProvisioningError is nil", p.aksMachineName, provisioningState), true

	// Unrecognized state
	default:
		log.FromContext(ctx).V(1).Info("Poller: warning: polling for AKS machine found unrecognized provisioning state, may retry",
			"aksMachineName", p.aksMachineName,
			"aksMachineID", aksMachine.ID,
			"provisioningState", provisioningState,
			"retryAttemptsLeft", *retryAttemptsLeft,
			"retryDelay", *currentRetryDelay,
		)

		shouldRetry, backoffErr := p.retryWithBackoff(ctx, retryAttemptsLeft, currentRetryDelay)
		if backoffErr != nil {
			return nil, backoffErr, true
		}
		if shouldRetry {
			return nil, nil, false
		}
		return nil, fmt.Errorf("AKS machine %q sees unrecognized provisioning state %s after exhausting %d retry attempts", p.aksMachineName, provisioningState, p.config.MaxRetries), true
	}
}

// isTransientError determines if an error is retryable based on Azure SDK retry policy.
// Matches Azure SDK RetryOptions.StatusCodes default behavior for GET operations.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Check for Azure ResponseError with retryable status codes
	// Based on Azure SDK policy.RetryOptions default StatusCodes:
	// 408 (RequestTimeout), 429 (TooManyRequests), 500 (InternalServerError),
	// 502 (BadGateway), 503 (ServiceUnavailable), 504 (GatewayTimeout)
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.StatusCode {
		case http.StatusRequestTimeout, // 408
			http.StatusTooManyRequests,     // 429
			http.StatusInternalServerError, // 500
			http.StatusBadGateway,          // 502
			http.StatusServiceUnavailable,  // 503
			http.StatusGatewayTimeout:      // 504
			return true
		default:
			// Non-retryable status codes (e.g., 401 Unauthorized, 403 Forbidden, 404 Not Found)
			return false
		}
	}

	// Network errors, timeouts, and other transient errors should be retried
	// This catches things like temporary DNS failures, connection resets, etc.
	return true
}

func (p *Poller) getAKSMachine(ctx context.Context) (*armcontainerservice.Machine, error) {
	resp, err := p.client.Get(ctx, p.resourceGroupName, p.clusterName, p.agentPoolName, p.aksMachineName, nil)
	if err != nil {
		return nil, err
	}
	return lo.ToPtr(resp.Machine), nil
}

// retryWithBackoff applies exponential backoff and returns true if retry should continue, false if exhausted.
// It decrements retryAttemptsLeft, sleeps with exponential backoff, and updates currentRetryDelay.
func (p *Poller) retryWithBackoff(ctx context.Context, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (shouldRetry bool, err error) {
	if *retryAttemptsLeft <= 0 {
		return false, nil
	}

	*retryAttemptsLeft--

	// Apply exponential backoff before next retry
	select {
	case <-time.After(*currentRetryDelay):
		// Exponentially increase delay, capped at maxRetryDelay
		*currentRetryDelay = min(*currentRetryDelay*2, p.config.MaxRetryDelay)
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// resetRetryState returns the initial retry state values.
func (p *Poller) resetRetryState(retryAttemptsLeft *int, currentRetryDelay *time.Duration) {
	*retryAttemptsLeft = p.config.MaxRetries
	*currentRetryDelay = p.config.InitialRetryDelay
}
