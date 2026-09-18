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

	. "github.com/onsi/gomega"
)

func TestParsePreemptionNotice(t *testing.T) {
	tests := []struct {
		name              string
		message           string
		expectedNotBefore time.Time
		expectedEventID   string
		expectedErr       string
	}{
		{
			// Verbatim shape emitted by the AKS node-problem-detector.
			name:              "observed AKS node-problem-detector message",
			message:           "Preempt Scheduled: Sat, 01 Aug 2026 05:41:02 GMT. For more information, see https://aka.ms/aks-spot-eviction. EventId: A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
			expectedNotBefore: time.Date(2026, 8, 1, 5, 41, 2, 0, time.UTC),
			expectedEventID:   "A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
		},
		{
			name:              "lower case event id is normalized",
			message:           "Preempt Scheduled: Sat, 01 Aug 2026 05:41:02 GMT. EventId: A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
			expectedNotBefore: time.Date(2026, 8, 1, 5, 41, 2, 0, time.UTC),
			expectedEventID:   "A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
		},
		{
			// RFC 1123 section 5.2.14 permits a single digit day; time.RFC1123 alone rejects it.
			name:              "single digit day",
			message:           "Preempt Scheduled: Mon, 3 Aug 2026 05:41:02 GMT. EventId: A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
			expectedNotBefore: time.Date(2026, 8, 3, 5, 41, 2, 0, time.UTC),
			expectedEventID:   "A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
		},
		{
			name:              "UTC zone",
			message:           "Preempt Scheduled: Sat, 01 Aug 2026 05:41:02 UTC. EventId: A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
			expectedNotBefore: time.Date(2026, 8, 1, 5, 41, 2, 0, time.UTC),
			expectedEventID:   "A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
		},
		{
			name:              "irregular whitespace",
			message:           "Preempt Scheduled:   Sat,  01  Aug  2026  05:41:02  GMT.  EventId:   A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
			expectedNotBefore: time.Date(2026, 8, 1, 5, 41, 2, 0, time.UTC),
			expectedEventID:   "A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
		},
		{
			// The EventId is correlation metadata only, so its absence must not cost us the deadline.
			name:              "missing event id still yields a deadline",
			message:           "Preempt Scheduled: Sat, 01 Aug 2026 05:41:02 GMT.",
			expectedNotBefore: time.Date(2026, 8, 1, 5, 41, 2, 0, time.UTC),
			expectedEventID:   "",
		},
		{
			name:        "empty message",
			message:     "",
			expectedErr: "no \"Preempt Scheduled:\" timestamp found",
		},
		{
			name:        "no deadline in message",
			message:     "Preempt Scheduled. EventId: A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
			expectedErr: "no \"Preempt Scheduled:\" timestamp found",
		},
		{
			name:        "truncated timestamp",
			message:     "Preempt Scheduled: Sat, 01 Aug 2026 GMT. EventId: A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
			expectedErr: "no \"Preempt Scheduled:\" timestamp found",
		},
		{
			// Go silently invents a zero-offset zone for abbreviations it cannot resolve, which would
			// turn a PST deadline into a UTC one and skew the drain budget by hours.
			name:        "non UTC time zone is rejected rather than misparsed",
			message:     "Preempt Scheduled: Sat, 01 Aug 2026 05:41:02 PST. EventId: A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
			expectedErr: "unsupported time zone \"PST\"",
		},
		{
			name:        "impossible date",
			message:     "Preempt Scheduled: Sat, 32 Aug 2026 05:41:02 GMT. EventId: A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
			expectedErr: "as an RFC 1123 timestamp",
		},
		{
			name:        "RFC3339 is not accepted",
			message:     "Preempt Scheduled: 2026-08-01T05:41:02Z. EventId: A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
			expectedErr: "no \"Preempt Scheduled:\" timestamp found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			notice, err := ParsePreemptionNotice(tc.message)
			if tc.expectedErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErr))
				g.Expect(notice).To(BeNil())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(notice.NotBefore).To(Equal(tc.expectedNotBefore))
			g.Expect(notice.NotBefore.Location()).To(Equal(time.UTC))
			g.Expect(notice.EventID).To(Equal(tc.expectedEventID))
		})
	}
}

// TestParsePreemptionNoticeIsDeterministicAcrossHostTimeZones guards the parse from the host's local
// zone database: the controller may run anywhere, but a GMT deadline always means the same instant.
func TestParsePreemptionNoticeIsDeterministicAcrossHostTimeZones(t *testing.T) {
	g := NewWithT(t)

	original := time.Local
	defer func() { time.Local = original }()

	expected := time.Date(2026, 8, 1, 5, 41, 2, 0, time.UTC)
	for _, name := range []string{"UTC", "America/Los_Angeles", "Asia/Kolkata", "Europe/London"} {
		location, err := time.LoadLocation(name)
		if err != nil {
			// Hosts without a zone database (common on Windows CI) can only exercise UTC.
			continue
		}
		time.Local = location

		notice, err := ParsePreemptionNotice("Preempt Scheduled: Sat, 01 Aug 2026 05:41:02 GMT. EventId: A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(notice.NotBefore.Equal(expected)).To(BeTrue(), "expected %s in %s to equal %s", notice.NotBefore, name, expected)
	}
}
