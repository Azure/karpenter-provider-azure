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
	"context"
	"errors"
	"fmt"
)

// Intended for lifecycle handling on the higher abstractions.
type Promise interface {
	// Cleanup removes the instance from the cloud provider.
	Cleanup(ctx context.Context) error
	// Wait blocks until the instance is ready. An LROPollingError
	// means the outcome is unknown and must not trigger Cleanup.
	Wait() error
	// GetInstanceName returns the name of the instance. Recommended to be used for logging only due to generic nature.
	GetInstanceName() string
}

// LROPollingError indicates that observation stopped without establishing
// a provisioning failure. This can happen due to giving up polling the LRO, for example
// due to throttling or internal server errors.
type LROPollingError struct {
	Err error
}

func (e *LROPollingError) Error() string {
	return fmt.Sprintf("could not observe provisioning outcome: %v", e.Err)
}

func (e *LROPollingError) Unwrap() error {
	return e.Err
}

func IsLROPollingError(err error) bool {
	var pollingErr *LROPollingError
	return errors.As(err, &pollingErr)
}
