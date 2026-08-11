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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLaunchFailureTracker_FirstFailureIsToleratedNoBackoff(t *testing.T) {
	tracker := NewLaunchFailureTracker()
	ctx := context.Background()

	tracker.RecordLaunchFailure(ctx, "pool-1")

	assert.False(t, tracker.IsBackedOff("pool-1"), "first failure should not trigger backoff")
	assert.Equal(t, 1, tracker.ConsecutiveFailures("pool-1"))
}

func TestLaunchFailureTracker_SecondFailureTriggersBackoff(t *testing.T) {
	tracker := NewLaunchFailureTracker()
	ctx := context.Background()

	tracker.RecordLaunchFailure(ctx, "pool-1")
	tracker.RecordLaunchFailure(ctx, "pool-1")

	assert.True(t, tracker.IsBackedOff("pool-1"), "second failure should trigger backoff")
	assert.Equal(t, 2, tracker.ConsecutiveFailures("pool-1"))

	remaining := tracker.BackoffRemaining("pool-1")
	assert.Greater(t, remaining, time.Duration(0), "should have positive remaining backoff")
	assert.LessOrEqual(t, remaining, 1*time.Minute, "should be at most 1 minute for 2nd failure")
}

func TestLaunchFailureTracker_BackoffEscalates(t *testing.T) {
	tracker := NewLaunchFailureTracker()
	ctx := context.Background()

	// 1st failure: no backoff
	tracker.RecordLaunchFailure(ctx, "pool-1")
	assert.False(t, tracker.IsBackedOff("pool-1"))

	// 2nd failure: 1 min backoff
	tracker.RecordLaunchFailure(ctx, "pool-1")
	remaining2 := tracker.BackoffRemaining("pool-1")

	// 3rd failure: 5 min backoff
	tracker.RecordLaunchFailure(ctx, "pool-1")
	remaining3 := tracker.BackoffRemaining("pool-1")
	assert.Greater(t, remaining3, remaining2, "backoff should escalate")

	// 4th failure: 15 min backoff
	tracker.RecordLaunchFailure(ctx, "pool-1")
	remaining4 := tracker.BackoffRemaining("pool-1")
	assert.Greater(t, remaining4, remaining3, "backoff should continue escalating")

	// 5th failure: 30 min cap
	tracker.RecordLaunchFailure(ctx, "pool-1")
	remaining5 := tracker.BackoffRemaining("pool-1")
	assert.LessOrEqual(t, remaining5, LaunchFailureBackoffMaxDuration, "backoff should be capped at max")

	// 6th failure: still 30 min cap
	tracker.RecordLaunchFailure(ctx, "pool-1")
	remaining6 := tracker.BackoffRemaining("pool-1")
	assert.LessOrEqual(t, remaining6, LaunchFailureBackoffMaxDuration, "backoff should remain at cap")
}

func TestLaunchFailureTracker_SuccessClearsBackoff(t *testing.T) {
	tracker := NewLaunchFailureTracker()
	ctx := context.Background()

	// Build up failures
	tracker.RecordLaunchFailure(ctx, "pool-1")
	tracker.RecordLaunchFailure(ctx, "pool-1")
	tracker.RecordLaunchFailure(ctx, "pool-1")
	assert.True(t, tracker.IsBackedOff("pool-1"))

	// Success clears everything
	tracker.RecordLaunchSuccess(ctx, "pool-1")
	assert.False(t, tracker.IsBackedOff("pool-1"))
	assert.Equal(t, 0, tracker.ConsecutiveFailures("pool-1"))
}

func TestLaunchFailureTracker_ResetClearsBackoff(t *testing.T) {
	tracker := NewLaunchFailureTracker()
	ctx := context.Background()

	tracker.RecordLaunchFailure(ctx, "pool-1")
	tracker.RecordLaunchFailure(ctx, "pool-1")
	assert.True(t, tracker.IsBackedOff("pool-1"))

	tracker.ResetNodePool(ctx, "pool-1")
	assert.False(t, tracker.IsBackedOff("pool-1"))
	assert.Equal(t, 0, tracker.ConsecutiveFailures("pool-1"))
}

func TestLaunchFailureTracker_IsolatedPerNodePool(t *testing.T) {
	tracker := NewLaunchFailureTracker()
	ctx := context.Background()

	// Fail pool-1 twice
	tracker.RecordLaunchFailure(ctx, "pool-1")
	tracker.RecordLaunchFailure(ctx, "pool-1")

	// pool-2 should be unaffected
	assert.False(t, tracker.IsBackedOff("pool-2"))
	assert.Equal(t, 0, tracker.ConsecutiveFailures("pool-2"))
	assert.True(t, tracker.IsBackedOff("pool-1"))
}

func TestLaunchFailureTracker_UnknownPoolNotBackedOff(t *testing.T) {
	tracker := NewLaunchFailureTracker()

	assert.False(t, tracker.IsBackedOff("nonexistent"))
	assert.Equal(t, time.Duration(0), tracker.BackoffRemaining("nonexistent"))
	assert.Equal(t, 0, tracker.ConsecutiveFailures("nonexistent"))
}

func TestLaunchFailureTracker_SuccessOnCleanPoolIsNoop(t *testing.T) {
	tracker := NewLaunchFailureTracker()
	ctx := context.Background()

	// Should not panic or create state
	tracker.RecordLaunchSuccess(ctx, "pool-1")
	assert.False(t, tracker.IsBackedOff("pool-1"))
	assert.Equal(t, 0, tracker.ConsecutiveFailures("pool-1"))
}

func TestLaunchFailureTracker_ResetOnCleanPoolIsNoop(t *testing.T) {
	tracker := NewLaunchFailureTracker()
	ctx := context.Background()

	// Should not panic or create state
	tracker.ResetNodePool(ctx, "pool-1")
	assert.False(t, tracker.IsBackedOff("pool-1"))
}

func TestLaunchFailureTracker_FailureAfterSuccessStartsFresh(t *testing.T) {
	tracker := NewLaunchFailureTracker()
	ctx := context.Background()

	// Build up and clear
	tracker.RecordLaunchFailure(ctx, "pool-1")
	tracker.RecordLaunchFailure(ctx, "pool-1")
	tracker.RecordLaunchSuccess(ctx, "pool-1")

	// New failure starts from scratch
	tracker.RecordLaunchFailure(ctx, "pool-1")
	assert.False(t, tracker.IsBackedOff("pool-1"), "first failure after success should not trigger backoff")
	assert.Equal(t, 1, tracker.ConsecutiveFailures("pool-1"))
}

func TestLaunchFailureTracker_BackoffDurationSchedule(t *testing.T) {
	tracker := NewLaunchFailureTracker()

	// Below threshold
	assert.Equal(t, time.Duration(0), tracker.backoffDurationForFailureCount(0))
	assert.Equal(t, time.Duration(0), tracker.backoffDurationForFailureCount(1))

	// Scheduled values
	assert.Equal(t, 1*time.Minute, tracker.backoffDurationForFailureCount(2))
	assert.Equal(t, 5*time.Minute, tracker.backoffDurationForFailureCount(3))
	assert.Equal(t, 15*time.Minute, tracker.backoffDurationForFailureCount(4))

	// Beyond schedule = max
	assert.Equal(t, LaunchFailureBackoffMaxDuration, tracker.backoffDurationForFailureCount(5))
	assert.Equal(t, LaunchFailureBackoffMaxDuration, tracker.backoffDurationForFailureCount(100))
}
