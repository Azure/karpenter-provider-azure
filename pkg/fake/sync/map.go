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

package sync

import "sync"

// Map is a typesafe wrapper around a map with sync.RWMutex.
// It exposes the same methods as sync.Map but with generic type parameters.
// NOTE: This lives here for now because most of the time you may want to enforce some other invariants while under the RWMutex (not just the map invaraints),
// so this helper exists primarily for the fake pkg which is only used for tests. We can think about promoting this to a more general utility package if we
// find more use cases for it.
type Map[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]V
}

func (m *Map[K, V]) init() {
	if m.m == nil {
		m.m = make(map[K]V)
	}
}

func (m *Map[K, V]) Load(key K) (value V, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.m == nil {
		var zero V
		return zero, false
	}
	value, ok = m.m[key]
	return value, ok
}

func (m *Map[K, V]) Store(key K, value V) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	m.m[key] = value
}

func (m *Map[K, V]) Delete(key K) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m != nil {
		delete(m.m, key)
	}
}

func (m *Map[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	if existing, ok := m.m[key]; ok {
		return existing, true
	}
	m.m[key] = value
	return value, false
}

func (m *Map[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		var zero V
		return zero, false
	}
	value, ok := m.m[key]
	if ok {
		delete(m.m, key)
	}
	return value, ok
}

func (m *Map[K, V]) Swap(key K, value V) (previous V, loaded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	previous, loaded = m.m[key]
	m.m[key] = value
	return previous, loaded
}

func (m *Map[K, V]) CompareAndSwap(key K, old, new V) (swapped bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		return false
	}
	if existing, ok := m.m[key]; ok && any(existing) == any(old) {
		m.m[key] = new
		return true
	}
	return false
}

func (m *Map[K, V]) CompareAndDelete(key K, old V) (deleted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		return false
	}
	if existing, ok := m.m[key]; ok && any(existing) == any(old) {
		delete(m.m, key)
		return true
	}
	return false
}

func (m *Map[K, V]) Range(f func(key K, value V) bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for k, v := range m.m {
		if !f(k, v) {
			break
		}
	}
}

func (m *Map[K, V]) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m = nil
}
