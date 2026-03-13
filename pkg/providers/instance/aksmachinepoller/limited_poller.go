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

// Package aksmachinepoller provides a GET-based poller for tracking individual AKS machine
// provisioning status by polling GET machine until terminal state. This is an alternative
// to the Azure SDK poller, which polls on AKS operation objects (through GET operation).
//
// This approach works because provisioning error details and success status are derived
// from the AKS machine object itself (through the ProvisioningError field). One use case
// is batched AKS machine provisioning, where the batch coordinator sends one API call for
// N machines and gets back one SDK poller for the entire batch — it cannot track individual
// machines. Each machine needs its own poller, and polling GET machine is the most
// straightforward approach.
//
// The poller sits on top of the same SDK HTTP client, so each GET call still passes through
// the SDK pipeline's per-request retry policy. See the Options doc comment for a detailed
// comparison with the SDK poller.
//
// Note: there is a proposal to stop relying on ProvisioningError from machine objects and
// rely on AKS operation errors instead. That would require batched request error returning
// (potentially via upcoming ARM batch API) and rewriting error handling based on AKS error
// formats instead of CRP error formats. If that transition happens, this approach would
// need to be revisited.
package aksmachinepoller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
)

// LimitedPoller polls AKS machine instances until they reach a terminal state.
// This follows Azure SDK polling patterns with exponential backoff for transient errors.
// Includes a semaphore to limit concurrent GETs, protecting against thundering herd at scale.
type LimitedPoller struct {
	config              Options
	client              AKSMachineGetter
	resourceGroupName   string
	clusterName         string
	aksMachinesPoolName string
	getLimit            chan struct{} // Shared semaphore to limit concurrent GETs
}

func NewLimitedPoller(
	config Options,
	client AKSMachineGetter,
	resourceGroupName string,
	clusterName string,
	aksMachinesPoolName string,
	getLimit chan struct{},
) *LimitedPoller {
	return &LimitedPoller{
		config:              config,
		client:              client,
		resourceGroupName:   resourceGroupName,
		clusterName:         clusterName,
		aksMachinesPoolName: aksMachinesPoolName,
		getLimit:            getLimit,
	}
}

// PollUntilDone polls the AKS machine instance with GET calls until provisioning state is stabilized.
// If the provisioning is a success, returns nil. If provisioning is a failure, returns provisioning error.
// The function itself will error (second return value) only if the function is not performing as expected.
// E.g., getting a proper provisioning error from AKS machine API is the expected behavior of this function,
// this won't be considered function error.
//
// ASSUMPTION: the AKS machine creation has already begun, and is visible from the API (using GET).
func (p *LimitedPoller) PollUntilDone(ctx context.Context, aksMachineName string) (*armcontainerservice.ErrorDetail, error) {
	var retryAttemptsLeft int
	var currentRetryDelay time.Duration
	p.resetRetryState(&retryAttemptsLeft, &currentRetryDelay)

	ticker := time.NewTicker(p.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled while polling for AKS machine %q", aksMachineName)

		case <-ticker.C:
			provisioningErr, pollerErr, done := p.pollOnce(ctx, &retryAttemptsLeft, &currentRetryDelay, aksMachineName)
			if done {
				return provisioningErr, pollerErr
			}
		}
	}
}

// pollOnce performs a single GET poll and returns (provisioningErr, pollerErr, done).
func (p *LimitedPoller) pollOnce(ctx context.Context, retryAttemptsLeft *int, currentRetryDelay *time.Duration, aksMachineName string) (*armcontainerservice.ErrorDetail, error, bool) {
	aksMachine, err := p.getAKSMachine(ctx, aksMachineName)
	if err != nil {
		return p.handleGetError(ctx, err, retryAttemptsLeft, currentRetryDelay, aksMachineName)
	}

	if aksMachine.Properties == nil || aksMachine.Properties.ProvisioningState == nil {
		return p.handleNilProvisioningState(ctx, aksMachine, retryAttemptsLeft, currentRetryDelay, aksMachineName)
	}

	return p.handleProvisioningState(ctx, aksMachine, retryAttemptsLeft, currentRetryDelay, aksMachineName)
}

