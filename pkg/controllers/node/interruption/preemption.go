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
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

const ConditionTypePreemptionScheduled = corev1.NodeConditionType("PreemptionScheduled")

type noticeKind string

const (
	unknownNotice   noticeKind = ""
	advisoryNotice  noticeKind = "advisory"
	scheduledNotice noticeKind = "scheduled"
	startedNotice   noticeKind = "started"
)

// NPD forwards "<EventType> <EventStatus>[: <NotBefore>]. ..." in the condition message,
// omitting the colon and NotBefore when the date is absent.
// Both advisory and mandatory events set PreemptionScheduled=True with SpotEvictionIncoming.
// Classify the leading event before interpreting any date; neither the condition nor its reason
// alone proves that Azure is reclaiming the VM.
func parseNotice(message string) (noticeKind, time.Time, error) {
	message = strings.Join(strings.Fields(message), " ")
	if _, ok := noticePayload(message, "SpotRebalanceRecommendation Advisory"); ok {
		return advisoryNotice, time.Time{}, nil
	}
	if _, ok := noticePayload(message, "Preempt Started"); ok {
		// Started means eviction is already in progress, even if NotBefore is absent or in the future.
		return startedNotice, time.Time{}, nil
	}
	raw, ok := noticePayload(message, "Preempt Scheduled")
	if !ok {
		return unknownNotice, time.Time{}, fmt.Errorf("unrecognized spot event type or status")
	}
	raw, _, _ = strings.Cut(strings.TrimSpace(raw), ".")
	// Parse in an explicit UTC location: Go otherwise uses the host's Local zone for matching
	// abbreviations, and fabricates a zero offset for unknown ones such as PST.
	zone := raw[strings.LastIndex(raw, " ")+1:]
	if zone != "GMT" && zone != "UTC" {
		return scheduledNotice, time.Time{}, fmt.Errorf("missing or non-UTC spot eviction deadline")
	}
	deadline, err := time.ParseInLocation("Mon, 2 Jan 2006 15:04:05 MST", raw, time.UTC)
	if err != nil {
		// Do not include the raw NPD message (which can contain resource and event identifiers).
		return scheduledNotice, time.Time{}, fmt.Errorf("invalid RFC 1123 spot eviction deadline")
	}
	return scheduledNotice, deadline.UTC(), nil
}

func noticePayload(message, header string) (string, bool) {
	suffix, ok := strings.CutPrefix(message, header)
	if !ok {
		return "", false
	}
	if raw, ok := strings.CutPrefix(suffix, ":"); ok {
		return raw, true
	}
	// A description is not a date. Require its delimiter rather than accepting header lookalikes.
	return "", suffix == "." || strings.HasPrefix(suffix, ". ")
}
