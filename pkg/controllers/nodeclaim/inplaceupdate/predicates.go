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
	"maps"

	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

type tagsChangedPredicate struct {
	predicate.Funcs
}

var _ predicate.Predicate = tagsChangedPredicate{}

func (p tagsChangedPredicate) Delete(e event.DeleteEvent) bool {
	// We never want updates on delete
	return false
}

func (p tagsChangedPredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectOld == nil {
		return true // This isn't expected, so propagate the event so we don't miss anything
	}
	if e.ObjectNew == nil {
		return true // This isn't expected, so propagate the event so we don't miss anything
	}

	typedOld, ok := e.ObjectOld.(*v1beta1.AKSNodeClass)
	if !ok {
		return true // If we don't know the type, we assume it has changed
	}
	typedNew, ok := e.ObjectNew.(*v1beta1.AKSNodeClass)
	if !ok {
		return true // If we don't know the type, we assume it has changed
	}

	return !maps.Equal(typedOld.Spec.Tags, typedNew.Spec.Tags)
}
