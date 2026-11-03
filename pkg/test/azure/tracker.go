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

package azure

import (
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"
)

type Cleanup func() error

type Tracker struct {
	// Can't easily use the armresources.NewClient here for generic deletion because we would need to know the api-version for each resource. It's easier
	// to just have cleanup passed in as part of Add. If we really wanted to, we could probably hook the clients, detect PUTs in a PerRequestPolicy, extract
	// the API version and save it here, but that seems like more trouble than it's worth.
	ids map[string]Cleanup
	mu  sync.Mutex
}

func NewTracker() *Tracker {
	return &Tracker{
		ids: make(map[string]Cleanup),
	}
}

func (t *Tracker) Add(id string, cleanup Cleanup) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// TODO: This isn't case-insensitive, but it probably should be
	if t.ids[id] != nil {
		return
	}
	t.ids[id] = cleanup
}

func (t *Tracker) Cleanup() error {
	// We could avoid holding the lock across cleanup - we don't expect adds to happen during cleanup so for now not worrying about it
	t.mu.Lock()
	defer t.mu.Unlock()

	var g errgroup.Group
	for id, c := range t.ids {
		// TODO: Should be using the test logger
		fmt.Printf("## Cleaning up Azure resource: %s\n", id)
		g.Go(c)
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("cleanup errors: %w", err)
	}
	return nil
}
