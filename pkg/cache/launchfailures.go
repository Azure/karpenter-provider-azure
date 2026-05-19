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

package cache

import (
	"context"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// LaunchFailureBackoffBaseDuration is the initial backoff duration after the second consecutive failure.
	LaunchFailureBackoffBaseDuration = 1 * time.Minute
	// LaunchFailureBackoffMaxDuration caps the backoff to prevent excessively long pauses.
	LaunchFailureBackoffMaxDuration = 30 * time.Minute
	// LaunchFailureMinConsecutiveForBackoff is the number of consecutive failures before backoff kicks in.
	// The first failure is tolerated to handle transient errors.
	LaunchFailureMinConsecutiveForBackoff = 2
)

// launchFailureBackoffSchedule defines the backoff duration for each consecutive failure count.
// Index 0 = 2nd failure (first that triggers backoff), Index 1 = 3rd failure, etc.
// Beyond the end of this slice, LaunchFailureBackoffMaxDuration is used.
var launchFailureBackoffSchedule = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
}

// nodePoolFailureState tracks the failure state for a single NodePool.
type nodePoolFailureState struct {
	consecutiveFailures int
	backoffUntil        time.Time
}

// LaunchFailureTracker tracks consecutive launch failures per NodePool and provides
// exponential backoff to prevent runaway provisioning when nodes consistently fail
// to register (e.g., due to CSE failures from network misconfigurations).
//
// This addresses the scenario where Karpenter enters an infinite retry loop:
// pod pending → create NodeClaim → VM created → CSE fails → delete NodeClaim →
// pod pending again → repeat forever, each cycle costing ~10 minutes of VM compute.
//
// The tracker is in-memory only. On restart, all backoff state is cleared, which is
// acceptable because a restart itself provides a natural cooldown period.
type LaunchFailureTracker struct {
	mu     sync.RWMutex
	states map[string]*nodePoolFailureState // key: nodepool name
}

// NewLaunchFailureTracker creates a new LaunchFailureTracker.
func NewLaunchFailureTracker() *LaunchFailureTracker {
	return &LaunchFailureTracker{
		states: make(map[string]*nodePoolFailureState),
	}
}

// IsBackedOff returns true if the given NodePool is currently in a backoff period
// due to consecutive launch failures. When true, provisioning should not be attempted.
func (t *LaunchFailureTracker) IsBackedOff(nodePoolName string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	state, exists := t.states[nodePoolName]
	if !exists {
		return false
	}
	return time.Now().Before(state.backoffUntil)
}

// BackoffRemaining returns the remaining backoff duration for a NodePool, or 0 if not backed off.
func (t *LaunchFailureTracker) BackoffRemaining(nodePoolName string) time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	state, exists := t.states[nodePoolName]
	if !exists {
		return 0
	}
	remaining := time.Until(state.backoffUntil)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// RecordLaunchFailure records a launch failure for the given NodePool and starts or
// extends the backoff period. The first failure is tolerated (no backoff).
// Subsequent consecutive failures trigger exponential backoff:
//
//	2nd failure: 1 minute
//	3rd failure: 5 minutes
//	4th failure: 15 minutes
//	5th+ failure: 30 minutes (cap)
func (t *LaunchFailureTracker) RecordLaunchFailure(ctx context.Context, nodePoolName string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, exists := t.states[nodePoolName]
	if !exists {
		state = &nodePoolFailureState{}
		t.states[nodePoolName] = state
	}
	state.consecutiveFailures++

	if state.consecutiveFailures < LaunchFailureMinConsecutiveForBackoff {
		log.FromContext(ctx).V(1).Info("launch failure recorded, tolerating as transient",
			"nodepool", nodePoolName,
			"consecutiveFailures", state.consecutiveFailures)
		return
	}

	backoffDuration := t.backoffDurationForFailureCount(state.consecutiveFailures)
	state.backoffUntil = time.Now().Add(backoffDuration)

	log.FromContext(ctx).Info("launch failure backoff activated",
		"nodepool", nodePoolName,
		"consecutiveFailures", state.consecutiveFailures,
		"backoffDuration", backoffDuration,
		"backoffUntil", state.backoffUntil)
}

// RecordLaunchSuccess clears the failure state for the given NodePool.
// Any successful node registration proves the NodePool can provision successfully.
func (t *LaunchFailureTracker) RecordLaunchSuccess(ctx context.Context, nodePoolName string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, exists := t.states[nodePoolName]
	if !exists || state.consecutiveFailures == 0 {
		return
	}

	log.FromContext(ctx).Info("launch failure backoff cleared on success",
		"nodepool", nodePoolName,
		"previousConsecutiveFailures", state.consecutiveFailures)

	delete(t.states, nodePoolName)
}

// ResetNodePool clears the failure state for a given NodePool. Called when the
// NodePool or NodeClass spec changes, because the user may be fixing the issue.
func (t *LaunchFailureTracker) ResetNodePool(ctx context.Context, nodePoolName string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.states[nodePoolName]; exists {
		log.FromContext(ctx).V(1).Info("launch failure backoff reset due to spec change",
			"nodepool", nodePoolName)
		delete(t.states, nodePoolName)
	}
}

// ConsecutiveFailures returns the current consecutive failure count for a NodePool.
// Returns 0 if no failures have been recorded.
func (t *LaunchFailureTracker) ConsecutiveFailures(nodePoolName string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	state, exists := t.states[nodePoolName]
	if !exists {
		return 0
	}
	return state.consecutiveFailures
}

// backoffDurationForFailureCount returns the backoff duration for a given consecutive failure count.
func (t *LaunchFailureTracker) backoffDurationForFailureCount(consecutiveFailures int) time.Duration {
	// Index into schedule: 2nd failure = index 0, 3rd = index 1, etc.
	scheduleIndex := consecutiveFailures - LaunchFailureMinConsecutiveForBackoff
	if scheduleIndex < 0 {
		return 0
	}
	if scheduleIndex >= len(launchFailureBackoffSchedule) {
		return LaunchFailureBackoffMaxDuration
	}
	return launchFailureBackoffSchedule[scheduleIndex]
}
