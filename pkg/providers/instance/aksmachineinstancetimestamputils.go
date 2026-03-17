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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func standardizeAKSMachineTimestamp(t time.Time) time.Time {
	// Truncate to centisecond precision (10ms) to ensure consistent 2-digit format
	return t.UTC().Truncate(10 * time.Millisecond)
}

// NewAKSMachineTimestamp returns the current time truncated to centisecond precision for AKS machine creation timestamps
func NewAKSMachineTimestamp() time.Time {
	return standardizeAKSMachineTimestamp(time.Now())
}

func ZeroAKSMachineTimestamp() time.Time {
	return standardizeAKSMachineTimestamp(time.Unix(0, 0))
}

// AKSMachineTimestampToMeta converts a time.Time to metav1.Time for AKS machine creation timestamps
func AKSMachineTimestampToMeta(t time.Time) metav1.Time {
	return metav1.Time{Time: standardizeAKSMachineTimestamp(t)}
}
