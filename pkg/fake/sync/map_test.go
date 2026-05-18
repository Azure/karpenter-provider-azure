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

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMap_StoreAndLoad(t *testing.T) {
	var m Map[string, int]
	m.Store("a", 1)
	v, ok := m.Load("a")
	assert.True(t, ok)
	assert.Equal(t, 1, v)

	_, ok = m.Load("missing")
	assert.False(t, ok)
}

func TestMap_Delete(t *testing.T) {
	var m Map[string, int]
	m.Store("a", 1)
	m.Delete("a")
	_, ok := m.Load("a")
	assert.False(t, ok)
}

func TestMap_LoadOrStore(t *testing.T) {
	var m Map[string, int]
	v, loaded := m.LoadOrStore("a", 1)
	assert.False(t, loaded)
	assert.Equal(t, 1, v)

	v, loaded = m.LoadOrStore("a", 2)
	assert.True(t, loaded)
	assert.Equal(t, 1, v)
}

func TestMap_LoadAndDelete(t *testing.T) {
	var m Map[string, int]
	m.Store("a", 1)
	v, loaded := m.LoadAndDelete("a")
	assert.True(t, loaded)
	assert.Equal(t, 1, v)

	_, loaded = m.LoadAndDelete("a")
	assert.False(t, loaded)
}

func TestMap_Range(t *testing.T) {
	var m Map[string, int]
	m.Store("a", 1)
	m.Store("b", 2)

	seen := map[string]int{}
	m.Range(func(k string, v int) bool {
		seen[k] = v
		return true
	})
	assert.Equal(t, map[string]int{"a": 1, "b": 2}, seen)
}

func TestMap_Clear(t *testing.T) {
	var m Map[string, int]
	m.Store("a", 1)
	m.Clear()
	_, ok := m.Load("a")
	assert.False(t, ok)
}

func TestMap_ZeroValue(t *testing.T) {
	var m Map[string, int]
	_, ok := m.Load("x")
	assert.False(t, ok)
	m.Delete("x") // should not panic
	m.Range(func(k string, v int) bool { return true })
}
