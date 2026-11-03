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

	. "github.com/onsi/gomega"
)

func TestMap_StoreAndLoad(t *testing.T) {
	g := NewWithT(t)
	var m Map[string, int]
	m.Store("a", 1)
	v, ok := m.Load("a")
	g.Expect(ok).To(BeTrue())
	g.Expect(v).To(Equal(1))

	_, ok = m.Load("missing")
	g.Expect(ok).To(BeFalse())
}

func TestMap_Delete(t *testing.T) {
	g := NewWithT(t)
	var m Map[string, int]
	m.Store("a", 1)
	m.Delete("a")
	_, ok := m.Load("a")
	g.Expect(ok).To(BeFalse())
}

func TestMap_LoadOrStore(t *testing.T) {
	g := NewWithT(t)
	var m Map[string, int]
	v, loaded := m.LoadOrStore("a", 1)
	g.Expect(loaded).To(BeFalse())
	g.Expect(v).To(Equal(1))

	v, loaded = m.LoadOrStore("a", 2)
	g.Expect(loaded).To(BeTrue())
	g.Expect(v).To(Equal(1))
}

func TestMap_LoadAndDelete(t *testing.T) {
	g := NewWithT(t)
	var m Map[string, int]
	m.Store("a", 1)
	v, loaded := m.LoadAndDelete("a")
	g.Expect(loaded).To(BeTrue())
	g.Expect(v).To(Equal(1))

	_, loaded = m.LoadAndDelete("a")
	g.Expect(loaded).To(BeFalse())
}

func TestMap_Range(t *testing.T) {
	g := NewWithT(t)
	var m Map[string, int]
	m.Store("a", 1)
	m.Store("b", 2)

	seen := map[string]int{}
	m.Range(func(k string, v int) bool {
		seen[k] = v
		return true
	})
	g.Expect(seen).To(Equal(map[string]int{"a": 1, "b": 2}))
}

func TestMap_Clear(t *testing.T) {
	g := NewWithT(t)
	var m Map[string, int]
	m.Store("a", 1)
	m.Clear()
	_, ok := m.Load("a")
	g.Expect(ok).To(BeFalse())
}

func TestMap_ZeroValue(t *testing.T) {
	g := NewWithT(t)
	var m Map[string, int]
	_, ok := m.Load("x")
	g.Expect(ok).To(BeFalse())
	m.Delete("x") // should not panic
	m.Range(func(k string, v int) bool { return true })
}
