// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cache

import "time"

const (
	// KubernetesVersionTTL is the time before the detected Kubernetes version is removed from cache,
	// to be re-detected next time it is needed.
	KubernetesVersionTTL = 15 * time.Minute
	// UnavailableOfferingsTTL is the time before offerings that were marked as unavailable
	// are removed from the cache and are available for launch again
	UnavailableOfferingsTTL = 3 * time.Minute
	// DefaultCleanupInterval triggers cache cleanup (lazy eviction) at this interval.
	DefaultCleanupInterval = 10 * time.Minute
)
