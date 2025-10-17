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

package inplaceupdate

import (
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
)

func TestTagsChangedPredicate_Delete(t *testing.T) {
	g := NewWithT(t)
	predicate := tagsChangedPredicate{}

	nodeClass := test.AKSNodeClass(v1beta1.AKSNodeClass{
		Spec: v1beta1.AKSNodeClassSpec{
			Tags: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
		},
	})

	deleteEvent := event.DeleteEvent{
		Object: nodeClass,
	}

	result := predicate.Delete(deleteEvent)
	g.Expect(result).To(BeFalse())
}

func TestTagsChangedPredicate_Update(t *testing.T) {
	tests := []struct {
		name           string
		oldObject      client.Object
		newObject      client.Object
		expectedResult bool
	}{
		{
			name:           "tags are identical",
			oldObject:      newTestNodeClass(map[string]string{"key1": "value1", "key2": "value2"}),
			newObject:      newTestNodeClass(map[string]string{"key1": "value1", "key2": "value2"}),
			expectedResult: false,
		},
		{
			name:           "tags are reordered but values are the same",
			oldObject:      newTestNodeClass(map[string]string{"key1": "value1", "key2": "value2"}),
			newObject:      newTestNodeClass(map[string]string{"key2": "value2", "key1": "value1"}),
			expectedResult: false,
		},
		{
			name:           "both objects have nil tags",
			oldObject:      newTestNodeClass(nil),
			newObject:      newTestNodeClass(nil),
			expectedResult: false,
		},
		{
			name:           "both objects have empty tags",
			oldObject:      newTestNodeClass(map[string]string{}),
			newObject:      newTestNodeClass(map[string]string{}),
			expectedResult: false,
		},
		{
			name:           "tag values changed",
			oldObject:      newTestNodeClass(map[string]string{"key1": "value1", "key2": "value2"}),
			newObject:      newTestNodeClass(map[string]string{"key1": "modified-value1", "key2": "value2"}),
			expectedResult: true,
		},
		{
			name:           "tags added",
			oldObject:      newTestNodeClass(map[string]string{"key1": "value1", "key2": "value2"}),
			newObject:      newTestNodeClass(map[string]string{"key1": "value1", "key2": "value2", "key3": "value3"}),
			expectedResult: true,
		},
		{
			name:           "tags removed",
			oldObject:      newTestNodeClass(map[string]string{"key1": "value1", "key2": "value2"}),
			newObject:      newTestNodeClass(map[string]string{"key1": "value1"}),
			expectedResult: true,
		},
		{
			name:           "nil to non-nil tags",
			oldObject:      newTestNodeClass(nil),
			newObject:      newTestNodeClass(map[string]string{"key1": "value1"}),
			expectedResult: true,
		},
		{
			name:           "non-nil to nil tags",
			oldObject:      newTestNodeClass(map[string]string{"key1": "value1", "key2": "value2"}),
			newObject:      newTestNodeClass(nil),
			expectedResult: true,
		},
		{
			name:           "empty to non-empty tags",
			oldObject:      newTestNodeClass(map[string]string{}),
			newObject:      newTestNodeClass(map[string]string{"key1": "value1"}),
			expectedResult: true,
		},
		{
			name:           "non-empty to empty tags",
			oldObject:      newTestNodeClass(map[string]string{"key1": "value1", "key2": "value2"}),
			newObject:      newTestNodeClass(map[string]string{}),
			expectedResult: true,
		},
		{
			name:           "ObjectOld is nil",
			oldObject:      nil,
			newObject:      newTestNodeClass(map[string]string{"key1": "value1"}),
			expectedResult: true,
		},
		{
			name:           "ObjectNew is nil",
			oldObject:      newTestNodeClass(map[string]string{"key1": "value1"}),
			newObject:      nil,
			expectedResult: true,
		},
		{
			name:           "ObjectOld is wrong type",
			oldObject:      &corev1.ConfigMap{},
			newObject:      newTestNodeClass(map[string]string{"key1": "value1"}),
			expectedResult: true,
		},
		{
			name:           "ObjectNew is wrong type",
			oldObject:      newTestNodeClass(map[string]string{"key1": "value1"}),
			newObject:      &corev1.ConfigMap{},
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			predicate := tagsChangedPredicate{}

			updateEvent := event.UpdateEvent{
				ObjectOld: tt.oldObject,
				ObjectNew: tt.newObject,
			}

			result := predicate.Update(updateEvent)
			g.Expect(result).To(Equal(tt.expectedResult))
		})
	}
}

func newTestNodeClass(tags map[string]string) client.Object {
	return test.AKSNodeClass(v1beta1.AKSNodeClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-nodeclass",
		},
		Spec: v1beta1.AKSNodeClassSpec{
			Tags: tags,
		},
	})
}
