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

package metrics_test

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestImageSelectionErrorCount(t *testing.T) {
	tests := []struct {
		name          string
		setup         func()
		expectedCount int
	}{
		{
			name:          "should have no errors initially",
			setup:         func() {},
			expectedCount: 0,
		},
		{
			name: "should increment the error count for a family",
			setup: func() {
				metrics.ImageSelectionErrorCount.WithLabelValues("Ubuntu2204").Inc()
			},
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics.ImageSelectionErrorCount.Reset()
			tt.setup()
			got := testutil.CollectAndCount(metrics.ImageSelectionErrorCount)
			if got != tt.expectedCount {
				t.Errorf("CollectAndCount = %d, want %d", got, tt.expectedCount)
			}
		})
	}
}
