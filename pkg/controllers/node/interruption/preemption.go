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
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

const (
	// ConditionTypePreemptionScheduled is the Node condition the AKS node-problem-detector sets when the
	// Azure Instance Metadata Service publishes a `Preempt` Scheduled Event for the underlying Spot VM.
	//
	// It is deliberately narrower than the `VMEventScheduled` condition, which also covers Reboot,
	// Redeploy, Freeze and Terminate events. Only `Preempt` means the platform is reclaiming the VM
	// because it is Spot capacity, so only `Preempt` should trigger deadline-aware cleanup here.
	ConditionTypePreemptionScheduled = corev1.NodeConditionType("PreemptionScheduled")

	// ConditionReasonSpotEvictionIncoming is the reason the AKS node-problem-detector reports alongside
	// ConditionTypePreemptionScheduled. It is recorded for observability only; the controller keys off the
	// condition type and status so that a reason rename cannot silently disable Spot handling.
	ConditionReasonSpotEvictionIncoming = "SpotEvictionIncoming"

	preemptScheduledPrefix = "Preempt Scheduled:"

	// rfc1123SingleDigitDayLayout accepts the `1*2DIGIT` day permitted by RFC 1123 section 5.2.14,
	// which time.RFC1123 (a fixed two-digit layout) rejects.
	rfc1123SingleDigitDayLayout = "Mon, 2 Jan 2006 15:04:05 MST"
)

// The condition message is emitted by the AKS node-problem-detector and looks like:
//
//	Preempt Scheduled: Sat, 01 Aug 2026 05:41:02 GMT. For more information, see
//	<docs link>. EventId: 40B7BD88-C56E-44E4-93F6-75CEDACE2869
//
// The timestamp is the Scheduled Event's `NotBefore` field, forwarded verbatim from the Azure Instance
// Metadata Service, which documents it as an RFC 1123 timestamp in GMT (e.g. "Mon, 19 Sep 2016 18:29:47
// GMT"). Only that shape is accepted: coercing an unrecognized layout into a deadline risks draining
// against a wildly wrong time, which is worse than the caller's documented fallback of cleaning up
// immediately.
var (
	notBeforeRegexp = regexp.MustCompile(`Preempt Scheduled:\s*(?P<NotBefore>[A-Za-z]{3},\s+\d{1,2}\s+[A-Za-z]{3}\s+\d{4}\s+\d{1,2}:\d{2}:\d{2}\s+[A-Za-z]{2,4})`)
	eventIDRegexp   = regexp.MustCompile(`(?i)EventId:\s*(?P<EventID>[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12})`)
)

// PreemptionNotice is the parsed content of a ConditionTypePreemptionScheduled condition message.
type PreemptionNotice struct {
	// NotBefore is the Azure Scheduled Event deadline, normalized to UTC. Azure will not evict the VM
	// before this instant, so it is the budget Karpenter has to drain the node. It may already be in the
	// past by the time the condition is observed.
	NotBefore time.Time
	// EventID is the Azure Scheduled Event identifier. Azure republishes the same EventId for the
	// lifetime of a notice, so it identifies the preemption rather than an individual observation of it.
	// It is best-effort: it is used for correlation with Azure-side telemetry, never for control flow.
	EventID string
}

// ParsePreemptionNotice extracts the eviction deadline and Azure Scheduled Event ID from a
// ConditionTypePreemptionScheduled condition message. It returns an error, never a guessed deadline,
// when the message does not carry a deadline in the documented format.
func ParsePreemptionNotice(message string) (*PreemptionNotice, error) {
	match := notBeforeRegexp.FindStringSubmatch(message)
	if match == nil {
		return nil, fmt.Errorf("no %q timestamp found in condition message %q", preemptScheduledPrefix, message)
	}
	// Collapse irregular whitespace so the fixed-width layouts below line up.
	raw := strings.Join(strings.Fields(match[notBeforeRegexp.SubexpIndex("NotBefore")]), " ")

	// Go fabricates a zero-offset location for zone abbreviations it cannot resolve, so an unexpected
	// abbreviation such as "PST" would silently parse as if it were UTC. Require the documented zone.
	zone := raw[strings.LastIndex(raw, " ")+1:]
	if !strings.EqualFold(zone, "GMT") && !strings.EqualFold(zone, "UTC") {
		return nil, fmt.Errorf("unsupported time zone %q in %q, only GMT/UTC eviction deadlines are understood", zone, raw)
	}

	var notBefore time.Time
	var err error
	for _, layout := range []string{time.RFC1123, rfc1123SingleDigitDayLayout} {
		if notBefore, err = time.Parse(layout, raw); err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("parsing %q as an RFC 1123 timestamp: %w", raw, err)
	}

	notice := &PreemptionNotice{NotBefore: notBefore.UTC()}
	if eventIDMatch := eventIDRegexp.FindStringSubmatch(message); eventIDMatch != nil {
		notice.EventID = strings.ToUpper(eventIDMatch[eventIDRegexp.SubexpIndex("EventID")])
	}
	return notice, nil
}
