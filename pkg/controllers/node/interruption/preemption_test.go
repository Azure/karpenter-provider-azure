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

package interruption

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestParseNotice(t *testing.T) {
	deadline := time.Date(2026, time.August, 1, 12, 0, 30, 0, time.UTC)
	for _, tt := range []struct {
		name     string
		message  string
		kind     noticeKind
		deadline time.Time
		wantErr  bool
	}{
		{name: "advisory", message: "SpotRebalanceRecommendation Advisory: . For more information, see https://example.com.", kind: advisoryNotice},
		{name: "advisory without date separator", message: "SpotRebalanceRecommendation Advisory. For more information, see https://example.com.", kind: advisoryNotice},
		{name: "advisory date is not a deadline", message: "SpotRebalanceRecommendation Advisory: Sat, 01 Aug 2026 12:00:30 GMT.", kind: advisoryNotice},
		{name: "advisory mentions preempt", message: "SpotRebalanceRecommendation Advisory: Preempt Scheduled: Sat, 01 Aug 2026 12:00:30 GMT.", kind: advisoryNotice},
		{name: "scheduled", message: "Preempt Scheduled: Sat, 01 Aug 2026 12:00:30 GMT. For more information, see https://example.com. EventId: 00000000-0000-0000-0000-000000000001", kind: scheduledNotice, deadline: deadline},
		{name: "UTC", message: "Preempt Scheduled: Sat, 01 Aug 2026 12:00:30 UTC", kind: scheduledNotice, deadline: deadline},
		{name: "single digit and whitespace", message: " \nPreempt  Scheduled:\tSat,  1 Aug 2026 12:00:30 GMT. ", kind: scheduledNotice, deadline: deadline},
		{name: "started no date", message: "Preempt Started: . For more information, see https://example.com.", kind: startedNotice},
		{name: "started without date separator", message: "Preempt Started. For more information, see https://example.com.", kind: startedNotice},
		{name: "started empty description", message: "Preempt Started.", kind: startedNotice},
		{name: "started description whitespace", message: " \nPreempt  Started.\tFor more information, see https://example.com.", kind: startedNotice},
		{name: "started ignores future date", message: "Preempt Started: Sat, 01 Aug 2026 12:00:30 GMT.", kind: startedNotice},
		{name: "started malformed date", message: "Preempt Started: invalid", kind: startedNotice},
		{name: "empty", wantErr: true},
		{name: "reason alone", message: "SpotEvictionIncoming", wantErr: true},
		{name: "embedded preempt", message: "Unknown event: Preempt Scheduled: Sat, 01 Aug 2026 12:00:30 GMT.", wantErr: true},
		{name: "embedded started without date", message: "Unknown event: Preempt Started. For more information, see https://example.com.", wantErr: true},
		{name: "different type", message: "Terminate Scheduled: Sat, 01 Aug 2026 12:00:30 GMT.", wantErr: true},
		{name: "different status", message: "Preempt Advisory: Sat, 01 Aug 2026 12:00:30 GMT.", wantErr: true},
		{name: "different status without date", message: "Preempt Advisory. For more information, see https://example.com.", wantErr: true},
		{name: "type suffix", message: "NotPreempt Scheduled: Sat, 01 Aug 2026 12:00:30 GMT.", wantErr: true},
		{name: "status suffix", message: "Preempt ScheduledSomething: Sat, 01 Aug 2026 12:00:30 GMT.", wantErr: true},
		{name: "started status suffix", message: "Preempt StartedSomething. For more information, see https://example.com.", wantErr: true},
		{name: "scheduled status suffix without date", message: "Preempt ScheduledSomething. For more information, see https://example.com.", wantErr: true},
		{name: "advisory status suffix", message: "SpotRebalanceRecommendation AdvisorySomething. For more information, see https://example.com.", wantErr: true},
		{name: "started type suffix", message: "NotPreempt Started. For more information, see https://example.com.", wantErr: true},
		{name: "started with arbitrary trailing text", message: "Preempt Started eventually.", wantErr: true},
		{name: "started without a boundary", message: "Preempt Started.Something", wantErr: true},
		{name: "bare started", message: "Preempt Started", wantErr: true},
		{name: "missing delimiter", message: "Preempt Scheduled Sat, 01 Aug 2026 12:00:30 GMT.", wantErr: true},
		{name: "missing date", message: "Preempt Scheduled:", kind: scheduledNotice, wantErr: true},
		{name: "scheduled without date separator", message: "Preempt Scheduled. For more information, see https://example.com.", kind: scheduledNotice, wantErr: true},
		{name: "scheduled description is not a deadline", message: "Preempt Scheduled. Sat, 01 Aug 2026 12:00:30 GMT.", kind: scheduledNotice, wantErr: true},
		{name: "missing zone", message: "Preempt Scheduled: Sat, 01 Aug 2026 12:00:30", kind: scheduledNotice, wantErr: true},
		{name: "unknown zone", message: "Preempt Scheduled: Sat, 01 Aug 2026 12:00:30 PST.", kind: scheduledNotice, wantErr: true},
		{name: "RFC3339", message: "Preempt Scheduled: 2026-08-01T12:00:30Z.", kind: scheduledNotice, wantErr: true},
		{name: "impossible date", message: "Preempt Scheduled: Sat, 32 Aug 2026 12:00:30 GMT.", kind: scheduledNotice, wantErr: true},
		{name: "truncated zone", message: "Preempt Scheduled: Sat, 01 Aug 2026 12:00:30 GMTinvalid.", kind: scheduledNotice, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			kind, got, err := parseNotice(tt.message)
			if kind != tt.kind || !got.Equal(tt.deadline) || (err != nil) != tt.wantErr {
				t.Fatalf("parseNotice() = (%q, %v, %v), want (%q, %v, error=%v)", kind, got, err, tt.kind, tt.deadline, tt.wantErr)
			}
		})
	}
}