// handleGetError processes errors from the GET call during polling.
func (p *LimitedPoller) handleGetError(ctx context.Context, err error, retryAttemptsLeft *int, currentRetryDelay *time.Duration, aksMachineName string) (*armcontainerservice.ErrorDetail, error, bool) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("failed to get AKS machine %q during polling as context is canceled: %w", aksMachineName, err), true
	}

	if !isTransientError(err) {
		// Non-transient error (not found, auth, permissions, etc.) - fail immediately
		// Not found is possible if the AKS machine is deleted mid-way.
		// If the deletion takes time, it might appear with provisioning state "Deleting" before this can be reached.
		return nil, fmt.Errorf("failed to get AKS machine %q during polling with non-retryable error: %w", aksMachineName, err), true
	}

	log.FromContext(ctx).V(2).Info("Poller: polling for AKS machine failed to get AKS machine, may retry",
		"aksMachineName", aksMachineName,
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
	return nil, fmt.Errorf("failed to get AKS machine %q during polling: %w after exhausting %d retry attempts", aksMachineName, err, p.config.MaxRetries), true
}

// handleNilProvisioningState handles the case where the machine's provisioning state is nil.
func (p *LimitedPoller) handleNilProvisioningState(ctx context.Context, aksMachine *armcontainerservice.Machine, retryAttemptsLeft *int, currentRetryDelay *time.Duration, aksMachineName string) (*armcontainerservice.ErrorDetail, error, bool) {
	log.FromContext(ctx).V(1).Info("Poller: warning: polling for AKS machine found nil provisioning state, may retry",
		"aksMachineName", aksMachineName,
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
	return nil, fmt.Errorf("AKS machine %q sees nil provisioning state after exhausting %d retry attempts", aksMachineName, p.config.MaxRetries), true
}

// handleProvisioningState processes the machine's provisioning state and returns the appropriate action.
func (p *LimitedPoller) handleProvisioningState(ctx context.Context, aksMachine *armcontainerservice.Machine, retryAttemptsLeft *int, currentRetryDelay *time.Duration, aksMachineName string) (*armcontainerservice.ErrorDetail, error, bool) {
	provisioningState := lo.FromPtr(aksMachine.Properties.ProvisioningState)
	switch provisioningState {
	// Non-terminal state
	case consts.ProvisioningStateCreating, consts.ProvisioningStateUpdating:
		log.FromContext(ctx).V(2).Info("Poller: polling for AKS machine ongoing",
			"aksMachineName", aksMachineName,
			"aksMachineID", aksMachine.ID,
			"provisioningState", provisioningState,
		)
		// Reset retry counter on healthy non-terminal state (progress is being made)
		p.resetRetryState(retryAttemptsLeft, currentRetryDelay)
		return nil, nil, false

	// Canceled terminal state
	case consts.ProvisioningStateDeleting:
		// If polling interval is too long/deletion is too fast, then we might get 404 from GET instead of reaching here.
		return nil, fmt.Errorf("AKS machine %q sees canceled provisioning state %s", aksMachineName, provisioningState), true

	// Succeeded terminal state
	case consts.ProvisioningStateSucceeded:
		return nil, nil, true

	// Fatal terminal state
	case consts.ProvisioningStateFailed:
		if aksMachine.Properties.Status != nil && aksMachine.Properties.Status.ProvisioningError != nil {
			return aksMachine.Properties.Status.ProvisioningError, nil, true
		}
		return nil, fmt.Errorf("AKS machine %q sees fatal provisioning state %s, but ProvisioningError is nil", aksMachineName, provisioningState), true

	// Unrecognized state
	default:
		log.FromContext(ctx).V(1).Info("Poller: warning: polling for AKS machine found unrecognized provisioning state, may retry",
			"aksMachineName", aksMachineName,
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
		return nil, fmt.Errorf("AKS machine %q sees unrecognized provisioning state %s after exhausting %d retry attempts", aksMachineName, provisioningState, p.config.MaxRetries), true
	}
}

func (p *LimitedPoller) getAKSMachine(ctx context.Context, aksMachineName string) (*armcontainerservice.Machine, error) {
	// Acquire semaphore to limit concurrent GETs
	select {
	case p.getLimit <- struct{}{}:
		defer func() { <-p.getLimit }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	resp, err := p.client.Get(ctx, p.resourceGroupName, p.clusterName, p.aksMachinesPoolName, aksMachineName, nil)
	if err != nil {
		return nil, err
	}
	return lo.ToPtr(resp.Machine), nil
}

// retryWithBackoff applies exponential backoff and returns true if retry should continue, false if exhausted.
// It decrements retryAttemptsLeft, sleeps with exponential backoff, and updates currentRetryDelay.
func (p *LimitedPoller) retryWithBackoff(ctx context.Context, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (shouldRetry bool, err error) {
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
func (p *LimitedPoller) resetRetryState(retryAttemptsLeft *int, currentRetryDelay *time.Duration) {
	*retryAttemptsLeft = p.config.MaxRetries
	*currentRetryDelay = p.config.RetryDelay
}
