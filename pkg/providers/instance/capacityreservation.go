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
	"fmt"
	"strings"
)

func validateCapacityReservationGroupAssociation(actual, desired string) error {
	// ARM echoes resource IDs back with different casing than it was given.
	if !strings.EqualFold(actual, desired) {
		return fmt.Errorf("is associated with capacity reservation group %q, but the NodeClass now specifies %q", actual, desired)
	}
	return nil
}
