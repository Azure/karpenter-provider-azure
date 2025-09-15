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

package utils

import "time"

const (
	// CreationTimestampFormat is the RFC3339 format used for creation timestamp tags
	CreationTimestampFormat = "2006-01-02T15:04:05.000Z"
)

// GetStringFromCreationTimestamp converts a time.Time to the string format used in creation timestamp tags
func GetStringFromCreationTimestamp(t time.Time) string {
	return t.UTC().Format(CreationTimestampFormat)
}

// GetCreationTimestampFromString parses a creation timestamp tag value back to time.Time
func GetCreationTimestampFromString(timestampStr string) (time.Time, error) {
	return time.Parse(CreationTimestampFormat, timestampStr)
}