func TestParseNoticeIgnoresLocalTimeZone(t *testing.T) {
	original := time.Local
	t.Cleanup(func() { time.Local = original })
	time.Local = time.FixedZone("GMT", -7*60*60)
	_, deadline, err := parseNotice("Preempt Scheduled: Sat, 01 Aug 2026 12:00:30 GMT.")
	if err != nil || !deadline.Equal(time.Date(2026, time.August, 1, 12, 0, 30, 0, time.UTC)) {
		t.Fatalf("deadline depends on local time zone: %v, %v", deadline, err)
	}
}

func TestNoticePredicate(t *testing.T) {
	node := &corev1.Node{Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
		Type: ConditionTypePreemptionScheduled, Status: corev1.ConditionTrue,
		Reason: "SpotEvictionIncoming", Message: "SpotRebalanceRecommendation Advisory:",
	}}}}
	p := noticePredicate()
	if !p.Create(event.CreateEvent{Object: node}) {
		t.Fatal("startup must reconcile an existing signal")
	}
	for _, tt := range []struct {
		name   string
		change func(*corev1.Node)
		want   bool
	}{
		{name: "unchanged"},
		{name: "heartbeat only", change: func(n *corev1.Node) { n.Status.Conditions[0].LastHeartbeatTime = metav1.Now() }},
		{name: "advisory to preempt with same True", change: func(n *corev1.Node) {
			n.Status.Conditions[0].Message = "Preempt Scheduled: Sat, 01 Aug 2026 12:00:30 GMT."
		}, want: true},
		{name: "reason only", change: func(n *corev1.Node) { n.Status.Conditions[0].Reason = "NewReason" }, want: true},
		{name: "provider ID appears", change: func(n *corev1.Node) { n.Spec.ProviderID = "test-provider-id" }, want: true},
		{name: "ownership appears", change: func(n *corev1.Node) { n.Labels = map[string]string{"test": "managed"} }, want: true},
		{name: "false", change: func(n *corev1.Node) { n.Status.Conditions[0].Status = corev1.ConditionFalse }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			updated := node.DeepCopy()
			if tt.change != nil {
				tt.change(updated)
			}
			if got := p.Update(event.UpdateEvent{ObjectOld: node, ObjectNew: updated}); got != tt.want {
				t.Fatalf("Update() = %v, want %v", got, tt.want)
			}
		})
	}
}
